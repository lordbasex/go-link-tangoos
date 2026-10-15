package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":               {Data: []byte("<!doctype html><title>TangoOS Connect</title>")},
		"assets/index-abc.js":      {Data: []byte("console.log(1)")},
		"img/opt/desktop-800.webp": {Data: []byte("RIFF")},
		"bundle-sw.js":             {Data: []byte("self")},
	}
}

func get(t *testing.T, h http.Handler, p string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
	return rr
}

func TestRoutesFallBackToIndex(t *testing.T) {
	h := Handler(testFS(), "wss://signal.go-link.org")
	for _, p := range []string{"/", "/demo", "/nas", "/nas/vincular", "/nas/11111111-1111-4111-8111-111111111111", "/s/abcDEF123"} {
		rr := get(t, h, p)
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), "TangoOS Connect") {
			t.Fatalf("%s: %d %q", p, rr.Code, rr.Body.String())
		}
		if rr.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: cache %q", p, rr.Header().Get("Cache-Control"))
		}
	}
}

func TestAssetsCacheAndHeaders(t *testing.T) {
	h := Handler(testFS(), "wss://signal.go-link.org")
	rr := get(t, h, "/assets/index-abc.js")
	if rr.Code != 200 || !strings.Contains(rr.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %q", rr.Code, rr.Header().Get("Cache-Control"))
	}
	if ct := get(t, h, "/img/opt/desktop-800.webp").Header().Get("Content-Type"); ct != "image/webp" {
		t.Fatalf("webp content-type %q", ct)
	}
	csp := rr.Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self'", "connect-src 'self' wss://signal.go-link.org", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP sin %q: %s", want, csp)
		}
	}
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" || rr.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("faltan cabeceras de seguridad")
	}
}

func TestMissingAssetIs404AndTraversalStaysInside(t *testing.T) {
	h := Handler(testFS(), "wss://x")
	if rr := get(t, h, "/assets/nope.js"); rr.Code != 404 {
		t.Fatalf("asset faltante: %d", rr.Code)
	}
	if rr := get(t, h, "/../../etc/passwd"); rr.Code == 200 && strings.Contains(rr.Body.String(), "root:") {
		t.Fatal("traversal")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rr.Code)
	}
}
