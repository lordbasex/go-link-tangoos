// tango-agent es el único proceso root de TangoOS (docs/02, docs/07 §5, ADR 0007).
//
// Escucha en un socket Unix, acepta solo a tangod (o root) por SO_PEERCRED y ejecuta
// operaciones tipadas de una allow-list. Nunca ejecuta strings ni comandos arbitrarios.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/smart"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

var version = "dev"

type config struct {
	socket     string
	journal    string
	allowUser  string
	allowGroup string
}

func main() {
	if runFileworkerMode() { // re-ejecutado como file-worker (fileworker_linux.go)
		return
	}
	var c config
	flag.StringVar(&c.socket, "socket", envOr("TANGO_AGENT_SOCKET", "/run/tango/agent.sock"), "socket Unix (ignorado con activación por systemd)")
	flag.StringVar(&c.journal, "journal", envOr("TANGO_AGENT_JOURNAL", "/var/lib/tango-agent/journal"), "directorio del journal de operaciones")
	flag.StringVar(&c.allowUser, "allow-user", "tango", "usuario autorizado además de root")
	flag.StringVar(&c.allowGroup, "socket-group", "tango-agent", "grupo dueño del socket")
	showVersion := flag.Bool("version", false, "muestra la versión y sale")
	flag.Parse()
	if *showVersion {
		fmt.Println("tango-agent", version)
		return
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(c, log); err != nil {
		log.Error("tango-agent terminó con error", "err", err)
		os.Exit(1)
	}
}

func run(c config, log *slog.Logger) error {
	j, err := openJournal(c.journal)
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	if n, err := j.recover(); err != nil {
		return fmt.Errorf("journal: %w", err)
	} else if n > 0 {
		log.Warn("operaciones interrumpidas en el arranque anterior", "count", n)
	}

	allowed := map[uint32]bool{0: true}
	if u, err := user.Lookup(c.allowUser); err == nil {
		if uid, err := strconv.ParseUint(u.Uid, 10, 32); err == nil {
			allowed[uint32(uid)] = true
		}
	} else {
		log.Warn("usuario autorizado inexistente: solo root podrá conectarse", "user", c.allowUser)
	}

	ln, err := listen(c, log)
	if err != nil {
		return err
	}
	reg := newRegistry(smart.DefaultPaths)
	registerStorage(reg, smart.DefaultPaths, diskops.DefaultEnv, newAsyncJobs(j, log), log)
	registerApps(reg, newAsyncJobs(j, log), log) // Centro de apps: su propia cola (no bloquea al almacenamiento)
	m4jobs := newAsyncJobs(j, log)
	registerM4(reg, m4jobs, log)      // M4: sistema, servicios, actualizaciones (m4_linux.go)
	registerUPS(reg, m4jobs)          // UPS por USB con NUT (ups_linux.go), misma cola que M4
	registerGluster(reg, m4jobs, log) // clúster GlusterFS (gluster_linux.go, protocol/README.md §17)
	prevHook := fdHook                // term.spawn también responde con un fd (el PTY)
	fdHook = func(ctx context.Context, c *net.UnixConn, req *agentproto.Request, l *slog.Logger) bool {
		return termHook(ctx, c, req, l) || (prevHook != nil && prevHook(ctx, c, req, l))
	}
	srv := &server{reg: reg, allowed: allowed, log: log, journal: j}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	log.Info("tango-agent escuchando", "version", version, "addr", ln.Addr().String(), "ops", srv.reg.Ops())
	err = srv.serve(ctx, ln)
	if ctx.Err() != nil {
		log.Info("tango-agent detenido")
		return nil
	}
	return err
}

// listen usa el socket de systemd (LISTEN_FDS) si existe; si no, crea uno 0660 root:grupo.
func listen(c config, log *slog.Logger) (*net.UnixListener, error) {
	if os.Getenv("LISTEN_PID") == strconv.Itoa(os.Getpid()) && os.Getenv("LISTEN_FDS") == "1" {
		f := os.NewFile(3, "systemd-socket")
		l, err := net.FileListener(f)
		if err != nil {
			return nil, fmt.Errorf("socket de systemd: %w", err)
		}
		ul, ok := l.(*net.UnixListener)
		if !ok {
			return nil, errors.New("el socket de systemd no es Unix")
		}
		log.Info("usando el socket de systemd")
		return ul, nil
	}
	if err := os.MkdirAll(filepath.Dir(c.socket), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(c.socket)
	old := syscall.Umask(0o117)
	ul, err := net.ListenUnix("unix", &net.UnixAddr{Name: c.socket, Net: "unix"})
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if g, err := user.LookupGroup(c.allowGroup); err == nil {
		if gid, err := strconv.Atoi(g.Gid); err == nil {
			_ = os.Chown(c.socket, 0, gid)
		}
	}
	_ = os.Chmod(c.socket, 0o660)
	return ul, nil
}

func newRegistry(paths smart.Paths) *agentproto.Registry {
	r := agentproto.NewRegistry()
	agentproto.Register(r, agentproto.OpVersion, nil, func(context.Context, *agentproto.NoArgs) (any, *agentproto.Error) {
		return agentproto.VersionResult{Agent: version, Protocol: agentproto.Version, Ops: r.Ops()}, nil
	})
	agentproto.Register(r, agentproto.OpDiskSmart, func(a *agentproto.DiskSmartArgs) *agentproto.Error {
		if a.DevID == "" {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "falta dev_id")
		}
		return nil
	}, func(ctx context.Context, a *agentproto.DiskSmartArgs) (any, *agentproto.Error) {
		dev, e := paths.Resolve(a.DevID)
		if e != nil {
			return nil, e
		}
		done := make(chan agentproto.DiskSmartResult, 1)
		go func() { done <- smart.Read(a.DevID, dev) }()
		select {
		case res := <-done:
			return res, nil
		case <-ctx.Done():
			return nil, agentproto.Errf(agentproto.CodeInternal, "el disco no respondió a tiempo")
		}
	})
	return r
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

const opTimeout = 30 * time.Second
