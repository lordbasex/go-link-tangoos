//go:build linux

package fileworker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// resolveFlags: nunca salir de la raíz, ni por "..", ni por symlinks absolutos, ni
// por /proc/self/fd, ni cruzando otro montaje. Los symlinks relativos que quedan
// adentro se siguen.
const resolveFlags = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV

// maxPacket es el tamaño máximo de un mensaje (cabe en el buffer por defecto de un
// socket SEQPACKET: net.core.wmem_default = 208 KiB).
const maxPacket = 192 << 10

// Serve atiende pedidos en conn (el extremo SOCK_SEQPACKET que heredó el worker)
// hasta que tangod cierra su extremo.
func Serve(conn *net.UnixConn) error {
	unix.Umask(0o007)
	w := &worker{conn: conn}
	buf := make([]byte, maxPacket)
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	// Al cerrar tangod la conexión se espera a lo sumo 2 s a lo que está en curso: un
	// long-poll de watch (hasta 30 s) retenía fds dentro del volumen e impedía desmontarlo
	// al borrarlo (hallazgo e2e 2026-10-15). El proceso termina igual y el kernel cierra todo.
	defer func() {
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}()
	for {
		n, _, _, _, err := conn.ReadMsgUnix(buf, nil)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if n == 0 {
			return nil // EOF en SEQPACKET
		}
		var req Request
		if json.Unmarshal(buf[:n], &req) != nil {
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			w.handle(&req)
		}()
	}
}

type worker struct {
	conn *net.UnixConn
	wmu  sync.Mutex
}

func (w *worker) reply(id uint64, res any, fd *os.File, e *Error) {
	r := Response{ID: id, OK: e == nil, Error: e}
	if e == nil && res != nil {
		b, err := json.Marshal(res)
		if err != nil {
			r.OK, r.Error = false, errf(CodeInternal, "no se pudo serializar la respuesta")
		} else {
			r.Result = b
		}
	}
	b, _ := json.Marshal(r)
	if len(b) > maxPacket {
		b, _ = json.Marshal(Response{ID: id, Error: errf(CodeInternal, "respuesta demasiado grande")})
		fd = nil
	}
	var oob []byte
	if fd != nil && r.OK {
		oob = unix.UnixRights(int(fd.Fd()))
	}
	w.wmu.Lock()
	_, _, _ = w.conn.WriteMsgUnix(b, oob, nil)
	w.wmu.Unlock()
	if fd != nil {
		fd.Close() // tangod tiene su propia copia
	}
}

func (w *worker) handle(r *Request) {
	defer func() {
		if p := recover(); p != nil {
			w.reply(r.ID, nil, nil, errf(CodeInternal, "error interno del worker"))
		}
	}()
	var (
		res any
		fd  *os.File
		e   *Error
	)
	switch r.Op {
	case OpProbe:
		res = probe(r.Roots)
	case OpStat:
		res, e = withRoot(r.Root, func(root int) (any, *Error) { return statRel(root, r.Rel) })
	case OpList:
		res, e = withRoot(r.Root, func(root int) (any, *Error) { return list(root, r.Rel, r.Offset, r.Limit) })
	case OpMkdir:
		res, e = withRoot(r.Root, func(root int) (any, *Error) { return mkdir(root, r.Rel, r.Name) })
	case OpRename:
		res, e = withRoot(r.Root, func(root int) (any, *Error) { return rename(root, r.Rel, r.Name) })
	case OpMove:
		res, e = withRoot(r.Root, func(root int) (any, *Error) {
			return withRoot(r.DstRoot, func(dst int) (any, *Error) { return move(root, r.Rel, dst, r.DstRel) })
		})
	case OpTrash:
		res, e = withRoot(r.Root, func(root int) (any, *Error) { return trash(root, r.Rel) })
	case OpTrashList:
		res, e = withRoot(r.Root, func(root int) (any, *Error) { return trashList(root) })
	case OpRestore:
		res, e = withRoot(r.Root, func(root int) (any, *Error) { return restore(root, r.Name) })
	case OpTrashEmpty:
		res, e = withRoot(r.Root, func(root int) (any, *Error) { return trashEmpty(root) })
	case OpSearch:
		res, e = search(r.Roots, r.Query, r.Limit)
	case OpRestoreVersion:
		res, e = withRoot(r.Root, func(snap int) (any, *Error) {
			return withRoot(r.DstRoot, func(dst int) (any, *Error) { return restoreVersion(snap, r.Rel, dst) })
		})
	case OpThumb:
		res, e = withRoot(r.Root, func(root int) (any, *Error) { return thumb(root, r.Rel, r.Size) })
	case OpOpenRead:
		var or OpenResult
		_, e = withRoot(r.Root, func(root int) (any, *Error) {
			f, sz, err := openRead(root, r.Rel)
			fd, or.Size = f, sz
			return nil, err
		})
		res = or
	case OpOpenWrite:
		var or OpenResult
		_, e = withRoot(r.Root, func(root int) (any, *Error) {
			f, part, err := openWrite(root, r.Rel, r.Name)
			fd, or.Part = f, part
			return nil, err
		})
		res = or
	case OpCommit:
		res, e = withRoot(r.Root, func(root int) (any, *Error) { return commit(root, r.Rel, r.Name, r.DstRel, r.Overwrite) })
	case OpAbort:
		res, e = withRoot(r.Root, func(root int) (any, *Error) { return struct{}{}, abort(root, r.Rel, r.Name) })
	case OpWatch:
		res, e = watch(r.Roots, r.Rels, r.Limit)
	default:
		e = errf(CodeInvalid, "op desconocida %q", r.Op)
	}
	if e != nil && fd != nil {
		fd.Close()
		fd = nil
	}
	w.reply(r.ID, res, fd, e)
}

// ---- Raíces y resolución ------------------------------------------------------------

func withRoot(root string, fn func(fd int) (any, *Error)) (any, *Error) {
	if e := ValidRoot(root); e != nil {
		return nil, e
	}
	fd, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errnoErr(err, root)
	}
	defer unix.Close(fd)
	return fn(fd)
}

func openBeneath(root int, rel string, flags uint64, mode uint64) (int, *Error) {
	c, e := CleanRel(rel)
	if e != nil {
		return -1, e
	}
	fd, err := openat2Beneath(root, c, flags|unix.O_CLOEXEC, mode)
	if err != nil {
		return -1, errnoErr(err, rel)
	}
	return fd, nil
}

// parentOf abre el directorio padre de rel y devuelve el último componente.
func parentOf(root int, rel string) (int, string, *Error) {
	c, e := CleanRel(rel)
	if e != nil {
		return -1, "", e
	}
	if c == "." {
		return -1, "", errf(CodeInvalid, "no se puede operar sobre la raíz de la carpeta")
	}
	dir, base := path.Split(c)
	fd, e := openBeneath(root, dir, unix.O_PATH|unix.O_DIRECTORY, 0)
	if e != nil {
		return -1, "", e
	}
	return fd, base, nil
}

func errnoErr(err error, what string) *Error {
	var en unix.Errno
	if !errors.As(err, &en) {
		return errf(CodeInternal, "%v", err)
	}
	switch en {
	case unix.ENOENT, unix.ENOTDIR:
		return errf(CodeNotFound, "no existe %s", what)
	case unix.EACCES, unix.EPERM, unix.EROFS:
		return errf(CodePermission, "no tenés permiso sobre %s", what)
	case unix.EEXIST, unix.ENOTEMPTY:
		return errf(CodeExists, "ya existe %s", what)
	case unix.EXDEV, unix.ELOOP:
		return errf(CodeEscape, "la ruta sale de la carpeta compartida")
	case unix.ENOSPC, unix.EDQUOT:
		return errf(CodeNoSpace, "no hay espacio en el volumen")
	case unix.EISDIR:
		return errf(CodeInvalid, "%s es una carpeta", what)
	case unix.ENAMETOOLONG:
		return errf(CodeInvalidName, "el nombre es demasiado largo")
	}
	return errf(CodeInternal, "%s: %v", what, en.Error())
}

func entryFromStat(name, rel string, st *unix.Stat_t) Entry {
	dir := st.Mode&unix.S_IFMT == unix.S_IFDIR
	size := st.Size
	if dir {
		size = 0
	}
	return Entry{Name: name, Rel: rel, Dir: dir, Size: size, MTime: time.Unix(st.Mtim.Sec, st.Mtim.Nsec).UTC().Format(time.RFC3339)}
}

func statRel(root int, rel string) (Entry, *Error) {
	c, e := CleanRel(rel)
	if e != nil {
		return Entry{}, e
	}
	fd, e := openBeneath(root, c, unix.O_PATH, 0)
	if e != nil {
		return Entry{}, e
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return Entry{}, errnoErr(err, rel)
	}
	name := path.Base(c)
	if c == "." {
		name = ""
	}
	return entryFromStat(name, c, &st), nil
}

// ---- Listado ------------------------------------------------------------------------

func readNames(dirfd int) ([]string, *Error) {
	f := os.NewFile(uintptr(dirfd), "dir")
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return nil, errnoErr(err, "la carpeta")
	}
	return names, nil
}

func list(root int, rel string, offset, limit int) (ListResult, *Error) {
	c, e := CleanRel(rel)
	if e != nil {
		return ListResult{}, e
	}
	dirfd, e := openBeneath(root, c, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if e != nil {
		return ListResult{}, e
	}
	statfd, err := unix.Dup(dirfd)
	if err != nil {
		unix.Close(dirfd)
		return ListResult{}, errnoErr(err, rel)
	}
	defer unix.Close(statfd)
	names, e := readNames(dirfd)
	if e != nil {
		return ListResult{}, e
	}
	vis := names[:0]
	for _, n := range names {
		if !Hidden(n) {
			vis = append(vis, n)
		}
	}
	sort.Slice(vis, func(i, j int) bool { return strings.ToLower(vis[i]) < strings.ToLower(vis[j]) })
	if limit <= 0 || limit > 300 {
		limit = 300
	}
	if offset < 0 || offset > len(vis) {
		offset = len(vis)
	}
	end := min(len(vis), offset+limit)
	out := ListResult{Entries: []Entry{}, More: end < len(vis)}
	for _, n := range vis[offset:end] {
		var st unix.Stat_t
		if unix.Fstatat(statfd, n, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
			continue
		}
		childRel := path.Join(c, n)
		if st.Mode&unix.S_IFMT == unix.S_IFLNK {
			// Solo se muestran los symlinks que resuelven adentro de la carpeta.
			fd, e := openBeneath(root, childRel, unix.O_PATH, 0)
			if e != nil {
				continue
			}
			err := unix.Fstat(fd, &st)
			unix.Close(fd)
			if err != nil {
				continue
			}
		}
		out.Entries = append(out.Entries, entryFromStat(n, childRel, &st))
	}
	return out, nil
}

func probe(roots []string) []bool {
	out := make([]bool, len(roots))
	for i, r := range roots {
		if ValidRoot(r) != nil {
			continue
		}
		fd, err := unix.Open(r, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err == nil {
			out[i] = true
			unix.Close(fd)
		}
	}
	return out
}

// ---- Mutaciones ---------------------------------------------------------------------

func mkdir(root int, rel, name string) (Entry, *Error) {
	if e := ValidName(name); e != nil {
		return Entry{}, e
	}
	dirfd, e := openBeneath(root, rel, unix.O_PATH|unix.O_DIRECTORY, 0)
	if e != nil {
		return Entry{}, e
	}
	defer unix.Close(dirfd)
	if err := unix.Mkdirat(dirfd, name, 0o770); err != nil {
		return Entry{}, errnoErr(err, name)
	}
	c, _ := CleanRel(rel)
	return statRel(root, path.Join(c, name))
}

func rename(root int, rel, name string) (Entry, *Error) {
	if e := ValidName(name); e != nil {
		return Entry{}, e
	}
	pfd, base, e := parentOf(root, rel)
	if e != nil {
		return Entry{}, e
	}
	defer unix.Close(pfd)
	if base == name {
		c, _ := CleanRel(rel)
		return statRel(root, c)
	}
	if err := unix.Renameat2(pfd, base, pfd, name, unix.RENAME_NOREPLACE); err != nil {
		return Entry{}, errnoErr(err, name)
	}
	c, _ := CleanRel(rel)
	return statRel(root, path.Join(path.Dir(c), name))
}

// move mueve rel (en root) adentro de la carpeta dstRel (en dst). Entre subvolúmenes
// distintos rename(2) devuelve EXDEV: se copia (con reflink si se puede) y se borra.
func move(root int, rel string, dst int, dstRel string) (Entry, *Error) {
	pfd, base, e := parentOf(root, rel)
	if e != nil {
		return Entry{}, e
	}
	defer unix.Close(pfd)
	dfd, e := openBeneath(dst, dstRel, unix.O_PATH|unix.O_DIRECTORY, 0)
	if e != nil {
		return Entry{}, e
	}
	defer unix.Close(dfd)
	dc, _ := CleanRel(dstRel)
	err := unix.Renameat2(pfd, base, dfd, base, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EXDEV) {
		if e := copyTree(pfd, base, dfd, base); e != nil {
			_ = removeTree(dfd, base)
			return Entry{}, e
		}
		if e := removeTree(pfd, base); e != nil {
			return Entry{}, e
		}
		err = nil
	}
	if err != nil {
		return Entry{}, errnoErr(err, base)
	}
	return statRel(dst, path.Join(dc, base))
}

func randomID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x", uint32(time.Now().Unix())) + hex.EncodeToString(b)
}

func ensureTrash(root int) (int, *Error) {
	err := unix.Mkdirat(root, TrashDir, 0o770)
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return -1, errnoErr(err, "la papelera")
	}
	fd, err := openTrash(root, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return -1, errnoErr(err, "la papelera")
	}
	return fd, nil
}

func trash(root int, rel string) (TrashItem, *Error) {
	c, e := CleanRel(rel)
	if e != nil {
		return TrashItem{}, e
	}
	pfd, base, e := parentOf(root, c)
	if e != nil {
		return TrashItem{}, e
	}
	defer unix.Close(pfd)
	tfd, e := ensureTrash(root)
	if e != nil {
		return TrashItem{}, e
	}
	defer unix.Close(tfd)
	var st unix.Stat_t
	if err := unix.Fstatat(pfd, base, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return TrashItem{}, errnoErr(err, rel)
	}
	id := randomID()
	meta := TrashMeta{Name: base, Origin: c, DeletedAt: time.Now().UTC().Format(time.RFC3339)}
	mb, _ := json.Marshal(meta)
	if e := writeFileAt(tfd, id+".meta", mb); e != nil {
		return TrashItem{}, e
	}
	if err := unix.Renameat2(pfd, base, tfd, id, unix.RENAME_NOREPLACE); err != nil {
		_ = unix.Unlinkat(tfd, id+".meta", 0)
		return TrashItem{}, errnoErr(err, rel)
	}
	en := entryFromStat(base, c, &st)
	return TrashItem{ID: id, Name: base, Origin: c, DeletedAt: meta.DeletedAt, Size: en.Size, Dir: en.Dir}, nil
}

func writeFileAt(dirfd int, name string, data []byte) *Error {
	fd, err := unix.Openat(dirfd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o660)
	if err != nil {
		return errnoErr(err, name)
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return errnoErr(err, name)
	}
	return nil
}

func readMeta(tfd int, id string) (TrashMeta, bool) {
	fd, err := unix.Openat(tfd, id+".meta", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return TrashMeta{}, false
	}
	f := os.NewFile(uintptr(fd), "meta")
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8<<10))
	if err != nil {
		return TrashMeta{}, false
	}
	var m TrashMeta
	return m, json.Unmarshal(b, &m) == nil
}

func trashList(root int) ([]TrashItem, *Error) {
	out := []TrashItem{}
	tfd, err := openTrash(root, unix.O_RDONLY|unix.O_DIRECTORY)
	if errors.Is(err, unix.ENOENT) {
		return out, nil
	}
	if err != nil {
		return nil, errnoErr(err, "la papelera")
	}
	sfd, _ := unix.Dup(tfd)
	defer unix.Close(sfd)
	names, e := readNames(tfd)
	if e != nil {
		return nil, e
	}
	for _, n := range names {
		if !ValidTrashID(n) {
			continue
		}
		m, ok := readMeta(sfd, n)
		if !ok {
			m = TrashMeta{Name: n, Origin: n}
		}
		var st unix.Stat_t
		if unix.Fstatat(sfd, n, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
			continue
		}
		en := entryFromStat(m.Name, m.Origin, &st)
		out = append(out, TrashItem{ID: n, Name: m.Name, Origin: m.Origin, DeletedAt: m.DeletedAt, Size: en.Size, Dir: en.Dir})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt > out[j].DeletedAt })
	return out, nil
}

func restore(root int, id string) (Entry, *Error) {
	if !ValidTrashID(id) {
		return Entry{}, errf(CodeInvalid, "id de papelera inválido")
	}
	tfd, err := openTrash(root, unix.O_PATH|unix.O_DIRECTORY)
	if err != nil {
		return Entry{}, errnoErr(err, "la papelera")
	}
	defer unix.Close(tfd)
	m, ok := readMeta(tfd, id)
	if !ok {
		return Entry{}, errf(CodeNotFound, "no existe ese elemento en la papelera")
	}
	origin, e := CleanRel(m.Origin)
	if e != nil || origin == "." {
		origin = m.Name
	}
	dir, base := path.Split(origin)
	pfd, e := openBeneath(root, dir, unix.O_PATH|unix.O_DIRECTORY, 0)
	if e != nil {
		// La carpeta original ya no existe: se restaura en la raíz.
		dir = ""
		if pfd, e = openBeneath(root, ".", unix.O_PATH|unix.O_DIRECTORY, 0); e != nil {
			return Entry{}, e
		}
	}
	defer unix.Close(pfd)
	name := base
	for i := 2; ; i++ {
		err = unix.Renameat2(tfd, id, pfd, name, unix.RENAME_NOREPLACE)
		if !errors.Is(err, unix.EEXIST) || i > 50 {
			break
		}
		name = numbered(base, i)
	}
	if err != nil {
		return Entry{}, errnoErr(err, name)
	}
	_ = unix.Unlinkat(tfd, id+".meta", 0)
	return statRel(root, path.Join(dir, name))
}

// numbered devuelve "foto (2).jpg" para "foto.jpg".
func numbered(name string, n int) string {
	ext := path.Ext(name)
	if ext == name {
		ext = ""
	}
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n, ext)
}

func trashEmpty(root int) (map[string]int, *Error) {
	tfd, err := openTrash(root, unix.O_RDONLY|unix.O_DIRECTORY)
	if errors.Is(err, unix.ENOENT) {
		return map[string]int{"count": 0}, nil
	}
	if err != nil {
		return nil, errnoErr(err, "la papelera")
	}
	sfd, _ := unix.Dup(tfd)
	defer unix.Close(sfd)
	names, e := readNames(tfd)
	if e != nil {
		return nil, e
	}
	n := 0
	for _, name := range names {
		if !ValidTrashID(name) && !strings.HasSuffix(name, ".meta") {
			continue
		}
		if e := removeTree(sfd, name); e != nil {
			return nil, e
		}
		if ValidTrashID(name) {
			n++
		}
	}
	return map[string]int{"count": n}, nil
}

// removeTree borra name (archivo o carpeta, recursivo) dentro de dirfd sin seguir
// symlinks.
func removeTree(dirfd int, name string) *Error {
	err := unix.Unlinkat(dirfd, name, 0)
	if err == nil || errors.Is(err, unix.ENOENT) {
		return nil
	}
	if !errors.Is(err, unix.EISDIR) && !errors.Is(err, unix.EPERM) {
		return errnoErr(err, name)
	}
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errnoErr(err, name)
	}
	sfd, _ := unix.Dup(fd)
	names, e := readNames(fd)
	if e != nil {
		unix.Close(sfd)
		return e
	}
	for _, n := range names {
		if e := removeTree(sfd, n); e != nil {
			unix.Close(sfd)
			return e
		}
	}
	unix.Close(sfd)
	if err := unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR); err != nil {
		return errnoErr(err, name)
	}
	return nil
}

// copyTree copia src (en sdir) como dst (en ddir), con reflink si el filesystem lo
// permite (Btrfs entre subvolúmenes del mismo volumen).
func copyTree(sdir int, src string, ddir int, dst string) *Error {
	var st unix.Stat_t
	if err := unix.Fstatat(sdir, src, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return errnoErr(err, src)
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		in, err := unix.Openat(sdir, src, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return errnoErr(err, src)
		}
		defer unix.Close(in)
		out, err := unix.Openat(ddir, dst, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o660)
		if err != nil {
			return errnoErr(err, dst)
		}
		fo := os.NewFile(uintptr(out), dst)
		defer fo.Close()
		if unix.IoctlFileClone(out, in) == nil {
			return nil
		}
		fi := os.NewFile(uintptr(in), src)
		if _, err := io.Copy(fo, fi); err != nil {
			return errnoErr(err, dst)
		}
		return nil
	case unix.S_IFDIR:
		if err := unix.Mkdirat(ddir, dst, 0o770); err != nil {
			return errnoErr(err, dst)
		}
		sfd, err := unix.Openat(sdir, src, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return errnoErr(err, src)
		}
		s2, _ := unix.Dup(sfd)
		defer unix.Close(s2)
		names, e := readNames(sfd)
		if e != nil {
			return e
		}
		dfd, err := unix.Openat(ddir, dst, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return errnoErr(err, dst)
		}
		defer unix.Close(dfd)
		for _, n := range names {
			if Hidden(n) {
				continue
			}
			if e := copyTree(s2, n, dfd, n); e != nil {
				return e
			}
		}
		return nil
	}
	return nil // symlinks y especiales no se copian
}

// ---- Búsqueda -----------------------------------------------------------------------

func search(roots []string, q string, limit int) ([]Entry, *Error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return []Entry{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := []Entry{}
	visited := 0
	for _, root := range roots {
		if ValidRoot(root) != nil {
			continue
		}
		rfd, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		type item struct{ rel string }
		queue := []item{{"."}}
		for len(queue) > 0 && len(out) < limit && visited < 100_000 && ctx.Err() == nil {
			cur := queue[0]
			queue = queue[1:]
			dfd, e := openBeneath(rfd, cur.rel, unix.O_RDONLY|unix.O_DIRECTORY, 0)
			if e != nil {
				continue
			}
			sfd, _ := unix.Dup(dfd)
			names, e := readNames(dfd)
			if e != nil {
				unix.Close(sfd)
				continue
			}
			sort.Strings(names)
			for _, n := range names {
				if Hidden(n) {
					continue
				}
				visited++
				var st unix.Stat_t
				if unix.Fstatat(sfd, n, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
					continue
				}
				rel := path.Join(cur.rel, n)
				if strings.Contains(strings.ToLower(n), q) {
					en := entryFromStat(n, rel, &st)
					en.Root = root
					out = append(out, en)
					if len(out) >= limit {
						break
					}
				}
				if st.Mode&unix.S_IFMT == unix.S_IFDIR {
					queue = append(queue, item{rel})
				}
			}
			unix.Close(sfd)
		}
		unix.Close(rfd)
	}
	return out, nil
}

// ---- Versiones (instantáneas) ---------------------------------------------------------

// restoreVersion copia rel desde la instantánea snap a la misma carpeta en dst, con
// el nombre "<nombre> (restaurado AAAA-MM-DD HH.MM)<ext>".
func restoreVersion(snap int, rel string, dst int) (Entry, *Error) {
	c, e := CleanRel(rel)
	if e != nil {
		return Entry{}, e
	}
	if c == "." {
		return Entry{}, errf(CodeInvalid, "elegí un archivo o carpeta")
	}
	spfd, base, e := parentOf(snap, c)
	if e != nil {
		return Entry{}, e
	}
	defer unix.Close(spfd)
	dir := path.Dir(c)
	dpfd, e := openBeneath(dst, dir, unix.O_PATH|unix.O_DIRECTORY, 0)
	if e != nil {
		dir = "."
		if dpfd, e = openBeneath(dst, ".", unix.O_PATH|unix.O_DIRECTORY, 0); e != nil {
			return Entry{}, e
		}
	}
	defer unix.Close(dpfd)
	ext := path.Ext(base)
	if ext == base {
		ext = ""
	}
	name := fmt.Sprintf("%s (restaurado %s)%s", strings.TrimSuffix(base, ext), time.Now().Format("2006-01-02 15.04"), ext)
	if e := copyTree(spfd, base, dpfd, name); e != nil {
		_ = removeTree(dpfd, name)
		return Entry{}, e
	}
	return statRel(dst, path.Join(dir, name))
}

// ---- Transferencias -----------------------------------------------------------------

func openRead(root int, rel string) (*os.File, int64, *Error) {
	fd, e := openBeneath(root, rel, unix.O_RDONLY, 0)
	if e != nil {
		return nil, 0, e
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, 0, errnoErr(err, rel)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, 0, errf(CodeInvalid, "%s no es un archivo", rel)
	}
	return os.NewFile(uintptr(fd), rel), st.Size, nil
}

// openWrite crea ".<nombre>.<id>.tango-part" en la carpeta rel. Recién en commit se
// renombra (atómico) al nombre final: una subida cortada nunca pisa nada.
func openWrite(root int, rel, name string) (*os.File, string, *Error) {
	if e := ValidName(name); e != nil {
		return nil, "", e
	}
	dirfd, e := openBeneath(root, rel, unix.O_PATH|unix.O_DIRECTORY, 0)
	if e != nil {
		return nil, "", e
	}
	defer unix.Close(dirfd)
	short := name
	if len(short) > 180 {
		short = short[:180]
	}
	part := "." + short + "." + randomID()[8:] + PartSuffix
	fd, err := unix.Openat(dirfd, part, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o660)
	if err != nil {
		return nil, "", errnoErr(err, name)
	}
	return os.NewFile(uintptr(fd), part), part, nil
}

func validPart(part string) bool {
	return strings.HasPrefix(part, ".") && strings.HasSuffix(part, PartSuffix) && !strings.ContainsAny(part, "/\x00")
}

func commit(root int, rel, part, name string, overwrite bool) (Entry, *Error) {
	if e := ValidName(name); e != nil {
		return Entry{}, e
	}
	if !validPart(part) {
		return Entry{}, errf(CodeInvalid, "archivo parcial inválido")
	}
	dirfd, e := openBeneath(root, rel, unix.O_PATH|unix.O_DIRECTORY, 0)
	if e != nil {
		return Entry{}, e
	}
	defer unix.Close(dirfd)
	flags := uint(unix.RENAME_NOREPLACE)
	if overwrite {
		flags = 0
		var st unix.Stat_t
		if unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Mode&unix.S_IFMT == unix.S_IFDIR {
			return Entry{}, errf(CodeExists, "ya existe una carpeta %s", name)
		}
	}
	if err := unix.Renameat2(dirfd, part, dirfd, name, flags); err != nil {
		return Entry{}, errnoErr(err, name)
	}
	c, _ := CleanRel(rel)
	return statRel(root, path.Join(c, name))
}

func abort(root int, rel, part string) *Error {
	if !validPart(part) {
		return errf(CodeInvalid, "archivo parcial inválido")
	}
	dirfd, e := openBeneath(root, rel, unix.O_PATH|unix.O_DIRECTORY, 0)
	if e != nil {
		return e
	}
	defer unix.Close(dirfd)
	if err := unix.Unlinkat(dirfd, part, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return errnoErr(err, part)
	}
	return nil
}

// ---- Miniaturas ---------------------------------------------------------------------

func thumb(root int, rel string, size int) (ThumbResult, *Error) {
	f, sz, e := openRead(root, rel)
	if e != nil {
		return ThumbResult{}, e
	}
	defer f.Close()
	if sz > 64<<20 {
		return ThumbResult{}, errf(CodeNotSupported, "archivo demasiado grande para miniatura")
	}
	b, err := MakeThumb(bufio.NewReader(f), size)
	if err != nil {
		return ThumbResult{}, errf(CodeNotSupported, "%v", err)
	}
	return ThumbResult{MIME: "image/jpeg", Data: base64.StdEncoding.EncodeToString(b)}, nil
}

// SelfCheck verifica que el proceso corre sin privilegios: UID distinto de 0 y sin
// capacidades efectivas. El worker se niega a arrancar si no.
func SelfCheck() error {
	if os.Getuid() == 0 || os.Geteuid() == 0 {
		return errors.New("el file-worker no puede correr como root")
	}
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return err
	}
	for _, line := range bytes.Split(b, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("CapEff:")) {
			v := strings.TrimSpace(string(line[len("CapEff:"):]))
			if strings.Trim(v, "0") != "" {
				return fmt.Errorf("el file-worker tiene capacidades efectivas (%s)", v)
			}
		}
	}
	return nil
}
