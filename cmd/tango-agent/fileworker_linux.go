//go:build linux

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"os/exec"
	osuser "os/user"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/fileworker"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// fileworkerArg es el argv[1] con que el agente se re-ejecuta como file-worker.
const fileworkerArg = "__fileworker"

// runFileworkerMode corre el proceso como file-worker si así fue lanzado: hereda en
// el fd 3 su extremo del socketpair, verifica que no tiene privilegios y atiende a
// tangod hasta que cierre. Devuelve false si el proceso es el agente normal.
func runFileworkerMode() bool {
	if len(os.Args) < 2 || os.Args[1] != fileworkerArg {
		return false
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})).With("component", "fileworker", "uid", os.Getuid())
	if err := fileworker.SelfCheck(); err != nil {
		log.Error("file-worker sin arrancar", "err", err)
		os.Exit(2)
	}
	f := os.NewFile(3, "fileworker-socket")
	c, err := net.FileConn(f)
	f.Close()
	if err != nil {
		log.Error("file-worker: socket", "err", err)
		os.Exit(2)
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		os.Exit(2)
	}
	if err := fileworker.Serve(uc); err != nil {
		log.Warn("file-worker terminó con error", "err", err)
	}
	os.Exit(0)
	return true
}

// fdHook atiende files.worker: lanza el worker y responde con el fd.
var fdHook = func(ctx context.Context, c *net.UnixConn, req *agentproto.Request, log *slog.Logger) bool {
	if req.Op != agentproto.OpFileWorker {
		return false
	}
	resp := agentproto.Response{V: agentproto.Version, ID: req.ID}
	var a agentproto.FileWorkerArgs
	fail := func(e *agentproto.Error) bool {
		resp.Error = e
		b, _ := json.Marshal(resp)
		_, _ = c.Write(append(b, '\n'))
		log.Info("op", "op", req.Op, "id", req.ID, "ok", false, "code", e.Code)
		return true
	}
	if err := agentproto.DecodeStrict(req.Args, &a); err != nil {
		return fail(agentproto.Errf(agentproto.CodeInvalidArgs, "argumentos inválidos: %v", err))
	}
	res, fd, e := spawnFileworker(a.User)
	if e != nil {
		return fail(e)
	}
	defer fd.Close()
	b, _ := json.Marshal(res)
	resp.OK, resp.Result = true, b
	out, _ := json.Marshal(resp)
	if _, _, err := c.WriteMsgUnix(append(out, '\n'), unix.UnixRights(int(fd.Fd())), nil); err != nil {
		log.Warn("files.worker: no se pudo entregar el fd", "err", err)
	}
	log.Info("op", "op", req.Op, "id", req.ID, "ok", true, "user", a.User, "pid", res.PID)
	return true
}

// spawnFileworker crea el socketpair y lanza el worker con las credenciales del
// usuario. Devuelve el extremo de tangod.
func spawnFileworker(name string) (agentproto.FileWorkerResult, *os.File, *agentproto.Error) {
	var res agentproto.FileWorkerResult
	if err := diskops.ValidUserName(name); err != nil {
		return res, nil, agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	u, err := osuser.Lookup(name)
	if err != nil {
		return res, nil, agentproto.Errf(agentproto.CodeInvalidArgs, "no existe el usuario Unix %s", name)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	if uid < diskops.MinUID || gid <= 0 {
		return res, nil, agentproto.Errf(agentproto.CodeInvalidArgs, "%s no es un usuario de TangoOS", name)
	}
	gids, err := u.GroupIds()
	if err != nil {
		return res, nil, agentproto.Errf(agentproto.CodeInternal, "grupos de %s: %v", name, err)
	}
	groups := []uint32{}
	for _, g := range gids {
		if n, err := strconv.Atoi(g); err == nil && n > 0 {
			groups = append(groups, uint32(n))
		}
	}
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return res, nil, agentproto.Errf(agentproto.CodeInternal, "socketpair: %v", err)
	}
	mine := os.NewFile(uintptr(fds[0]), "fileworker-tangod")
	theirs := os.NewFile(uintptr(fds[1]), "fileworker-child")
	cmd := exec.Command("/proc/self/exe", fileworkerArg)
	cmd.Env = []string{"LANG=C.UTF-8", "PATH=/usr/bin:/bin"}
	cmd.Dir = "/"
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{theirs}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: groups},
		Setsid:     true,
	}
	if err := cmd.Start(); err != nil {
		mine.Close()
		theirs.Close()
		return res, nil, agentproto.Errf(agentproto.CodeInternal, "no se pudo lanzar el file-worker: %v", err)
	}
	theirs.Close()
	go func() { _ = cmd.Wait() }() // cosechar al terminar
	res = agentproto.FileWorkerResult{PID: cmd.Process.Pid, UID: uint32(uid), Groups: groups}
	return res, mine, nil
}
