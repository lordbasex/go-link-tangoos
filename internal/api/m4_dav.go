package api

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"golang.org/x/net/webdav"

	"github.com/lordbasex/go-link-tangoos/internal/auth"
)

// WebDAV (protocol/README.md §12): tangod lo sirve en https://<nas>/dav/ con usuario y
// contraseña de TangoOS (Basic sobre TLS). Cada usuario ve sus carpetas compartidas con
// sus permisos reales (file-worker). Borrar manda a la papelera.

const davPrefix = "/dav"

var davLocks = webdav.NewMemLS()

// DAVHandler es el handler HTTP de /dav/ (main lo monta en el mux de 443).
func (a *API) DAVHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.m4 == nil || !a.m4.serviceOn("webdav") || a.files == nil {
			http.NotFound(w, r)
			return
		}
		if r.TLS == nil {
			http.Error(w, "WebDAV solo por HTTPS", http.StatusForbidden)
			return
		}
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		user, pass, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="TangoOS", charset="UTF-8"`)
			http.Error(w, "iniciá sesión", http.StatusUnauthorized)
			return
		}
		if _, err := a.d.Auth.VerifyService(user, pass, ip); err != nil {
			if errors.Is(err, auth.ErrLocked) {
				http.Error(w, "demasiados intentos", http.StatusTooManyRequests)
				return
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="TangoOS", charset="UTF-8"`)
			http.Error(w, "usuario o contraseña incorrectos", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != "PROPFIND" && r.Method != http.MethodOptions {
			a.m4.audit(user, ip, "webdav", "webdav."+strings.ToLower(r.Method), r.URL.Path, "ok")
		}
		h := &webdav.Handler{Prefix: davPrefix, FileSystem: davFS{a.files.newUserFS(user, ip, "webdav")}, LockSystem: davLocks}
		h.ServeHTTP(w, r)
	})
}

type davFS struct{ u *userFS }

func (d davFS) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	return d.u.Mkdir(ctx, name)
}

func (d davFS) RemoveAll(ctx context.Context, name string) error { return d.u.Remove(ctx, name) }

func (d davFS) Rename(ctx context.Context, from, to string) error { return d.u.Rename(ctx, from, to) }

func (d davFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	return d.u.Stat(ctx, name)
}

func (d davFS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0 {
		w, err := d.u.Create(ctx, name)
		if err != nil {
			return nil, err
		}
		return &davWrite{w: w, name: path.Base(name)}, nil
	}
	info, err := d.u.Stat(ctx, name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return &davDir{u: d.u, p: name, info: info}, nil
	}
	f, err := d.u.OpenRead(ctx, name)
	if err != nil {
		return nil, err
	}
	return &davRead{File: f, info: info}, nil
}

// davRead lee un archivo; Stat devuelve la info del worker (nombre virtual).
type davRead struct {
	*os.File
	info vfsInfo
}

func (f *davRead) Stat() (fs.FileInfo, error)         { return f.info, nil }
func (f *davRead) Readdir(int) ([]fs.FileInfo, error) { return nil, fs.ErrInvalid }
func (f *davRead) Write([]byte) (int, error)          { return 0, fs.ErrPermission }

// davWrite escribe en el parcial; el commit pasa al cerrar.
type davWrite struct {
	w    *vfsWriter
	name string
	size int64
}

func (f *davWrite) Write(b []byte) (int, error) {
	n, err := f.w.Write(b)
	f.size += int64(n)
	return n, err
}
func (f *davWrite) Read([]byte) (int, error)                  { return 0, fs.ErrInvalid }
func (f *davWrite) Seek(off int64, whence int) (int64, error) { return f.w.Seek(off, whence) }
func (f *davWrite) Close() error                              { return f.w.Close() }
func (f *davWrite) Readdir(int) ([]fs.FileInfo, error)        { return nil, fs.ErrInvalid }
func (f *davWrite) Stat() (fs.FileInfo, error) {
	return vfsInfo{name: f.name, size: f.size, mtime: time.Now()}, nil
}

// davDir lista un directorio.
type davDir struct {
	u    *userFS
	p    string
	info vfsInfo
	list []vfsInfo
	pos  int
	read bool
}

func (d *davDir) Read([]byte) (int, error)       { return 0, fs.ErrInvalid }
func (d *davDir) Write([]byte) (int, error)      { return 0, fs.ErrInvalid }
func (d *davDir) Seek(int64, int) (int64, error) { return 0, nil }
func (d *davDir) Close() error                   { return nil }
func (d *davDir) Stat() (fs.FileInfo, error)     { return d.info, nil }
func (d *davDir) Readdir(count int) ([]fs.FileInfo, error) {
	if !d.read {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		l, err := d.u.ReadDir(ctx, d.p)
		if err != nil {
			return nil, err
		}
		d.list, d.read = l, true
	}
	rest := d.list[d.pos:]
	if count > 0 && len(rest) > count {
		rest = rest[:count]
	}
	d.pos += len(rest)
	out := make([]fs.FileInfo, len(rest))
	for i, x := range rest {
		out[i] = x
	}
	if count > 0 && len(out) == 0 {
		return nil, io.EOF
	}
	return out, nil
}
