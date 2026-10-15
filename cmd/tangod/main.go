// Command tangod is the TangoOS server: it serves the desktop bundle, the
// /signal WebSocket, the embedded STUN server and the WebRTC peer that
// carries the rpc/events/metrics DataChannels (docs/02, protocol/README.md).
// It runs unprivileged; systemd grants CAP_NET_BIND_SERVICE for :80/:443.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"github.com/lordbasex/go-link-tangoos/internal/appproxy"
	appcatalog "github.com/lordbasex/go-link-tangoos/system/apps"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/agentclient"
	"github.com/lordbasex/go-link-tangoos/internal/api"
	"github.com/lordbasex/go-link-tangoos/internal/audit"
	"github.com/lordbasex/go-link-tangoos/internal/auth"
	"github.com/lordbasex/go-link-tangoos/internal/certs"
	"github.com/lordbasex/go-link-tangoos/internal/connect"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/metrics"
	"github.com/lordbasex/go-link-tangoos/internal/notify"
	"github.com/lordbasex/go-link-tangoos/internal/rtc"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/internal/sysinfo"
	"github.com/lordbasex/go-link-tangoos/internal/webui"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

var version = "dev" // set by -ldflags "-X main.version=…"

type config struct {
	dev       bool
	httpsAddr string
	httpAddr  string
	devAddr   string
	stateDir  string
	icePort   int
	stunPort  int
	nat1to1   string
	agentSock string
	debug     bool
}

func main() {
	var cfg config
	flag.BoolVar(&cfg.dev, "dev", envBool("TANGOD_DEV"), "plain HTTP on --dev-addr, no TLS (local development only)")
	flag.StringVar(&cfg.httpsAddr, "https-addr", envOr("TANGOD_HTTPS_ADDR", ":443"), "HTTPS listen address")
	flag.StringVar(&cfg.httpAddr, "http-addr", envOr("TANGOD_HTTP_ADDR", ":80"), "HTTP listen address (redirects to HTTPS); empty disables it")
	flag.StringVar(&cfg.devAddr, "dev-addr", envOr("TANGOD_DEV_ADDR", "127.0.0.1:8080"), "listen address in --dev mode")
	flag.StringVar(&cfg.stateDir, "state-dir", envOr("STATE_DIRECTORY", ""), "state directory (systemd sets STATE_DIRECTORY; default /var/lib/tango, or a temp dir in --dev)")
	flag.IntVar(&cfg.icePort, "ice-port", envInt("TANGOD_ICE_PORT", 8443), "single UDP+TCP port for WebRTC ICE")
	flag.IntVar(&cfg.stunPort, "stun-port", envInt("TANGOD_STUN_PORT", 3478), "embedded STUN server port (UDP+TCP); 0 disables it")
	flag.StringVar(&cfg.nat1to1, "nat1to1", envOr("TANGOD_NAT1TO1", ""), "comma-separated public IPs to advertise as ICE host candidates")
	flag.StringVar(&cfg.agentSock, "agent-socket", envOr("TANGOD_AGENT_SOCKET", agentclient.DefaultSocket), "tango-agent socket (SMART and privileged operations)")
	flag.BoolVar(&cfg.debug, "debug", envBool("TANGOD_DEBUG"), "debug logging")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		os.Stdout.WriteString(version + "\n")
		return
	}

	level := slog.LevelInfo
	if cfg.debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Error("tangod stopped", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config) error {
	if cfg.stateDir == "" {
		cfg.stateDir = "/var/lib/tango"
		if cfg.dev {
			cfg.stateDir = filepath.Join(os.TempDir(), "tangod-dev")
		}
	}
	if err := os.MkdirAll(cfg.stateDir, 0o700); err != nil {
		return err
	}

	// ---- services behind the RPC methods ----
	users, err := auth.Open(cfg.stateDir)
	if err != nil {
		return err
	}
	if pw, err := users.EnsureAdmin(cfg.stateDir); err != nil {
		return err
	} else if pw != "" {
		slog.Warn("first run: created user admin; its password is in the file (root-only), change it", "file", filepath.Join(cfg.stateDir, "initial-admin-password"))
	}
	// Compuerta de sesión del proxy de apps (§12): antes de registrar las apps, que levantan sus proxies.
	if g, err := appproxy.NewGate(cfg.stateDir); err == nil {
		g.SessionAlive = users.SessionAlive // la cookie de una app muere con la sesión (§13)
		appproxy.DefaultGate = g
	} else {
		slog.Warn("compuerta del proxy de apps desactivada", "err", err)
	}
	auditLog, err := audit.Open(cfg.stateDir)
	if err != nil {
		return err
	}
	defer auditLog.Close()
	var certMgr *certs.Manager
	if !cfg.dev {
		if certMgr, err = certs.Open(cfg.stateDir); err != nil {
			return err
		}
	}
	inv := storage.New()
	agent := agentclient.New(cfg.agentSock)
	defer agent.Close()
	inv.Smart = api.NewSmartCache(api.AgentSmart{C: agent}, time.Minute)

	hub := rtcrpc.NewHub(api.Topics...)
	router := rtcrpc.NewRouter(slog.Default())
	jobStore := jobs.Open(cfg.stateDir, func(j jobs.Job) { hub.Publish("jobs.changed", j) })
	apiH := api.Register(router, api.Deps{
		Auth: users, Inventory: inv, Hub: hub, StateDir: cfg.stateDir, Log: slog.Default(),
		SysInfo: sysinfo.Reader{Root: "/", Version: version},
		Agent:   agent, Jobs: jobStore,
	})
	apiH.RegisterM4(router, api.M4Deps{Agent: agent, Certs: certMgr, Audit: auditLog, Notify: notify.Open(cfg.stateDir), Version: version})
	go api.RunMetrics(ctx, hub, metrics.New())
	go api.WatchStorage(ctx, hub, inv)
	go apiH.RunMaintenance(ctx) // monitor de volúmenes y programador de instantáneas (§11)

	if cfg.stunPort > 0 {
		stun, err := rtc.StartSTUN(cfg.stunPort)
		if err != nil {
			return err
		}
		defer stun.Close()
	}
	var nat []string
	for _, ip := range strings.Split(cfg.nat1to1, ",") {
		if ip = strings.TrimSpace(ip); ip != "" {
			nat = append(nat, ip)
		}
	}
	rtcSrv, err := rtc.New(ctx, rtc.Config{
		ICEPort: cfg.icePort, STUNPort: cfg.stunPort, NAT1To1: nat, Loopback: cfg.dev,
		Server: "tangod/" + version, NASID: nasID(cfg.stateDir),
	}, router, hub, slog.Default())
	if err != nil {
		return err
	}
	defer rtcSrv.Close()
	rtcSrv.SetFileHandler(apiH.FileFrame) // canales files.N (protocol/README.md §10)
	// TangoConnect (§15): acceso remoto por signalhub y enlaces públicos. Desactivado
	// hasta que el admin lo prenda en Ajustes; el certificado DTLS persistente se crea igual.
	cstore, err := connect.OpenStore(cfg.stateDir)
	if err != nil {
		return err
	}
	bundle, err := connect.NewBundle(webui.Bundle(), version)
	if err != nil {
		slog.Warn("connect: el escritorio no se puede servir por bundle", "err", err)
	}
	csvc := connect.New(connect.Deps{Store: cstore, RTC: rtcSrv, Log: slog.Default(), Version: version,
		OnChange: func() { hub.Publish("connect.changed", map[string]any{}) }})
	apiH.RegisterConnect(router, api.ConnectDeps{Service: csvc, Bundle: bundle, Server: "tangod/" + version, NASID: nasID(cfg.stateDir)})
	if err := csvc.Start(ctx, cfg.stateDir); err != nil {
		return err
	}
	rtcSrv.SetTermHandler(apiH.TermFrame) // canal term (protocol/README.md §12.1)
	startWSD(ctx, cfg)                    // WS-Discovery para Windows (protocol/README.md §14)

	mux := http.NewServeMux()
	mux.Handle("/signal", rtcSrv)
	mux.Handle("/app-icons/", appcatalog.IconHandler())
	mux.Handle("/dav/", apiH.DAVHandler()) // WebDAV (protocol/README.md §12)
	mux.Handle("/dav", http.RedirectHandler("/dav/", http.StatusPermanentRedirect))
	mux.Handle("/", webui.Handler(webui.Bundle()))
	app := securityHeaders(mux)

	var servers []*http.Server
	errc := make(chan error, 2)
	start := func(srv *http.Server, tlsOn bool) {
		servers = append(servers, srv)
		go func() {
			var err error
			if tlsOn {
				err = srv.ListenAndServeTLS("", "")
			} else {
				err = srv.ListenAndServe()
			}
			if !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}

	if cfg.dev {
		slog.Warn("development mode: plain HTTP, do not expose to the network", "addr", "http://"+cfg.devAddr, "version", version)
		start(newServer(cfg.devAddr, app), false)
	} else {
		// El certificado vigente se toma en cada handshake: cert.localca lo cambia sin reiniciar.
		fp := certMgr.Info().Fingerprint
		srv := newServer(cfg.httpsAddr, app)
		srv.TLSConfig = certMgr.TLSConfig()
		appproxy.Default.SetTLS(srv.TLSConfig) // proxies HTTPS del Centro de apps (9100+)
		slog.Info("tangod listening", "https", cfg.httpsAddr, "http", cfg.httpAddr, "ice", cfg.icePort, "stun", cfg.stunPort, "version", version, "cert", fp)
		start(srv, true)
		if cfg.httpAddr != "" {
			start(newServer(cfg.httpAddr, redirectToHTTPS(cfg.httpsAddr)), false)
		}
	}

	select {
	case err := <-errc:
		shutdown(servers)
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdown(servers)
		return nil
	}
}

func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
	}
}

func shutdown(servers []*http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(ctx)
	}
}

// csp: scripts only from the bundle. Inline styles are allowed because
// the desktop positions windows and widgets with style attributes; scripts
// stay strict. connect-src includes wss: for the signaling WebSocket.
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; " +
	"connect-src 'self' wss:; worker-src 'self'; manifest-src 'self'; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp+frameSrc(r.Host))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// redirectToHTTPS sends every plain-HTTP request to the same host on the
// HTTPS port.
func redirectToHTTPS(httpsAddr string) http.Handler {
	_, port, _ := net.SplitHostPort(httpsAddr)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if strings.Contains(host, ":") { // bare IPv6 literal
			host = "[" + host + "]"
		}
		if port != "" && port != "443" {
			host = net.JoinHostPort(strings.Trim(host, "[]"), port)
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusPermanentRedirect)
	})
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// nasID is this NAS's stable identity (hello.nas_id), created on first run.
func nasID(dir string) string {
	p := filepath.Join(dir, "nas-id")
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return strings.TrimSpace(string(b))
	}
	b := make([]byte, 6)
	_, _ = io.ReadFull(rand.Reader, b)
	id := "tg_" + hex.EncodeToString(b)
	_ = os.WriteFile(p, []byte(id+"\n"), 0o600)
	return id
}

func envBool(key string) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes":
		return true
	}
	return false
}
