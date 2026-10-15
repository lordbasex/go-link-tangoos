// Command connect-web sirve el portal TangoOS Connect (web/apps/connect) desde un único binario Go,
// para quien quiera publicarlo en su propio dominio en vez de usar https://tangoos.go-link.org
// (que se publica en S3 + CloudFront con scripts/deploy-connect-web.sh).
//
//	make connect-web && ./dist/connect-web -addr :8080 -signal wss://signal.go-link.org
//
// TLS conviene ponerlo adelante (un proxy o un balanceador): el portal no maneja secretos propios.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var distFS embed.FS

func main() {
	addr := flag.String("addr", envOr("CONNECT_WEB_ADDR", ":8080"), "dirección de escucha")
	signal := flag.String("signal", envOr("CONNECT_WEB_SIGNAL", "wss://signal.go-link.org"), "origen del signalhub permitido en connect-src")
	flag.Parse()

	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		slog.Error("bundle embebido", "err", err)
		os.Exit(1)
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           Handler(sub, *signal),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	slog.Info("connect-web escuchando", "addr", *addr, "signal", *signal)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("servidor", "err", err)
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// CSP del portal. connect-src: el propio sitio y el signalhub. Los estilos inline los usa el escritorio
// (posiciones de ventanas); los scripts solo salen del sitio (el escritorio del NAS se sirve desde
// /nas-bundle/ por el Service Worker, también 'self').
func csp(signal string) string {
	return "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; " +
		"connect-src 'self' " + signal + "; worker-src 'self'; frame-src 'self' data: blob:; " +
		"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}

// Handler sirve los archivos del portal con caché inmutable para /assets/ y /img/opt/, y devuelve
// index.html para las rutas de la app (/, /demo, /nas/…, /s/…).
func Handler(files fs.FS, signal string) http.Handler {
	policy := csp(signal)
	fileServer := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", policy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}
		p := path.Clean("/" + r.URL.Path)
		name := strings.TrimPrefix(p, "/")
		if name != "" {
			if st, err := fs.Stat(files, name); err == nil && !st.IsDir() {
				switch {
				case strings.HasPrefix(p, "/assets/") || strings.HasPrefix(p, "/img/opt/"):
					h.Set("Cache-Control", "public, max-age=31536000, immutable")
				default:
					h.Set("Cache-Control", "no-cache")
				}
				if strings.HasSuffix(name, ".webp") {
					h.Set("Content-Type", "image/webp")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
			// Un archivo con extensión que no existe es un 404, no la app.
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		h.Set("Cache-Control", "no-cache")
		h.Set("Content-Type", "text/html; charset=utf-8")
		b, err := fs.ReadFile(files, "index.html")
		if err != nil {
			http.Error(w, "portal sin construir", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(b)
	})
}
