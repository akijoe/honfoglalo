// Honfoglaló játékszerver: kiszolgálja a játékot (index.html, Ruffle, SWF, kérdések, hangok),
// kezeli a többjátékos szobákat, és fogadja a visszajelzéseket.
//
// A szerver nem számolja a játékot: minden kliens ugyanazt a játékot futtatja, a szerver csak
// sorba rendezi és szétküldi a döntéseket (kulcsonként az első érvényes). Minden állapot a
// memóriában van; a szobák egy idő után maguktól megszűnnek.
//
//	go run ./server                       -> http://localhost:8765/
//	go run ./server -addr 127.0.0.1:9000  -> csak helyben, másik porton
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type server struct {
	root         string
	feedbackPath string
	maxLobbies   int
	serveStatic  bool            // a játék fájljait is kiszolgálja (false: csak az API)
	origins      map[string]bool // más címről (pl. Cloudflare Pages) ezek használhatják az API-t
	anyOrigin    bool
	trustProxy   bool // proxy (Caddy, nginx, Cloudflare) mögött: a kliens IP-je a fejlécből

	mu      sync.Mutex
	lobbies map[string]*lobby
	public  map[string]*lobby // a publikus szobák (a Várók listája, server/hub.go)

	roomsByIP map[string]int // nyitott szobák a létrehozó IP-je szerint (s.mu védi)

	fbMu   sync.Mutex
	fbLast map[string]time.Time

	gz gzCache

	joins    *limiter // szoba létrehozása / csatlakozás (server/limits.go)
	calls    *limiter // minden API-hívás
	feedback *limiter // visszajelzés, összesen
	streams  streams
	hub      hub
}

// allow: legfeljebb maxJoinsPerMinute létrehozás/csatlakozás percenként egy IP-ről.
func (s *server) allow(r *http.Request) bool {
	return s.joins.allow(s.clientIP(r))
}

var errTooFast = apiError{http.StatusTooManyRequests, "Túl sok próbálkozás, várj egy percet."}

func newServer(root, feedbackPath string) *server {
	return &server{
		root:         root,
		feedbackPath: feedbackPath,
		maxLobbies:   50000,
		serveStatic:  true,
		origins:      map[string]bool{},
		lobbies:      map[string]*lobby{},
		public:       map[string]*lobby{},
		fbLast:       map[string]time.Time{},
		roomsByIP:    map[string]int{},
		joins:        newLimiter(time.Minute, maxJoinsPerMinute),
		calls:        newLimiter(10*time.Second, maxCallsPer10s),
		feedback:     newLimiter(time.Minute, maxFeedbackPerMinute),
	}
}

func randomString(alphabet string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// ------------------------------------------------------------------------------------- HTTP segédek

type apiError struct {
	status int
	msg    string
}

func (e apiError) Error() string { return e.msg }

var (
	errNoLobby  = apiError{http.StatusNotFound, "Nincs ilyen szoba."}
	errBadToken = apiError{http.StatusForbidden, "Már nem vagy ebben a szobában."}
	errLinkName = apiError{http.StatusBadRequest, "A név nem lehet link."}
	errBadName  = apiError{http.StatusBadRequest, "Ez a név nem használható: trágár szó van benne."}
)

// checkName: a játékos neve nem lehet link, és nem lehet benne trágár szó (server/profanity.go).
func checkName(name string) error {
	switch {
	case hasLink(name):
		return errLinkName
	case hasProfanity(name):
		return errBadName
	}
	return nil
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return apiError{http.StatusBadRequest, "Hibás kérés."}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var ae apiError
	if !errors.As(err, &ae) {
		ae = apiError{http.StatusInternalServerError, "Szerverhiba."}
	}
	writeJSON(w, ae.status, map[string]string{"error": ae.msg})
}

// clientIP: a játékos IP-címe (a percenkénti korlátokhoz). Proxy mögött (-trust-proxy) a proxy
// fejlécéből: Cloudflare-nél CF-Connecting-IP, egyébként az X-Forwarded-For utolsó (a saját proxynk
// által hozzáfűzött) eleme.
func (s *server) clientIP(r *http.Request) string {
	if s.trustProxy {
		if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
			return ip
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *server) lobby(code string) *lobby {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lobbies[strings.ToUpper(strings.TrimSpace(code))]
}

// ------------------------------------------------------------------------------------- szobák

type joinReply struct {
	Code  string `json:"code"`
	Seat  int    `json:"seat"`
	Token string `json:"token"`
}

func (s *server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !s.allow(r) {
		writeErr(w, errTooFast)
		return
	}
	var req struct {
		Name   string `json:"name"`
		Map    string `json:"map"`
		Long   bool   `json:"long"`
		Cats   string `json:"cats"` // a témakörök (cleanCats); érvénytelen lista: mind
		Public bool   `json:"public"`
		Title  string `json:"title"`  // a publikus szoba neve (üres: "<név> lobbyja")
		Client string `json:"client"` // a böngésző azonosítója (js/hf.js: clientId)
	}
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	name, ok := cleanName(req.Name)
	if !ok {
		writeErr(w, apiError{http.StatusBadRequest, "Adj meg egy nevet (legfeljebb 16 betű)."})
		return
	}
	if err := checkName(name); err != nil {
		writeErr(w, err)
		return
	}
	title := ""
	if req.Public {
		var tok bool
		if title, tok = cleanLine(req.Title, maxTitleLen); !tok {
			if strings.TrimSpace(req.Title) != "" {
				writeErr(w, apiError{http.StatusBadRequest, "A váró neve legfeljebb 28 betű lehet."})
				return
			}
			title = name + " lobbyja"
		}
		if hasLink(title) {
			writeErr(w, apiError{http.StatusBadRequest, "A váró nevében nem lehet link."})
			return
		}
		if hasProfanity(title) {
			writeErr(w, apiError{http.StatusBadRequest, "A váró neve nem tartalmazhat trágár szót."})
			return
		}
	}
	now := time.Now()
	ip := s.clientIP(r)
	s.mu.Lock()
	if len(s.lobbies) >= s.maxLobbies {
		s.mu.Unlock()
		writeErr(w, apiError{http.StatusServiceUnavailable, "Most túl sok szoba van nyitva, próbáld később."})
		return
	}
	if s.roomsByIP[ip] >= maxRoomsPerIP {
		s.mu.Unlock()
		writeErr(w, apiError{http.StatusTooManyRequests, "Erről a gépről már túl sok szoba van nyitva. Lépj ki a régiekből, vagy várj egy kicsit."})
		return
	}
	client := ""
	if clientIDRe.MatchString(req.Client) {
		client = req.Client
		n := 0
		for _, o := range s.lobbies {
			o.mu.Lock()
			if o.client == client && !o.closed && o.creatorIn() {
				n++
			}
			o.mu.Unlock()
		}
		if n >= maxRoomsPerClient {
			s.mu.Unlock()
			writeErr(w, apiError{http.StatusTooManyRequests, "Ebből a böngészőből már van két nyitott szobád. Lépj ki valamelyikből, és utána nyithatsz újat."})
			return
		}
	}
	code := randomString(codeAlphabet, codeLen)
	for s.lobbies[code] != nil {
		code = randomString(codeAlphabet, codeLen)
	}
	l := newLobby(code, now)
	l.ip = ip
	s.roomsByIP[ip]++
	tok := newToken()
	l.seats[1] = &player{name: name, token: tok}
	l.boss = 1
	if m := cleanMap(req.Map); m != "" {
		l.mapID = m
	}
	l.long = req.Long
	if c, ok := cleanCats(req.Cats); ok {
		l.cats = c
	}
	l.public, l.title = req.Public, title
	l.creator, l.client = l.seats[1], client
	l.mu.Lock()
	s.goneAfterGrace(l, l.seats[1])
	l.mu.Unlock()
	s.lobbies[code] = l
	if l.public {
		s.public[code] = l
	}
	s.mu.Unlock()
	log.Printf("szoba %s létrehozva (%d szoba, publikus: %v)", code, s.lobbyCount(), l.public)
	writeJSON(w, http.StatusOK, joinReply{Code: code, Seat: 1, Token: tok})
}

func (s *server) handleJoin(w http.ResponseWriter, r *http.Request) {
	if !s.allow(r) {
		writeErr(w, errTooFast)
		return
	}
	var req struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	name, ok := cleanName(req.Name)
	if !ok {
		writeErr(w, apiError{http.StatusBadRequest, "Adj meg egy nevet (legfeljebb 16 betű)."})
		return
	}
	if err := checkName(name); err != nil {
		writeErr(w, err)
		return
	}
	l := s.lobby(req.Code)
	if l == nil {
		writeErr(w, errNoLobby)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.closed:
		writeErr(w, errNoLobby)
		return
	case l.started:
		// Visszalépés egy futó játékba (pl. másik gépről, vagy ha a böngésző elfelejtette a szobát): a
		// kóddal és ugyanazzal a névvel a régi helyére kerül, ha ott most nincs élő kapcsolat. A régi
		// belépője érvényét veszti.
		for seat := 1; seat <= maxPlayers; seat++ {
			p := l.seats[seat]
			if p == nil || p.robot || !strings.EqualFold(p.name, name) {
				continue
			}
			// (p.online: a lecsatlakozás utáni türelmi idő alatt is a régi gazdájáé a hely)
			if p.conns > 0 || p.online {
				writeErr(w, apiError{http.StatusConflict, "Ezzel a névvel most is játszik valaki ebben a szobában."})
				return
			}
			p.token = newToken()
			if p.left {
				p.left, p.dropped = false, true // a visszatérését az eseményfolyama jelzi (handleEvents)
			}
			log.Printf("szoba %s: %d. hely visszalépett névvel", l.code, seat)
			l.broadcastState()
			writeJSON(w, http.StatusOK, joinReply{Code: l.code, Seat: seat, Token: p.token})
			return
		}
		writeErr(w, apiError{http.StatusConflict, "Ebben a szobában már elkezdődött a játék. Visszalépni azzal a névvel lehet, amellyel játszottál."})
		return
	}
	// az első üres hely; ha nincs, az utolsó robot helyére ül (a robotok csak kitöltik a helyet)
	free := 0
	for seat := maxPlayers; seat >= 1; seat-- {
		if l.seats[seat] == nil {
			free = seat
		}
	}
	for seat := maxPlayers; seat >= 1 && free == 0; seat-- {
		if l.seats[seat].robot {
			free = seat
		}
	}
	if free == 0 {
		writeErr(w, apiError{http.StatusConflict, "A szoba megtelt (legfeljebb 3 játékos)."})
		return
	}
	if seat := l.nameSeat(name); seat != 0 && seat != free {
		writeErr(w, apiError{http.StatusConflict, "Ez a név már foglalt a szobában."})
		return
	}
	tok := newToken()
	l.seats[free] = &player{name: name, token: tok}
	if l.boss == 0 {
		l.boss = free // mindenki kilépett, de a szoba még nem szűnt meg
	}
	s.goneAfterGrace(l, l.seats[free])
	l.broadcastState()
	l.announce(free, "joined", time.Now())
	writeJSON(w, http.StatusOK, joinReply{Code: l.code, Seat: free, Token: tok})
}

type tokenReq struct {
	Code  string `json:"code"`
	Token string `json:"token"`
}

// withSeat: megkeresi a szobát és a játékost, és zárolt szobával hívja f-et.
func (s *server) withSeat(w http.ResponseWriter, r *http.Request, req *tokenReq, f func(l *lobby, seat int) error) {
	l := s.lobby(req.Code)
	if l == nil {
		writeErr(w, errNoLobby)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		writeErr(w, errNoLobby)
		return
	}
	seat := l.seatOf(req.Token)
	if seat == 0 || l.seats[seat].left {
		writeErr(w, errBadToken)
		return
	}
	if err := f(l, seat); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleStart(w http.ResponseWriter, r *http.Request) {
	var req tokenReq
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	s.withSeat(w, r, &req, func(l *lobby, seat int) error {
		switch {
		case l.started:
			return nil
		case seat != l.boss:
			return apiError{http.StatusForbidden, "Csak a szoba főnöke indíthatja a játékot."}
		case l.count() < 2:
			return apiError{http.StatusConflict, "Legalább 2 játékos kell az induláshoz (az egyik lehet robot)."}
		}
		// új játék: a döntések elölről (a kulcsok újra 0-tól jönnek; js/hf.js a game alapján tudja)
		l.started = true
		l.game++
		l.progress = progress{}
		l.decs = nil
		l.seen = map[int]bool{}
		for st := 1; st <= maxPlayers; st++ {
			if p := l.seats[st]; p != nil {
				p.played = l.game
				p.dropped = false
			}
		}
		log.Printf("szoba %s: a(z) %d. játék elindult (%d ember, %d robot)", l.code, l.game, l.humans(), l.count()-l.humans())
		l.broadcastState()
		return nil
	})
}

// handleAgain: a játék végén "Vissza a váróba": a szoba újra várakozik, a főnök új játékot indíthat.
// Akik a játék végén kiléptek vagy elmentek, kikerülnek. (A döntések az új indításig megmaradnak,
// hogy egy lemaradt gép még befejezhesse a régi játékot.)
func (s *server) handleAgain(w http.ResponseWriter, r *http.Request) {
	var req tokenReq
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	s.withSeat(w, r, &req, func(l *lobby, seat int) error {
		if !l.started {
			return nil
		}
		l.started = false
		l.progress = progress{}
		for st := 1; st <= maxPlayers; st++ {
			if p := l.seats[st]; p != nil && st != seat && (p.left || (p.conns == 0 && !p.online)) {
				l.removeSeat(st)
			}
		}
		log.Printf("szoba %s: vissza a váróba (%d játékos)", l.code, l.count())
		l.broadcastState()
		return nil
	})
}

// handleMap: a főnök a játék indulása előtt átállítja a szoba térképét.
func (s *server) handleMap(w http.ResponseWriter, r *http.Request) {
	var req struct {
		tokenReq
		Map string `json:"map"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	s.withSeat(w, r, &req.tokenReq, func(l *lobby, seat int) error {
		m := cleanMap(req.Map)
		switch {
		case m == "":
			return apiError{http.StatusBadRequest, "Ismeretlen térkép."}
		case seat != l.boss:
			return apiError{http.StatusForbidden, "A térképet a szoba főnöke választja."}
		case l.started:
			return apiError{http.StatusConflict, "A játék már elkezdődött."}
		}
		if l.mapID != m {
			l.mapID = m
			l.broadcastState()
		}
		return nil
	})
}

// handleLong: a főnök a játék indulása előtt átállítja a szabályt (villámháború / hosszú hadjárat).
func (s *server) handleLong(w http.ResponseWriter, r *http.Request) {
	var req struct {
		tokenReq
		Long bool `json:"long"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	s.withSeat(w, r, &req.tokenReq, func(l *lobby, seat int) error {
		switch {
		case seat != l.boss:
			return apiError{http.StatusForbidden, "A szabályt a szoba főnöke választja."}
		case l.started:
			return apiError{http.StatusConflict, "A játék már elkezdődött."}
		}
		if l.long != req.Long {
			l.long = req.Long
			l.broadcastState()
		}
		return nil
	})
}

// handleCats: a főnök a játék indulása előtt átállítja a kérdések témaköreit ("": mind).
func (s *server) handleCats(w http.ResponseWriter, r *http.Request) {
	var req struct {
		tokenReq
		Cats string `json:"cats"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	s.withSeat(w, r, &req.tokenReq, func(l *lobby, seat int) error {
		c, ok := cleanCats(req.Cats)
		switch {
		case !ok:
			return apiError{http.StatusBadRequest, "Ismeretlen témakör."}
		case seat != l.boss:
			return apiError{http.StatusForbidden, "A témaköröket a szoba főnöke választja."}
		case l.started:
			return apiError{http.StatusConflict, "A játék már elkezdődött."}
		}
		if l.cats != c {
			l.cats = c
			l.broadcastState()
		}
		return nil
	})
}

// handleProgress: hol tart a futó játék (a házigazda gépe küldi, ha változik; a publikus Várók
// listája mutatja, server/hub.go).
func (s *server) handleProgress(w http.ResponseWriter, r *http.Request) {
	var req struct {
		tokenReq
		progress
	}
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	s.withSeat(w, r, &req.tokenReq, func(l *lobby, seat int) error {
		switch {
		case !l.started:
			return nil // közben véget ért (vissza a váróba)
		case req.State < 0 || req.State > 20 || req.Round < 0 || req.Round > 99:
			return apiError{http.StatusBadRequest, "Hibás kérés."}
		}
		l.progress = req.progress
		return nil
	})
}

// handleRobot: a főnök a játék indulása előtt robotot tesz egy üres helyre (add), vagy kiveszi onnan.
// Seat 0: az első üres hely. A robot csak kitölti a helyet: ha nincs üres hely, a belépő ember a
// helyére ül (handleJoin). Robot nélkül ketten is indíthatnak: a harmadik hely üres marad.
func (s *server) handleRobot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		tokenReq
		Seat int  `json:"seat"`
		Add  bool `json:"add"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	s.withSeat(w, r, &req.tokenReq, func(l *lobby, seat int) error {
		switch {
		case seat != l.boss:
			return apiError{http.StatusForbidden, "Robotot a szoba főnöke tehet be."}
		case l.started:
			return apiError{http.StatusConflict, "A játék már elkezdődött."}
		case req.Seat < 0 || req.Seat > maxPlayers:
			return apiError{http.StatusBadRequest, "Hibás kérés."}
		}
		// a gyakoriság (robotFlood) csak a valódi változásokat számolja
		now := time.Now()
		if req.Add {
			at := req.Seat
			for st := 1; st <= maxPlayers && at == 0; st++ {
				if l.seats[st] == nil {
					at = st
				}
			}
			if at == 0 || l.seats[at] != nil {
				return apiError{http.StatusConflict, "Nincs szabad hely."}
			}
			if ok, wait := l.robots.allow(robotFlood, now); !ok {
				return tooFast(wait)
			}
			l.seats[at] = &player{name: l.robotName(), robot: true, online: true}
			l.addChat(seat, l.seats[at].name, "robot", now)
		} else {
			p := l.seats[req.Seat]
			if req.Seat == 0 || p == nil || !p.robot {
				return nil // már nincs ott robot
			}
			if ok, wait := l.robots.allow(robotFlood, now); !ok {
				return tooFast(wait)
			}
			l.addChat(seat, p.name, "unrobot", now)
			l.seats[req.Seat] = nil
		}
		l.broadcastState()
		return nil
	})
}

func (s *server) handleLeave(w http.ResponseWriter, r *http.Request) {
	var req tokenReq
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	s.withSeat(w, r, &req, func(l *lobby, seat int) error {
		if l.started {
			p := l.seats[seat]
			if !p.left {
				l.addChat(seat, "", "quit", time.Now())
			}
			p.left, p.online = true, false
			for sub := range l.subs {
				if sub.seat == seat {
					l.dropSub(sub)
				}
			}
			l.noteHumans(time.Now())
		} else {
			l.removeSeat(seat)
		}
		l.broadcastState()
		return nil
	})
}

// handleCheck: él-e még a játékos a szobában, és mi a szoba állapota (ugyanaz, mint az eseményfolyam
// "state" üzenete). A kliens ezzel dönti el - élő kapcsolat nyitása nélkül -, hogy visszalépjen-e.
func (s *server) handleCheck(w http.ResponseWriter, r *http.Request) {
	var req tokenReq
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	l := s.lobby(req.Code)
	if l == nil {
		writeErr(w, errNoLobby)
		return
	}
	l.mu.Lock()
	seat := l.seatOf(req.Token)
	if l.closed || seat == 0 || l.seats[seat].left {
		l.mu.Unlock()
		writeErr(w, errBadToken)
		return
	}
	ev := l.stateEvent()
	live := l.seats[seat].conns > 0
	ev.Live = &live
	l.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(ev)
}

// handleChat: csevegőüzenet a szobának (a játék előtt és alatt is). A szöveg egysoros, trágárság és
// link nélkül (server/profanity.go), ugyanazt nem lehet rögtön újra elküldeni, és játékosonként
// korlátozott a gyakorisága (chatFlood: túllépve egy ideig nem írhat). presence: "left" (a játékos
// kilépett a csevegésből, az X-szel) vagy "back" (visszalépett) - ezt a többiek látják, de ha túl
// sűrűn váltogat (presenceFlood), csak csendben vált.
func (s *server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		tokenReq
		Text     string `json:"text"`
		Presence string `json:"presence"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	text := ""
	switch req.Presence {
	case "left", "back":
	case "":
		var ok bool
		if text, ok = cleanLine(req.Text, maxChatLen); !ok {
			writeErr(w, apiError{http.StatusBadRequest, "Az üzenet legfeljebb 200 betű lehet."})
			return
		}
		if hasLink(text) {
			writeErr(w, apiError{http.StatusBadRequest, "Linket nem lehet küldeni."})
			return
		}
		text = cleanProfanity(capRepeats(text))
	default:
		writeErr(w, apiError{http.StatusBadRequest, "Hibás kérés."})
		return
	}
	s.withSeat(w, r, &req.tokenReq, func(l *lobby, seat int) error {
		now := time.Now()
		p := l.seats[seat]
		if req.Presence != "" {
			if off := req.Presence == "left"; off != p.chatOff {
				p.chatOff = off
				if ok, _ := p.toggles.allow(presenceFlood, now); ok {
					l.addChat(seat, "", req.Presence, now)
				}
			}
			return nil
		}
		if low := strings.ToLower(text); low == p.said && now.Sub(p.saidAt) < chatRepeat {
			return apiError{http.StatusConflict, "Ezt az üzenetet már elküldted."}
		}
		if ok, wait := p.chats.allow(chatFlood, now); !ok {
			return tooFast(wait)
		}
		p.said, p.saidAt = strings.ToLower(text), now
		p.chatOff = false
		l.addChat(seat, text, "", now)
		return nil
	})
}

func (s *server) handleSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		tokenReq
		K int             `json:"k"`
		D json.RawMessage `json:"d"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	s.withSeat(w, r, &req.tokenReq, func(l *lobby, seat int) error {
		switch {
		case !l.started:
			return apiError{http.StatusConflict, "A játék még nem indult el."}
		case req.K < 0 || req.K >= maxDecKey || len(req.D) == 0 || len(req.D) > maxDecSize || req.D[0] != '{':
			return apiError{http.StatusBadRequest, "Hibás lépés."}
		case l.seen[req.K]:
			return nil // valaki már beküldte ezt a döntést: az első számít
		case len(l.decs) >= maxDecisions:
			return apiError{http.StatusConflict, "Túl hosszú játék."}
		}
		d := decision{K: req.K, D: req.D, From: seat}
		l.seen[req.K] = true
		l.decs = append(l.decs, d)
		b, _ := json.Marshal(struct {
			Type string `json:"type"`
			decision
		}{"dec", d})
		l.broadcast(b)
		return nil
	})
}

// handleEvents: Server-Sent Events folyam. Csatlakozáskor a szoba teljes állapotát és a játék
// összes eddigi döntését küldi, utána az újakat.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	l := s.lobby(q.Get("code"))
	if l == nil {
		writeErr(w, errNoLobby)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, errors.New("no flusher"))
		return
	}
	// A ReadTimeout határideje a válasz alatt is érvényben maradna: lejártakor a szerver megszakítja
	// a kérést, és az eseményfolyam 20 mp-enként újraindulna. Az eseményfolyamra nem vonatkozik.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	l.mu.Lock()
	seat := l.seatOf(q.Get("token"))
	if l.closed || seat == 0 || l.seats[seat].left {
		l.mu.Unlock()
		writeErr(w, errBadToken)
		return
	}
	ip := s.clientIP(r)
	if len(l.subs) >= maxStreamsPerRoom || !s.streams.open(ip) {
		l.mu.Unlock()
		writeErr(w, errBusy)
		return
	}
	defer s.streams.close(ip)
	sub := &subscriber{seat: seat, ch: make(chan []byte, 256), kick: make(chan struct{})}
	p := l.seats[seat]
	p.conns++
	p.gen++
	wasOnline := p.online
	p.online = true
	l.noteHumans(time.Now())
	if !wasOnline {
		l.broadcastState() // a többieknek; az új kliens a pillanatképet kapja
	}
	if p.dropped && l.started {
		p.dropped = false
		l.addChat(seat, "", "rejoin", time.Now())
	}
	l.subs[sub] = struct{}{}
	snapshot := [][]byte{l.state()}
	for _, m := range l.chat {
		snapshot = append(snapshot, chatEvent(m, true))
	}
	// a döntések: a futó játékéi, vagy (a játék után várakozó szobában) annak, aki abban játszott, hogy
	// egy lemaradt gép befejezhesse; az új belépőnek nem kellenek
	decs := l.decs
	if !l.started && p.played != l.game {
		decs = nil
	}
	for _, d := range decs {
		b, _ := json.Marshal(struct {
			Type string `json:"type"`
			decision
		}{"dec", d})
		snapshot = append(snapshot, b)
	}
	l.mu.Unlock()

	defer s.disconnect(l, sub, p)

	// Egy írás legfeljebb ennyi ideig akadhat el: egy eltűnt kliens (alvó telefon) kapcsolata így
	// hamarabb lezárul, és a türelmi idő után távollévő lesz (helyette a házigazda lép).
	deadline := func() { _ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout)) }
	deadline()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	for _, b := range snapshot {
		if !writeEvent(w, b) {
			return
		}
	}
	flusher.Flush()

	// A ping adatüzenet (nem megjegyzés), hogy a kliens lássa, él-e a kapcsolat (js/hf.js: watchStream).
	ping := time.NewTicker(ssePing)
	defer ping.Stop()
	for {
		select {
		case b := <-sub.ch:
			deadline()
			if !writeEvent(w, b) {
				return
			}
			// ami még várakozik, menjen ki egyben
			for n := len(sub.ch); n > 0; n-- {
				if !writeEvent(w, <-sub.ch) {
					return
				}
			}
			flusher.Flush()
		case <-ping.C:
			deadline()
			if !writeEvent(w, pingEvent) {
				return
			}
			flusher.Flush()
		case <-sub.kick:
			// ami még a sorban van (pl. hogy a szoba megszűnt), menjen ki
			for n := len(sub.ch); n > 0; n-- {
				writeEvent(w, <-sub.ch)
			}
			flusher.Flush()
			return
		case <-r.Context().Done():
			return
		}
	}
}

var pingEvent = []byte(`{"type":"ping"}`)

func writeEvent(w io.Writer, b []byte) bool {
	_, err := io.WriteString(w, "data: "+string(b)+"\n\n")
	return err == nil
}

func (s *server) disconnect(l *lobby, sub *subscriber, p *player) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.subs[sub]; ok {
		delete(l.subs, sub)
	}
	p.conns--
	if p.conns > 0 {
		return
	}
	if l.totalConns() == 0 {
		l.idleSince = time.Now()
	}
	s.goneAfterGrace(l, p)
}

// goneAfterGrace: ha p a türelmi idő alatt sem csatlakozik (újra), távollévőnek számít, és a játék
// indulása előtt a helye is felszabadul. A belépéskor is elindul: aki belép, de az eseményfolyamát
// meg sem nyitja (pl. rögtön bezárja a lapot), ne foglalja a helyet. A hívó tartja a zárat.
func (s *server) goneAfterGrace(l *lobby, p *player) {
	gen := p.gen
	time.AfterFunc(offlineGrace, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.closed || p.conns > 0 || p.gen != gen {
			return
		}
		changed := p.online
		p.online = false
		for seat := 1; seat <= maxPlayers; seat++ {
			switch {
			case l.seats[seat] != p:
			case !l.started:
				l.removeSeat(seat)
				changed = true
			case changed && !p.left:
				p.dropped = true // a többiek látják; ha visszajön, azt is (handleEvents)
				l.addChat(seat, "", "lost", time.Now())
			}
		}
		l.noteHumans(time.Now())
		if changed {
			l.broadcastState()
		}
	})
}

// cleanup: megszünteti az elhagyott és a nagyon régi szobákat, és azokat, ahol robotsOnlyTTL óta csak
// robotok játszanak.
func (s *server) cleanup(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for code, l := range s.lobbies {
		l.mu.Lock()
		l.noteHumans(now)
		botsOnly := !l.botsSince.IsZero() && now.Sub(l.botsSince) >= robotsOnlyTTL
		dead := l.closed || l.humans() == 0 || botsOnly ||
			(l.totalConns() == 0 && now.Sub(l.idleSince) > idleLobbyTTL) ||
			now.Sub(l.created) > maxLobbyAge
		if botsOnly && !l.closed {
			log.Printf("szoba %s: csak robotok maradtak a játékban, bezárva", code)
		}
		if dead {
			l.closed = true
			for sub := range l.subs {
				l.dropSub(sub)
			}
			delete(s.lobbies, code)
			delete(s.public, code)
			if s.roomsByIP[l.ip]--; s.roomsByIP[l.ip] <= 0 {
				delete(s.roomsByIP, l.ip)
			}
		}
		l.mu.Unlock()
	}
}

func (s *server) lobbyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.lobbies)
}

// ------------------------------------------------------------------------------------- visszajelzés

func (s *server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Text string `json:"text"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	text := strings.TrimSpace(req.Text)
	if !utf8.ValidString(text) || text == "" || utf8.RuneCountInString(text) > 2000 {
		writeErr(w, apiError{http.StatusBadRequest, "Írj pár szót (legfeljebb 2000 karakter)."})
		return
	}
	name, ok := cleanName(req.Name)
	if !ok {
		name = ""
	}
	ip, now := s.clientIP(r), time.Now()
	s.fbMu.Lock()
	defer s.fbMu.Unlock()
	if last, ok := s.fbLast[ip]; ok && now.Sub(last) < 15*time.Second {
		writeErr(w, apiError{http.StatusTooManyRequests, "Egy kicsit várj a következő üzenettel."})
		return
	}
	if st, err := os.Stat(s.feedbackPath); (err == nil && st.Size() > maxFeedbackBytes) || !s.feedback.allow("") {
		writeErr(w, apiError{http.StatusServiceUnavailable, "Most nem tudunk több üzenetet fogadni, próbáld később."})
		return
	}
	for k, t := range s.fbLast {
		if now.Sub(t) > time.Minute {
			delete(s.fbLast, k)
		}
	}
	s.fbLast[ip] = now
	line, _ := json.Marshal(map[string]string{"time": now.Format(time.RFC3339), "name": name, "text": text})
	f, err := os.OpenFile(s.feedbackPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = f.Write(append(line, '\n'))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		log.Printf("visszajelzés mentése: %v", err)
		writeErr(w, err)
		return
	}
	log.Printf("visszajelzés érkezett (%d karakter)", utf8.RuneCountInString(text))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ------------------------------------------------------------------------------------- fájlok

var publicPrefixes = []string{"/js/", "/swf/", "/vendor/ruffle/", "/sounds/", "/dat/", "/img/"}
var gamemodeRe = regexp.MustCompile(`^[0-9]$`)

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.serveStatic {
		http.NotFound(w, r) // csak API: a játék fájljai máshonnan jönnek (pl. Cloudflare Pages)
		return
	}
	p := path.Clean(r.URL.Path)
	switch {
	case p == "/":
		p = "/index.html"
	case p == "/gettgquestions.php": // a próbajáték régi kérdés-végpontja
		p = "/questions.xml"
	case p == "/quickgame.php": // a régi menü gombjai ide vittek
		target := "/"
		if gm := r.URL.Query().Get("gamemode"); gamemodeRe.MatchString(gm) {
			target = "/?gamemode=" + gm
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	case p == "/sounds/manifest.json":
		s.serveSoundManifest(w, r)
		return
	case p == "/registration.php":
		http.Redirect(w, r, "/", http.StatusFound)
		return
	case strings.HasPrefix(p, "/clientquery_"):
		w.WriteHeader(http.StatusOK)
		return
	}
	allowed := p == "/index.html" || p == "/questions.xml" || p == "/favicon.ico"
	for _, pre := range publicPrefixes {
		allowed = allowed || strings.HasPrefix(p, pre)
	}
	// Windowson a "\" is elválasztó: a "/js/..\..\x" a path.Clean után is kivezetne a mappából
	rel := filepath.FromSlash(strings.TrimPrefix(p, "/"))
	if !allowed || strings.ContainsRune(p, '\\') || !filepath.IsLocal(rel) {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.root, rel)
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, r, full, st)
}

// handleHealth: üzemfigyeléshez (pl. uptime-monitor): él-e a szerver, hány szoba van nyitva.
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "lobbies": s.lobbyCount()})
}

// cors: ha a játék oldala más címről jön (pl. Cloudflare Pages), a böngésző csak akkor engedi az
// API-hívásokat, ha a szerver ezt a címet (-origins) visszaigazolja.
func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && strings.HasPrefix(r.URL.Path, "/api/") && (s.anyOrigin || s.origins[o]) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", o)
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST")
				h.Set("Access-Control-Allow-Headers", "Content-Type")
				h.Set("Access-Control-Max-Age", "86400")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/create", s.handleCreate)
	mux.HandleFunc("POST /api/join", s.handleJoin)
	mux.HandleFunc("POST /api/start", s.handleStart)
	mux.HandleFunc("POST /api/again", s.handleAgain)
	mux.HandleFunc("POST /api/map", s.handleMap)
	mux.HandleFunc("POST /api/long", s.handleLong)
	mux.HandleFunc("POST /api/cats", s.handleCats)
	mux.HandleFunc("POST /api/robot", s.handleRobot)
	mux.HandleFunc("POST /api/progress", s.handleProgress)
	mux.HandleFunc("POST /api/leave", s.handleLeave)
	mux.HandleFunc("POST /api/check", s.handleCheck)
	mux.HandleFunc("POST /api/send", s.handleSend)
	mux.HandleFunc("POST /api/chat", s.handleChat)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/hub", s.handleHub)
	mux.HandleFunc("POST /api/feedback", s.handleFeedback)
	mux.HandleFunc("/", s.handleStatic)
	return s.cors(s.limitAPI(mux))
}

func main() {
	addr := flag.String("addr", ":8765", "listen address")
	root := flag.String("root", ".", "the project directory (index.html, swf/, vendor/ruffle/ ...)")
	feedback := flag.String("feedback", "feedback.jsonl", "file the feedback messages are appended to")
	static := flag.Bool("static", true, "serve the game files too; false = API only (the game is hosted elsewhere)")
	origins := flag.String("origins", "", "comma-separated page origins allowed to call the API (CORS), e.g. https://honfoglalo.pages.dev; * = any")
	trustProxy := flag.Bool("trust-proxy", false, "behind a reverse proxy: take the client IP from CF-Connecting-IP / X-Forwarded-For")
	flag.Parse()

	s := newServer(*root, *feedback)
	s.serveStatic = *static
	s.trustProxy = *trustProxy
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o == "*" {
			s.anyOrigin = true
		} else if o != "" {
			s.origins[o] = true
		}
	}
	if s.serveStatic {
		if _, err := os.Stat(filepath.Join(*root, "index.html")); err != nil {
			log.Fatalf("nincs index.html itt: %s (a projekt mappájából indítsd, add meg: -root, vagy csak API: -static=false)", *root)
		}
		mime.AddExtensionType(".swf", "application/x-shockwave-flash")
		mime.AddExtensionType(".wasm", "application/wasm")
		go s.gz.warm(filepath.Join(*root, "vendor", "ruffle"))
	}
	go func() {
		for now := range time.Tick(15 * time.Second) {
			s.cleanup(now)
		}
	}()
	srv := &http.Server{
		Addr:              *addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second, // a kérés beolvasása; az eseményfolyam feloldja (handleEvents)
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
	}
	if s.serveStatic {
		log.Printf("Honfoglaló: http://localhost%s/", portOf(*addr))
	} else {
		log.Printf("Honfoglaló szerver (csak API) itt: %s", *addr)
	}
	log.Fatal(srv.ListenAndServe())
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ""
}
