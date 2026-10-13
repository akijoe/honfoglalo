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

	roomsByIP map[string]int // nyitott szobák a létrehozó IP-je szerint (s.mu védi)

	fbMu   sync.Mutex
	fbLast map[string]time.Time

	gz gzCache

	joins    *limiter // szoba létrehozása / csatlakozás (server/limits.go)
	calls    *limiter // minden API-hívás
	feedback *limiter // visszajelzés, összesen
	streams  streams
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
)

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
		Name string `json:"name"`
		Map  string `json:"map"`
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
	s.lobbies[code] = l
	s.mu.Unlock()
	log.Printf("szoba %s létrehozva (%d szoba)", code, s.lobbyCount())
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
			if p == nil || !strings.EqualFold(p.name, name) {
				continue
			}
			if p.conns > 0 {
				writeErr(w, apiError{http.StatusConflict, "Ezzel a névvel most is játszik valaki ebben a szobában."})
				return
			}
			p.token = newToken()
			p.left = false
			log.Printf("szoba %s: %d. hely visszalépett névvel", l.code, seat)
			l.broadcastState()
			writeJSON(w, http.StatusOK, joinReply{Code: l.code, Seat: seat, Token: p.token})
			return
		}
		writeErr(w, apiError{http.StatusConflict, "Ebben a szobában már elkezdődött a játék. Visszalépni azzal a névvel lehet, amellyel játszottál."})
		return
	}
	free := 0
	for seat := maxPlayers; seat >= 1; seat-- {
		p := l.seats[seat]
		if p == nil {
			free = seat
		} else if strings.EqualFold(p.name, name) {
			writeErr(w, apiError{http.StatusConflict, "Ez a név már foglalt a szobában."})
			return
		}
	}
	if free == 0 {
		writeErr(w, apiError{http.StatusConflict, "A szoba megtelt (legfeljebb 3 játékos)."})
		return
	}
	tok := newToken()
	l.seats[free] = &player{name: name, token: tok}
	l.broadcastState()
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
			return apiError{http.StatusConflict, "Legalább 2 játékos kell az induláshoz."}
		}
		l.started = true
		log.Printf("szoba %s: a játék elindult (%d játékos)", l.code, l.count())
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

func (s *server) handleLeave(w http.ResponseWriter, r *http.Request) {
	var req tokenReq
	if err := readJSON(w, r, &req); err != nil {
		writeErr(w, err)
		return
	}
	s.withSeat(w, r, &req, func(l *lobby, seat int) error {
		if l.started {
			p := l.seats[seat]
			p.left, p.online = true, false
			for sub := range l.subs {
				if sub.seat == seat {
					l.dropSub(sub)
				}
			}
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
	if !wasOnline {
		l.broadcastState() // a többieknek; az új kliens a pillanatképet kapja
	}
	l.subs[sub] = struct{}{}
	snapshot := [][]byte{l.state()}
	for _, d := range l.decs {
		b, _ := json.Marshal(struct {
			Type string `json:"type"`
			decision
		}{"dec", d})
		snapshot = append(snapshot, b)
	}
	l.mu.Unlock()

	defer s.disconnect(l, sub, p)

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

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case b := <-sub.ch:
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
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-sub.kick:
			return
		case <-r.Context().Done():
			return
		}
	}
}

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
	gen := p.gen
	time.AfterFunc(offlineGrace, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.closed || p.conns > 0 || p.gen != gen || !p.online {
			return
		}
		p.online = false
		if !l.started {
			for seat := 1; seat <= maxPlayers; seat++ {
				if l.seats[seat] == p {
					l.removeSeat(seat)
				}
			}
		}
		l.broadcastState()
	})
}

// cleanup: megszünteti az elhagyott és a nagyon régi szobákat.
func (s *server) cleanup(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for code, l := range s.lobbies {
		l.mu.Lock()
		dead := l.count() == 0 ||
			(l.totalConns() == 0 && now.Sub(l.idleSince) > idleLobbyTTL) ||
			now.Sub(l.created) > maxLobbyAge
		if dead {
			l.closed = true
			for sub := range l.subs {
				l.dropSub(sub)
			}
			delete(s.lobbies, code)
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
	full := filepath.Join(s.root, filepath.FromSlash(p))
	st, err := os.Stat(full)
	if !allowed || err != nil || st.IsDir() {
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
	mux.HandleFunc("POST /api/map", s.handleMap)
	mux.HandleFunc("POST /api/leave", s.handleLeave)
	mux.HandleFunc("POST /api/check", s.handleCheck)
	mux.HandleFunc("POST /api/send", s.handleSend)
	mux.HandleFunc("GET /api/events", s.handleEvents)
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
		for now := range time.Tick(time.Minute) {
			s.cleanup(now)
		}
	}()
	srv := &http.Server{
		Addr:              *addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second, // csak a kérés beolvasása: az eseményfolyamot nem vágja el
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
