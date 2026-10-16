//go:build linux

package main

// Replicación ZFS (protocol/README.md §18.1, ADR 0016). El agente corre zfs send y
// zfs receive y le pasa a tangod un extremo de un pipe por SCM_RIGHTS (no tiene red);
// zfs.repl.wait devuelve cómo terminó el proceso. Nada de esto monta: receive -u y la
// réplica hereda mountpoint=none de <pool>/tgrepl. Solo zfs.repl.promote monta, en la
// unidad transitoria de siempre (zfs-mount).

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lordbasex/go-link-tangoos/internal/zfs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// zstream es un zfs send/receive en curso.
type zstream struct {
	done   chan struct{}
	err    error
	stderr bytes.Buffer
	at     time.Time
}

var zstreams = struct {
	sync.Mutex
	m map[string]*zstream
}{m: map[string]*zstream{}}

// zstreamMax: un stream que nadie espera se olvida a las 48 h.
const zstreamMax = 48 * time.Hour

func zstreamStart(cmd *exec.Cmd) (string, *zstream, error) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	s := &zstream{done: make(chan struct{}), at: time.Now()}
	cmd.Stderr = &s.stderr
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	zstreams.Lock()
	for k, v := range zstreams.m {
		if time.Since(v.at) > zstreamMax {
			delete(zstreams.m, k)
		}
	}
	zstreams.m[id] = s
	zstreams.Unlock()
	go func() {
		err := cmd.Wait()
		if err != nil {
			msg := firstLineOf(strings.TrimSpace(s.stderr.String()))
			if msg == "" {
				msg = err.Error()
			}
			s.err = errors.New(msg)
		}
		close(s.done)
	}()
	return id, s, nil
}

// replHook atiende zfs.repl.send y zfs.repl.recv: responden con un fd.
func replHook(ctx context.Context, c *net.UnixConn, req *agentproto.Request, log *slog.Logger) bool {
	if req.Op != agentproto.OpZfsReplSend && req.Op != agentproto.OpZfsReplRecv {
		return false
	}
	resp := agentproto.Response{V: agentproto.Version, ID: req.ID}
	fail := func(e *agentproto.Error) bool {
		resp.Error = e
		b, _ := json.Marshal(resp)
		_, _ = c.Write(append(b, '\n'))
		log.Info("op", "op", req.Op, "id", req.ID, "ok", false, "code", e.Code, "msg", e.Message)
		return true
	}
	if !zfsAvailable() {
		return fail(agentproto.Errf(agentproto.CodeZfsUnavailable, "ZFS no está habilitado"))
	}
	var res agentproto.ZfsStreamResult
	var fd *os.File
	var e *agentproto.Error
	if req.Op == agentproto.OpZfsReplSend {
		var a agentproto.ZfsReplSendArgs
		if err := agentproto.DecodeStrict(req.Args, &a); err != nil {
			return fail(agentproto.Errf(agentproto.CodeInvalidArgs, "argumentos inválidos: %v", err))
		}
		res, fd, e = replSend(ctx, &a)
	} else {
		var a agentproto.ZfsReplRecvArgs
		if err := agentproto.DecodeStrict(req.Args, &a); err != nil {
			return fail(agentproto.Errf(agentproto.CodeInvalidArgs, "argumentos inválidos: %v", err))
		}
		res, fd, e = replRecv(ctx, &a)
	}
	if e != nil {
		return fail(e)
	}
	defer fd.Close()
	b, _ := json.Marshal(res)
	resp.OK, resp.Result = true, b
	out, _ := json.Marshal(resp)
	if _, _, err := c.WriteMsgUnix(append(out, '\n'), unix.UnixRights(int(fd.Fd())), nil); err != nil {
		log.Warn(req.Op+": no se pudo entregar el fd", "err", err)
	}
	log.Info("op", "op", req.Op, "id", req.ID, "ok", true, "stream", res.ID, "size", res.Size)
	return true
}

func validReplVol(pool, vol string) *agentproto.Error {
	if err := zfs.ValidPoolName(pool); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if err := zfs.ValidVolumeName(vol); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	return nil
}

func validReplTarget(pool, origin, vol, rel string) *agentproto.Error {
	if e := validReplVol(pool, vol); e != nil {
		return e
	}
	if err := zfs.ValidOrigin(origin); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if err := zfs.ValidRel(rel); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	return nil
}

func replSend(ctx context.Context, a *agentproto.ZfsReplSendArgs) (agentproto.ZfsStreamResult, *os.File, *agentproto.Error) {
	var res agentproto.ZfsStreamResult
	// También con token: el token lo da el destino y se verifica contra este dataset.
	if e := validReplVol(a.Pool, a.Vol); e != nil {
		return res, nil, e
	}
	if err := zfs.ValidRel(a.Rel); err != nil {
		return res, nil, agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	ds := zfs.Join(zfs.VolumeDataset(a.Pool, a.Vol), a.Rel)
	dry, err := zfs.ArgsSend(ds, a.Snap, a.From, a.Token, true)
	if err != nil {
		return res, nil, agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	out, err := zrun(ctx, zfs.ZfsBin, nil, dry...)
	if err != nil {
		return res, nil, zerr(err)
	}
	if a.Token != "" {
		enc, err := zfsGetProp(ctx, ds, "encryption")
		if err != nil {
			return res, nil, zerr(err)
		}
		if err := zfs.CheckToken(string(out), ds, enc != "" && enc != "off"); err != nil {
			return res, nil, agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
	}
	res.Size = zfs.ParseSendSize(string(out))
	argv, _ := zfs.ArgsSend(ds, a.Snap, a.From, a.Token, false)
	r, w, err := os.Pipe()
	if err != nil {
		return res, nil, agentproto.Errf(agentproto.CodeInternal, "pipe: %v", err)
	}
	cmd := exec.Command(zfs.ZfsBin, argv...)
	cmd.Stdout = w
	id, _, err := zstreamStart(cmd)
	w.Close()
	if err != nil {
		r.Close()
		return res, nil, agentproto.Errf(agentproto.CodeFailed, "zfs send: %v", err)
	}
	res.ID = id
	return res, r, nil
}

// replEnsureParents crea <pool>/tgrepl (sin montar, solo lectura) y <pool>/tgrepl/<origen>.
func replEnsureParents(ctx context.Context, pool, origin string) error {
	root := pool + "/" + zfs.ReplRoot
	if !zfsExists(ctx, root) {
		if _, err := zrun(ctx, zfs.ZfsBin, nil, "create", "-u", "-o", "canmount=off", "-o", "mountpoint=none", "-o", "readonly=on", root); err != nil && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	parent := zfs.ReplParent(pool, origin)
	if !zfsExists(ctx, parent) {
		if _, err := zrun(ctx, zfs.ZfsBin, nil, "create", "-u", "-o", "canmount=off", parent); err != nil && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	return nil
}

func replRecv(ctx context.Context, a *agentproto.ZfsReplRecvArgs) (agentproto.ZfsStreamResult, *os.File, *agentproto.Error) {
	var res agentproto.ZfsStreamResult
	if e := validReplTarget(a.Pool, a.Origin, a.Vol, a.Rel); e != nil {
		return res, nil, e
	}
	if a.BaseGUID != "" {
		if err := zfs.ValidGUID(a.BaseGUID); err != nil {
			return res, nil, agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
	}
	if err := replEnsureParents(ctx, a.Pool, a.Origin); err != nil {
		return res, nil, zerr(err)
	}
	root := zfs.ReplDataset(a.Pool, a.Origin, a.Vol)
	target := zfs.Join(root, a.Rel)
	if a.BaseGUID != "" {
		st, e := replState(ctx, root)
		if e != nil {
			return res, nil, e
		}
		for _, d := range st.Datasets {
			if d.Rel != a.Rel {
				continue
			}
			for _, s := range zfs.SnapsAfter(d, a.BaseGUID) {
				if _, err := zrun(ctx, zfs.ZfsBin, nil, "destroy", target+"@"+s); err != nil {
					return res, nil, zerr(err)
				}
			}
		}
	}
	r, w, err := os.Pipe()
	if err != nil {
		return res, nil, agentproto.Errf(agentproto.CodeInternal, "pipe: %v", err)
	}
	cmd := exec.Command(zfs.ZfsBin, zfs.ArgsRecv(target, a.BaseGUID != "")...)
	cmd.Stdin = r
	id, _, err := zstreamStart(cmd)
	r.Close()
	if err != nil {
		w.Close()
		return res, nil, agentproto.Errf(agentproto.CodeFailed, "zfs receive: %v", err)
	}
	res.ID = id
	return res, w, nil
}

// replState lee los datasets de root con sus instantáneas y tokens.
func replState(ctx context.Context, root string) (agentproto.ZfsReplState, *agentproto.Error) {
	st := agentproto.ZfsReplState{Datasets: []agentproto.ReplDataset{}}
	fs, err := zrun(ctx, zfs.ZfsBin, nil, "list", "-j", "-p", "-t", "filesystem", "-r", "-o", zfs.ReplListProps, root)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return st, nil
		}
		return st, zerr(err)
	}
	sn, err := zrun(ctx, zfs.ZfsBin, nil, "list", "-j", "-p", "-t", "snapshot", "-r", "-o", "name,guid,createtxg", root)
	if err != nil {
		return st, zerr(err)
	}
	ds, err := zfs.ParseReplState(root, fs, sn)
	if err != nil {
		return st, agentproto.Errf(agentproto.CodeFailed, "%v", err)
	}
	st.Exists, st.Datasets = len(ds) > 0, ds
	return st, nil
}

func registerZfsRepl(r *agentproto.Registry) {
	agentproto.Register(r, agentproto.OpZfsReplState, func(a *agentproto.ZfsReplStateArgs) *agentproto.Error {
		if a.Origin != "" {
			return validReplTarget(a.Pool, a.Origin, a.Vol, "")
		}
		return validReplVol(a.Pool, a.Vol)
	}, func(ctx context.Context, a *agentproto.ZfsReplStateArgs) (any, *agentproto.Error) {
		root := zfs.VolumeDataset(a.Pool, a.Vol)
		if a.Origin != "" {
			root = zfs.ReplDataset(a.Pool, a.Origin, a.Vol)
		}
		return replState(ctx, root)
	})

	agentproto.Register(r, agentproto.OpZfsReplSnapshot, func(a *agentproto.ZfsReplSnapshotArgs) *agentproto.Error {
		if e := validReplVol(a.Pool, a.Vol); e != nil {
			return e
		}
		if err := zfs.ValidReplSnap(a.Name); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		return nil
	}, func(ctx context.Context, a *agentproto.ZfsReplSnapshotArgs) (any, *agentproto.Error) {
		if _, err := zrun(ctx, zfs.ZfsBin, nil, "snapshot", "-r", zfs.VolumeDataset(a.Pool, a.Vol)+"@"+a.Name); err != nil && !strings.Contains(err.Error(), "already exists") {
			return nil, zerr(err)
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpZfsReplWait, func(a *agentproto.ZfsReplWaitArgs) *agentproto.Error {
		if len(a.ID) != 16 {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "stream inválido")
		}
		return nil
	}, func(ctx context.Context, a *agentproto.ZfsReplWaitArgs) (any, *agentproto.Error) {
		zstreams.Lock()
		s := zstreams.m[a.ID]
		zstreams.Unlock()
		if s == nil {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe el stream")
		}
		select { // el agente corta las ops a los 30 s: tangod vuelve a preguntar
		case <-s.done:
		case <-time.After(20 * time.Second):
			return map[string]bool{"done": false}, nil
		case <-ctx.Done():
			return map[string]bool{"done": false}, nil
		}
		zstreams.Lock()
		delete(zstreams.m, a.ID)
		zstreams.Unlock()
		if s.err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "%v", s.err)
		}
		return map[string]bool{"done": true}, nil
	})

	agentproto.Register(r, agentproto.OpZfsReplPrune, func(a *agentproto.ZfsReplPruneArgs) *agentproto.Error {
		if e := validReplVol(a.Pool, a.Vol); e != nil {
			return e
		}
		if err := zfs.ValidTaskID(a.Task); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		if a.Keep != "" && zfs.ReplSnapTask(a.Keep) != a.Task {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "instantánea de otra tarea")
		}
		return nil
	}, func(ctx context.Context, a *agentproto.ZfsReplPruneArgs) (any, *agentproto.Error) {
		ds := zfs.VolumeDataset(a.Pool, a.Vol)
		out, err := zrun(ctx, zfs.ZfsBin, nil, "list", "-H", "-t", "snapshot", "-d", "1", "-o", "name", ds)
		if err != nil {
			return nil, zerr(err)
		}
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			_, snap, ok := strings.Cut(l, "@")
			if !ok || snap == a.Keep || zfs.ReplSnapTask(snap) != a.Task {
				continue
			}
			if _, err := zrun(ctx, zfs.ZfsBin, nil, "destroy", "-r", ds+"@"+snap); err != nil {
				return nil, zerr(err)
			}
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpZfsReplMirror, func(a *agentproto.ZfsReplMirrorArgs) *agentproto.Error {
		if e := validReplTarget(a.Pool, a.Origin, a.Vol, ""); e != nil {
			return e
		}
		for rel, guids := range a.Keep {
			if err := zfs.ValidRel(rel); err != nil {
				return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
			}
			for _, g := range guids {
				if err := zfs.ValidGUID(g); err != nil {
					return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
				}
			}
		}
		return nil
	}, func(ctx context.Context, a *agentproto.ZfsReplMirrorArgs) (any, *agentproto.Error) {
		root := zfs.ReplDataset(a.Pool, a.Origin, a.Vol)
		st, e := replState(ctx, root)
		if e != nil {
			return nil, e
		}
		n := 0
		for _, d := range st.Datasets {
			keep, ok := a.Keep[d.Rel]
			if !ok || d.Token != "" { // carpeta borrada en el origen, o envío a medias: no se toca
				continue
			}
			set := map[string]bool{}
			for _, g := range keep {
				set[g] = true
			}
			for _, s := range d.Snaps {
				if set[s.GUID] {
					continue
				}
				if _, err := zrun(ctx, zfs.ZfsBin, nil, "destroy", zfs.Join(root, d.Rel)+"@"+s.Name); err != nil {
					return nil, zerr(err)
				}
				n++
			}
		}
		return map[string]int{"destroyed": n}, nil
	})

	agentproto.Register(r, agentproto.OpZfsReplAbort, func(a *agentproto.ZfsReplTargetArgs) *agentproto.Error {
		return validReplTarget(a.Pool, a.Origin, a.Vol, a.Rel)
	}, func(ctx context.Context, a *agentproto.ZfsReplTargetArgs) (any, *agentproto.Error) {
		if _, err := zrun(ctx, zfs.ZfsBin, nil, "receive", "-A", zfs.Join(zfs.ReplDataset(a.Pool, a.Origin, a.Vol), a.Rel)); err != nil &&
			!strings.Contains(err.Error(), "does not have any resumable") && !strings.Contains(err.Error(), "does not exist") {
			return nil, zerr(err)
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpZfsReplDestroy, func(a *agentproto.ZfsReplTargetArgs) *agentproto.Error {
		return validReplTarget(a.Pool, a.Origin, a.Vol, "")
	}, func(ctx context.Context, a *agentproto.ZfsReplTargetArgs) (any, *agentproto.Error) {
		ds := zfs.ReplDataset(a.Pool, a.Origin, a.Vol)
		if _, err := zrun(ctx, zfs.ZfsBin, nil, "destroy", "-r", ds); err != nil && !strings.Contains(err.Error(), "does not exist") {
			return nil, zerr(err)
		}
		zfsInvalidate()
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpZfsReplPromote, func(a *agentproto.ZfsReplPromoteArgs) *agentproto.Error {
		if e := validReplTarget(a.Pool, a.Origin, a.Vol, ""); e != nil {
			return e
		}
		if err := zfs.ValidVolumeName(a.Name); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		return nil
	}, replPromote)
}

// replPromote convierte la réplica en un volumen de TangoOS: rename -u al pool, el
// esquema de puntos de montaje de siempre y, si está cifrada y llegó la frase, la clave
// cargada (con las carpetas que hayan llegado como raíces de cifrado propias vueltas a
// heredar la del volumen). Después monta en la unidad transitoria.
func replPromote(ctx context.Context, a *agentproto.ZfsReplPromoteArgs) (any, *agentproto.Error) {
	src := zfs.ReplDataset(a.Pool, a.Origin, a.Vol)
	dst := zfs.VolumeDataset(a.Pool, a.Name)
	st, e := replState(ctx, src)
	if e != nil {
		return nil, e
	}
	if !st.Exists {
		return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe la réplica")
	}
	for _, d := range st.Datasets {
		if d.Token != "" {
			return nil, agentproto.Errf(agentproto.CodeBusy, "la réplica tiene un envío a medias: terminalo o descartalo primero")
		}
	}
	if zfsExists(ctx, dst) {
		return nil, agentproto.Errf(agentproto.CodeExists, "ya existe el volumen %s", a.Name)
	}
	// Un pool destruido deja los puntos de montaje vacíos (/vol/@shares/<Carpeta>): eso no
	// cuenta como ocupado; un archivo, sí.
	if fi, err := os.Stat("/" + a.Name); err == nil && (!fi.IsDir() || !onlyEmptyDirs("/"+a.Name)) {
		return nil, agentproto.Errf(agentproto.CodeExists, "/%s ya existe y no está vacío", a.Name)
	}
	root := st.Datasets[0]
	if root.Encrypted && a.Passphrase != "" && !root.KeyLoaded {
		if _, err := zrun(ctx, zfs.ZfsBin, []byte(a.Passphrase+"\n"), "load-key", src); err != nil {
			if strings.Contains(err.Error(), "ncorrect") {
				return nil, agentproto.Errf(agentproto.CodeWrongPassphrase, "la frase no abre esta réplica")
			}
			return nil, zerr(err)
		}
	}
	if _, err := zrun(ctx, zfs.ZfsBin, nil, "rename", "-u", src, dst); err != nil {
		return nil, zerr(err)
	}
	set := func(ds string, props ...string) error {
		_, err := zrun(ctx, zfs.ZfsBin, nil, append(append([]string{"set"}, props...), ds)...)
		return err
	}
	// Cada dataset llegó con canmount=off mountpoint=none readonly=on propios (ArgsRecv):
	// se pone el esquema de TangoOS y se vuelve a escribir.
	for _, d := range st.Datasets {
		ds := zfs.Join(dst, d.Rel)
		var props []string
		switch {
		case d.Rel == "":
			props = []string{"mountpoint=/" + a.Name, "canmount=on"}
		case d.Rel == "shares" || d.Rel == "apps":
			props = []string{"canmount=off", "mountpoint=/" + a.Name + "/@" + d.Rel}
		default:
			props = []string{"canmount=on"}
		}
		if err := set(ds, props...); err != nil {
			return nil, zerr(err)
		}
		inherit := []string{"readonly"}
		if strings.Contains(d.Rel, "/") {
			inherit = append(inherit, "mountpoint")
		}
		for _, p := range inherit {
			if _, err := zrun(ctx, zfs.ZfsBin, nil, "inherit", p, ds); err != nil {
				return nil, zerr(err)
			}
		}
	}
	// Los datasets recibidos de a uno pueden quedar como raíces de cifrado propias (con la
	// misma frase): se cargan y vuelven a heredar la clave del volumen.
	encrypted, locked := root.Encrypted, root.Encrypted
	if root.Encrypted && (a.Passphrase != "" || root.KeyLoaded) {
		after, e := replState(ctx, dst)
		if e != nil {
			return nil, e
		}
		locked = false
		for _, d := range after.Datasets[1:] {
			if !d.Encrypted || d.EncRoot != zfs.Join(dst, d.Rel) {
				continue
			}
			ds := zfs.Join(dst, d.Rel)
			if !d.KeyLoaded {
				if a.Passphrase == "" {
					locked = true
					continue
				}
				if _, err := zrun(ctx, zfs.ZfsBin, []byte(a.Passphrase+"\n"), "load-key", ds); err != nil && !strings.Contains(err.Error(), "already loaded") {
					return nil, zerr(fmt.Errorf("%s: %w", d.Rel, err))
				}
			}
			if _, err := zrun(ctx, zfs.ZfsBin, nil, "change-key", "-i", ds); err != nil {
				return nil, zerr(fmt.Errorf("%s: %w", d.Rel, err))
			}
		}
	}
	zfsInvalidate()
	if !encrypted || !locked {
		if err := zfsHelper(ctx, "zfs-mount", &agentproto.ZfsVolArgs{Pool: a.Pool, Name: a.Name}); err != nil {
			return nil, zerr(err)
		}
	}
	zfsInvalidate()
	return map[string]bool{"locked": encrypted && locked}, nil
}

// onlyEmptyDirs dice si p tiene solo directorios vacíos (restos de puntos de montaje) y no
// es un punto de montaje.
func onlyEmptyDirs(p string) bool {
	if isMountedAt(p) {
		return false
	}
	ok := true
	_ = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || (path != p && isMountedAt(path)) {
			ok = false
			return filepath.SkipAll
		}
		return nil
	})
	return ok
}
