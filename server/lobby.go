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
	maxDecisions = 5000
	maxDecKey    = 1 << 20
	maxDecSize   = 1024

	// A lecsatlakozott játékos ennyi idő múlva számít távollévőnek (közben az EventSource újracsatlakozhat).
	offlineGrace = 10 * time.Second
	// Az eseményfolyam pingje (a kliens 35 mp csend után újracsatlakozik), és egy írás leghosszabb ideje.
	ssePing         = 15 * time.Second
	sseWriteTimeout = 30 * time.Second
	// Üres (senki sem csatlakozik) szoba élettartama, és bármely szoba legnagyobb kora.
	idleLobbyTTL = 15 * time.Minute
	maxLobbyAge  = 8 * time.Hour
)

// A szobakódban nincsenek összetéveszthető karakterek (0/O, 1/I).
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
const codeLen = 5

type player struct {
	name   string
	token  string
	conns  int  // élő eseményfolyamok
	online bool // így látják a többiek
	left   bool // kilépett a "Kilépés" gombbal (a játék után)
	gen    int  // a lecsatlakozás-ellenőrzés sorszáma
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
	mapID     string // a térkép (validMaps), a főnök választja az indulás előtt
	long      bool   // hosszú hadjárat (6 harci kör), a főnök választja az indulás előtt
	started   bool
	decs      []decision
	seen      map[int]bool
	subs      map[*subscriber]struct{}
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

// cleanName: 1–16 karakter, vezérlőkarakterek nélkül, a szóközök összevonva.
func cleanName(s string) (string, bool) {
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
	return s, n >= 1 && n <= maxNameLen
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
	ev := stateEvent{Type: "state", Code: l.code, Started: l.started, Boss: l.boss, Map: l.mapID, Long: l.long, Players: []playerView{}}
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
func (l *lobby) removeSeat(seat int) {
	l.seats[seat] = nil
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

func (l *lobby) totalConns() int {
	n := 0
	for s := 1; s <= maxPlayers; s++ {
		if p := l.seats[s]; p != nil {
			n += p.conns
		}
	}
	return n
}
