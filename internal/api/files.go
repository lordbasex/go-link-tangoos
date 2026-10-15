package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash"
	"hash/fnv"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/fileworker"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Archivos real (protocol/README.md §10). Toda operación sobre archivos corre en un
// file-worker con el UID/GIDs del usuario (docs/07 §5): tangod solo traduce rutas
// virtuales ("/Familia/sub/x.jpg") a raíces de carpetas compartidas, y lee o escribe
// por los descriptores que el worker abrió con los permisos del usuario.

// WorkerSpawner lanza file-workers (agentclient.Client lo implementa).
type WorkerSpawner interface {
	SpawnFileWorker(ctx context.Context, user string) (*net.UnixConn, agentproto.FileWorkerResult, error)
}

// fileChans es lo que la capa WebRTC guarda en la sesión con la clave "files".
type fileChans interface {
	N() int
	SendFile(ch int, b []byte) error
	WaitFile(ctx context.Context, ch int, max uint64) error
}

// FileEntry es exactamente el FileEntry de TS.
type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Size  int64  `json:"size"`
	MTime string `json:"mtime"`
	Seed  uint32 `json:"seed"`
}

// TrashEntry es un elemento de la papelera.
type TrashEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`   // ruta virtual en la papelera: /Carpeta/.tango-trash/<id>
	Origin    string `json:"origin"` // ruta virtual original
	DeletedAt string `json:"deletedAt"`
	Size      int64  `json:"size"`
	Kind      string `json:"kind"`
}

// FileVersion es una versión de un archivo en una instantánea.
type FileVersion struct {
	Snapshot Snapshot `json:"snapshot"`
	Size     int64    `json:"size"`
	MTime    string   `json:"mtime"`
}

const (
	chunkSize      = 64 << 10 // frames de 64 KiB: lo que cualquier navegador acepta
	maxBuffered    = 4 << 20  // backpressure por canal (docs/03 §7)
	maxXfersPerSes = 8
	uploadIdle     = 60 * time.Second
	workerIdle     = 5 * time.Minute
)

type filesAPI struct {
	a       *API
	spawner WorkerSpawner
	thumbs  string

	mu      sync.Mutex
	workers map[string]*fileworker.Client
	spawnMu sync.Mutex

	xmu    sync.Mutex
	xfers  map[uint32]*xfer
	nextID atomic.Uint32

	// Cambios externos (SMB/NFS/SFTP) en carpetas abiertas: files_watch.go.
	wmu      sync.Mutex
	recent   map[string]map[string]*watchDir // usuario → clave → carpeta
	watching map[string]bool
}

// xfer es una transferencia en curso.
type xfer struct {
	id      uint32
	sess    *rtcrpc.Session
	upload  bool
	ch      int
	f       *os.File
	size    int64
	from    int64 // bajada con rango: [from, to)
	to      int64
	done    atomic.Int64
	hasher  hash.Hash
	nextOff int64 // para el sha256 en orden
	share   Share
	root    string
	dirRel  string
	name    string
	part    string
	over    bool
	vpath   string
	cancel  context.CancelFunc
	ctx     context.Context
	last    atomic.Int64 // última actividad (unix nano)
	mu      sync.Mutex
	failed  error
	lastEvt time.Time
}

func (a *API) registerFiles(r *rtcrpc.Router, sp WorkerSpawner) *filesAPI {
	fa := &filesAPI{a: a, spawner: sp, thumbs: filepath.Join(a.d.StateDir, "thumbs"), workers: map[string]*fileworker.Client{}, xfers: map[uint32]*xfer{}}
	_ = os.MkdirAll(fa.thumbs, 0o700)
	go fa.reaper()

	type pathP struct {
		Path string `json:"path"`
	}
	type searchP struct {
		Q    string `json:"q"`
		Path string `json:"path"`
	}
	type renameP struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	type pathsP struct {
		Paths []string `json:"paths"`
	}
	type moveP struct {
		Paths []string `json:"paths"`
		Dest  string   `json:"dest"`
	}
	type shareP struct {
		Share string `json:"share"`
	}
	type versionP struct {
		Path     string `json:"path"`
		Snapshot string `json:"snapshot"`
	}
	type thumbP struct {
		Path     string `json:"path"`
		Size     int    `json:"size"`
		Snapshot string `json:"snapshot"`
	}
	// Lectura de instantáneas (protocol/README.md §13): solo list, stat, thumb y download
	// aceptan "snapshot"; las operaciones que escriben no (DisallowUnknownFields).
	type snapPathP struct {
		Path     string `json:"path"`
		Snapshot string `json:"snapshot"`
	}
	type downP struct {
		Path     string `json:"path"`
		Snapshot string `json:"snapshot"`
		Offset   *int64 `json:"offset"`
		Length   *int64 `json:"length"`
	}
	type countR struct {
		Count int `json:"count"`
	}
	type upBeginP struct {
		Dir       string `json:"dir"`
		Name      string `json:"name"`
		Size      int64  `json:"size"`
		Overwrite bool   `json:"overwrite"`
	}
	type upBeginR struct {
		Xfer    uint32 `json:"xfer"`
		Channel string `json:"channel"`
		Chunk   int    `json:"chunk"`
	}
	type upEndP struct {
		Xfer   uint32 `json:"xfer"`
		SHA256 string `json:"sha256"`
	}
	type downBeginR struct {
		Xfer    uint32 `json:"xfer"`
		Channel string `json:"channel"`
		Size    int64  `json:"size"` // tamaño total del archivo
		Chunk   int    `json:"chunk"`
		Offset  int64  `json:"offset"` // primer byte que se manda
		Length  int64  `json:"length"` // bytes que se mandan (== size sin rango)
	}
	type xferP struct {
		Xfer uint32 `json:"xfer"`
	}

	r.Handle("files.list", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p snapPathP) ([]FileEntry, error) {
		return fa.listAt(ctx, s, p.Path, p.Snapshot)
	}))
	r.Handle("files.stat", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p snapPathP) (FileEntry, error) {
		loc, err := fa.locate(ctx, s, p.Path, p.Snapshot)
		if err != nil {
			return FileEntry{}, err
		}
		var e fileworker.Entry
		if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpStat, Root: loc.root, Rel: loc.rel}, &e); err != nil {
			return FileEntry{}, err
		}
		if loc.rel == "." {
			e.Name, e.Dir = loc.share.Name, true
		}
		return loc.entry(e), nil
	}))
	r.Handle("files.search", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p searchP) ([]FileEntry, error) {
		return fa.search(ctx, s, p.Q, p.Path)
	}))
	r.Handle("files.mkdir", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p pathP) (FileEntry, error) {
		loc, err := fa.resolve(ctx, s, p.Path)
		if err != nil {
			return FileEntry{}, err
		}
		if loc.rel == "." {
			return FileEntry{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "las carpetas compartidas se crean en Ajustes")
		}
		var e fileworker.Entry
		if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpMkdir, Root: loc.root, Rel: path.Dir(loc.rel), Name: path.Base(loc.rel)}, &e); err != nil {
			return FileEntry{}, err
		}
		fa.changed(loc.share, path.Dir(loc.rel))
		return loc.entry(e), nil
	}))
	r.Handle("files.rename", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p renameP) (FileEntry, error) {
		loc, err := fa.resolve(ctx, s, p.Path)
		if err != nil {
			return FileEntry{}, err
		}
		var e fileworker.Entry
		if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpRename, Root: loc.root, Rel: loc.rel, Name: p.Name}, &e); err != nil {
			return FileEntry{}, err
		}
		fa.changed(loc.share, path.Dir(loc.rel))
		return loc.entry(e), nil
	}))
	r.Handle("files.move", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p moveP) (countR, error) {
		dst, err := fa.resolve(ctx, s, p.Dest)
		if err != nil {
			return countR{}, err
		}
		n := 0
		for _, src := range p.Paths {
			loc, err := fa.resolve(ctx, s, src)
			if err != nil {
				return countR{Count: n}, err
			}
			if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpMove, Root: loc.root, Rel: loc.rel, DstRoot: dst.root, DstRel: dst.rel}, nil); err != nil {
				return countR{Count: n}, err
			}
			fa.changed(loc.share, path.Dir(loc.rel))
			n++
		}
		fa.changed(dst.share, dst.rel)
		return countR{Count: n}, nil
	}))
	r.Handle("files.trash", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p pathsP) (countR, error) {
		n := 0
		for _, vp := range p.Paths {
			loc, err := fa.resolve(ctx, s, vp)
			if err != nil {
				return countR{Count: n}, err
			}
			if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpTrash, Root: loc.root, Rel: loc.rel}, nil); err != nil {
				return countR{Count: n}, err
			}
			fa.changed(loc.share, path.Dir(loc.rel))
			n++
		}
		return countR{Count: n}, nil
	}))
	r.Handle("files.trash.list", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p shareP) ([]TrashEntry, error) {
		return fa.trashList(ctx, s, p.Share)
	}))
	r.Handle("files.restore", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p pathsP) (countR, error) {
		n := 0
		for _, vp := range p.Paths {
			sh, id, ok := splitTrashPath(vp)
			if !ok {
				return countR{Count: n}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "no es una ruta de la papelera: %s", vp)
			}
			loc, err := fa.resolve(ctx, s, "/"+sh)
			if err != nil {
				return countR{Count: n}, err
			}
			var e fileworker.Entry
			if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpRestore, Root: loc.root, Name: id}, &e); err != nil {
				return countR{Count: n}, err
			}
			fa.changed(loc.share, path.Dir(e.Rel))
			n++
		}
		return countR{Count: n}, nil
	}))
	r.Handle("files.trash.empty", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p shareP) (countR, error) {
		loc, err := fa.resolve(ctx, s, "/"+p.Share)
		if err != nil {
			return countR{}, err
		}
		var out countR
		if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpTrashEmpty, Root: loc.root}, &out); err != nil {
			return countR{}, err
		}
		return out, nil
	}))
	r.Handle("files.versions", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p pathP) ([]FileVersion, error) {
		return fa.versions(ctx, s, p.Path)
	}))
	r.Handle("files.restoreVersion", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p versionP) (FileEntry, error) {
		loc, err := fa.resolve(ctx, s, p.Path)
		if err != nil {
			return FileEntry{}, err
		}
		snapRoot, ok := fa.snapshotRoot(ctx, loc, p.Snapshot)
		if !ok {
			return FileEntry{}, rtcrpc.Errorf("SNAPSHOT_NOT_FOUND", "no existe esa instantánea")
		}
		var e fileworker.Entry
		if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpRestoreVersion, Root: snapRoot, Rel: loc.rel, DstRoot: loc.root}, &e); err != nil {
			return FileEntry{}, err
		}
		fa.changed(loc.share, path.Dir(loc.rel))
		return loc.entry(e), nil
	}))
	r.Handle("files.thumb", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p thumbP) (fileworker.ThumbResult, error) {
		return fa.thumb(ctx, s, p.Path, p.Snapshot, p.Size)
	}))

	// ---- Transferencias ----
	r.Handle("files.upload.begin", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p upBeginP) (upBeginR, error) {
		x, err := fa.uploadBegin(ctx, s, p.Dir, p.Name, p.Size, p.Overwrite)
		if err != nil {
			return upBeginR{}, err
		}
		return upBeginR{Xfer: x.id, Channel: chanLabel(x.ch), Chunk: chunkSize}, nil
	}))
	r.Handle("files.upload.end", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p upEndP) (FileEntry, error) {
		return fa.uploadEnd(ctx, s, p.Xfer, p.SHA256)
	}))
	r.Handle("files.download.begin", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p downP) (downBeginR, error) {
		x, err := fa.downloadBegin(ctx, s, p.Path, p.Snapshot, p.Offset, p.Length)
		if err != nil {
			return downBeginR{}, err
		}
		return downBeginR{Xfer: x.id, Channel: chanLabel(x.ch), Size: x.size, Chunk: chunkSize, Offset: x.from, Length: x.to - x.from}, nil
	}))
	r.Handle("files.xfer.cancel", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p xferP) (struct{}, error) {
		if x := fa.getXfer(s, p.Xfer); x != nil {
			fa.finish(x, errors.New("cancelada"))
		}
		return struct{}{}, nil
	}))
	return fa
}

func chanLabel(ch int) string { return "files." + string(rune('0'+ch)) }

// ---- Workers ------------------------------------------------------------------------

func (fa *filesAPI) worker(ctx context.Context, user string) (*fileworker.Client, error) {
	fa.mu.Lock()
	w := fa.workers[user]
	fa.mu.Unlock()
	if w != nil {
		select {
		case <-w.Done():
		default:
			return w, nil
		}
	}
	fa.spawnMu.Lock()
	defer fa.spawnMu.Unlock()
	fa.mu.Lock()
	w = fa.workers[user]
	fa.mu.Unlock()
	if w != nil {
		select {
		case <-w.Done():
		default:
			return w, nil
		}
	}
	conn, _, err := fa.spawner.SpawnFileWorker(ctx, user)
	if err != nil {
		var pe *agentproto.Error
		if errors.As(err, &pe) {
			return nil, rtcrpc.Errorf("WORKER_UNAVAILABLE", "no se pudo abrir tus archivos: %s", pe.Message)
		}
		return nil, rtcrpc.Errorf("WORKER_UNAVAILABLE", "no se pudo abrir tus archivos (tango-agent no responde)")
	}
	w = fileworker.NewClient(conn)
	fa.mu.Lock()
	fa.workers[user] = w
	fa.mu.Unlock()
	return w, nil
}

// InvalidateWorkers cierra los workers: los próximos se lanzan con los grupos al día
// (después de cambiar permisos de carpetas o usuarios).
func (fa *filesAPI) InvalidateWorkers() {
	fa.mu.Lock()
	ws := fa.workers
	fa.workers = map[string]*fileworker.Client{}
	fa.mu.Unlock()
	for _, w := range ws {
		// Se cierra recién cuando no tenga transferencias abiertas: los fds ya
		// obtenidos siguen valiendo aunque el worker termine.
		_ = w.Close()
	}
}

func (fa *filesAPI) reaper() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		fa.mu.Lock()
		for u, w := range fa.workers {
			if w.Idle() > workerIdle {
				delete(fa.workers, u)
				_ = w.Close()
			}
		}
		fa.mu.Unlock()
		// Subidas abandonadas.
		fa.xmu.Lock()
		var stale []*xfer
		for _, x := range fa.xfers {
			if x.upload && time.Since(time.Unix(0, x.last.Load())) > uploadIdle {
				stale = append(stale, x)
			}
		}
		fa.xmu.Unlock()
		for _, x := range stale {
			fa.finish(x, errors.New("la subida se cortó (60 s sin datos)"))
		}
	}
}

// call ejecuta una op del worker del usuario de la sesión y traduce los errores.
func (fa *filesAPI) call(ctx context.Context, s *rtcrpc.Session, req fileworker.Request, out any) error {
	_, err := fa.callFD(ctx, s, req, out)
	return err
}

func (fa *filesAPI) callFD(ctx context.Context, s *rtcrpc.Session, req fileworker.Request, out any) (*os.File, error) {
	w, err := fa.worker(ctx, filesUser(s))
	if err != nil {
		return nil, err
	}
	fd, err := w.Call(ctx, req, out)
	if err != nil {
		var we *fileworker.Error
		if errors.As(err, &we) {
			return nil, rtcrpc.Errorf(we.Code, "%s", we.Message)
		}
		return nil, err
	}
	return fd, nil
}

// filesUser es el usuario con cuyos permisos se tocan los archivos: el de la sesión o,
// en una sesión de invitado de un enlace público (protocol/README.md §15), el dueño del
// enlace (lo fija connect.go después de validar el alcance del pedido).
func filesUser(s *rtcrpc.Session) string {
	if u := s.User(); u != "" {
		return u
	}
	if u, ok := s.Get("files.as").(string); ok {
		return u
	}
	return ""
}

// ---- Rutas virtuales ------------------------------------------------------------------

// location es una ruta virtual resuelta: carpeta compartida + ruta relativa.
type location struct {
	share Share
	root  string // /volumeN/@shares/<Carpeta>
	rel   string // "." = raíz de la carpeta
}

func (l location) vpath(rel string) string {
	if rel == "." || rel == "" {
		return "/" + l.share.Name
	}
	return "/" + l.share.Name + "/" + rel
}

func (l location) entry(e fileworker.Entry) FileEntry {
	return toFileEntry(e, l.vpath(e.Rel))
}

func toFileEntry(e fileworker.Entry, vpath string) FileEntry {
	kind := "dir"
	if !e.Dir {
		kind = KindOf(e.Name)
	}
	h := fnv.New32a()
	h.Write([]byte(vpath))
	return FileEntry{Name: e.Name, Path: vpath, Kind: kind, Size: e.Size, MTime: e.MTime, Seed: h.Sum32()}
}

func (fa *filesAPI) sharesList(ctx context.Context) []Share {
	if fa.a.storage == nil {
		return nil
	}
	return fa.a.storage.shares(ctx)
}

// resolve traduce "/Carpeta/sub/x" (no acepta "/"). La carpeta se busca por nombre sin
// distinguir mayúsculas.
func (fa *filesAPI) resolve(ctx context.Context, _ *rtcrpc.Session, vp string) (location, error) {
	vp = strings.TrimSpace(vp)
	if !strings.HasPrefix(vp, "/") {
		vp = "/" + vp
	}
	clean := path.Clean(vp)
	if clean == "/" {
		return location{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "elegí una carpeta compartida")
	}
	parts := strings.SplitN(strings.TrimPrefix(clean, "/"), "/", 2)
	for _, sh := range fa.sharesList(ctx) {
		if strings.EqualFold(sh.Name, parts[0]) {
			rel := "."
			if len(parts) == 2 {
				var e *fileworker.Error
				if rel, e = fileworker.CleanRel(parts[1]); e != nil {
					return location{}, rtcrpc.Errorf(e.Code, "%s", e.Message)
				}
			}
			return location{share: sh, root: sh.Path, rel: rel}, nil
		}
	}
	return location{}, rtcrpc.Errorf(fileworker.CodeNotFound, "no existe la carpeta compartida %s", parts[0])
}

// accessible devuelve las carpetas compartidas que el usuario puede abrir (lo decide
// el kernel con los permisos reales, a través del worker).
func (fa *filesAPI) accessible(ctx context.Context, s *rtcrpc.Session) ([]Share, error) {
	shares := fa.sharesList(ctx)
	if len(shares) == 0 {
		return []Share{}, nil
	}
	roots := make([]string, len(shares))
	for i, sh := range shares {
		roots[i] = sh.Path
	}
	var ok []bool
	if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpProbe, Roots: roots}, &ok); err != nil {
		return nil, err
	}
	out := []Share{}
	for i, sh := range shares {
		if i < len(ok) && ok[i] {
			out = append(out, sh)
		}
	}
	return out, nil
}

func (fa *filesAPI) list(ctx context.Context, s *rtcrpc.Session, vp string) ([]FileEntry, error) {
	return fa.listAt(ctx, s, vp, "")
}

// locate es resolve + (opcional) la raíz de una instantánea de la carpeta, solo lectura.
func (fa *filesAPI) locate(ctx context.Context, s *rtcrpc.Session, vp, snap string) (location, error) {
	loc, err := fa.resolve(ctx, s, vp)
	if err != nil || snap == "" {
		return loc, err
	}
	root, ok := fa.snapshotRoot(ctx, loc, snap)
	if !ok {
		return location{}, rtcrpc.Errorf("SNAPSHOT_NOT_FOUND", "esa instantánea no es de la carpeta %s", loc.share.Name)
	}
	loc.root = root
	return loc, nil
}

func (fa *filesAPI) listAt(ctx context.Context, s *rtcrpc.Session, vp, snap string) ([]FileEntry, error) {
	if path.Clean("/"+strings.TrimSpace(vp)) == "/" {
		if snap != "" {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "una instantánea es de una carpeta compartida: elegí la carpeta")
		}
		shares, err := fa.accessible(ctx, s)
		if err != nil {
			return nil, err
		}
		out := make([]FileEntry, 0, len(shares))
		for _, sh := range shares {
			var e fileworker.Entry
			_ = fa.call(ctx, s, fileworker.Request{Op: fileworker.OpStat, Root: sh.Path, Rel: "."}, &e)
			e.Name, e.Rel, e.Dir = sh.Name, ".", true
			out = append(out, toFileEntry(e, "/"+sh.Name))
		}
		return out, nil
	}
	loc, err := fa.locate(ctx, s, vp, snap)
	if err != nil {
		return nil, err
	}
	if snap == "" {
		fa.noteDir(s.User(), loc) // vigilar cambios hechos por SMB/NFS/SFTP (§13)
	}
	out := []FileEntry{}
	for off := 0; off < 100_000; {
		var page fileworker.ListResult
		if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpList, Root: loc.root, Rel: loc.rel, Offset: off, Limit: 300}, &page); err != nil {
			return nil, err
		}
		for _, e := range page.Entries {
			out = append(out, loc.entry(e))
		}
		off += len(page.Entries)
		if !page.More || len(page.Entries) == 0 {
			break
		}
	}
	return out, nil
}

func (fa *filesAPI) search(ctx context.Context, s *rtcrpc.Session, q, within string) ([]FileEntry, error) {
	if len(strings.TrimSpace(q)) < 1 {
		return []FileEntry{}, nil
	}
	var roots []string
	byRoot := map[string]location{}
	if c := path.Clean("/" + strings.TrimSpace(within)); within != "" && c != "/" {
		loc, err := fa.resolve(ctx, s, within)
		if err != nil {
			return nil, err
		}
		roots = []string{loc.root}
		byRoot[loc.root] = loc
	} else {
		shares, err := fa.accessible(ctx, s)
		if err != nil {
			return nil, err
		}
		for _, sh := range shares {
			roots = append(roots, sh.Path)
			byRoot[sh.Path] = location{share: sh, root: sh.Path, rel: "."}
		}
	}
	if len(roots) == 0 {
		return []FileEntry{}, nil
	}
	var hits []fileworker.Entry
	if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpSearch, Roots: roots, Query: q, Limit: 200}, &hits); err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(hits))
	for _, h := range hits {
		if loc, ok := byRoot[h.Root]; ok {
			out = append(out, loc.entry(h))
		}
	}
	return out, nil
}

// splitTrashPath separa "/Carpeta/.tango-trash/<id>".
func splitTrashPath(vp string) (share, id string, ok bool) {
	parts := strings.Split(strings.Trim(path.Clean("/"+vp), "/"), "/")
	if len(parts) != 3 || parts[1] != fileworker.TrashDir || !fileworker.ValidTrashID(parts[2]) {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func (fa *filesAPI) trashList(ctx context.Context, s *rtcrpc.Session, share string) ([]TrashEntry, error) {
	var shares []Share
	if share != "" {
		loc, err := fa.resolveShareID(ctx, s, share)
		if err != nil {
			return nil, err
		}
		shares = []Share{loc.share}
	} else {
		var err error
		if shares, err = fa.accessible(ctx, s); err != nil {
			return nil, err
		}
	}
	out := []TrashEntry{}
	for _, sh := range shares {
		var items []fileworker.TrashItem
		if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpTrashList, Root: sh.Path}, &items); err != nil {
			if share != "" {
				return nil, err
			}
			continue
		}
		for _, it := range items {
			kind := "dir"
			if !it.Dir {
				kind = KindOf(it.Name)
			}
			origin := "/" + sh.Name
			if it.Origin != "." && it.Origin != "" {
				origin += "/" + it.Origin
			}
			out = append(out, TrashEntry{Name: it.Name, Path: "/" + sh.Name + "/" + fileworker.TrashDir + "/" + it.ID, Origin: origin, DeletedAt: it.DeletedAt, Size: it.Size, Kind: kind})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt > out[j].DeletedAt })
	return out, nil
}

// resolveShareID acepta el id de la carpeta (minúsculas) o su nombre.
func (fa *filesAPI) resolveShareID(ctx context.Context, s *rtcrpc.Session, share string) (location, error) {
	for _, sh := range fa.sharesList(ctx) {
		if sh.ID == share || strings.EqualFold(sh.Name, share) {
			return location{share: sh, root: sh.Path, rel: "."}, nil
		}
	}
	return location{}, rtcrpc.Errorf("SHARE_NOT_FOUND", "no existe la carpeta compartida %s", share)
}

// snapshotRoot devuelve /volumeN/@snapshots/<Carpeta>/<instantánea> para un id
// "<carpeta>@<nombre>" de Snapshot.
func (fa *filesAPI) snapshotRoot(ctx context.Context, loc location, snapID string) (string, bool) {
	if fa.a.storage == nil {
		return "", false
	}
	for _, sn := range fa.a.storage.snapshots(ctx, loc.share.ID) {
		if sn.ID == snapID {
			_, name, _ := strings.Cut(sn.ID, "@")
			mount := strings.TrimSuffix(loc.root, "/@shares/"+loc.share.Name)
			return filepath.Join(mount, "@snapshots", loc.share.Name, name), true
		}
	}
	return "", false
}

func (fa *filesAPI) versions(ctx context.Context, s *rtcrpc.Session, vp string) ([]FileVersion, error) {
	loc, err := fa.resolve(ctx, s, vp)
	if err != nil {
		return nil, err
	}
	out := []FileVersion{}
	if fa.a.storage == nil {
		return out, nil
	}
	mount := strings.TrimSuffix(loc.root, "/@shares/"+loc.share.Name)
	for _, sn := range fa.a.storage.snapshots(ctx, loc.share.ID) {
		_, name, _ := strings.Cut(sn.ID, "@")
		root := filepath.Join(mount, "@snapshots", loc.share.Name, name)
		var e fileworker.Entry
		if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpStat, Root: root, Rel: loc.rel}, &e); err != nil {
			continue // en esa instantánea el archivo no existía (o no hay permiso)
		}
		out = append(out, FileVersion{Snapshot: sn, Size: e.Size, MTime: e.MTime})
	}
	return out, nil
}

func (fa *filesAPI) thumb(ctx context.Context, s *rtcrpc.Session, vp, snap string, size int) (fileworker.ThumbResult, error) {
	if size != 128 && size != 256 {
		size = 256
	}
	loc, err := fa.locate(ctx, s, vp, snap)
	if err != nil {
		return fileworker.ThumbResult{}, err
	}
	switch strings.ToLower(path.Ext(loc.rel)) {
	case ".jpg", ".jpeg", ".png", ".gif":
	default:
		return fileworker.ThumbResult{}, rtcrpc.Errorf(fileworker.CodeNotSupported, "sin miniatura para este tipo de archivo")
	}
	var st fileworker.Entry
	if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpStat, Root: loc.root, Rel: loc.rel}, &st); err != nil {
		return fileworker.ThumbResult{}, err
	}
	sum := sha256.Sum256([]byte(loc.root + "\x00" + loc.rel + "\x00" + st.MTime + "\x00" + itoa(st.Size) + "\x00" + itoa(int64(size))))
	cache := filepath.Join(fa.thumbs, hex.EncodeToString(sum[:16])+".jpg")
	if b, err := os.ReadFile(cache); err == nil {
		return fileworker.ThumbResult{MIME: "image/jpeg", Data: base64.StdEncoding.EncodeToString(b)}, nil
	}
	var t fileworker.ThumbResult
	if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpThumb, Root: loc.root, Rel: loc.rel, Size: size}, &t); err != nil {
		return fileworker.ThumbResult{}, err
	}
	if b, err := base64.StdEncoding.DecodeString(t.Data); err == nil {
		tmp := cache + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			_ = os.Rename(tmp, cache)
		}
	}
	return t, nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// changed avisa que cambió una carpeta (ruta virtual).
func (fa *filesAPI) changed(sh Share, rel string) {
	dir := "/" + sh.Name
	if rel != "." && rel != "" {
		dir += "/" + rel
	}
	fa.a.d.Hub.Publish("files.changed", map[string]string{"dir": dir})
}

// ---- Transferencias ---------------------------------------------------------------------

func (fa *filesAPI) chans(s *rtcrpc.Session) (fileChans, error) {
	fc, ok := s.Get("files").(fileChans)
	if !ok || fc == nil {
		return nil, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "esta conexión no tiene canales de archivos")
	}
	return fc, nil
}

func (fa *filesAPI) addXfer(s *rtcrpc.Session, x *xfer) error {
	fa.xmu.Lock()
	defer fa.xmu.Unlock()
	n := 0
	used := make([]int, 8)
	for _, o := range fa.xfers {
		if o.sess == s {
			n++
			if o.ch < len(used) {
				used[o.ch]++
			}
		}
	}
	if n >= maxXfersPerSes {
		return rtcrpc.Errorf(rtcrpc.CodeRateLimited, "demasiadas transferencias a la vez (máx. %d)", maxXfersPerSes)
	}
	// El canal menos usado de esta sesión.
	best := 0
	for i := 1; i < 4; i++ {
		if used[i] < used[best] {
			best = i
		}
	}
	x.ch = best
	for {
		x.id = fa.nextID.Add(1)
		if _, dup := fa.xfers[x.id]; !dup && x.id != 0 {
			break
		}
	}
	x.last.Store(time.Now().UnixNano())
	fa.xfers[x.id] = x
	return nil
}

func (fa *filesAPI) getXfer(s *rtcrpc.Session, id uint32) *xfer {
	fa.xmu.Lock()
	defer fa.xmu.Unlock()
	x := fa.xfers[id]
	if x == nil || x.sess != s {
		return nil
	}
	return x
}

func (fa *filesAPI) emitXfer(x *xfer, state string, err error) {
	ev := map[string]any{"xfer": x.id, "state": state, "bytes": x.done.Load()}
	if err != nil {
		ev["error"] = err.Error()
	}
	x.sess.Emit("files.xfer", ev)
}

// finish cierra una transferencia. err == nil: terminó bien.
func (fa *filesAPI) finish(x *xfer, err error) {
	fa.xmu.Lock()
	if fa.xfers[x.id] != x {
		fa.xmu.Unlock()
		return
	}
	delete(fa.xfers, x.id)
	fa.xmu.Unlock()
	x.cancel()
	x.mu.Lock()
	if x.f != nil {
		x.f.Close()
		x.f = nil
	}
	x.mu.Unlock()
	if x.upload && err != nil {
		// Se borra el parcial con los permisos del usuario.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = fa.call(ctx, x.sess, fileworker.Request{Op: fileworker.OpAbort, Root: x.root, Rel: x.dirRel, Name: x.part}, nil)
		cancel()
	}
	if err != nil {
		fa.emitXfer(x, "error", err)
	} else {
		fa.emitXfer(x, "done", nil)
	}
}

func (fa *filesAPI) uploadBegin(ctx context.Context, s *rtcrpc.Session, dir, name string, size int64, overwrite bool) (*xfer, error) {
	if _, err := fa.chans(s); err != nil {
		return nil, err
	}
	if size < 0 || size > 1<<40 {
		return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "tamaño inválido")
	}
	if e := fileworker.ValidName(name); e != nil {
		return nil, rtcrpc.Errorf(e.Code, "%s", e.Message)
	}
	loc, err := fa.resolve(ctx, s, dir)
	if err != nil {
		return nil, err
	}
	if !overwrite {
		var st fileworker.Entry
		if fa.call(ctx, s, fileworker.Request{Op: fileworker.OpStat, Root: loc.root, Rel: path.Join(loc.rel, name)}, &st) == nil {
			return nil, rtcrpc.Errorf(fileworker.CodeExists, "ya existe %s en esa carpeta", name)
		}
	}
	var or fileworker.OpenResult
	fd, err := fa.callFD(ctx, s, fileworker.Request{Op: fileworker.OpOpenWrite, Root: loc.root, Rel: loc.rel, Name: name}, &or)
	if err != nil {
		return nil, err
	}
	if fd == nil {
		return nil, rtcrpc.Errorf(rtcrpc.CodeInternal, "el worker no entregó el archivo")
	}
	if size > 0 {
		_ = fd.Truncate(size) // reserva el tamaño (sparse); se verifica al cerrar
	}
	xctx, cancel := context.WithCancel(context.Background())
	x := &xfer{sess: s, upload: true, f: fd, size: size, hasher: sha256.New(), share: loc.share, root: loc.root, dirRel: loc.rel, name: name, part: or.Part, over: overwrite,
		vpath: loc.vpath(path.Join(loc.rel, name)), ctx: xctx, cancel: cancel}
	if err := fa.addXfer(s, x); err != nil {
		cancel()
		fd.Close()
		_ = fa.call(ctx, s, fileworker.Request{Op: fileworker.OpAbort, Root: loc.root, Rel: loc.rel, Name: or.Part}, nil)
		return nil, err
	}
	return x, nil
}

// FileFrame atiende un frame binario de files.N (lo registra tangod en la capa rtc).
func (a *API) FileFrame(s *rtcrpc.Session, _ int, data []byte) {
	fa := a.files
	if fa == nil {
		return
	}
	id, off, payload, err := fileworker.DecodeFrame(data)
	if err != nil {
		return
	}
	x := fa.getXfer(s, id)
	if x == nil || !x.upload {
		return
	}
	x.last.Store(time.Now().UnixNano())
	end := int64(off) + int64(len(payload))
	if int64(off) < 0 || end > x.size {
		fa.finish(x, errors.New("el cliente mandó datos fuera del tamaño declarado"))
		return
	}
	x.mu.Lock()
	if x.f == nil {
		x.mu.Unlock()
		return
	}
	_, werr := x.f.WriteAt(payload, int64(off))
	if werr == nil && int64(off) == x.nextOff {
		x.hasher.Write(payload)
		x.nextOff = end
	} else if werr == nil {
		x.hasher = nil // llegaron fuera de orden: no se puede verificar en línea
	}
	x.mu.Unlock()
	if werr != nil {
		fa.finish(x, errors.New("no se pudo escribir en el volumen: "+werr.Error()))
		return
	}
	done := x.done.Add(int64(len(payload)))
	if time.Since(x.lastEvt) > 250*time.Millisecond || done == x.size {
		x.lastEvt = time.Now()
		fa.emitXfer(x, "progress", nil)
	}
}

func (fa *filesAPI) uploadEnd(ctx context.Context, s *rtcrpc.Session, id uint32, sum string) (FileEntry, error) {
	x := fa.getXfer(s, id)
	if x == nil || !x.upload {
		return FileEntry{}, rtcrpc.Errorf("XFER_NOT_FOUND", "no existe esa transferencia")
	}
	// El end viaja por `rpc` y los datos por files.N: pueden llegar antes que los
	// últimos frames. Se espera hasta tener todo (o 30 s sin progreso).
	deadline := time.Now().Add(30 * time.Second)
	last := x.done.Load()
	for x.done.Load() < x.size {
		if time.Now().After(deadline) {
			fa.finish(x, errors.New("faltan datos"))
			return FileEntry{}, rtcrpc.Errorf("XFER_INCOMPLETE", "llegaron %d de %d bytes", x.done.Load(), x.size)
		}
		select {
		case <-ctx.Done():
			return FileEntry{}, ctx.Err()
		case <-x.ctx.Done():
			return FileEntry{}, rtcrpc.Errorf("XFER_FAILED", "la transferencia se canceló")
		case <-time.After(20 * time.Millisecond):
		}
		if d := x.done.Load(); d != last {
			last, deadline = d, time.Now().Add(30*time.Second)
		}
	}
	x.mu.Lock()
	f := x.f
	h := x.hasher
	x.f = nil
	x.mu.Unlock()
	if f == nil {
		return FileEntry{}, rtcrpc.Errorf("XFER_FAILED", "la transferencia se canceló")
	}
	serr := f.Sync()
	f.Close()
	if serr != nil {
		fa.finish(x, serr)
		return FileEntry{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "no se pudo guardar: %v", serr)
	}
	if sum != "" && h != nil && !strings.EqualFold(sum, hex.EncodeToString(h.Sum(nil))) {
		fa.finish(x, errors.New("el sha256 no coincide"))
		return FileEntry{}, rtcrpc.Errorf("CHECKSUM_MISMATCH", "el archivo llegó dañado (sha256 distinto): se descartó")
	}
	var e fileworker.Entry
	if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpCommit, Root: x.root, Rel: x.dirRel, Name: x.part, DstRel: x.name, Overwrite: x.over}, &e); err != nil {
		fa.finish(x, err)
		return FileEntry{}, err
	}
	fa.finish(x, nil)
	loc := location{share: x.share, root: x.root}
	fa.changed(x.share, x.dirRel)
	return loc.entry(e), nil
}

func (fa *filesAPI) downloadBegin(ctx context.Context, s *rtcrpc.Session, vp, snap string, offset, length *int64) (*xfer, error) {
	fc, err := fa.chans(s)
	if err != nil {
		return nil, err
	}
	loc, err := fa.locate(ctx, s, vp, snap)
	if err != nil {
		return nil, err
	}
	var or fileworker.OpenResult
	fd, err := fa.callFD(ctx, s, fileworker.Request{Op: fileworker.OpOpenRead, Root: loc.root, Rel: loc.rel}, &or)
	if err != nil {
		return nil, err
	}
	if fd == nil {
		return nil, rtcrpc.Errorf(rtcrpc.CodeInternal, "el worker no entregó el archivo")
	}
	from, to, rerr := byteRange(or.Size, offset, length)
	if rerr != nil {
		fd.Close()
		return nil, rerr
	}
	xctx, cancel := context.WithCancel(context.Background())
	x := &xfer{sess: s, f: fd, size: or.Size, from: from, to: to, vpath: loc.vpath(loc.rel), ctx: xctx, cancel: cancel}
	if err := fa.addXfer(s, x); err != nil {
		cancel()
		fd.Close()
		return nil, err
	}
	go fa.stream(x, fc)
	return x, nil
}

// stream envía el archivo por files.N con backpressure. El cliente lo da por
// terminado al recibir `size` bytes; el evento done es informativo (viaja por otro
// canal y puede llegar antes que los últimos frames).
func (fa *filesAPI) stream(x *xfer, fc fileChans) {
	// Un respiro para que la respuesta de download.begin llegue antes que los datos
	// (canales distintos: el cliente igual guarda frames de xfers que aún no conoce).
	time.Sleep(20 * time.Millisecond)
	buf := make([]byte, chunkSize)
	frame := make([]byte, fileworker.FrameHeader+chunkSize)
	off := x.from
	for off < x.to {
		if err := fc.WaitFile(x.ctx, x.ch, maxBuffered); err != nil {
			fa.finish(x, errors.New("se cortó la conexión"))
			return
		}
		x.mu.Lock()
		f := x.f
		x.mu.Unlock()
		if f == nil {
			return
		}
		want := buf
		if rest := x.to - off; rest < int64(len(want)) {
			want = buf[:rest]
		}
		n, err := f.ReadAt(want, off)
		if n > 0 {
			fr := fileworker.EncodeFrame(frame, x.id, uint64(off), buf[:n])
			if serr := fc.SendFile(x.ch, fr); serr != nil {
				fa.finish(x, errors.New("se cortó la conexión"))
				return
			}
			off += int64(n)
			x.done.Add(int64(n))
			if time.Since(x.lastEvt) > 250*time.Millisecond {
				x.lastEvt = time.Now()
				fa.emitXfer(x, "progress", nil)
			}
		}
		if err != nil && off < x.to {
			fa.finish(x, errors.New("no se pudo leer el archivo"))
			return
		}
	}
	fa.finish(x, nil)
}

// byteRange valida un rango pedido sobre un archivo de size bytes y devuelve [from, to).
// Sin offset ni length es el archivo entero.
func byteRange(size int64, offset, length *int64) (int64, int64, error) {
	from, to := int64(0), size
	if offset != nil {
		if *offset < 0 || *offset > size {
			return 0, 0, rtcrpc.Errorf("RANGE_NOT_SATISFIABLE", "el archivo tiene %d bytes", size)
		}
		from = *offset
	}
	if length != nil {
		if *length < 0 {
			return 0, 0, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "length inválido")
		}
		if from+*length < to {
			to = from + *length
		}
	}
	return from, to, nil
}
