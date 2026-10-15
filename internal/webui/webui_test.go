package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func testBundle() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>TangoOS</title>")},
		"assets/app-abc123.js": {Data: []byte("console.log(1)")},
		"assets/inter.woff2":   {Data: []byte("wOF2")},
		"favicon.svg":          {Data: []byte("<svg/>")},
	}
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHandler(t *testing.T) {
	h := Handler(testBundle())
	tests := []struct {
		name, method, target string
		status               int
		cache, ctype, body   string
	}{
		{"root serves index", "GET", "/", 200, "no-cache", "text/html; charset=utf-8", "<!doctype html><title>TangoOS</title>"},
		{"index.html is not redirected", "GET", "/index.html", 200, "no-cache", "text/html; charset=utf-8", ""},
		{"service worker stream path is never the SPA", "GET", "/dc/stream/abcdefghijklmnop/video.mp4", 404, "no-store", "", ""},
		{"client route falls back to index", "GET", "/apps/storage/volumes", 200, "no-cache", "text/html; charset=utf-8", "<!doctype html><title>TangoOS</title>"},
		{"hashed asset is immutable", "GET", "/assets/app-abc123.js", 200, "public, max-age=31536000, immutable", "text/javascript; charset=utf-8", "console.log(1)"},
		{"font mime", "GET", "/assets/inter.woff2", 200, "public, max-age=31536000, immutable", "font/woff2", ""},
		{"svg mime and revalidate outside assets", "GET", "/favicon.svg", 200, "no-cache", "image/svg+xml", ""},
		{"missing asset is 404, not index", "GET", "/assets/missing.js", 404, "", "", ""},
		{"missing file with extension is 404", "GET", "/nope.png", 404, "", "", ""},
		{"traversal is cleaned", "GET", "/../../etc/passwd", 200, "no-cache", "text/html; charset=utf-8", ""},
		{"head works", "HEAD", "/", 200, "no-cache", "text/html; charset=utf-8", ""},
		{"post rejected", "POST", "/", 405, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(t, h, tt.method, tt.target)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if tt.cache != "" && rec.Header().Get("Cache-Control") != tt.cache {
				t.Errorf("Cache-Control = %q, want %q", rec.Header().Get("Cache-Control"), tt.cache)
			}
			if tt.ctype != "" && rec.Header().Get("Content-Type") != tt.ctype {
				t.Errorf("Content-Type = %q, want %q", rec.Header().Get("Content-Type"), tt.ctype)
			}
			if tt.body != "" && rec.Body.String() != tt.body {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.body)
			}
			if tt.method == "HEAD" && rec.Body.Len() != 0 {
				t.Errorf("HEAD returned a body")
			}
		})
	}
}

func TestEmbeddedBundleHasIndex(t *testing.T) {
	rec := get(t, Handler(Bundle()), "GET", "/")
	if rec.Code != 200 {
		t.Fatalf("embedded bundle: status %d", rec.Code)
	}
}
