package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCleanProfanity(t *testing.T) {
	for in, want := range map[string]string{
		"Szép napot!":            "Szép napot!",
		"Te kurva anyád":         "Te k**** anyád",
		"KURVÁRA jó":             "K****** jó",
		"faszén és baszk zászló": "faszén és baszk zászló",
		"ez fasza":               "ez f****",
		"kibaszott kérdés":       "k******** kérdés",
		"basszusgitár":           "basszusgitár",
		"segít a szarvas":        "segít a szarvas",
		"seggfej":                "s******",
		"ez szar":                "ez s***",
		"Fuuuck this sh1t":       "F***** this s***",
		"dickens, dick":          "dickens, d***",
		"cockpit":                "cockpit",
		"geci!!":                 "g***!!",
		"tejes pohár":            "tejes pohár",
	} {
		if got := cleanProfanity(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// getHub: a Várók lekérdezése egy böngészőazonosítóval
func getHub(t *testing.T, base, id string) hubReply {
	t.Helper()
	resp, err := http.Get(base + "/api/hub?id=" + id)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("hub: %v %v", err, resp)
	}
	defer resp.Body.Close()
	var out hubReply
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func TestPublicLobbiesAndOnline(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()
	fresh := func() { s.hub.mu.Lock(); s.hub.roomsAt = time.Time{}; s.hub.mu.Unlock() } // a szobalista ne a gyorsítótárból jöjjön

	if h := getHub(t, ts.URL, "browserAAAA"); h.Online != 1 || len(h.Lobbies) != 0 {
		t.Fatalf("empty hub: %+v", h)
	}
	// ugyanannak a böngészőnek egy másik lapja nem számít külön
	getHub(t, ts.URL, "browserAAAA")
	if h := getHub(t, ts.URL, "browserBBBB"); h.Online != 2 {
		t.Fatalf("online: %d", h.Online)
	}
	// aki régen kérdezett, elment
	s.hub.mu.Lock()
	s.hub.seen["browserBBBB"] = time.Now().Add(-2 * hubPresence)
	s.hub.pruned = time.Time{}
	s.hub.mu.Unlock()
	if h := getHub(t, ts.URL, "browserAAAA"); h.Online != 1 {
		t.Fatalf("online after leaving: %d", h.Online)
	}

	// privát szoba nem látszik; a publikus az alapértelmezett nevével igen
	post(t, ts.URL, "/api/create", map[string]any{"name": "Titok"})
	_, a := post(t, ts.URL, "/api/create", map[string]any{"name": "Anna", "public": true, "map": "fr", "long": true, "cats": "sport,irodalom"})
	code, tokA := a["code"].(string), a["token"].(string)
	evA := events(t, ts.URL, code, tokA)
	if m := next(t, evA, "state"); m["public"] != true || m["title"] != "Anna lobbyja" {
		t.Fatalf("state: %v", m)
	}
	fresh()
	h := getHub(t, ts.URL, "browserAAAA")
	if len(h.Lobbies) != 1 {
		t.Fatalf("lobbies: %+v", h.Lobbies)
	}
	// aki a lista ezen változatát már megkapta, annak csak a létszám megy (same)
	fresh()
	resp, _ := http.Get(ts.URL + "/api/hub?id=browserAAAA&v=" + strconv.Itoa(h.V))
	var same hubReply
	json.NewDecoder(resp.Body).Decode(&same)
	resp.Body.Close()
	if !same.Same || same.Lobbies != nil || same.V != h.V {
		t.Fatalf("same version: %+v", same)
	}
	if r := h.Lobbies[0]; r.Code != code || r.Title != "Anna lobbyja" || r.Map != "FR" || !r.Long || r.Cats != "sport,irodalom" || len(r.Players) != 1 {
		t.Fatalf("lobby: %+v", r)
	}
	// saját név; trágár szóval, linkkel vagy túl hosszan nem lehet
	if st, _ := post(t, ts.URL, "/api/create", map[string]any{"name": "Béla", "public": true, "title": "Béla meccse"}); st != 200 {
		t.Fatalf("create titled: %d", st)
	}
	for _, title := range []string{"Ide a geci jó", "nézd: x.hu", strings.Repeat("x", 29)} {
		if st, _ := post(t, ts.URL, "/api/create", map[string]any{"name": "Cili", "public": true, "title": title}); st != 400 {
			t.Fatalf("bad title %q: %d", title, st)
		}
	}
	// trágár név: se létrehozni, se belépni nem lehet vele
	if st, _ := post(t, ts.URL, "/api/create", map[string]any{"name": "Kurva Béla"}); st != 400 {
		t.Fatalf("bad name create: %d", st)
	}
	if st, _ := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Fasz"}); st != 400 {
		t.Fatalf("bad name join: %d", st)
	}
	fresh()
	if h := getHub(t, ts.URL, "browserAAAA"); len(h.Lobbies) != 2 || h.Lobbies[0].Title != "Béla meccse" {
		t.Fatalf("titled: %+v", h.Lobbies)
	}

	// a teli szoba és a futó játék is látszik, de a lista végén; a játék állását a házigazda jelenti
	_, d := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Dani"})
	_, e := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Ede"})
	fresh()
	if h := getHub(t, ts.URL, "browserAAAA"); len(h.Lobbies) != 2 || h.Lobbies[1].Code != code || h.Lobbies[1].Status != "full" {
		t.Fatalf("full room: %+v", h.Lobbies)
	}
	created := getHub(t, ts.URL, "browserAAAA").Lobbies[1].Created
	evD := events(t, ts.URL, code, d["token"].(string)) // Dani kapcsolódva marad, Ede kilép
	next(t, evD, "state")
	post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokA})
	post(t, ts.URL, "/api/progress", map[string]any{"code": code, "token": tokA, "state": 4, "round": 2})
	post(t, ts.URL, "/api/leave", map[string]string{"code": code, "token": e["token"].(string)})
	fresh()
	h = getHub(t, ts.URL, "browserAAAA")
	if r := h.Lobbies[1]; r.Code != code || r.Status != "playing" || r.Progress == nil || r.Progress.State != 4 || r.Progress.Round != 2 ||
		r.Players[2].Name != "Ede" || !r.Players[2].Away || r.Players[1].Away {
		t.Fatalf("playing room: %+v", h.Lobbies)
	}
	// a játék után vissza a váróba: ugyanaz a szoba, ugyanazzal a létrehozási idővel
	post(t, ts.URL, "/api/again", map[string]string{"code": code, "token": d["token"].(string)})
	fresh()
	h = getHub(t, ts.URL, "browserAAAA")
	if len(h.Lobbies) != 2 || h.Lobbies[1].Code != code || h.Lobbies[1].Status != "open" || h.Lobbies[1].Created != created || h.Lobbies[1].Progress != nil {
		t.Fatalf("after again: %+v", h.Lobbies)
	}
}

// rövid korlátok a teszt idejére
func quickFloods(t *testing.T) {
	saved := []floodRule{chatFlood, presenceFlood, robotFlood, comingsFlood}
	chatFlood = floodRule{burst: 3, window: time.Second, gap: 60 * time.Millisecond, mute: 400 * time.Millisecond}
	presenceFlood = floodRule{burst: 3, window: time.Second, gap: 60 * time.Millisecond}
	robotFlood = floodRule{burst: 2, window: time.Second, gap: 60 * time.Millisecond, mute: 400 * time.Millisecond}
	t.Cleanup(func() { chatFlood, presenceFlood, robotFlood, comingsFlood = saved[0], saved[1], saved[2], saved[3] })
}

// korlátok nélkül (a többi teszt sűrűn hív)
func noFloods(t *testing.T) {
	quickFloods(t)
	free := floodRule{burst: 1000, window: time.Second}
	chatFlood, presenceFlood, robotFlood, comingsFlood = free, free, free, free
}

func TestFlood(t *testing.T) {
	r := floodRule{burst: 3, window: 10 * time.Second, gap: time.Second, mute: 30 * time.Second}
	var f flood
	t0 := time.Now()
	if ok, _ := f.allow(r, t0); !ok {
		t.Fatal("first")
	}
	if ok, wait := f.allow(r, t0.Add(500*time.Millisecond)); ok || wait != 500*time.Millisecond {
		t.Fatalf("gap: %v %v", ok, wait)
	}
	f.allow(r, t0.Add(2*time.Second))
	f.allow(r, t0.Add(4*time.Second))
	if ok, wait := f.allow(r, t0.Add(6*time.Second)); ok || wait != 30*time.Second {
		t.Fatalf("burst -> mute: %v %v", ok, wait)
	}
	if ok, _ := f.allow(r, t0.Add(30*time.Second)); ok {
		t.Fatal("still muted")
	}
	if ok, _ := f.allow(r, t0.Add(37*time.Second)); !ok {
		t.Fatal("after mute")
	}
	if got := capRepeats("szia!!!!!!!! jóóóóóó 1000000 Ft"); got != "szia!!!! jóóóó 1000000 Ft" {
		t.Fatalf("capRepeats: %q", got)
	}
}

func TestChat(t *testing.T) {
	quickFloods(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	_, a := post(t, ts.URL, "/api/create", map[string]any{"name": "Anna"})
	code, tokA := a["code"].(string), a["token"].(string)
	_, b := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Béla"})
	tokB := b["token"].(string)
	evB := events(t, ts.URL, code, tokB)
	next(t, evB, "state")
	if m := next(t, evB, "chat"); m["sys"] != "joined" || m["old"] != true {
		t.Fatalf("joined: %v", m)
	}

	if st, _ := post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": tokA, "text": "  szia   Béla, te   fasz! "}); st != 200 {
		t.Fatalf("chat: %d", st)
	}
	if m := next(t, evB, "chat"); m["text"] != "szia Béla, te f***!" || m["name"] != "Anna" || m["seat"].(float64) != 1 || m["old"] != nil {
		t.Fatalf("chat event: %v", m)
	}
	// túl gyorsan: korlát; üres és túl hosszú üzenet: hiba; idegen: nem a szobában
	if st, _ := post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": tokA, "text": "megint"}); st != 429 {
		t.Fatalf("rate: %d", st)
	}
	if st, _ := post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": tokB, "text": "   "}); st != 400 {
		t.Fatalf("empty: %d", st)
	}
	if st, _ := post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": tokB, "text": strings.Repeat("a", 201)}); st != 400 {
		t.Fatalf("long: %d", st)
	}
	if st, _ := post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": "nope", "text": "hali"}); st != 403 {
		t.Fatalf("stranger: %d", st)
	}
	// aki később csatlakozik, megkapja a korábbi üzeneteket (old): Béla belépése, Anna üzenete
	evA := events(t, ts.URL, code, tokA)
	if m := next(t, evA, "chat"); m["old"] != true || m["sys"] != "joined" || m["name"] != "Béla" {
		t.Fatalf("history: %v", m)
	}
	if m := next(t, evA, "chat"); m["old"] != true || m["text"] != "szia Béla, te f***!" {
		t.Fatalf("history 2: %v", m)
	}
	// linket nem lehet küldeni, rövidet sem
	time.Sleep(chatFlood.gap)
	for _, l := range []string{"nézd: facebook.com", "https://x.hu/a", "www.pelda", "bit.ly/abc", "írj: anna@gmail.com"} {
		if st, _ := post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": tokB, "text": l}); st != 400 {
			t.Fatalf("link %q: %d", l, st)
		}
	}
	// kilépés a csevegésből és vissza: a többiek látják; kétszer egymás után csak egyszer
	post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": tokB, "presence": "left"})
	if m := next(t, evA, "chat"); m["sys"] != "left" || m["name"] != "Béla" {
		t.Fatalf("left: %v", m)
	}
	time.Sleep(presenceFlood.gap)
	post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": tokB, "presence": "left"})
	time.Sleep(presenceFlood.gap)
	post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": tokB, "presence": "back"})
	if m := next(t, evA, "chat"); m["sys"] != "back" {
		t.Fatalf("back: %v", m)
	}
	// ki-be kapcsolgatva: a korláton túl csendben vált (nem kerül a csevegésbe), de a hívás sikeres
	time.Sleep(presenceFlood.gap)
	for i, p := range []string{"left", "back", "left"} {
		if st, _ := post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": tokB, "presence": p}); st != 200 {
			t.Fatalf("toggle %d: %d", i, st)
		}
	}
	if m := next(t, evA, "chat"); m["sys"] != "left" {
		t.Fatalf("toggle: %v", m)
	}
	select {
	case m := <-evA:
		if m["type"] == "chat" {
			t.Fatalf("toggle spam reached the chat: %v", m)
		}
	case <-time.After(200 * time.Millisecond):
	}

	// ugyanaz az üzenet nem mehet újra; a betűismétlés rövidül; a sorozat után egy ideig semmi
	time.Sleep(chatFlood.window)
	say := func(text string) (int, map[string]any) {
		return post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": tokA, "text": text})
	}
	say("Haliiiiiiii!!!!!!!")
	m := next(t, evB, "chat")
	for m["sys"] != nil { // Béla saját ki-be lépései
		m = next(t, evB, "chat")
	}
	if m["text"] != "Haliiii!!!!" {
		t.Fatalf("repeats: %v", m)
	}
	time.Sleep(chatFlood.gap)
	if st, m := say("haliiii!!!!"); st != 409 {
		t.Fatalf("same message: %d %v", st, m)
	}
	say("egy")
	time.Sleep(chatFlood.gap)
	say("kettő")
	time.Sleep(chatFlood.gap)
	if st, m := say("három"); st != 429 || !strings.Contains(m["error"].(string), "lassabban") && !strings.Contains(m["error"].(string), "várj") {
		t.Fatalf("burst: %d %v", st, m)
	}
	time.Sleep(chatFlood.gap)
	if st, _ := say("négy"); st != 429 {
		t.Fatalf("muted: %d", st)
	}
	time.Sleep(chatFlood.mute)
	if st, _ := say("öt"); st != 200 {
		t.Fatalf("after mute: %d", st)
	}
}

// a robot ki-be rakosgatása és a ki-be lépkedés sem árasztja el a csevegést
func TestNoSpam(t *testing.T) {
	quickFloods(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	s := newServer(dir, filepath.Join(dir, "fb.jsonl"))
	ts := httptest.NewServer(s.routes())
	defer func() { ts.CloseClientConnections(); ts.Close() }()

	_, a := post(t, ts.URL, "/api/create", map[string]any{"name": "Anna"})
	code, tokA := a["code"].(string), a["token"].(string)
	robot := func(add bool) int {
		st, _ := post(t, ts.URL, "/api/robot", map[string]any{"code": code, "token": tokA, "seat": 2, "add": add})
		return st
	}
	if robot(true) != 200 || robot(false) != 429 {
		t.Fatal("robot gap")
	}
	time.Sleep(robotFlood.gap)
	robot(false)
	time.Sleep(robotFlood.gap)
	if st := robot(true); st != 429 {
		t.Fatalf("robot burst: %d", st)
	}
	time.Sleep(robotFlood.mute)
	if st := robot(true); st != 200 {
		t.Fatalf("robot after mute: %d", st)
	}
	// ugyanaz a név be- és kilépése sysRepeat-en belül csak egyszer kerül a csevegésbe
	for i := 0; i < 3; i++ {
		_, b := post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Béla"})
		post(t, ts.URL, "/api/leave", map[string]string{"code": code, "token": b["token"].(string)})
	}
	evA := events(t, ts.URL, code, tokA)
	var sys []string
	for {
		select {
		case m := <-evA:
			if m["type"] == "chat" {
				sys = append(sys, m["sys"].(string))
			}
			continue
		case <-time.After(200 * time.Millisecond):
		}
		break
	}
	if strings.Join(sys, ",") != "robot,unrobot,robot,joined,gone" {
		t.Fatalf("chat lines: %v", sys)
	}
}

func TestHasLink(t *testing.T) {
	for s, want := range map[string]bool{
		"facebook.com":            true,
		"FB: Facebook.Com/anna":   true,
		"honfoglalo.online":       true,
		"t.me/csoport":            true,
		"Szia, pl. ez jó":         false,
		"1.5 óra":                 false,
		"Te.is jössz?":            false,
		"Jó volt.Szép napot":      false,
		"Ez a kérdés: 3.14 körül": false,
	} {
		if got := hasLink(s); got != want {
			t.Errorf("%q: %v", s, got)
		}
	}
}
