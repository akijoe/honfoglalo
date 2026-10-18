package main

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Egyszerű védelem a túlterhelés ellen (egy komoly támadás ellen a Cloudflare / a tárhely védelme
// kell, ez a szokásos visszaéléseket fogja meg):
//
//   - szoba létrehozása / csatlakozás: IP-nként percenként maxJoinsPerMinute
//   - egy IP-ről egyszerre legfeljebb maxRoomsPerIP, egy böngészőből maxRoomsPerClient nyitott szoba
//   - API-hívások: IP-nként 10 másodpercenként legfeljebb maxCallsPer10s
//   - eseményfolyamok (élő kapcsolatok): IP-nként maxStreamsPerIP, szobánként maxStreamsPerRoom,
//     összesen maxStreams
//   - visszajelzés: IP-nként 15 mp-enként egy, összesen percenként maxFeedbackPerMinute, és a fájl
//     legfeljebb maxFeedbackBytes
//   - kérés: legfeljebb 8 KB törzs, 16 KB fejléc, 20 mp a beolvasására
const (
	maxJoinsPerMinute    = 30
	maxRoomsPerIP        = 5
	maxRoomsPerClient    = 2
	maxCallsPer10s       = 300
	maxStreamsPerIP      = 20
	maxStreamsPerRoom    = 12
	maxStreams           = 20000
	maxFeedbackPerMinute = 30
	maxFeedbackBytes     = 20 << 20
)

// limiter: kulcsonkénti (IP) számláló rögzített időablakra.
type limiter struct {
	mu     sync.Mutex
	window time.Duration
	max    int
	start  time.Time
	counts map[string]int
}

func newLimiter(window time.Duration, max int) *limiter {
	return &limiter{window: window, max: max, counts: map[string]int{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now := time.Now(); now.Sub(l.start) >= l.window {
		l.start, l.counts = now, map[string]int{}
	}
	l.counts[key]++
	return l.counts[key] <= l.max
}

// streams: az élő eseményfolyamok száma IP-nként és összesen.
type streams struct {
	mu    sync.Mutex
	total int
	byIP  map[string]int
}

// open: helyet foglal egy új folyamnak; ha nincs hely, false.
func (st *streams) open(ip string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.byIP == nil {
		st.byIP = map[string]int{}
	}
	if st.total >= maxStreams || st.byIP[ip] >= maxStreamsPerIP {
		return false
	}
	st.total++
	st.byIP[ip]++
	return true
}

func (st *streams) close(ip string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.total--
	if st.byIP[ip]--; st.byIP[ip] <= 0 {
		delete(st.byIP, ip)
	}
}

var errBusy = apiError{http.StatusTooManyRequests, "Túl sok kérés, várj egy kicsit."}

// limitAPI: az API-hívások száma IP-nként (a fájlokat nem érinti).
func (s *server) limitAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodOptions && !s.calls.allow(s.clientIP(r)) {
			writeErr(w, errBusy)
			return
		}
		next.ServeHTTP(w, r)
	})
}
