// Package webui serves the desktop bundle embedded in the tangod binary.
//
// The bundle is a single-page app: any path that is not a file in the
// bundle and does not look like an asset falls back to index.html, so the
// client-side router can handle it.
package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// Bundle returns the embedded dist/ directory.
func Bundle() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err) // dist/ is embedded at build time; this cannot fail
	}
	return sub
}

func init() {
	// Go's built-in table depends on the host's mime.types; pin the types
	// the bundle uses so the result is the same on every system.
	for ext, typ := range map[string]string{
		".js":          "text/javascript; charset=utf-8",
		".mjs":         "text/javascript; charset=utf-8",
		".css":         "text/css; charset=utf-8",
		".html":        "text/html; charset=utf-8",
		".json":        "application/json",
		".webmanifest": "application/manifest+json",
		".svg":         "image/svg+xml",
		".woff2":       "font/woff2",
		".avif":        "image/avif",
		".webp":        "image/webp",
		".wasm":        "application/wasm",
	} {
		_ = mime.AddExtensionType(ext, typ)
	}
}

// Handler serves files from bundle with SPA fallback and cache headers:
// hashed files under /assets/ are immutable, everything else revalidates.
func Handler(bundle fs.FS) http.Handler {
	files := http.FileServerFS(bundle)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		// /dc/… lo sirve el Service Worker (sw.js, protocol §13). Si llega acá es que no hay
		// SW controlando la página: 404, nunca index.html (un <video> no debe recibir HTML).
		if strings.HasPrefix(name, "dc/") {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}

		info, err := fs.Stat(bundle, name)
		switch {
		case err == nil && !info.IsDir():
			// served below
		case isAssetPath(name):
			// A missing asset is a real 404, never index.html: returning
			// HTML for a .js request breaks the page in confusing ways.
			http.NotFound(w, r)
			return
		default:
			name = "index.html"
		}

		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if name == "index.html" {
			// Served directly so FileServer does not redirect "/index.html" to "/".
			serveIndex(w, r, bundle)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + name
		files.ServeHTTP(w, r2)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, bundle fs.FS) {
	data, err := fs.ReadFile(bundle, "index.html")
	if err != nil {
		http.Error(w, "index.html missing from bundle", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

// isAssetPath reports whether name looks like a static file (has an
// extension or lives under assets/) rather than a client-side route.
func isAssetPath(name string) bool {
	return strings.HasPrefix(name, "assets/") || path.Ext(name) != ""
}
