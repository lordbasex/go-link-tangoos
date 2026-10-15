// Package appproxy abre un proxy HTTPS por cada app del Centro de apps (protocol/README.md §9).
//
// Cada app instalada recibe un puerto propio (9100+) con el mismo certificado de tangod,
// que reenvía a 127.0.0.1:<puerto web de la app>. Así la ventana del escritorio puede
// mostrar la app en un iframe HTTPS (sin contenido mixto) y se quitan X-Frame-Options y
// frame-ancestors, que la mayoría de las apps mandan para impedir justamente eso.
//
// Riesgo conocido (MVP): el proxy no exige la sesión de TangoOS; cada app tiene su propio
// login. TODO M4: compuerta por cookie firmada por tangod (docs/07).
package appproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// FirstPort es el primer puerto del rango de proxies de apps.
const FirstPort = 9100

// LastPort es el último puerto del rango.
const LastPort = 9199

type entry struct {
	port, target int
	srv          *http.Server
}

// Manager administra los proxies.
type Manager struct {
	Log *slog.Logger

	mu      sync.Mutex
	tls     *tls.Config
	entries map[string]*entry
}

// Default es el manager de tangod (main le pasa el TLS cuando carga el certificado).
var Default = &Manager{}

// SetTLS fija el certificado y arranca los proxies pendientes.
func (m *Manager) SetTLS(cfg *tls.Config) {
	m.mu.Lock()
	m.tls = cfg
	pending := []string{}
	for id, e := range m.entries {
		if e.srv == nil {
			pending = append(pending, id)
		}
	}
	m.mu.Unlock()
	for _, id := range pending {
		m.mu.Lock()
		e := m.entries[id]
		m.mu.Unlock()
		if e != nil {
			_ = m.Ensure(id, e.port, e.target)
		}
	}
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// Ensure deja corriendo el proxy :port → 127.0.0.1:target para la app id.
func (m *Manager) Ensure(id string, port, target int) error {
	if port < FirstPort || port > LastPort || target < 1 || target > 65535 {
		return errors.New("puerto de proxy inválido")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = map[string]*entry{}
	}
	if e := m.entries[id]; e != nil && e.srv != nil && e.port == port && e.target == target {
		return nil
	}
	if e := m.entries[id]; e != nil && e.srv != nil {
		_ = e.srv.Close()
	}
	e := &entry{port: port, target: target}
	m.entries[id] = e
	if m.tls == nil {
		return nil // se arranca en SetTLS
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("proxy de %s: %w", id, err)
	}
	e.srv = &http.Server{
		Handler:           handlerFor(id, target), // compuerta de sesión (gate.go)
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		TLSConfig:         m.tls.Clone(),
		ErrorLog:          slog.NewLogLogger(m.log().Handler(), slog.LevelDebug),
	}
	srv := e.srv
	go func() {
		if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.log().Warn("proxy de app terminó", "app", id, "port", port, "err", err)
		}
	}()
	m.log().Info("proxy de app", "app", id, "port", port, "target", target)
	return nil
}

// Remove cierra el proxy de la app.
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.entries[id]; e != nil {
		if e.srv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = e.srv.Shutdown(ctx)
			cancel()
		}
		delete(m.entries, id)
	}
}

// Ports devuelve los puertos de proxy en uso por app.
func (m *Manager) Ports() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for id, e := range m.entries {
		out[id] = e.port
	}
	return out
}

// Handler es el reverse proxy hacia 127.0.0.1:target (WebSockets incluidos).
func Handler(target int) http.Handler {
	up := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", target)}
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(up)
			pr.Out.Host = pr.In.Host // las apps validan su dominio (p. ej. trusted_domains)
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Proto", "https")
		},
		ModifyResponse: func(r *http.Response) error {
			StripFrameHeaders(r.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><body style="font:14px system-ui;padding:40px;color:#555">` +
				`<h2 style="margin:0 0 8px">La app no responde</h2><p>Puede estar iniciando todavía. Esperá unos segundos y recargá.</p>`))
		},
		FlushInterval: -1,
	}
}

// StripFrameHeaders quita X-Frame-Options y la directiva frame-ancestors del CSP.
func StripFrameHeaders(h http.Header) {
	h.Del("X-Frame-Options")
	for _, k := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		vals := h.Values(k)
		if len(vals) == 0 {
			continue
		}
		h.Del(k)
		for _, v := range vals {
			var keep []string
			for _, d := range strings.Split(v, ";") {
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(d)), "frame-ancestors") {
					continue
				}
				if strings.TrimSpace(d) != "" {
					keep = append(keep, strings.TrimSpace(d))
				}
			}
			if len(keep) > 0 {
				h.Add(k, strings.Join(keep, "; "))
			}
		}
	}
}
