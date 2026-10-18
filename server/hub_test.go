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
	_, a := post(t, ts.URL, "/api/create", map[string]any{"name": "Anna", "public": true, "map": "fr", "long": true})
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
	if r := h.Lobbies[0]; r.Code != code || r.Title != "Anna lobbyja" || r.Map != "FR" || !r.Long || len(r.Players) != 1 {
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

	// teli és elindult szoba lekerül a listáról
	post(t, ts.URL, "/api/join", map[string]string{"code": code, "name": "Dani"})
	post(t, ts.URL, "/api/start", map[string]string{"code": code, "token": tokA})
	fresh()
	if h := getHub(t, ts.URL, "browserAAAA"); len(h.Lobbies) != 1 || h.Lobbies[0].Code == code {
		t.Fatalf("started still listed: %+v", h.Lobbies)
	}
}

func TestChat(t *testing.T) {
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
	time.Sleep(chatGap)
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
	time.Sleep(chatGap)
	post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": tokB, "presence": "left"})
	time.Sleep(chatGap)
	post(t, ts.URL, "/api/chat", map[string]string{"code": code, "token": tokB, "presence": "back"})
	if m := next(t, evA, "chat"); m["sys"] != "back" {
		t.Fatalf("back: %v", m)
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
