// Terhelésteszt a publikus várókra: virtuális játékosok, mint a valódi kliens (js/hf.js).
//
//	go run ./server -addr 127.0.0.1:8792 -static=false -trust-proxy
//	go run ./tools/loadtest -url http://127.0.0.1:8792 -players 1000 -ramp 20s -dur 60s -pid <a szerver folyamata>
//
// A szerver -trust-proxy-val fusson: így minden játékos saját IP-ről jön (X-Forwarded-For), és az
// IP-nkénti korlátok úgy működnek, mint élesben.
//
// Minden játékos saját (X-Forwarded-For) IP-ről és böngészőazonosítóval jön; 6 mp-enként lekérdezi a
// Várókat; a lista alapján belép egy szobába (vagy nyit egyet); eseményfolyamot tart nyitva; a főnök
// indít; játék közben döntéseket és csevegést küld; a játék végén vissza a váróba vagy kilép.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	base      = flag.String("url", "http://127.0.0.1:8792", "szerver")
	nPlayers  = flag.Int("players", 300, "virtuális játékosok")
	ramp      = flag.Duration("ramp", 20*time.Second, "felfutás")
	dur       = flag.Duration("dur", 60*time.Second, "mérés a felfutás után")
	decEvery  = flag.Duration("dec", 4*time.Second, "döntés játékosonként (játék közben)")
	chatEvery = flag.Duration("chat", 20*time.Second, "csevegés játékosonként")
	gameLen   = flag.Duration("game", 40*time.Second, "egy játék hossza")
	serverPID = flag.Int("pid", 0, "a szerver folyamata (CPU, memória)")
)

// ---------------------------------------------------------------- mérés

type stats struct {
	mu      sync.Mutex
	lat     map[string][]time.Duration
	status  map[string]map[int]int
	neterr  map[string]int
	deliver map[string][]time.Duration
	bytes   map[string]int64
}

var st = stats{lat: map[string][]time.Duration{}, status: map[string]map[int]int{}, neterr: map[string]int{}, deliver: map[string][]time.Duration{}, bytes: map[string]int64{}}
var sseBytes atomic.Int64
var measuring atomic.Bool

func (s *stats) req(ep string, d time.Duration, code int, err error) {
	if !measuring.Load() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.neterr[ep]++
		return
	}
	s.lat[ep] = append(s.lat[ep], d)
	if s.status[ep] == nil {
		s.status[ep] = map[int]int{}
	}
	s.status[ep][code]++
}

func (s *stats) del(kind string, d time.Duration) {
	if !measuring.Load() {
		return
	}
	s.mu.Lock()
	s.deliver[kind] = append(s.deliver[kind], d)
	s.mu.Unlock()
}

var (
	streamsOpen, streamsFail, closedEv atomic.Int64
	decSent, decEcho, chatSent         atomic.Int64
	orderViol                          atomic.Int64
	gamesStarted                       atomic.Int64
)

// ---------------------------------------------------------------- HTTP

// a tömörítést magunk kezeljük, hogy a valódi (átküldött) bájtokat mérjük
var hc = &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{MaxIdleConns: 20000, MaxIdleConnsPerHost: 20000, IdleConnTimeout: 60 * time.Second, DisableCompression: true}}
var sseClient = &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 0, DisableKeepAlives: true}}

type player struct {
	id               int
	ip, client, name string

	mu      sync.Mutex
	hub     hubReply
	code    string
	token   string
	seat    int
	state   stateEv
	cancel  context.CancelFunc
	keys    []int // az aktuális játék döntéseinek sorrendje (ahogy megérkeztek)
	gameKey string
}

// a Várók egy szobája (server/hub.go); a "full" és a "playing" szobába nem lehet belépni
type hubRoom struct {
	Code    string `json:"code"`
	Status  string `json:"status"`
	Players []struct {
		Name string `json:"name"`
	} `json:"players"`
}
type hubReply struct {
	Online  int       `json:"online"`
	V       int       `json:"v"`
	Same    bool      `json:"same"`
	Lobbies []hubRoom `json:"lobbies"`
}
type stateEv struct {
	Started bool `json:"started"`
	Boss    int  `json:"boss"`
	Game    int  `json:"game"`
	Players []struct {
		Seat int `json:"seat"`
	} `json:"players"`
}

func (p *player) do(method, ep, path string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, *base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", p.ip)
	req.Header.Set("Accept-Encoding", "gzip")
	t := time.Now()
	resp, err := hc.Do(req)
	if err != nil {
		st.req(ep, 0, 0, err)
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	st.req(ep, time.Since(t), resp.StatusCode, nil)
	if measuring.Load() {
		st.mu.Lock()
		st.bytes[ep] += int64(len(b))
		st.mu.Unlock()
	}
	if resp.Header.Get("Content-Encoding") == "gzip" {
		if zr, err := gzip.NewReader(bytes.NewReader(b)); err == nil {
			b, _ = io.ReadAll(zr)
		}
	}
	if out != nil {
		json.Unmarshal(b, out)
	}
	return resp.StatusCode, nil
}

// ---------------------------------------------------------------- játékos

func (p *player) hubLoop(ctx context.Context) {
	for {
		var h hubReply
		p.mu.Lock()
		v := p.hub.V
		p.mu.Unlock()
		if code, err := p.do("GET", "hub", fmt.Sprintf("/api/hub?id=%s&v=%d", p.client, v), nil, &h); err == nil && code == 200 {
			p.mu.Lock()
			if h.Same {
				h.Lobbies = p.hub.Lobbies
			}
			p.hub = h
			p.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(6*time.Second + time.Duration(rand.Intn(800))*time.Millisecond):
		}
	}
}

// szoba keresése a listában (vagy új nyitása)
func (p *player) enter(ctx context.Context) bool {
	for tries := 0; ctx.Err() == nil; tries++ {
		p.mu.Lock()
		rooms := append([]hubRoom(nil), p.hub.Lobbies...)
		p.mu.Unlock()
		var free []hubRoom
		for _, r := range rooms {
			if r.Status == "open" {
				free = append(free, r)
			}
		}
		var out struct {
			Code  string `json:"code"`
			Token string `json:"token"`
			Seat  int    `json:"seat"`
		}
		if len(free) > 0 && rand.Intn(10) < 8 {
			r := free[rand.Intn(len(free))]
			code, _ := p.do("POST", "join", "/api/join", map[string]string{"code": r.Code, "name": p.name}, &out)
			if code == 200 {
				p.setRoom(out.Code, out.Token, out.Seat)
				return true
			}
		} else {
			code, _ := p.do("POST", "create", "/api/create", map[string]any{"name": p.name, "public": true, "client": p.client}, &out)
			if code == 200 {
				p.setRoom(out.Code, out.Token, out.Seat)
				return true
			}
		}
		time.Sleep(time.Duration(500+rand.Intn(1500)) * time.Millisecond)
	}
	return false
}

func (p *player) setRoom(code, token string, seat int) {
	p.mu.Lock()
	p.code, p.token, p.seat = code, token, seat
	p.state = stateEv{}
	p.mu.Unlock()
}

// eseményfolyam: állapot, döntések (késés, sorrend), csevegés
func (p *player) stream(ctx context.Context, code, token string) {
	req, _ := http.NewRequestWithContext(ctx, "GET", *base+"/api/events?code="+code+"&token="+token, nil)
	req.Header.Set("X-Forwarded-For", p.ip)
	resp, err := sseClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		streamsFail.Add(1)
		if resp != nil {
			resp.Body.Close()
		}
		return
	}
	streamsOpen.Add(1)
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if measuring.Load() {
			sseBytes.Add(int64(len(line) + 1))
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev struct {
			Type string          `json:"type"`
			K    int             `json:"k"`
			From int             `json:"from"`
			D    json.RawMessage `json:"d"`
			Old  bool            `json:"old"`
			Text string          `json:"text"`
			stateEv
		}
		if json.Unmarshal([]byte(line[6:]), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "state":
			p.mu.Lock()
			if ev.Game != p.state.Game {
				p.keys = nil
				p.gameKey = fmt.Sprintf("%s#%d", code, ev.Game)
			}
			p.state = ev.stateEv
			p.mu.Unlock()
		case "dec":
			var d struct {
				TS int64 `json:"ts"`
			}
			json.Unmarshal(ev.D, &d)
			lat := time.Since(time.Unix(0, d.TS))
			p.mu.Lock()
			mine := ev.From == p.seat
			p.keys = append(p.keys, ev.K)
			p.mu.Unlock()
			if mine {
				decEcho.Add(1)
				st.del("dec_echo", lat)
			} else {
				st.del("dec_other", lat)
			}
		case "chat":
			if !ev.Old && strings.HasPrefix(ev.Text, "t") {
				var ns int64
				fmt.Sscanf(ev.Text, "t%d", &ns)
				st.del("chat", time.Since(time.Unix(0, ns)))
			}
		case "closed":
			closedEv.Add(1)
			return
		}
	}
}

var orders sync.Map // gameKey -> [][]int (játékosonként a döntések sorrendje)

func (p *player) recordOrder() {
	p.mu.Lock()
	k, keys := p.gameKey, append([]int(nil), p.keys...)
	p.mu.Unlock()
	if k == "" || len(keys) == 0 {
		return
	}
	v, _ := orders.LoadOrStore(k, &[][]int{})
	lst := v.(*[][]int)
	*lst = append(*lst, keys)
}

func (p *player) session(ctx context.Context) {
	p.mu.Lock()
	code, token, seat := p.code, p.token, p.seat
	p.mu.Unlock()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go p.stream(sctx, code, token)
	waitStart := time.Now()
	n := 0
	var gameStart time.Time
	nextDec := time.Now()
	nextChat := time.Now().Add(time.Duration(rand.Int63n(int64(*chatEvery))))
	tok := map[string]string{"code": code, "token": token}
	for ctx.Err() == nil {
		time.Sleep(200 * time.Millisecond)
		p.mu.Lock()
		s := p.state
		p.mu.Unlock()
		if !s.Started {
			// a főnök indít: 3 játékossal rögtön, 2-vel 15 mp után
			if s.Boss == seat && (len(s.Players) == 3 || (len(s.Players) == 2 && time.Since(waitStart) > 15*time.Second)) {
				if code, _ := p.do("POST", "start", "/api/start", tok, nil); code == 200 {
					gamesStarted.Add(1)
				}
			}
			if time.Since(waitStart) > 60*time.Second {
				p.do("POST", "leave", "/api/leave", tok, nil)
				return
			}
			gameStart = time.Time{}
			continue
		}
		if gameStart.IsZero() {
			gameStart = time.Now()
			n = 0
		}
		if time.Now().After(nextDec) {
			n++
			k := n*4 + seat
			d := map[string]any{"v": n, "ts": time.Now().UnixNano()}
			if code, _ := p.do("POST", "send", "/api/send", map[string]any{"code": code, "token": token, "k": k, "d": d}, nil); code == 200 {
				decSent.Add(1)
			}
			nextDec = time.Now().Add(*decEvery/2 + time.Duration(rand.Int63n(int64(*decEvery))))
		}
		if time.Now().After(nextChat) {
			text := fmt.Sprintf("t%d", time.Now().UnixNano())
			if code, _ := p.do("POST", "chat", "/api/chat", map[string]string{"code": code, "token": token, "text": text}, nil); code == 200 {
				chatSent.Add(1)
			}
			nextChat = time.Now().Add(*chatEvery/2 + time.Duration(rand.Int63n(int64(*chatEvery))))
		}
		if time.Since(gameStart) > *gameLen {
			time.Sleep(time.Second) // az utolsó döntések visszaérnek
			p.recordOrder()
			if rand.Intn(10) < 7 {
				p.do("POST", "again", "/api/again", tok, nil)
				waitStart = time.Now()
				gameStart = time.Time{}
				continue
			}
			p.do("POST", "leave", "/api/leave", tok, nil)
			return
		}
	}
}

func (p *player) run(ctx context.Context) {
	go p.hubLoop(ctx)
	time.Sleep(time.Duration(rand.Intn(1500)) * time.Millisecond)
	for ctx.Err() == nil {
		if !p.enter(ctx) {
			return
		}
		p.session(ctx)
	}
}

// ---------------------------------------------------------------- kiértékelés

func pct(ds []time.Duration, q float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return ds[int(float64(len(ds)-1)*q)]
}

func procStat(pid int) (cpuTicks int64, rssKB int64, fds int) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return
	}
	f := strings.Fields(string(b)[strings.LastIndex(string(b), ")")+2:])
	var ut, stt int64
	fmt.Sscan(f[11], &ut)
	fmt.Sscan(f[12], &stt)
	cpuTicks = ut + stt
	s, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	for _, l := range strings.Split(string(s), "\n") {
		if strings.HasPrefix(l, "VmRSS:") {
			fmt.Sscan(strings.TrimSpace(l[6:]), &rssKB)
		}
	}
	ents, _ := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	return cpuTicks, rssKB, len(ents)
}

func main() {
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	ps := make([]*player, *nPlayers)
	for i := range ps {
		ps[i] = &player{id: i, ip: fmt.Sprintf("10.%d.%d.%d", 1+i/65025, (i/255)%255, 1+i%255),
			client: fmt.Sprintf("load%08d", i), name: fmt.Sprintf("Játékos%d", i)}
	}
	start := time.Now()
	var wg sync.WaitGroup
	for i, p := range ps {
		time.Sleep(time.Duration(int64(*ramp) / int64(len(ps))))
		_ = i
		wg.Add(1)
		go func(p *player) { defer wg.Done(); p.run(ctx) }(p)
	}
	fmt.Printf("felfutás kész: %d játékos, %.0f mp\n", len(ps), time.Since(start).Seconds())
	time.Sleep(10 * time.Second) // beáll
	measuring.Store(true)
	c0, _, _ := procStat(*serverPID)
	t0 := time.Now()
	time.Sleep(*dur)
	c1, rss, fds := procStat(*serverPID)
	el := time.Since(t0).Seconds()
	measuring.Store(false)

	// online és szobák a végén (egy játékos szemével)
	var h hubReply
	ps[0].do("GET", "hub", "/api/hub?id="+ps[0].client, nil, &h)
	cancel()

	fmt.Printf("\n== %d játékos, mérés %.0f mp ==\n", len(ps), el)
	fmt.Printf("szerver: CPU %.0f%% (egy mag = 100%%), memória %.0f MB, nyitott fájlleírók %d\n",
		float64(c1-c0)/100/el*100, float64(rss)/1024, fds)
	fmt.Printf("Várók: online %d, publikus szobák a listában %d\n", h.Online, len(h.Lobbies))
	st.mu.Lock()
	eps := []string{"hub", "create", "join", "start", "send", "chat", "again", "leave"}
	fmt.Printf("%-7s %8s %7s %7s %7s %7s   státuszok\n", "végpont", "db/mp", "p50", "p95", "p99", "max")
	for _, ep := range eps {
		ds := st.lat[ep]
		fmt.Printf("%-7s %8.1f %7s %7s %7s %7s   %v neterr=%d\n", ep, float64(len(ds))/el,
			pct(ds, .5).Round(time.Millisecond), pct(ds, .95).Round(time.Millisecond), pct(ds, .99).Round(time.Millisecond),
			pct(ds, 1).Round(time.Millisecond), st.status[ep], st.neterr[ep])
	}
	for _, k := range []string{"dec_echo", "dec_other", "chat"} {
		ds := st.deliver[k]
		fmt.Printf("kézbesítés %-9s %7d db  p50 %v  p95 %v  p99 %v  max %v\n", k, len(ds),
			pct(ds, .5).Round(time.Millisecond), pct(ds, .95).Round(time.Millisecond), pct(ds, .99).Round(time.Millisecond), pct(ds, 1).Round(time.Millisecond))
	}
	st.mu.Unlock()
	var tot int64
	for _, b := range st.bytes {
		tot += b
	}
	fmt.Printf("forgalom (válaszok törzse): Várók %.0f KB/s, összes API %.0f KB/s, eseményfolyamok %.0f KB/s\n",
		float64(st.bytes["hub"])/1024/el, float64(tot)/1024/el, float64(sseBytes.Load())/1024/el)
	fmt.Printf("eseményfolyamok: megnyitva %d, sikertelen %d, szoba megszűnt üzenet %d\n", streamsOpen.Load(), streamsFail.Load(), closedEv.Load())
	fmt.Printf("döntések: elküldve %d, saját visszaérkezett %d; csevegés elküldve %d; elindított játékok %d\n",
		decSent.Load(), decEcho.Load(), chatSent.Load(), gamesStarted.Load())
	// sorrend: egy játékban mindenki ugyanabban a sorrendben kapta-e (a rövidebb a hosszabb eleje)
	games, viol := 0, 0
	orders.Range(func(_, v any) bool {
		lst := *(v.(*[][]int))
		games++
		for i := 1; i < len(lst); i++ {
			a, b := lst[0], lst[i]
			if len(b) < len(a) {
				a, b = b, a
			}
			for j := range a {
				if a[j] != b[j] {
					viol++
					break
				}
			}
		}
		return true
	})
	fmt.Printf("sorrend: %d játék összevetve, eltérés %d\n", games, viol)
}
