package appproxy

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Compuerta de sesión del proxy de apps (protocol/README.md §12): sin una cookie
// tango_app firmada por tangod para esa app, el proxy no deja pasar. La cookie se
// obtiene con un ticket de un solo uso (apps.ticket desde una sesión de TangoOS), que el
// proxy canjea en /.tango/ticket y redirige a la app.

const (
	CookieName = "tango_app"
	TicketPath = "/.tango/ticket"
	cookieTTL  = 12 * time.Hour
	ticketTTL  = 60 * time.Second
)

// Gate emite tickets y valida cookies. Si es nil el proxy no exige sesión.
//
// La cookie queda atada a la sesión de TangoOS que pidió el ticket (protocol/README.md
// §13): app|sid|exp|firma. Si SessionAlive dice que esa sesión cerró o venció, la
// cookie deja de valer aunque su firma y su vencimiento estén bien.
type Gate struct {
	key []byte
	// SessionAlive (opcional) consulta si la sesión sigue viva (auth.Store.SessionAlive).
	SessionAlive func(sid string) bool

	mu      sync.Mutex
	tickets map[string]ticket
}

type ticket struct {
	app string
	sid string
	exp time.Time
}

// DefaultGate es la compuerta de tangod (nil = sin compuerta).
var DefaultGate *Gate

// NewGate carga (o crea) la clave HMAC en <dir>/app-gate.key (0600): así las cookies
// sobreviven a un reinicio de tangod.
func NewGate(dir string) (*Gate, error) {
	p := filepath.Join(dir, "app-gate.key")
	key, err := os.ReadFile(p)
	if err != nil || len(key) < 32 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, key, 0o600); err != nil {
			return nil, err
		}
	}
	return &Gate{key: key, tickets: map[string]ticket{}}, nil
}

// Ticket crea un ticket de un solo uso para la app, atado a la sesión sid.
func (g *Gate) Ticket(app, sid string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	t := base64.RawURLEncoding.EncodeToString(b)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	for k, v := range g.tickets {
		if now.After(v.exp) {
			delete(g.tickets, k)
		}
	}
	g.tickets[t] = ticket{app: app, sid: sid, exp: now.Add(ticketTTL)}
	return t
}

// redeem canjea un ticket y devuelve la sesión a la que está atado.
func (g *Gate) redeem(app, t string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := g.tickets[t]
	delete(g.tickets, t)
	return v.sid, ok && v.app == app && time.Now().Before(v.exp)
}

func (g *Gate) sign(app, sid string, exp int64) string {
	m := hmac.New(sha256.New, g.key)
	fmt.Fprintf(m, "%s|%s|%d", app, sid, exp)
	return hex.EncodeToString(m.Sum(nil))
}

// Cookie arma el valor de la cookie para app y la sesión sid.
func (g *Gate) Cookie(app, sid string, now time.Time) string {
	exp := now.Add(cookieTTL).Unix()
	return fmt.Sprintf("%s|%s|%d|%s", app, sid, exp, g.sign(app, sid, exp))
}

// Valid verifica una cookie para app: firma, vencimiento y que la sesión siga viva.
func (g *Gate) Valid(app, v string, now time.Time) bool {
	parts := strings.Split(v, "|")
	if len(parts) != 4 || parts[0] != app {
		return false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	if !hmac.Equal([]byte(parts[3]), []byte(g.sign(app, parts[1], exp))) {
		return false
	}
	return g.SessionAlive == nil || g.SessionAlive(parts[1])
}

// wrap protege el handler de una app.
func (g *Gate) wrap(app string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == TicketPath {
			t := r.URL.Query().Get("t")
			sid, ok := g.redeem(app, t)
			if !ok {
				denied(w, "El enlace para abrir la app venció o ya se usó.")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: CookieName, Value: g.Cookie(app, sid, time.Now()), Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode, MaxAge: int(cookieTTL.Seconds())})
			next := r.URL.Query().Get("next")
			if u, err := url.Parse(next); err != nil || next == "" || u.IsAbs() || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
				next = "/"
			}
			http.Redirect(w, r, next, http.StatusFound)
			return
		}
		if c, err := r.Cookie(CookieName); err == nil && g.Valid(app, c.Value, time.Now()) {
			// La cookie de TangoOS no se reenvía a la app.
			keep := r.Cookies()
			r.Header.Del("Cookie")
			for _, ck := range keep {
				if ck.Name != CookieName {
					r.AddCookie(ck)
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		denied(w, "Abrí esta app desde el Centro de apps de TangoOS (si cerraste sesión, volvé a entrar).")
	})
}

func denied(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><body style="font:14px system-ui;padding:40px;color:#555"><h2 style="margin:0 0 8px">Acceso protegido por TangoOS</h2><p>` + msg + `</p>`))
}

// handlerFor aplica la compuerta si está activa.
func handlerFor(app string, target int) http.Handler {
	h := Handler(target)
	if DefaultGate != nil {
		return DefaultGate.wrap(app, h)
	}
	return h
}
