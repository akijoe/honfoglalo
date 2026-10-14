package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Egy játékos első letöltésének nagy része a Ruffle wasm-fájlja (14 MB, gzippel 5 MB), ezért a
// tömöríthető fájlokat gzippel küldjük; a tömörített változatot a memóriában tartjuk, amíg a fájl
// nem változik. A tartalom-hash nevű Ruffle-fájlok (pl. 172de42e6619eac8f371.wasm) egy évig
// cache-elhetők, a többit a böngésző minden betöltéskor ellenőrzi (304, ha nem változott).

var compressible = map[string]bool{".wasm": true, ".js": true, ".json": true, ".xml": true, ".html": true, ".css": true}
var hashedName = regexp.MustCompile(`[0-9a-f]{16,}\.(wasm|js)$`)

type gzEntry struct {
	mod  time.Time
	size int64
	data []byte
}

type gzCache struct {
	mu sync.Mutex // a tömörítés idejére is: így egy fájlt csak egyszer tömörítünk
	m  map[string]*gzEntry
}

func (c *gzCache) get(full string, st os.FileInfo) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.m[full]; e != nil && e.mod.Equal(st.ModTime()) && e.size == st.Size() {
		return e.data, nil
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(raw)
	zw.Close()
	if c.m == nil {
		c.m = map[string]*gzEntry{}
	}
	c.m[full] = &gzEntry{mod: st.ModTime(), size: st.Size(), data: buf.Bytes()}
	return buf.Bytes(), nil
}

// warm: induláskor előre betömöríti a nagy fájlokat, hogy az első játékosnak ne kelljen várnia.
func (c *gzCache) warm(dir string) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.wasm"))
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			if _, err := c.get(f, st); err != nil {
				log.Printf("tömörítés: %v", err)
			}
		}
	}
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, params, _ := strings.Cut(part, ";")
		if strings.TrimSpace(enc) != "gzip" {
			continue
		}
		// "gzip;q=0" (vagy 0.0, 0.000) tiltja; a "gzip;q=0.5" megengedi
		for _, prm := range strings.Split(params, ";") {
			k, v, _ := strings.Cut(strings.TrimSpace(prm), "=")
			if strings.EqualFold(strings.TrimSpace(k), "q") && strings.Trim(strings.TrimSpace(v), "0.") == "" {
				return false
			}
		}
		return true
	}
	return false
}

// serveFile: egy engedélyezett fájl kiszolgálása (gzip, cache-fejlécek, If-Modified-Since).
func (s *server) serveFile(w http.ResponseWriter, r *http.Request, full string, st os.FileInfo) {
	h := w.Header()
	if hashedName.MatchString(full) {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	if !compressible[strings.ToLower(filepath.Ext(full))] {
		http.ServeFile(w, r, full)
		return
	}
	h.Add("Vary", "Accept-Encoding")
	if !acceptsGzip(r) {
		http.ServeFile(w, r, full)
		return
	}
	gz, err := s.gz.get(full, st)
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}
	h.Set("Content-Encoding", "gzip")
	http.ServeContent(w, r, filepath.Base(full), st.ModTime(), bytes.NewReader(gz))
}

var soundExts = map[string]bool{".mp3": true, ".ogg": true, ".wav": true, ".m4a": true}

// serveSoundManifest: a játék hangneve -> hangfájl (js/hf.js tölti be). A sounds/ mappa minden
// hangfájlja a saját nevén szerepel (question_win.mp3 -> question_win), így egy új hanghoz elég a
// fájlt a megfelelő néven bemásolni. A sounds/manifest.json csak eltérő nevű fájlokat rendel
// hangnevekhez; a nem létező fájlra mutató sorait kihagyjuk.
func (s *server) serveSoundManifest(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(s.root, "sounds")
	m := map[string]string{}
	exists := map[string]bool{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		ext := filepath.Ext(name)
		if e.IsDir() || strings.HasPrefix(name, ".") || !soundExts[strings.ToLower(ext)] {
			continue
		}
		exists[name] = true
		m[strings.TrimSuffix(name, ext)] = name
	}
	if b, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
		var extra map[string]string
		if err := json.Unmarshal(b, &extra); err != nil {
			log.Printf("sounds/manifest.json: %v", err)
		}
		for k, v := range extra {
			if exists[v] {
				m[k] = v
			}
		}
	}
	writeJSON(w, http.StatusOK, m)
}
