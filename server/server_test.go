package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func post(t *testing.T, base, path string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(base+path, "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// events: az SSE folyam üzenetei egy csatornán
func events(t *testing.T, base, code, token string) chan map[string]any {
	t.Helper()
	resp, err := http.Get(base + "/api/events?code=" + code + "&token=" + token)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("events: %v %v", err, resp)
	}
	ch := make(chan map[string]any, 100)
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data: ") {
				var m map[string]any
				json.Unmarshal([]byte(line[6:]), &m)
				ch <- m
			}
		}
		close(ch)
	}()
	return ch
}

func next(t *testing.T, ch chan map[string]any, typ string) map[string]any {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case m := <-ch:
			if m["type"] == typ {
				return m
			}
		case <-timeout:
			t.Fatalf("no %s event", typ)
		}
	}
}

func TestLobbyFlow(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	st, a := post(t, ts.URL, "/api/create", map[string]string{"name": "  Anna  "})
	if st != 200 || a["seat"].(float64) != 1 {
		t.Fatalf("create: %d %v", st, a)
	}
	code, tokA := a["code"].(string), a["token"].(string)
	evA := events(t, ts.URL, code, tokA)
	if m := next(t, evA, "state"); m["boss"].(float64) != 1 || len(m["players"].([]any)) != 1 {
		t.Fatalf("state: %v", m)
	}

	// indítás egyedül nem megy
	if st, _ := post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokA}); st != 409 {
		t.Fatalf("start alone: %d", st)
	}
	// azonos név nem mehet
	if st, _ := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "anna"}); st != 409 {
		t.Fatalf("dup name: %d", st)
	}
	st, b := post(t, ts.URL, "/api/join", map[string]string{"code": strings.ToLower(code), "name": "Béla"})
	if st != 200 || b["seat"].(float64) != 2 {
		t.Fatalf("join: %d %v", st, b)
	}
	tokB := b["token"].(string)
	if m := next(t, evA, "state"); len(m["players"].([]any)) != 2 {
		t.Fatalf("state after join: %v", m)
	}
	// csak a főnök indíthat
	if st, _ := post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokB}); st != 403 {
		t.Fatalf("start by guest: %d", st)
	}
	if st, _ := post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokA}); st != 200 {
		t.Fatalf("start: %d", st)
	}
	if m := next(t, evA, "state"); m["started"] != true {
		t.Fatalf("not started: %v", m)
	}
	// indulás után nem lehet csatlakozni
	if st, _ := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Cili"}); st != 409 {
		t.Fatalf("join after start: %d", st)
	}

	// döntések: kulcsonként az első nyer
	post(t, ts.URL, "/api/send", map[string]any{"code": code, "token": tokB, "k": 6, "d": map[string]int{"v": 3}})
	post(t, ts.URL, "/api/send", map[string]any{"code": code, "token": tokA, "k": 6, "d": map[string]int{"v": 9}})
	post(t, ts.URL, "/api/send", map[string]any{"code": code, "token": tokA, "k": 7, "d": map[string]int{"v": 1}})
	d1, d2 := next(t, evA, "dec"), next(t, evA, "dec")
	if d1["k"].(float64) != 6 || d1["d"].(map[string]any)["v"].(float64) != 3 || d1["from"].(float64) != 2 || d2["k"].(float64) != 7 {
		t.Fatalf("decisions: %v %v", d1, d2)
	}
	// újracsatlakozáskor megjön az egész napló
	evB := events(t, ts.URL, code, tokB)
	next(t, evB, "state")
	if d := next(t, evB, "dec"); d["k"].(float64) != 6 {
		t.Fatalf("replay: %v", d)
	}
	if d := next(t, evB, "dec"); d["k"].(float64) != 7 {
		t.Fatalf("replay: %v", d)
	}
	if st, m := post(t, ts.URL, "/api/check", map[string]string{"code": code, "token": tokB}); st != 200 || m["started"] != true || m["type"] != "state" {
		t.Fatalf("check: %d %v", st, m)
	}
	if st, _ := post(t, ts.URL, "/api/check", map[string]string{"code": "ZZZZZ", "token": tokB}); st != 404 {
		t.Fatalf("check unknown lobby: %d", st)
	}
	// rossz token
	if st, _ := post(t, ts.URL, "/api/send", map[string]any{"code": code, "token": "x", "k": 8, "d": map[string]int{}}); st != 403 {
		t.Fatalf("bad token: %d", st)
	}
}

func TestFeedbackAndStatic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	os.WriteFile(filepath.Join(dir, "questions.xml"), []byte("<q/>"), 0o644)
	os.MkdirAll(filepath.Join(dir, "tools"), 0o755)
	os.WriteFile(filepath.Join(dir, "tools", "secret.py"), []byte("x"), 0o644)
	fb := filepath.Join(dir, "fb.jsonl")
	s := newServer(dir, fb)
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	if st, _ := post(t, ts.URL, "/api/feedback", map[string]string{"name": "A", "text": "  Szuper!  "}); st != 200 {
		t.Fatalf("feedback: %d", st)
	}
	if st, _ := post(t, ts.URL, "/api/feedback", map[string]string{"text": "megint"}); st != 429 {
		t.Fatalf("feedback rate limit: %d", st)
	}
	data, _ := os.ReadFile(fb)
	if !strings.Contains(string(data), `"text":"Szuper!"`) {
		t.Fatalf("feedback file: %s", data)
	}
	for path, want := range map[string]int{"/": 200, "/gettgquestions.php": 200, "/tools/secret.py": 404, "/fb.jsonl": 404, "/../index.html": 200} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, resp.StatusCode, want)
		}
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, _ := c.Get(ts.URL + "/quickgame.php?gamemode=1")
	if loc := resp.Header.Get("Location"); loc != "/?gamemode=1" {
		t.Errorf("quickgame redirect: %q", loc)
	}
}

func TestJoinRateLimit(t *testing.T) {
	dir := t.TempDir()
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()
	limited := false // az ablak váltásakor a számláló újraindul, ezért két ablaknyi keretig próbálkozunk
	for i := 0; i <= 2*maxJoinsPerMinute && !limited; i++ {
		st, _ := post(t, ts.URL, "/api/join", map[string]string{"code": "ZZZZZ", "name": "X"})
		limited = st == 429
	}
	if !limited {
		t.Fatal("no rate limit")
	}
}

func TestRoomsPerIP(t *testing.T) {
	dir := t.TempDir()
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()
	for i := 0; i < maxRoomsPerIP; i++ {
		if st, _ := post(t, ts.URL, "/api/create", map[string]string{"name": "X"}); st != 200 {
			t.Fatalf("create %d: %d", i, st)
		}
	}
	if st, _ := post(t, ts.URL, "/api/create", map[string]string{"name": "X"}); st != 429 {
		t.Fatalf("too many rooms: %d", st)
	}
	// a megszűnt szobák (itt: a takarítás a "nagyon régi" szabály szerint) helyet adnak
	s.cleanup(time.Now().Add(maxLobbyAge + time.Minute))
	if st, _ := post(t, ts.URL, "/api/create", map[string]string{"name": "X"}); st != 200 {
		t.Fatalf("create after cleanup: %d", st)
	}
}

func TestAPICallLimit(t *testing.T) {
	dir := t.TempDir()
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	s.calls = newLimiter(time.Minute, 5)
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()
	last := 0
	for i := 0; i < 6; i++ {
		last, _ = post(t, ts.URL, "/api/check", map[string]string{"code": "ZZZZZ", "token": "x"})
	}
	if last != 429 {
		t.Fatalf("api limit: %d", last)
	}
	// a fájlokat nem érinti
	if resp, err := http.Get(ts.URL + "/index.html"); err != nil || resp.StatusCode == 429 {
		t.Fatalf("static limited: %v %v", err, resp)
	}
}

func TestStaticGzipAndCache(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	os.MkdirAll(filepath.Join(dir, "vendor", "ruffle"), 0o755)
	wasm := bytes.Repeat([]byte("ruffle "), 10000)
	os.WriteFile(filepath.Join(dir, "vendor", "ruffle", "172de42e6619eac8f371.wasm"), wasm, 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	req, _ := http.NewRequest("GET", ts.URL+"/vendor/ruffle/172de42e6619eac8f371.wasm", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	resp, err := http.DefaultTransport.RoundTrip(req) // a nyers (tömörített) választ akarjuk látni
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("Content-Type") != "application/wasm" ||
		!strings.Contains(resp.Header.Get("Cache-Control"), "immutable") || len(body) >= len(wasm)/10 {
		t.Fatalf("wasm: %v, %d bytes", resp.Header, len(body))
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if !bytes.Equal(plain, wasm) {
		t.Fatal("gzip content differs")
	}

	// gzip nélkül az eredeti; az index.html-t minden betöltéskor ellenőrizni kell
	req, _ = http.NewRequest("GET", ts.URL+"/", nil)
	resp, err = http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "" || string(body) != "<!doctype html>" || resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("index: %v %q", resp.Header, body)
	}
}

func TestSoundManifest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sounds"), 0o755)
	for _, f := range []string{"question_win.mp3", "boom.mp3", ".hidden.mp3", "notes.txt"} {
		os.WriteFile(filepath.Join(dir, "sounds", f), []byte("x"), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "sounds", "manifest.json"),
		[]byte(`{"castleattack_in": "boom.mp3", "tip_appear": "missing.mp3"}`), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()
	resp, err := http.Get(ts.URL + "/sounds/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	want := map[string]string{"question_win": "question_win.mp3", "boom": "boom.mp3", "castleattack_in": "boom.mp3"}
	if len(m) != len(want) {
		t.Fatalf("manifest: %v", m)
	}
	for k, v := range want {
		if m[k] != v {
			t.Fatalf("manifest: %v", m)
		}
	}
}

func TestCORSAndAPIOnly(t *testing.T) {
	dir := t.TempDir()
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	s.serveStatic = false
	s.origins["https://honfoglalo.pages.dev"] = true
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	// előzetes (preflight) kérés az engedélyezett címről
	req, _ := http.NewRequest("OPTIONS", ts.URL+"/api/create", nil)
	req.Header.Set("Origin", "https://honfoglalo.pages.dev")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "https://honfoglalo.pages.dev" {
		t.Fatalf("preflight: %d %v", resp.StatusCode, resp.Header)
	}
	// idegen cím: nincs engedély
	req, _ = http.NewRequest("GET", ts.URL+"/api/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "" || resp.StatusCode != 200 {
		t.Fatalf("foreign origin: %d %v", resp.StatusCode, resp.Header)
	}
	// csak API: nincs statikus fájl
	resp, _ = http.Get(ts.URL + "/")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("static served in API-only mode: %d", resp.StatusCode)
	}
}

func TestClientIPBehindProxy(t *testing.T) {
	s := newServer(t.TempDir(), "")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 1.2.3.4")
	if ip := s.clientIP(r); ip != "10.0.0.1" {
		t.Fatalf("without trust-proxy: %s", ip)
	}
	s.trustProxy = true
	if ip := s.clientIP(r); ip != "1.2.3.4" {
		t.Fatalf("x-forwarded-for: %s", ip)
	}
	r.Header.Set("CF-Connecting-IP", "5.5.5.5")
	if ip := s.clientIP(r); ip != "5.5.5.5" {
		t.Fatalf("cf-connecting-ip: %s", ip)
	}
}

func TestLobbyMap(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	// a létrehozáskor megadott térkép (kisbetűvel is), ismeretlennél a magyar
	_, a := post(t, ts.URL, "/api/create", map[string]string{"name": "Anna", "map": "fr"})
	code, tokA := a["code"].(string), a["token"].(string)
	evA := events(t, ts.URL, code, tokA)
	if m := next(t, evA, "state"); m["map"] != "FR" {
		t.Fatalf("map at create: %v", m["map"])
	}
	_, x := post(t, ts.URL, "/api/create", map[string]string{"name": "Xavér", "map": "XX"})
	if _, st := post(t, ts.URL, "/api/check", map[string]string{"code": x["code"].(string), "token": x["token"].(string)}); st["map"] != "HU" {
		t.Fatalf("default map: %v", st["map"])
	}

	_, b := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Béla"})
	tokB := b["token"].(string)
	next(t, evA, "state")
	if st, _ := post(t, ts.URL, "/api/map", map[string]string{"code": code, "token": tokB, "map": "DE"}); st != 403 {
		t.Fatalf("map by guest: %d", st)
	}
	if st, _ := post(t, ts.URL, "/api/map", map[string]string{"code": code, "token": tokA, "map": "Mars"}); st != 400 {
		t.Fatalf("bad map: %d", st)
	}
	if st, _ := post(t, ts.URL, "/api/map", map[string]string{"code": code, "token": tokA, "map": "DE"}); st != 200 {
		t.Fatalf("map: %d", st)
	}
	if m := next(t, evA, "state"); m["map"] != "DE" {
		t.Fatalf("map after change: %v", m["map"])
	}
	post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokA})
	if m := next(t, evA, "state"); m["started"] != true || m["map"] != "DE" {
		t.Fatalf("started: %v", m)
	}
	if st, _ := post(t, ts.URL, "/api/map", map[string]string{"code": code, "token": tokA, "map": "IT"}); st != 409 {
		t.Fatalf("map after start: %d", st)
	}
}

func TestRejoinByName(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	_, a := post(t, ts.URL, "/api/create", map[string]string{"name": "Anna"})
	code, tokA := a["code"].(string), a["token"].(string)
	evA := events(t, ts.URL, code, tokA)
	next(t, evA, "state")
	_, b := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Béla"})
	tokB := b["token"].(string)
	post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokA})

	// live: Annának élő eseményfolyama van, Bélának nincs
	if _, st := post(t, ts.URL, "/api/check", map[string]string{"code": code, "token": tokA}); st["live"] != true {
		t.Fatalf("live A: %v", st["live"])
	}
	if _, st := post(t, ts.URL, "/api/check", map[string]string{"code": code, "token": tokB}); st["live"] != false {
		t.Fatalf("live B: %v", st["live"])
	}
	// akinek élő kapcsolata van, annak a helyére nem lehet beülni
	if st, _ := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "anna"}); st != 409 {
		t.Fatalf("rejoin over live seat: %d", st)
	}
	// Béla a nevével visszaléphet: ugyanaz a hely, új belépő, a régi érvénytelen
	st, b2 := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "béla"})
	if st != 200 || b2["seat"].(float64) != 2 || b2["token"] == tokB {
		t.Fatalf("rejoin: %d %v", st, b2)
	}
	if st, _ := post(t, ts.URL, "/api/check", map[string]string{"code": code, "token": tokB}); st != 403 {
		t.Fatalf("old token still valid: %d", st)
	}
	if st, _ := post(t, ts.URL, "/api/check", map[string]string{"code": code, "token": b2["token"].(string)}); st != 200 {
		t.Fatalf("new token: %d", st)
	}
}

func TestLobbyLong(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	// a létrehozáskor megadott szabály, utána a főnök állítja az indulásig
	_, a := post(t, ts.URL, "/api/create", map[string]any{"name": "Anna", "long": true})
	code, tokA := a["code"].(string), a["token"].(string)
	evA := events(t, ts.URL, code, tokA)
	if m := next(t, evA, "state"); m["long"] != true {
		t.Fatalf("long at create: %v", m["long"])
	}
	_, b := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Béla"})
	tokB := b["token"].(string)
	next(t, evA, "state")
	if st, _ := post(t, ts.URL, "/api/long", map[string]any{"code": code, "token": tokB, "long": false}); st != 403 {
		t.Fatalf("long by guest: %d", st)
	}
	if st, _ := post(t, ts.URL, "/api/long", map[string]any{"code": code, "token": tokA, "long": false}); st != 200 {
		t.Fatalf("long: %d", st)
	}
	if m := next(t, evA, "state"); m["long"] != false {
		t.Fatalf("long after change: %v", m["long"])
	}
	post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokA})
	next(t, evA, "state")
	if st, _ := post(t, ts.URL, "/api/long", map[string]any{"code": code, "token": tokA, "long": true}); st != 409 {
		t.Fatalf("long after start: %d", st)
	}
}

func TestLobbyCats(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	// a létrehozáskor megadott témakörök kanonikus sorrendben, utána a főnök állítja az indulásig
	_, a := post(t, ts.URL, "/api/create", map[string]any{"name": "Anna", "cats": "termtud, Sport,termtud"})
	code, tokA := a["code"].(string), a["token"].(string)
	evA := events(t, ts.URL, code, tokA)
	if m := next(t, evA, "state"); m["cats"] != "sport,termtud" {
		t.Fatalf("cats at create: %v", m["cats"])
	}
	_, b := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Béla"})
	tokB := b["token"].(string)
	next(t, evA, "state")
	if st, _ := post(t, ts.URL, "/api/cats", map[string]any{"code": code, "token": tokB, "cats": "matfiz"}); st != 403 {
		t.Fatalf("cats by guest: %d", st)
	}
	if st, _ := post(t, ts.URL, "/api/cats", map[string]any{"code": code, "token": tokA, "cats": "matfiz"}); st != 200 {
		t.Fatalf("cats: %d", st)
	}
	if m := next(t, evA, "state"); m["cats"] != "matfiz" {
		t.Fatalf("cats after change: %v", m["cats"])
	}
	if st, _ := post(t, ts.URL, "/api/cats", map[string]any{"code": code, "token": tokA, "cats": "matfiz,foci"}); st != 400 {
		t.Fatalf("unknown cat: %d", st)
	}
	// mind a nyolc: ugyanaz, mint az üres (nincs cats a state-ben)
	all := strings.Join(validCats, ",")
	if st, _ := post(t, ts.URL, "/api/cats", map[string]any{"code": code, "token": tokA, "cats": all}); st != 200 {
		t.Fatalf("cats all: %d", st)
	}
	if m := next(t, evA, "state"); m["cats"] != nil {
		t.Fatalf("cats after all: %v", m["cats"])
	}
	post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokA})
	next(t, evA, "state")
	if st, _ := post(t, ts.URL, "/api/cats", map[string]any{"code": code, "token": tokA, "cats": "sport"}); st != 409 {
		t.Fatalf("cats after start: %d", st)
	}
	// érvénytelen lista a létrehozáskor: mind
	st, c := post(t, ts.URL, "/api/create", map[string]any{"name": "Cili", "cats": "sport,foci"})
	if st != 200 {
		t.Fatalf("bad cats at create: %d", st)
	}
	if m := next(t, events(t, ts.URL, c["code"].(string), c["token"].(string)), "state"); m["cats"] != nil {
		t.Fatalf("bad cats at create: %v", m["cats"])
	}
}

func TestCleanCats(t *testing.T) {
	for in, want := range map[string]string{"": "", " , ": "", "szorakozas,foldrajz": "foldrajz,szorakozas",
		"EletMod , eletmod": "eletmod", strings.Join(validCats, ","): ""} {
		if got, ok := cleanCats(in); !ok || got != want {
			t.Errorf("cleanCats(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := cleanCats("sport,xyz"); ok {
		t.Error("unknown accepted")
	}
}

// ha a létrehozó az első játék előtt elmegy, a szoba megszűnik (a bent lévők üzenetet kapnak)
func TestCreatorLeavesClosesLobby(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	_, a := post(t, ts.URL, "/api/create", map[string]string{"name": "Anna"})
	code, tokA := a["code"].(string), a["token"].(string)
	_, b := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Béla"})
	evB := events(t, ts.URL, code, b["token"].(string))
	next(t, evB, "state")
	if st, _ := post(t, ts.URL, "/api/leave", map[string]string{"code": code, "token": tokA}); st != 200 {
		t.Fatalf("leave: %d", st)
	}
	if m := next(t, evB, "closed"); !strings.Contains(m["msg"].(string), "létrehozója") {
		t.Fatalf("closed: %v", m)
	}
	if st, _ := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Cili"}); st != 404 {
		t.Fatalf("join closed room: %d", st)
	}
}

// a játék végén "Vissza a váróba": a szoba újra várakozik, aki elment, kikerül; az új játék új
// sorszámot kap, a döntések elölről kezdődnek. A létrehozó távozása ekkor már nem szünteti meg.
func TestAgainAfterGame(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	_, a := post(t, ts.URL, "/api/create", map[string]string{"name": "Anna"})
	code, tokA := a["code"].(string), a["token"].(string)
	_, b := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Béla"})
	tokB := b["token"].(string)
	_, c := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Cili"})
	evB := events(t, ts.URL, code, tokB)
	evA := events(t, ts.URL, code, tokA)
	next(t, evB, "state")
	post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokA})
	m := next(t, evB, "state")
	for m["started"] != true {
		m = next(t, evB, "state")
	}
	if m["game"].(float64) != 1 {
		t.Fatalf("start: %v", m)
	}
	post(t, ts.URL, "/api/send", map[string]any{"code": code, "token": tokB, "k": 6, "d": map[string]int{"v": 3}})
	next(t, evB, "dec")
	// Cili a játék végén kilép, Anna (a létrehozó) is
	post(t, ts.URL, "/api/leave", map[string]string{"code": code, "token": c["token"].(string)})
	if st, _ := post(t, ts.URL, "/api/again", map[string]string{"code": code, "token": tokB}); st != 200 {
		t.Fatalf("again: %d", st)
	}
	m = next(t, evB, "state")
	for m["started"] != false {
		m = next(t, evB, "state")
	}
	if len(m["players"].([]any)) != 2 {
		t.Fatalf("players after again: %v", m["players"])
	}
	post(t, ts.URL, "/api/leave", map[string]string{"code": code, "token": tokA})
	_ = evA
	if m := next(t, evB, "state"); m["boss"].(float64) != 2 || len(m["players"].([]any)) != 1 {
		t.Fatalf("after creator left: %v", m)
	}
	// új játékos, új játék: a döntések kulcsai újra szabadok. Az új belépő a régi játék döntéseit
	// nem kapja meg (Béla, aki játszott benne, újracsatlakozáskor igen).
	_, d := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Dani"})
	next(t, evB, "state")
	evD := events(t, ts.URL, code, d["token"].(string))
	next(t, evD, "state")
	evB2 := events(t, ts.URL, code, tokB)
	next(t, evB2, "dec")
	select {
	case m := <-evD:
		for m != nil && m["type"] != "dec" {
			select {
			case m = <-evD:
			case <-time.After(300 * time.Millisecond):
				m = nil
			}
		}
		if m != nil {
			t.Fatalf("new player got an old decision: %v", m)
		}
	case <-time.After(300 * time.Millisecond):
	}
	post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokB})
	for m = next(t, evB, "state"); m["started"] != true; m = next(t, evB, "state") {
	}
	if m["game"].(float64) != 2 {
		t.Fatalf("game: %v", m["game"])
	}
	post(t, ts.URL, "/api/send", map[string]any{"code": code, "token": d["token"].(string), "k": 6, "d": map[string]int{"v": 1}})
	if m := next(t, evB, "dec"); m["k"].(float64) != 6 || m["from"] != d["seat"] {
		t.Fatalf("new game decision: %v", m)
	}
}

// egy böngészőből legfeljebb maxRoomsPerClient szoba lehet nyitva
func TestRoomsPerClient(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	for i := 0; i < maxRoomsPerClient; i++ {
		if st, _ := post(t, ts.URL, "/api/create", map[string]string{"name": "Anna", "client": "browserAAAA"}); st != 200 {
			t.Fatalf("create %d: %d", i, st)
		}
	}
	if st, _ := post(t, ts.URL, "/api/create", map[string]string{"name": "Anna", "client": "browserAAAA"}); st != 429 {
		t.Fatalf("too many: %d", st)
	}
	if st, _ := post(t, ts.URL, "/api/create", map[string]string{"name": "Béla", "client": "browserBBBB"}); st != 200 {
		t.Fatalf("other browser: %d", st)
	}
	// ha a létrehozó egy játék után kilépett, a szoba a többieké: már nem számít a böngészője korlátjába
	// (új szerver: az IP-nkénti korlátba ne fusson bele)
	s2 := newServer(dir, filepath.Join(dir, "fb2.jsonl"))
	ts2 := httptest.NewServer(s2.routes())
	defer func() { ts2.CloseClientConnections(); ts2.Close() }()
	ts = ts2
	_, c := post(t, ts.URL, "/api/create", map[string]string{"name": "Cili", "client": "browserCCCC"})
	code, tokC := c["code"].(string), c["token"].(string)
	post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Dani"})
	post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokC})
	post(t, ts.URL, "/api/leave", map[string]string{"code": code, "token": tokC})
	post(t, ts.URL, "/api/create", map[string]string{"name": "Cili", "client": "browserCCCC"})
	if st, _ := post(t, ts.URL, "/api/create", map[string]string{"name": "Cili", "client": "browserCCCC"}); st != 200 {
		t.Fatalf("left room still counted: %d", st)
	}
}

func TestStaticBackslash(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "js"), 0o755)
	os.WriteFile(filepath.Join(dir, "js", "a.js"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "secret.json"), []byte("{}"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	for _, p := range []string{"/js/..%5Csecret.json", "/js/%5C..%5C..%5Csecret.json"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
	}
	resp, err := http.Get(ts.URL + "/js/a.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("js/a.js: %d", resp.StatusCode)
	}
}

func TestAcceptsGzip(t *testing.T) {
	for h, want := range map[string]bool{
		"":                 false,
		"gzip":             true,
		"br, gzip":         true,
		"gzip;q=0.5":       true,
		"gzip; q=1.0":      true,
		"gzip;q=0":         false,
		"gzip;q=0.000, br": false,
		"deflate":          false,
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Encoding", h)
		if got := acceptsGzip(r); got != want {
			t.Errorf("%q: %v", h, got)
		}
	}
}

// robotok a váróban: a főnök beteheti és kiveheti őket, egy belépő ember a robot helyére ül, és már
// egy ember és egy robot (vagy két ember) is indíthat; a többiek a csevegésben látják, ki mit tett
func TestRobots(t *testing.T) {
	noFloods(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	_, a := post(t, ts.URL, "/api/create", map[string]any{"name": "Anna", "public": true})
	code, tokA := a["code"].(string), a["token"].(string)
	evA := events(t, ts.URL, code, tokA)
	next(t, evA, "state")
	robot := func(tok string, seat int, add bool) int {
		st, _ := post(t, ts.URL, "/api/robot", map[string]any{"code": code, "token": tok, "seat": seat, "add": add})
		return st
	}
	players := func(m map[string]any) string {
		var out []string
		for _, p := range m["players"].([]any) {
			p := p.(map[string]any)
			n := p["name"].(string)
			if p["robot"] == true {
				n += "*"
			}
			out = append(out, n)
		}
		return strings.Join(out, ",")
	}
	if st := robot(tokA, 0, true); st != 200 {
		t.Fatalf("add robot: %d", st)
	}
	if m := next(t, evA, "chat"); m["sys"] != "robot" || m["name"] != "Anna" || m["text"] != "Jani" {
		t.Fatalf("robot chat: %v", m)
	}
	if m := next(t, evA, "state"); players(m) != "Anna,Jani*" {
		t.Fatalf("after robot: %v", players(m))
	}
	if st := robot(tokA, 2, true); st != 409 {
		t.Fatalf("robot on a taken seat: %d", st)
	}
	robot(tokA, 3, true)
	if m := next(t, evA, "state"); players(m) != "Anna,Jani*,Éva*" {
		t.Fatalf("two robots: %v", players(m))
	}
	// a publikus listában a robotok jelölve; a szoba tele van robottal, de egy ember még beléphet
	s.hub.mu.Lock()
	s.hub.roomsAt = time.Time{}
	s.hub.mu.Unlock()
	if h := getHub(t, ts.URL, "browserAAAA"); len(h.Lobbies) != 1 || len(h.Lobbies[0].Players) != 3 || !h.Lobbies[0].Players[2].Robot ||
		h.Lobbies[0].Status != "open" || h.Lobbies[0].Created <= 0 {
		t.Fatalf("hub: %+v", h)
	}
	// a robot nevével nem lehet belépni, ha az a robot marad; a belépő az utolsó robot helyére ül
	if st, _ := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "jani"}); st != 409 {
		t.Fatalf("robot's name: %d", st)
	}
	st, b := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Béla"})
	if st != 200 || b["seat"].(float64) != 3 {
		t.Fatalf("join over robot: %d %v", st, b)
	}
	tokB := b["token"].(string)
	if m := next(t, evA, "state"); players(m) != "Anna,Jani*,Béla" {
		t.Fatalf("after join: %v", players(m))
	}
	if st := robot(tokB, 0, true); st != 403 {
		t.Fatalf("guest adds robot: %d", st)
	}
	robot(tokA, 2, false)
	if m := next(t, evA, "state"); players(m) != "Anna,Béla" {
		t.Fatalf("robot removed: %v", players(m))
	}
	// ketten indítanak, a harmadik hely üres marad
	if st, _ := post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokA}); st != 200 {
		t.Fatalf("start with two: %d", st)
	}
	if st := robot(tokA, 0, true); st != 409 {
		t.Fatalf("robot after start: %d", st)
	}
	// játék közbeni kilépés: a többiek a csevegésben látják
	evB := events(t, ts.URL, code, tokB)
	next(t, evB, "state")
	post(t, ts.URL, "/api/leave", map[string]string{"code": code, "token": tokA})
	for m := next(t, evB, "chat"); m["sys"] != "quit"; m = next(t, evB, "chat") {
	}
}

// játék közben elment kapcsolat: a türelmi idő után a többiek látják, és azt is, ha visszajön
// Ha a játékban csak robotok maradnak (Anna kilépett, Béla kapcsolata megszakadt), robotsOnlyTTL után
// a szoba megszűnik, és vissza sem lehet lépni; amíg egy ember is ott van, marad.
func TestRobotsOnlyCloses(t *testing.T) {
	oldGrace, oldTTL := offlineGrace, robotsOnlyTTL
	offlineGrace, robotsOnlyTTL = 100*time.Millisecond, time.Minute
	defer func() { offlineGrace, robotsOnlyTTL = oldGrace, oldTTL }()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	_, a := post(t, ts.URL, "/api/create", map[string]any{"name": "Anna"})
	code, tokA := a["code"].(string), a["token"].(string)
	_, b := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Béla"})
	tokB := b["token"].(string)
	evA := events(t, ts.URL, code, tokA)
	resp, err := http.Get(ts.URL + "/api/events?code=" + code + "&token=" + tokB)
	if err != nil {
		t.Fatal(err)
	}
	next(t, evA, "state")
	post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokA})
	post(t, ts.URL, "/api/leave", map[string]string{"code": code, "token": tokA})
	// Béla még ott van: a szoba marad
	s.cleanup(time.Now().Add(10 * time.Minute))
	if s.lobbyCount() != 1 {
		t.Fatal("closed while a human plays")
	}
	resp.Body.Close() // Béla kapcsolata is megszakad
	time.Sleep(offlineGrace + 200*time.Millisecond)
	s.cleanup(time.Now().Add(robotsOnlyTTL / 2))
	if s.lobbyCount() != 1 {
		t.Fatal("closed too early")
	}
	s.cleanup(time.Now().Add(robotsOnlyTTL + time.Second))
	if s.lobbyCount() != 0 {
		t.Fatal("robots-only game not closed")
	}
	if st, _ := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Béla"}); st != http.StatusNotFound {
		t.Fatalf("rejoin after close: %d", st)
	}
}

func TestPresenceLost(t *testing.T) {
	old := offlineGrace
	offlineGrace = 150 * time.Millisecond
	defer func() { offlineGrace = old }()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	_, a := post(t, ts.URL, "/api/create", map[string]any{"name": "Anna"})
	code, tokA := a["code"].(string), a["token"].(string)
	_, b := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Béla"})
	tokB := b["token"].(string)
	evA := events(t, ts.URL, code, tokA)
	resp, err := http.Get(ts.URL + "/api/events?code=" + code + "&token=" + tokB)
	if err != nil {
		t.Fatal(err)
	}
	next(t, evA, "state")
	post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokA})
	resp.Body.Close() // Béla kapcsolata megszakad
	m := next(t, evA, "chat")
	for m["sys"] != "lost" {
		m = next(t, evA, "chat")
	}
	if m["name"] != "Béla" || m["t"].(float64) <= 0 {
		t.Fatalf("lost: %v", m)
	}
	evB := events(t, ts.URL, code, tokB)
	next(t, evB, "state")
	if m := next(t, evA, "chat"); m["sys"] != "rejoin" || m["name"] != "Béla" {
		t.Fatalf("rejoin: %v", m)
	}
}
