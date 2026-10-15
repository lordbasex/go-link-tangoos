package appproxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestStripFrameHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'self'; img-src *")
	StripFrameHeaders(h)
	if h.Get("X-Frame-Options") != "" {
		t.Error("X-Frame-Options sigue")
	}
	if got := h.Get("Content-Security-Policy"); got != "default-src 'self'; img-src *" {
		t.Errorf("CSP = %q", got)
	}
	h2 := http.Header{}
	h2.Set("Content-Security-Policy", "frame-ancestors 'none'")
	StripFrameHeaders(h2)
	if _, ok := h2["Content-Security-Policy"]; ok {
		t.Error("CSP vacío debería desaparecer")
	}
}

func TestProxyForwards(t *testing.T) {
	var gotHost, gotProto string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotProto = r.Host, r.Header.Get("X-Forwarded-Proto")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'; default-src 'self'")
		_, _ = w.Write([]byte("<html>app</html>"))
	}))
	defer up.Close()
	_, ps, _ := net.SplitHostPort(up.Listener.Addr().String())
	port, _ := strconv.Atoi(ps)

	front := httptest.NewServer(Handler(port))
	defer front.Close()
	req, _ := http.NewRequest("GET", front.URL+"/x", nil)
	req.Host = "nas.local:9100"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("X-Frame-Options") != "" {
		t.Fatalf("status %d xfo %q", resp.StatusCode, resp.Header.Get("X-Frame-Options"))
	}
	if resp.Header.Get("Content-Security-Policy") != "default-src 'self'" {
		t.Errorf("CSP = %q", resp.Header.Get("Content-Security-Policy"))
	}
	if gotHost != "nas.local:9100" || gotProto != "https" {
		t.Errorf("host %q proto %q", gotHost, gotProto)
	}
}

func TestEnsureValidatesPorts(t *testing.T) {
	m := &Manager{}
	if err := m.Ensure("x", 80, 3001); err == nil {
		t.Error("puerto fuera de rango aceptado")
	}
	if err := m.Ensure("x", 9100, 3001); err != nil { // sin TLS queda pendiente
		t.Fatal(err)
	}
	if m.Ports()["x"] != 9100 {
		t.Error("puerto no registrado")
	}
	m.Remove("x")
	if len(m.Ports()) != 0 {
		t.Error("no se quitó")
	}
}
