package api

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/fileworker"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// userFS es la vista de archivos de un usuario para WebDAV y SFTP (protocol/README.md
// §12): las mismas rutas virtuales que la app Archivos ("/<Carpeta>/sub/x") y las mismas
// operaciones del file-worker con su UID. Borrar manda a la papelera de la carpeta.
type userFS struct {
	fa   *filesAPI
	sess *rtcrpc.Session
}

// newUserFS arma una sesión sintética autenticada como user (no viaja por WebRTC).
func (fa *filesAPI) newUserFS(user, ip, client string) *userFS {
	s := rtcrpc.NewSession("svc-"+client+"-"+user, ip, client, nil, nil, nil)
	s.Authenticate(user, "")
	return &userFS{fa: fa, sess: s}
}

// vfsInfo es un fs.FileInfo de una entrada del worker.
type vfsInfo struct {
	name  string
	size  int64
	dir   bool
	mtime time.Time
}

func (i vfsInfo) Name() string { return i.name }
func (i vfsInfo) Size() int64  { return i.size }
func (i vfsInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o775
	}
	return 0o664
}
func (i vfsInfo) ModTime() time.Time { return i.mtime }
func (i vfsInfo) IsDir() bool        { return i.dir }
func (i vfsInfo) Sys() any           { return nil }

func infoOf(e fileworker.Entry) vfsInfo {
	t, _ := time.Parse(time.RFC3339, e.MTime)
	return vfsInfo{name: e.Name, size: e.Size, dir: e.Dir, mtime: t}
}

// Errores al estilo os para que webdav y sftp devuelvan 404/403/409.
func vfsErr(err error) error {
	var re *rtcrpc.Error
	if errors.As(err, &re) {
		switch re.Code {
		case fileworker.CodeNotFound, fileworker.CodeEscape:
			return fs.ErrNotExist
		case fileworker.CodePermission:
			return fs.ErrPermission
		case fileworker.CodeExists:
			return fs.ErrExist
		case fileworker.CodeInvalid, fileworker.CodeInvalidName:
			return fs.ErrInvalid
		}
	}
	return err
}

func cleanV(p string) string { return path.Clean("/" + strings.TrimSpace(p)) }

// Stat de una ruta virtual ("/" y "/<Carpeta>" son directorios).
func (u *userFS) Stat(ctx context.Context, p string) (vfsInfo, error) {
	p = cleanV(p)
	if p == "/" {
		return vfsInfo{name: "/", dir: true, mtime: time.Now()}, nil
	}
	if strings.Count(p, "/") == 1 {
		shares, err := u.fa.accessible(ctx, u.sess)
		if err != nil {
			return vfsInfo{}, vfsErr(err)
		}
		for _, sh := range shares {
			if strings.EqualFold(sh.Name, p[1:]) {
				var e fileworker.Entry
				_ = u.fa.call(ctx, u.sess, fileworker.Request{Op: fileworker.OpStat, Root: sh.Path, Rel: "."}, &e)
				e.Name, e.Dir = sh.Name, true
				return infoOf(e), nil
			}
		}
		return vfsInfo{}, fs.ErrNotExist
	}
	loc, err := u.fa.resolve(ctx, u.sess, p)
	if err != nil {
		return vfsInfo{}, vfsErr(err)
	}
	var e fileworker.Entry
	if err := u.fa.call(ctx, u.sess, fileworker.Request{Op: fileworker.OpStat, Root: loc.root, Rel: loc.rel}, &e); err != nil {
		return vfsInfo{}, vfsErr(err)
	}
	return infoOf(e), nil
}

// ReadDir lista un directorio.
func (u *userFS) ReadDir(ctx context.Context, p string) ([]vfsInfo, error) {
	entries, err := u.fa.list(ctx, u.sess, cleanV(p))
	if err != nil {
		return nil, vfsErr(err)
	}
	out := make([]vfsInfo, 0, len(entries))
	for _, e := range entries {
		t, _ := time.Parse(time.RFC3339, e.MTime)
		out = append(out, vfsInfo{name: e.Name, size: e.Size, dir: e.Kind == "dir", mtime: t})
	}
	return out, nil
}

// OpenRead abre un archivo para leer (el fd lo abrió el worker con el UID del usuario).
func (u *userFS) OpenRead(ctx context.Context, p string) (*os.File, error) {
	loc, err := u.fa.resolve(ctx, u.sess, cleanV(p))
	if err != nil {
		return nil, vfsErr(err)
	}
	var or fileworker.OpenResult
	f, err := u.fa.callFD(ctx, u.sess, fileworker.Request{Op: fileworker.OpOpenRead, Root: loc.root, Rel: loc.rel}, &or)
	if err != nil {
		return nil, vfsErr(err)
	}
	if f == nil {
		return nil, fs.ErrInvalid
	}
	return f, nil
}

// vfsWriter escribe en un parcial y al cerrar hace commit atómico (con reemplazo).
type vfsWriter struct {
	*os.File
	u          *userFS
	root, dir  string
	part, name string
	once       sync.Once
	err        error
}

// Create abre un archivo para escribir (reemplaza al cerrar si ya existía).
func (u *userFS) Create(ctx context.Context, p string) (*vfsWriter, error) {
	p = cleanV(p)
	dir, name := path.Split(p)
	if e := fileworker.ValidName(name); e != nil {
		return nil, fs.ErrInvalid
	}
	loc, err := u.fa.resolve(ctx, u.sess, dir)
	if err != nil {
		return nil, vfsErr(err)
	}
	var or fileworker.OpenResult
	f, err := u.fa.callFD(ctx, u.sess, fileworker.Request{Op: fileworker.OpOpenWrite, Root: loc.root, Rel: loc.rel, Name: name}, &or)
	if err != nil {
		return nil, vfsErr(err)
	}
	if f == nil {
		return nil, fs.ErrInvalid
	}
	return &vfsWriter{File: f, u: u, root: loc.root, dir: loc.rel, part: or.Part, name: name}, nil
}

// Close confirma la escritura.
func (w *vfsWriter) Close() error {
	w.once.Do(func() {
		if err := w.File.Sync(); err != nil {
			w.err = err
		}
		_ = w.File.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if w.err != nil {
			_ = w.u.fa.call(ctx, w.u.sess, fileworker.Request{Op: fileworker.OpAbort, Root: w.root, Rel: w.dir, Name: w.part}, nil)
			return
		}
		var e fileworker.Entry
		w.err = vfsErr(w.u.fa.call(ctx, w.u.sess, fileworker.Request{Op: fileworker.OpCommit, Root: w.root, Rel: w.dir, Name: w.part, DstRel: w.name, Overwrite: true}, &e))
	})
	return w.err
}

// Mkdir crea una carpeta.
func (u *userFS) Mkdir(ctx context.Context, p string) error {
	loc, err := u.fa.resolve(ctx, u.sess, cleanV(p))
	if err != nil {
		return vfsErr(err)
	}
	if loc.rel == "." {
		return fs.ErrPermission // las carpetas compartidas se crean desde Ajustes
	}
	var e fileworker.Entry
	return vfsErr(u.fa.call(ctx, u.sess, fileworker.Request{Op: fileworker.OpMkdir, Root: loc.root, Rel: path.Dir(loc.rel), Name: path.Base(loc.rel)}, &e))
}

// Remove manda a la papelera de la carpeta (se puede recuperar desde Archivos).
func (u *userFS) Remove(ctx context.Context, p string) error {
	loc, err := u.fa.resolve(ctx, u.sess, cleanV(p))
	if err != nil {
		return vfsErr(err)
	}
	if loc.rel == "." {
		return fs.ErrPermission
	}
	return vfsErr(u.fa.call(ctx, u.sess, fileworker.Request{Op: fileworker.OpTrash, Root: loc.root, Rel: loc.rel}, nil))
}

// Rename mueve y/o renombra.
func (u *userFS) Rename(ctx context.Context, from, to string) error {
	src, err := u.fa.resolve(ctx, u.sess, cleanV(from))
	if err != nil {
		return vfsErr(err)
	}
	dstDir, dstName := path.Split(cleanV(to))
	dst, err := u.fa.resolve(ctx, u.sess, dstDir)
	if err != nil {
		return vfsErr(err)
	}
	if src.rel == "." {
		return fs.ErrPermission
	}
	rel := src.rel
	if src.root != dst.root || path.Dir(src.rel) != dst.rel {
		var e fileworker.Entry
		if err := u.fa.call(ctx, u.sess, fileworker.Request{Op: fileworker.OpMove, Root: src.root, Rel: src.rel, DstRoot: dst.root, DstRel: dst.rel}, &e); err != nil {
			return vfsErr(err)
		}
		rel = path.Join(dst.rel, path.Base(src.rel))
	}
	if path.Base(rel) == dstName {
		return nil
	}
	var e fileworker.Entry
	return vfsErr(u.fa.call(ctx, u.sess, fileworker.Request{Op: fileworker.OpRename, Root: dst.root, Rel: rel, Name: dstName}, &e))
}
