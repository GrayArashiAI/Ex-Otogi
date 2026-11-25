package management

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPanelSendsTheCompressedFileTheBrowserAccepts(t *testing.T) {
	panel := spa(fstest.MapFS{
		"index.html":          {Data: []byte("<!doctype html><title>panel</title>")},
		"index.html.br":       {Data: []byte("br index")},
		"assets/app-1.js":     {Data: []byte("console.log('the panel, uncompressed')")},
		"assets/app-1.js.br":  {Data: []byte("br js")},
		"assets/app-1.js.gz":  {Data: []byte("gzip js")},
		"assets/font-1.woff2": {Data: []byte("font")},
	})
	for _, tc := range []struct {
		name, path, accept    string
		body, encoding, ctype string
		cache                 string
	}{
		{"brotli", "/assets/app-1.js", "gzip, deflate, br, zstd", "br js", "br", "application/javascript", "immutable"},
		{"gzip only", "/assets/app-1.js", "gzip", "gzip js", "gzip", "application/javascript", "immutable"},
		{"no encoding", "/assets/app-1.js", "", "console.log('the panel, uncompressed')", "", "application/javascript", "immutable"},
		{"nothing compressed", "/assets/font-1.woff2", "br", "font", "", "font/woff2", "immutable"},
		{"the app's own path", "/chats/telegram", "br", "br index", "br", "text/html", "no-cache"},
		{"the root", "/", "", "<!doctype html><title>panel</title>", "", "text/html", "no-cache"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.accept != "" {
				r.Header.Set("Accept-Encoding", tc.accept)
			}
			w := httptest.NewRecorder()
			panel.ServeHTTP(w, r)
			h := w.Header()
			if w.Code != http.StatusOK || w.Body.String() != tc.body || h.Get("Content-Encoding") != tc.encoding {
				t.Fatalf("status %d, Content-Encoding %q, body %q", w.Code, h.Get("Content-Encoding"), w.Body)
			}
			if !strings.HasPrefix(h.Get("Content-Type"), tc.ctype) || !strings.Contains(h.Get("Cache-Control"), tc.cache) || h.Get("Vary") != "Accept-Encoding" {
				t.Fatalf("Content-Type %q, Cache-Control %q, Vary %q", h.Get("Content-Type"), h.Get("Cache-Control"), h.Get("Vary"))
			}
		})
	}
}

func TestPanelNotBuiltSaysSo(t *testing.T) {
	w := httptest.NewRecorder()
	spa(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "make web") || w.Header().Get("Cache-Control") != "" {
		t.Fatalf("status %d, Cache-Control %q, body %q", w.Code, w.Header().Get("Cache-Control"), w.Body)
	}
}
