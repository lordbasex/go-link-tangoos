package appproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGate(t *testing.T) {
	g, err := NewGate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var gotCookie string
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte("app ok"))
	})
	h := g.wrap("jellyfin", app)

	// Sin cookie: 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/web/", nil))
	if rec.Code != 401 {
		t.Fatalf("no cookie: %d", rec.Code)
	}
	// Ticket de otra app o falso: 401.
	sid := "0123456789abcdef0123456789abcdef"
	alive := true
	g.SessionAlive = func(id string) bool { return id == sid && alive }
	tk := g.Ticket("uptime-kuma", sid)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", TicketPath+"?t="+tk, nil))
	if rec.Code != 401 {
		t.Fatalf("other app ticket: %d", rec.Code)
	}
	// Ticket válido: cookie y redirección (solo a rutas relativas).
	tk = g.Ticket("jellyfin", sid)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", TicketPath+"?t="+tk+"&next=//evil.com", nil))
	if rec.Code != 302 || rec.Header().Get("Location") != "/" {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	c := rec.Result().Cookies()[0]
	if c.Name != CookieName || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteNoneMode {
		t.Fatalf("cookie: %+v", c)
	}
	// Un solo uso.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", TicketPath+"?t="+tk, nil))
	if rec.Code != 401 {
		t.Fatal("ticket reused")
	}
	// Con la cookie pasa, y la cookie de TangoOS no llega a la app.
	req := httptest.NewRequest("GET", "/web/", nil)
	req.AddCookie(c)
	req.AddCookie(&http.Cookie{Name: "jf_session", Value: "x"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || strings.Contains(gotCookie, CookieName) || !strings.Contains(gotCookie, "jf_session=x") {
		t.Fatalf("pass: %d cookie=%q", rec.Code, gotCookie)
	}
	// La cookie no sirve para otra app ni vencida ni alterada.
	if g.Valid("uptime-kuma", c.Value, time.Now()) || g.Valid("jellyfin", c.Value, time.Now().Add(13*time.Hour)) || g.Valid("jellyfin", c.Value+"0", time.Now()) {
		t.Fatal("cookie validation too lax")
	}
	// Al cerrar la sesión de TangoOS la cookie deja de valer (§13).
	alive = false
	req = httptest.NewRequest("GET", "/web/", nil)
	req.AddCookie(c)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("cookie de sesión cerrada aceptada: %d", rec.Code)
	}
}
