package main

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

// A "Várók" panel adatai (js/hf.js): a publikus szobák (elöl, ahová most be lehet lépni; utánuk a
// megteltek és a futó játékok), és hányan vannak most a honlapon. A lap pár másodpercenként lekérdezi (GET /api/hub?id=...): nem tart nyitva
// kapcsolatot, mert HTTP/1.1-en a böngésző egy szerverhez legfeljebb 6-ot enged, és a szobák
// eseményfolyamának kell a hely. Az id a böngésző azonosítója (localStorage): ugyanannak a
// böngészőnek több lapja egy játékosnak számít; aki hubPresence-nél régebben kérdezett, az elment.

const (
	hubTick     = time.Second      // a szobalista legfeljebb ilyen régi
	hubPresence = 60 * time.Second // a háttérbe tett lap ritkábban kérdez (js/hf.js: 20 mp, böngészőnként akár 1 perc)
	maxHubRooms = 50               // ennyi szoba fér a listába (a legújabbak)
)

var clientIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,40}$`)

type hub struct {
	mu      sync.Mutex
	seen    map[string]time.Time // böngészőazonosító -> utolsó lekérdezés
	pruned  time.Time
	rooms   []hubRoom
	roomsAt time.Time
	roomsJS string // a lista JSON-ja (a változás figyeléséhez)
	ver     int    // a lista változata: ha a kliensnél ugyanez van, a listát nem küldjük újra
}

// hubRoom: egy publikus szoba a listában. Status: "open" (be lehet lépni), "full" (három ember várja
// az indulást) vagy "playing" (a játék megy; Progress: hol tart).
type hubRoom struct {
	Code     string    `json:"code"`
	Title    string    `json:"title"`
	Players  []hubSeat `json:"players"`
	Map      string    `json:"map"`
	Long     bool      `json:"long"`
	Cats     string    `json:"cats,omitempty"`
	Created  int64     `json:"created"` // Unix ms; a játékok után sem változik (ugyanaz a szoba)
	Status   string    `json:"status"`
	Progress *progress `json:"progress,omitempty"`
}

// hubSeat: egy foglalt hely. Robot: a főnök betette robot; Away: játék közben kilépett vagy elment a
// kapcsolata (helyette robot játszik).
type hubSeat struct {
	Name  string `json:"name"`
	Robot bool   `json:"robot,omitempty"`
	Away  bool   `json:"away,omitempty"`
}

// Same: a kliens a v-vel jelezte, hogy a lista ezen változata már megvan neki, ezért nem küldjük újra
// (így egy lekérdezés pár bájt; sok látogatónál ez a forgalom nagy része). Különben Lobbies a lista
// (üresen elmarad).
type hubReply struct {
	Online  int       `json:"online"`
	V       int       `json:"v"`
	Same    bool      `json:"same,omitempty"`
	Lobbies []hubRoom `json:"lobbies,omitempty"`
}

// publicRooms: a publikus szobák, ahol van ember: elöl azok, ahová be lehet lépni (szabad hely, vagy egy
// robot helyére), utánuk a megteltek és a játékban lévők; mindkét csoportban a legújabb elöl.
func (s *server) publicRooms() []hubRoom {
	s.mu.Lock()
	ls := make([]*lobby, 0, len(s.public))
	for _, l := range s.public {
		ls = append(ls, l)
	}
	s.mu.Unlock()
	sort.Slice(ls, func(i, j int) bool { return ls[i].created.After(ls[j].created) })
	open, busy := []hubRoom{}, []hubRoom{}
	for _, l := range ls {
		l.mu.Lock()
		n := l.humans()
		if !l.closed && n > 0 {
			r := hubRoom{Code: l.code, Title: l.title, Players: []hubSeat{}, Map: l.mapID, Long: l.long, Cats: l.cats, Created: l.created.UnixMilli(), Status: "open"}
			for seat := 1; seat <= maxPlayers; seat++ {
				if p := l.seats[seat]; p != nil {
					r.Players = append(r.Players, hubSeat{Name: p.name, Robot: p.robot, Away: l.started && !p.robot && (p.left || !p.online)})
				}
			}
			switch {
			case l.started:
				r.Status = "playing"
				pr := l.progress
				r.Progress = &pr
			case n >= maxPlayers:
				r.Status = "full"
			}
			if r.Status == "open" {
				open = append(open, r)
			} else {
				busy = append(busy, r)
			}
		}
		l.mu.Unlock()
	}
	out := append(open, busy...)
	if len(out) > maxHubRooms {
		out = out[:maxHubRooms]
	}
	return out
}

// hubState: a lekérdező feljegyzése és a mostani állapot (a szobalista hubTick-ig gyorsítótárazva).
// have: a kliensnél lévő lista változata (0: nincs).
func (s *server) hubState(id string, have int, now time.Time) hubReply {
	s.hub.mu.Lock()
	if s.hub.seen == nil {
		s.hub.seen = map[string]time.Time{}
	}
	s.hub.seen[id] = now
	if now.Sub(s.hub.pruned) > hubTick {
		s.hub.pruned = now
		for k, t := range s.hub.seen {
			if now.Sub(t) > hubPresence {
				delete(s.hub.seen, k)
			}
		}
	}
	online := len(s.hub.seen)
	rooms, fresh := s.hub.rooms, now.Sub(s.hub.roomsAt) <= hubTick && s.hub.rooms != nil
	s.hub.mu.Unlock()
	if !fresh {
		rooms = s.publicRooms()
		js, _ := json.Marshal(rooms)
		s.hub.mu.Lock()
		s.hub.rooms, s.hub.roomsAt = rooms, now
		if string(js) != s.hub.roomsJS {
			s.hub.roomsJS = string(js)
			s.hub.ver++
		}
		s.hub.mu.Unlock()
	}
	s.hub.mu.Lock()
	rooms, ver := s.hub.rooms, s.hub.ver
	s.hub.mu.Unlock()
	if have == ver {
		return hubReply{Online: online, V: ver, Same: true}
	}
	return hubReply{Online: online, V: ver, Lobbies: rooms}
}

func (s *server) handleHub(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !clientIDRe.MatchString(id) {
		id = "ip-" + s.clientIP(r) // azonosító nélkül IP-nként számít
	}
	have, _ := strconv.Atoi(r.URL.Query().Get("v"))
	b, _ := json.Marshal(s.hubState(id, have, time.Now()))
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Add("Vary", "Accept-Encoding")
	// a lista JSON-ja nagyon ismétlődő: tömörítve kb. ötöde (sok látogatónál ez a forgalom nagy része)
	if len(b) > 512 && acceptsGzip(r) {
		h.Set("Content-Encoding", "gzip")
		zw := hubGzip.Get().(*gzip.Writer)
		zw.Reset(w)
		zw.Write(b)
		zw.Close()
		hubGzip.Put(zw)
		return
	}
	w.Write(b)
}

var hubGzip = sync.Pool{New: func() any {
	zw, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return zw
}}
