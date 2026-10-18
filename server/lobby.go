package main

import (
	"encoding/json"
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

	// A lecsatlakozott játékos ennyi idő múlva számít távollévőnek (közben az EventSource újracsatlakozhat).
	offlineGrace = 10 * time.Second
	// Az eseményfolyam pingje (a kliens 35 mp csend után újracsatlakozik), és egy írás leghosszabb ideje.
	ssePing         = 15 * time.Second
	sseWriteTimeout = 30 * time.Second
	// Csevegés: egy üzenet hossza, a szobában megőrzött üzenetek (az újracsatlakozó ezeket kapja meg),
	// és egy játékos legfeljebb chatBurst üzenetet küldhet chatWindow alatt, chatGap szünetekkel.
	maxChatLen  = 200
	chatHistory = 50
	chatBurst   = 8
	chatWindow  = 10 * time.Second
	chatGap     = 700 * time.Millisecond
	// Üres (senki sem csatlakozik) szoba élettartama, és bármely szoba legnagyobb kora.
	idleLobbyTTL = 15 * time.Minute
	maxLobbyAge  = 8 * time.Hour
)

// A szobakódban nincsenek összetéveszthető karakterek (0/O, 1/I).
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
const codeLen = 5

type player struct {
	name    string
	token   string
	conns   int         // élő eseményfolyamok
	online  bool        // így látják a többiek
	left    bool        // kilépett a "Kilépés" gombbal (a játék után)
	gen     int         // a lecsatlakozás-ellenőrzés sorszáma
	chats   []time.Time // a legutóbbi üzenetei (csevegés, korlát)
	chatOff bool        // kilépett a csevegésből (az X-szel)
	played  int         // a szoba hányadik játékában játszott utoljára (lobby.game az indításkor)
}

// Egy csevegőüzenet. Az azonosító szobánként növekszik: a kliens ezzel szűri ki, amit az
// újracsatlakozáskor (a régi üzenetekkel együtt) már egyszer megkapott.
// Sys: rendszerüzenet szöveg nélkül: "joined" (belépett a váróba), "left" (elhagyta a csevegést),
// "back" (visszatért a csevegésbe).
type chatMsg struct {
	ID   int    `json:"id"`
	Seat int    `json:"seat"`
	Name string `json:"name"`
	Text string `json:"text,omitempty"`
	Sys  string `json:"sys,omitempty"`
	Time int64  `json:"t"` // Unix ms
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
	decs      []decision
	seen      map[int]bool
	subs      map[*subscriber]struct{}
	chat      []chatMsg // a legutóbbi chatHistory üzenet
	chatSeq   int
	idleSince time.Time
	closed    bool
}

type playerView struct {
	Seat   int    `json:"seat"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
	Left   bool   `json:"left,omitempty"`
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

// chatAllowed: küldhet-e most p (a hívó tartja a zárat); ha igen, feljegyzi az üzenetet.
func (p *player) chatAllowed(now time.Time) bool {
	recent := p.chats[:0]
	for _, t := range p.chats {
		if now.Sub(t) < chatWindow {
			recent = append(recent, t)
		}
	}
	p.chats = recent
	if len(recent) >= chatBurst || (len(recent) > 0 && now.Sub(recent[len(recent)-1]) < chatGap) {
		return false
	}
	p.chats = append(p.chats, now)
	return true
}

// A hívó tartja a zárat.
func (l *lobby) count() int {
	n := 0
	for s := 1; s <= maxPlayers; s++ {
		if l.seats[s] != nil {
			n++
		}
	}
	return n
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
			ev.Players = append(ev.Players, playerView{Seat: s, Name: p.name, Online: p.online, Left: p.left})
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

// removeSeat: indulás előtt a távozó helye felszabadul; ha a főnök ment el, a következő lesz a főnök.
// Ha a szoba létrehozója megy el (kilép, vagy bezárja a lapot) még az első játék előtt, a szoba megszűnik.
func (l *lobby) removeSeat(seat int) {
	gone := l.seats[seat]
	l.seats[seat] = nil
	if gone != nil && gone == l.creator && l.game == 0 && !l.started {
		l.close("A szoba megszűnt: a létrehozója kilépett.")
		return
	}
	for sub := range l.subs {
		if sub.seat == seat {
			l.dropSub(sub)
		}
	}
	if l.boss == seat {
		l.boss = 0
		for s := 1; s <= maxPlayers; s++ {
			if l.seats[s] != nil {
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
