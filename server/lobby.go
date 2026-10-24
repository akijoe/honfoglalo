package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxPlayers   = 3
	maxNameLen   = 16
	maxTitleLen  = 28 // a publikus szoba neve
	maxDecisions = 5000
	maxDecKey    = 1 << 20
	maxDecSize   = 1024

	// Az eseményfolyam pingje (a kliens 35 mp csend után újracsatlakozik), és egy írás leghosszabb ideje.
	ssePing         = 15 * time.Second
	sseWriteTimeout = 30 * time.Second
	// Csevegés: egy üzenet hossza, a szobában megőrzött üzenetek (az újracsatlakozó ezeket kapja meg),
	// ugyanaz az üzenet ennyi ideig nem küldhető újra, és egy betű legfeljebb ennyiszer jöhet egymás
	// után ("!!!!!!!!" -> "!!!!"). A gyakoriság: chatFlood és a többi (lent).
	maxChatLen  = 200
	chatHistory = 50
	chatRepeat  = time.Minute
	maxRunes    = 4
	// Ugyanannak a névnek a belépése (és a kilépése) ennyi időn belül csak egyszer kerül a csevegésbe.
	sysRepeat = 10 * time.Second
	// Üres (senki sem csatlakozik) szoba élettartama, és bármely szoba legnagyobb kora.
	idleLobbyTTL = 15 * time.Minute
	maxLobbyAge  = 8 * time.Hour
)

// A lecsatlakozott játékos ennyi idő múlva számít távollévőnek (közben az EventSource újracsatlakozhat).
// (Változó, hogy a teszt rövidebbre vehesse.)
var offlineGrace = 10 * time.Second

// Ha egy játékban ennyi ideje csak robotok vannak (minden ember kilépett, vagy elment a kapcsolata),
// a szoba megszűnik, és vissza sem lehet lépni bele: ne maradjon örökre a Várók listájában.
// (Változó, hogy a teszt rövidebbre vehesse.)
var robotsOnlyTTL = 2 * time.Minute

// A szobakódban nincsenek összetéveszthető karakterek (0/O, 1/I).
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
const codeLen = 5

type player struct {
	name    string
	token   string // robotnak nincs
	robot   bool   // a főnök tette be a váróban: robot játszik a helyén (handleRobot)
	dropped bool   // játék közben elment a kapcsolata (a visszatérését is jelezzük)
	conns   int    // élő eseményfolyamok
	online  bool   // így látják a többiek
	left    bool   // kilépett a "Kilépés" gombbal (a játék után)
	gen     int    // a lecsatlakozás-ellenőrzés sorszáma
	chats   flood  // csevegőüzenetei (chatFlood)
	toggles flood  // kilépés a csevegésből és vissza (presenceFlood)
	said    string // az utolsó üzenete (kisbetűvel) és az ideje: ugyanazt nem küldheti újra
	saidAt  time.Time
	chatOff bool // kilépett a csevegésből (az X-szel)
	played  int  // a szoba hányadik játékában játszott utoljára (lobby.game az indításkor)
}

// Egy csevegőüzenet. Az azonosító szobánként növekszik: a kliens ezzel szűri ki, amit az
// újracsatlakozáskor (a régi üzenetekkel együtt) már egyszer megkapott.
// Sys: rendszerüzenet (a név a játékosé, akiről szól): "joined" (belépett a váróba), "gone" (kilépett a
// váróból), "left" (elhagyta a csevegést), "back" (visszatért a csevegésbe), "quit" (kilépett a
// játékból), "lost" (játék közben elment a kapcsolata), "rejoin" (visszatért a játékba), "robot" és
// "unrobot" (a főnök betett / kivett egy robotot; Text: a robot neve).
type chatMsg struct {
	ID   int    `json:"id"`
	Seat int    `json:"seat"`
	Name string `json:"name"`
	Text string `json:"text,omitempty"`
	Sys  string `json:"sys,omitempty"`
	Time int64  `json:"t"` // Unix ms
}

// progress: a futó játék állása, ahogy a házigazda gépe jelenti (src/mp.as: reportProgress): a játék
// állapota (game.state: 1 bázisok, 3 felosztás, 4 harc, 5 végső tipp, 15 vége) és a köre.
type progress struct {
	State int `json:"state"`
	Round int `json:"round"`
}

// A játék egy döntése (terület, válasz, tipp, kérdés...). A k kulcsra az első beérkezett nyer.
type decision struct {
	K    int             `json:"k"`
	D    json.RawMessage `json:"d"`
	From int             `json:"from"`
}

type subscriber struct {
	seat int
	ch   chan []byte
	kick chan struct{} // bezárva, ha a puffer megtelt: a kliens újracsatlakozik, és friss állapotot kap
}

type lobby struct {
	code    string
	created time.Time
	ip      string // a létrehozó IP-je (szobák száma IP-nként)

	mu        sync.Mutex
	seats     [maxPlayers + 1]*player // 1..3
	boss      int
	mapID     string  // a térkép (validMaps), a főnök választja az indulás előtt
	long      bool    // hosszú hadjárat (6 harci kör), a főnök választja az indulás előtt
	public    bool    // a Várók listájában látszik, kód nélkül is be lehet lépni
	title     string  // a publikus szoba neve
	creator   *player // aki létrehozta: ha az első játék előtt elmegy, a szoba megszűnik
	client    string  // a létrehozó böngészőjének azonosítója (böngészőnként maxRoomsPerClient szoba)
	game      int     // hányadik játék megy (vagy ment) a szobában: az indításkor nő, a döntések újrakezdődnek
	started   bool
	progress  progress // hol tart a futó játék (a Várók listájához; handleProgress)
	decs      []decision
	seen      map[int]bool
	subs      map[*subscriber]struct{}
	chat      []chatMsg // a legutóbbi chatHistory üzenet
	chatSeq   int
	robots    flood                // robot be- és kivétele (robotFlood)
	comings   flood                // belépések és kilépések a csevegésben (comingsFlood)
	announced map[string]time.Time // név + rendszerüzenet -> mikor került a csevegésbe (sysRepeat)
	idleSince time.Time
	botsSince time.Time // mióta nincs ember a játékban (noteHumans, robotsOnlyTTL)
	closed    bool
}

type playerView struct {
	Seat   int    `json:"seat"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
	Left   bool   `json:"left,omitempty"`
	Robot  bool   `json:"robot,omitempty"`
}

type stateEvent struct {
	Type    string       `json:"type"`
	Code    string       `json:"code"`
	Started bool         `json:"started"`
	Boss    int          `json:"boss"`
	Map     string       `json:"map"`
	Long    bool         `json:"long"`
	Game    int          `json:"game"`
	Public  bool         `json:"public,omitempty"`
	Title   string       `json:"title,omitempty"`
	Players []playerView `json:"players"`
	Live    *bool        `json:"live,omitempty"` // csak a /api/check válaszában: a kérdező helyén van-e élő kapcsolat
}

// validMaps: a SWF 13 országtérképe (MAP_XX képkockák); az első az alapértelmezett.
var validMaps = []string{"HU", "FR", "BG", "BE", "RS", "RU", "CZ", "DE", "UA", "RO", "SI", "IT", "SK"}

// cleanMap: érvényes térképkód (kis-nagybetűtől függetlenül), vagy "" ha nem az.
func cleanMap(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	for _, m := range validMaps {
		if s == m {
			return m
		}
	}
	return ""
}

func newLobby(code string, now time.Time) *lobby {
	return &lobby{code: code, created: now, idleSince: now, mapID: validMaps[0], seen: map[int]bool{}, subs: map[*subscriber]struct{}{}}
}

// cleanName: 1–16 karakter, vezérlőkarakterek nélkül, a szóközök összevonva (link és trágár szó
// nélküli-e: checkName).
func cleanName(s string) (string, bool) {
	return cleanLine(s, maxNameLen)
}

// cleanLine: 1–max karakteres egysoros szöveg (név, szobanév, csevegés): vezérlőkarakterek nélkül, a
// szóközök összevonva.
func cleanLine(s string, max int) (string, bool) {
	if !utf8.ValidString(s) {
		return "", false
	}
	s = strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	n := utf8.RuneCountInString(s)
	return s, n >= 1 && n <= max
}

// chatEvent: egy csevegőüzenet az eseményfolyamon (old: a csatlakozáskor küldött korábbi üzenet).
func chatEvent(m chatMsg, old bool) []byte {
	b, _ := json.Marshal(struct {
		Type string `json:"type"`
		chatMsg
		Old bool `json:"old,omitempty"`
	}{"chat", m, old})
	return b
}

// addChat: egy üzenet (vagy sys != "": rendszerüzenet) a szobának. A hívó tartja a zárat.
func (l *lobby) addChat(seat int, text, sys string, now time.Time) chatMsg {
	l.chatSeq++
	m := chatMsg{ID: l.chatSeq, Seat: seat, Name: l.seats[seat].name, Text: text, Sys: sys, Time: now.UnixMilli()}
	l.chat = append(l.chat, m)
	if len(l.chat) > chatHistory {
		l.chat = append([]chatMsg(nil), l.chat[len(l.chat)-chatHistory:]...)
	}
	l.broadcast(chatEvent(m, false))
	return m
}

// floodRule: egy műveletből legfeljebb burst jöhet window alatt, egymás után legalább gap szünettel;
// aki ezt túllépi, mute ideig egyet sem küldhet (0: nincs tiltás, csak a korlát).
type floodRule struct {
	burst       int
	window, gap time.Duration
	mute        time.Duration
}

var (
	chatFlood     = floodRule{burst: 5, window: 15 * time.Second, gap: time.Second, mute: 15 * time.Second}
	presenceFlood = floodRule{burst: 4, window: time.Minute, gap: 3 * time.Second} // túllépve csendben vált
	robotFlood    = floodRule{burst: 4, window: 20 * time.Second, gap: time.Second, mute: 15 * time.Second}
	comingsFlood  = floodRule{burst: 8, window: time.Minute} // túllépve kimarad a csevegésből
)

// flood: egy játékos (vagy szoba) műveleteinek ideje egy floodRule-hoz. A hívó tartja a zárat.
type flood struct {
	times []time.Time
	until time.Time // eddig tiltva (mute)
}

// allow: mehet-e most egy újabb művelet (ha igen, feljegyzi); ha nem, wait: mennyi idő múlva igen.
func (f *flood) allow(r floodRule, now time.Time) (ok bool, wait time.Duration) {
	if now.Before(f.until) {
		return false, f.until.Sub(now)
	}
	recent := f.times[:0]
	for _, t := range f.times {
		if now.Sub(t) < r.window {
			recent = append(recent, t)
		}
	}
	f.times = recent
	switch {
	case len(recent) >= r.burst:
		wait = r.window - now.Sub(recent[0])
		if r.mute > 0 {
			f.until, wait = now.Add(r.mute), r.mute
			f.times = nil // a tiltás után tiszta lappal
		}
		return false, wait
	case len(recent) > 0 && now.Sub(recent[len(recent)-1]) < r.gap:
		return false, r.gap - now.Sub(recent[len(recent)-1])
	}
	f.times = append(f.times, now)
	return true, 0
}

// tooFast: a túl gyakori műveletre adott hiba (a várakozás másodpercben, ha hosszabb egy kicsinél).
func tooFast(wait time.Duration) error {
	if wait < 2*time.Second {
		return apiError{http.StatusTooManyRequests, "Egy kicsit lassabban!"}
	}
	return apiError{http.StatusTooManyRequests, fmt.Sprintf("Túl gyorsan: várj %d másodpercet.", int(wait.Round(time.Second)/time.Second))}
}

// capRepeats: egy betű (vagy jel) legfeljebb maxRunes-szor egymás után. A számjegyek maradnak
// ("1000000" nem lehet "10000").
func capRepeats(s string) string {
	var b strings.Builder
	var last rune
	n := 0
	for _, r := range s {
		if r == last {
			n++
		} else {
			last, n = r, 1
		}
		if n <= maxRunes || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// announce: egy játékos belépése / kilépése (sys) a csevegésbe, ha nem túl sűrű: ugyanaz a név
// sysRepeat-en belül egyszer, és a szobában comingsFlood szerint. A hívó tartja a zárat.
func (l *lobby) announce(seat int, sys string, now time.Time) {
	key := strings.ToLower(l.seats[seat].name) + "|" + sys
	if l.announced == nil {
		l.announced = map[string]time.Time{}
	}
	for k, t := range l.announced {
		if now.Sub(t) >= sysRepeat {
			delete(l.announced, k)
		}
	}
	if _, seen := l.announced[key]; seen {
		return
	}
	if ok, _ := l.comings.allow(comingsFlood, now); !ok {
		return
	}
	l.announced[key] = now
	l.addChat(seat, "", sys, now)
}

// count: a foglalt helyek (emberek és robotok) száma; humans: csak az embereké. A hívó tartja a zárat.
func (l *lobby) count() int {
	n := 0
	for s := 1; s <= maxPlayers; s++ {
		if l.seats[s] != nil {
			n++
		}
	}
	return n
}

// noteHumans: ha a játékban nincs jelen ember (mind kilépett, vagy elment a kapcsolata), feljegyzi,
// mióta (botsSince; cleanup). A hívó tartja a zárat.
func (l *lobby) noteHumans(now time.Time) {
	here := !l.started
	for s := 1; s <= maxPlayers && !here; s++ {
		p := l.seats[s]
		here = p != nil && !p.robot && p.online && !p.left
	}
	switch {
	case here:
		l.botsSince = time.Time{}
	case l.botsSince.IsZero():
		l.botsSince = now
	}
}

func (l *lobby) humans() int {
	n := 0
	for s := 1; s <= maxPlayers; s++ {
		if p := l.seats[s]; p != nil && !p.robot {
			n++
		}
	}
	return n
}

// robotNames: a robotok nevei (a próbajáték két ellenfele, dat/lang.json: tutorial_opponent_1/2).
var robotNames = []string{"Jani", "Éva", "Robi"}

// robotName: az első név, amely még nem foglalt a szobában. A hívó tartja a zárat.
func (l *lobby) robotName() string {
	for _, n := range robotNames {
		if l.nameSeat(n) == 0 {
			return n
		}
	}
	return "Robot"
}

// nameSeat: melyik helyen ül ilyen nevű játékos (kis- és nagybetűtől függetlenül); 0: sehol.
func (l *lobby) nameSeat(name string) int {
	for s := 1; s <= maxPlayers; s++ {
		if p := l.seats[s]; p != nil && strings.EqualFold(p.name, name) {
			return s
		}
	}
	return 0
}

func (l *lobby) seatOf(token string) int {
	if token == "" {
		return 0
	}
	for s := 1; s <= maxPlayers; s++ {
		if p := l.seats[s]; p != nil && p.token == token {
			return s
		}
	}
	return 0
}

func (l *lobby) stateEvent() stateEvent {
	ev := stateEvent{Type: "state", Code: l.code, Started: l.started, Boss: l.boss, Map: l.mapID, Long: l.long,
		Game: l.game, Public: l.public, Title: l.title, Players: []playerView{}}
	for s := 1; s <= maxPlayers; s++ {
		if p := l.seats[s]; p != nil {
			ev.Players = append(ev.Players, playerView{Seat: s, Name: p.name, Online: p.online || p.robot, Left: p.left, Robot: p.robot})
		}
	}
	return ev
}

func (l *lobby) state() []byte {
	b, _ := json.Marshal(l.stateEvent())
	return b
}

func (l *lobby) broadcast(msg []byte) {
	for sub := range l.subs {
		select {
		case sub.ch <- msg:
		default:
			l.dropSub(sub)
		}
	}
}

func (l *lobby) dropSub(sub *subscriber) {
	if _, ok := l.subs[sub]; ok {
		delete(l.subs, sub)
		close(sub.kick)
	}
}

func (l *lobby) broadcastState() {
	l.broadcast(l.state())
}

// removeSeat: indulás előtt a távozó helye felszabadul; ha a főnök ment el, a következő ember lesz a
// főnök. Ha a szoba létrehozója megy el (kilép, vagy bezárja a lapot) még az első játék előtt, a szoba
// megszűnik; egy embert a többiek a csevegésben látnak elmenni.
func (l *lobby) removeSeat(seat int) {
	gone := l.seats[seat]
	if gone != nil && gone == l.creator && l.game == 0 && !l.started {
		l.seats[seat] = nil
		l.close("A szoba megszűnt: a létrehozója kilépett.")
		return
	}
	if gone != nil && !gone.robot {
		l.announce(seat, "gone", time.Now())
	}
	l.seats[seat] = nil
	for sub := range l.subs {
		if sub.seat == seat {
			l.dropSub(sub)
		}
	}
	if l.boss == seat {
		l.boss = 0
		for s := 1; s <= maxPlayers; s++ {
			if p := l.seats[s]; p != nil && !p.robot {
				l.boss = s
				break
			}
		}
	}
}

// creatorIn: a létrehozó még bent van-e (csak az ilyen szoba számít a böngésző korlátjába; ha egy játék
// után kilépett, a szoba a többieké). A hívó tartja a zárat.
func (l *lobby) creatorIn() bool {
	for s := 1; s <= maxPlayers; s++ {
		if p := l.seats[s]; p != nil && p == l.creator && !p.left {
			return true
		}
	}
	return false
}

// close: a szoba megszűnik; a bent lévők üzenetet kapnak (js/hf.js: closed), a szobát a cleanup
// törli. A hívó tartja a zárat.
func (l *lobby) close(msg string) {
	b, _ := json.Marshal(map[string]string{"type": "closed", "msg": msg})
	l.broadcast(b)
	l.closed = true
	for sub := range l.subs {
		l.dropSub(sub)
	}
}

func (l *lobby) totalConns() int {
	n := 0
	for s := 1; s <= maxPlayers; s++ {
		if p := l.seats[s]; p != nil {
			n += p.conns
		}
	}
	return n
}
