package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRedirectToHTTPS(t *testing.T) {
	tests := []struct{ addr, host, target, want string }{
		{":443", "nas.local", "/a?b=1", "https://nas.local/a?b=1"},
		{":443", "nas.local:80", "/", "https://nas.local/"},
		{":8443", "192.168.1.5", "/x", "https://192.168.1.5:8443/x"},
		{":443", "[fe80::1]:80", "/", "https://[fe80::1]/"},
	}
	for _, tt := range tests {
		req := httptest.NewRequest("GET", tt.target, nil)
		req.Host = tt.host
		rec := httptest.NewRecorder()
		redirectToHTTPS(tt.addr).ServeHTTP(rec, req)
		if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != tt.want {
			t.Errorf("%s%s → %d %q, want %q", tt.host, tt.target, rec.Code, rec.Header().Get("Location"), tt.want)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	securityHeaders(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "X-Frame-Options"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be sent over plain HTTP")
	}
}
