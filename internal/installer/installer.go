// Package installer es el instalador gráfico de TangoOS (docs/08 §Instalador gráfico).
//
// Corre solo en el sistema live de la ISO (capa installer.squashfs, nunca en el disco
// instalado). Sirve una interfaz web por HTTP que se ve de dos formas:
//
//   - en la pantalla del equipo, con un navegador en modo kiosco (cog) sobre 127.0.0.1;
//   - desde otra PC de la red (como los asistentes web de los NAS comerciales), con un código de 6
//     dígitos que solo se muestra en la pantalla local.
//
// No instala nada por sí mismo: valida lo que elige la persona y llama al motor
// tango-install (el mismo que usa el modo texto) con parámetros, leyendo su progreso.
package installer

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui
var uiFS embed.FS

// Disk es un disco donde se puede instalar (lo decide el motor: nunca el medio de instalación).
type Disk struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Model  string `json:"model"`
	Serial string `json:"serial"`
}

// Network: DHCP (por defecto) o IP fija.
type Network struct {
	Mode    string `json:"mode"` // "dhcp" | "static"
	Address string `json:"address"`
	Gateway string `json:"gateway"`
	DNS     string `json:"dns"`
}

// Request es lo que elige la persona en la interfaz.
type Request struct {
	Disk     string  `json:"disk"`
	Confirm  string  `json:"confirm"` // tiene que repetir Disk: confirma que se borra
	Hostname string  `json:"hostname"`
	Keyboard string  `json:"keyboard"`
	Net      Network `json:"net"`
}

// Phase del instalador.
const (
	PhaseReady      = "ready"
	PhaseInstalling = "installing"
	PhaseDone       = "done"
	PhaseFailed     = "failed"
)

// Keyboards permitidos (XKBLAYOUT).
var Keyboards = []string{"latam", "es", "us", "br", "pt", "gb", "fr", "de", "it"}

var hostRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// Config del servidor.
type Config struct {
	Engine  string // ruta de tango-install
	Version string
	// Lang es el idioma de la pantalla local (el kiosco no informa uno útil: WPE dice "en-US").
	Lang string
	Log  *slog.Logger
	// Command crea los procesos (tests: un motor falso). Por defecto exec.CommandContext.
	Command func(ctx context.Context, name string, args ...string) *exec.Cmd
	// Reboot reinicia el equipo al terminar (por defecto: eject + systemctl reboot).
	Reboot func() error
	// Addrs devuelve las IPv4 de la LAN para mostrar la URL (por defecto: las interfaces).
	Addrs func() []string
}

// Server es el instalador gráfico.
type Server struct {
	cfg Config
	pin string

	mu       sync.Mutex
	sessions map[string]time.Time // cookie → vencimiento (acceso desde la LAN)
	fails    int
	lockedTo time.Time
	phase    string
	pct      int
	msg      string
	errMsg   string
	lines    []string
	hostname string
}

// New crea el servidor con un código de 6 dígitos nuevo.
func New(cfg Config) (*Server, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Command == nil {
		cfg.Command = exec.CommandContext
	}
	if cfg.Addrs == nil {
		cfg.Addrs = lanAddrs
	}
	if cfg.Reboot == nil {
		cfg.Reboot = func() error {
			_ = exec.Command("eject", "/run/live/medium").Run()
			return exec.Command("systemctl", "reboot").Run()
		}
	}
	pin, err := newPIN()
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, pin: pin, sessions: map[string]time.Time{}, phase: PhaseReady}, nil
}

func newPIN() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// PIN es el código de acceso desde la LAN (solo se muestra en la pantalla local).
func (s *Server) PIN() string { return s.pin }

// Handler arma las rutas.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	ui, _ := fs.Sub(uiFS, "ui")
	static := http.FileServer(http.FS(ui))
	mux.Handle("GET /", static)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/unlock", s.post(s.handleUnlock, false))
	mux.HandleFunc("GET /api/disks", s.gate(s.handleDisks))
	mux.HandleFunc("POST /api/install", s.post(s.handleInstall, true))
	mux.HandleFunc("POST /api/reboot", s.post(s.handleReboot, true))
	return securityHeaders(mux)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

// local: la pantalla del propio equipo (el kiosco abre 127.0.0.1).
func isLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.Unmap().IsLoopback()
}

const cookieName = "tango_installer"

func (s *Server) authorized(r *http.Request) bool {
	if isLocal(r) {
		return true
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[c.Value]
	return ok && time.Now().Before(exp)
}

func (s *Server) gate(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeErr(w, http.StatusUnauthorized, "LOCKED", "ingresá el código que se ve en la pantalla del equipo")
			return
		}
		h(w, r)
	}
}

// post: JSON + cabecera propia (un formulario de otro sitio no puede mandarla: sin CSRF).
func (s *Server) post(h http.HandlerFunc, gated bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tango-Installer") != "1" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeErr(w, http.StatusForbidden, "FORBIDDEN", "pedido inválido")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if gated {
			s.gate(h)(w, r)
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}

// State es lo que consulta la interfaz (cada segundo durante la instalación).
type State struct {
	Local    bool     `json:"local"`
	Unlocked bool     `json:"unlocked"`
	PIN      string   `json:"pin,omitempty"` // solo para la pantalla local
	URLs     []string `json:"urls"`
	Version  string   `json:"version"`
	Lang     string   `json:"lang,omitempty"` // solo para la pantalla local
	Phase    string   `json:"phase"`
	Percent  int      `json:"percent"`
	Message  string   `json:"message"`
	Error    string   `json:"error,omitempty"`
	Hostname string   `json:"hostname,omitempty"`
	Log      []string `json:"log,omitempty"`
}

// Snapshot del estado para un pedido (local o de la LAN).
func (s *Server) Snapshot(local, unlocked bool) State {
	urls := []string{}
	for _, a := range s.cfg.Addrs() {
		urls = append(urls, "http://"+a+"/")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{Local: local, Unlocked: unlocked || local, URLs: urls, Version: s.cfg.Version,
		Phase: s.phase, Percent: s.pct, Message: s.msg, Error: s.errMsg, Hostname: s.hostname}
	if local {
		st.PIN, st.Lang = s.pin, s.cfg.Lang
	}
	if st.Unlocked {
		st.Log = slices.Clone(s.lines)
	}
	return st
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Snapshot(isLocal(r), s.authorized(r)))
}

func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	var p struct {
		PIN string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID", "pedido inválido")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if now.Before(s.lockedTo) {
		writeErr(w, http.StatusTooManyRequests, "RATE_LIMITED", fmt.Sprintf("demasiados intentos: esperá %d s", int(s.lockedTo.Sub(now).Seconds())+1))
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(p.PIN)), []byte(s.pin)) != 1 {
		s.fails++
		if s.fails%5 == 0 {
			// 5 errores → 30 s, 10 → 60 s, 15 → 120 s… (un millón de códigos: no se adivina)
			s.lockedTo = now.Add(time.Duration(30<<min(s.fails/5-1, 6)) * time.Second)
		}
		s.cfg.Log.Warn("código incorrecto desde la LAN", "remote", r.RemoteAddr, "fails", s.fails)
		writeErr(w, http.StatusUnauthorized, "BAD_PIN", "el código no coincide")
		return
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	tok := hex.EncodeToString(b)
	s.sessions[tok] = now.Add(12 * time.Hour)
	s.cfg.Log.Info("acceso desde la LAN", "remote", r.RemoteAddr)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 3600})
	writeJSON(w, map[string]bool{"ok": true})
}

// Disks pregunta al motor qué discos son candidatos.
func (s *Server) Disks(ctx context.Context) ([]Disk, error) {
	out, err := s.cfg.Command(ctx, s.cfg.Engine, "--list-disks").Output()
	if err != nil {
		return nil, fmt.Errorf("no pude listar los discos: %w", err)
	}
	return ParseDisks(string(out)), nil
}

// ParseDisks lee el TSV de tango-install --list-disks.
func ParseDisks(out string) []Disk {
	disks := []Disk{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 || !strings.HasPrefix(f[0], "/dev/") {
			continue
		}
		d := Disk{Path: f[0]}
		d.Bytes, _ = strconv.ParseInt(strings.TrimSpace(f[1]), 10, 64)
		if len(f) > 2 {
			d.Model = strings.TrimSpace(f[2])
		}
		if len(f) > 3 {
			d.Serial = strings.TrimSpace(f[3])
		}
		disks = append(disks, d)
	}
	return disks
}

func (s *Server) handleDisks(w http.ResponseWriter, r *http.Request) {
	disks, err := s.Disks(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DISKS", err.Error())
		return
	}
	writeJSON(w, map[string]any{"disks": disks, "keyboards": Keyboards})
}

// Validate revisa la elección y devuelve los argumentos del motor (sin shell: argv).
func Validate(req Request, disks []Disk) ([]string, error) {
	if !slices.ContainsFunc(disks, func(d Disk) bool { return d.Path == req.Disk }) {
		return nil, errors.New("elegí uno de los discos de la lista")
	}
	if req.Confirm != req.Disk {
		return nil, errors.New("confirmá que se borra el disco")
	}
	if !hostRe.MatchString(req.Hostname) {
		return nil, errors.New("el nombre del equipo admite letras, números y guiones (hasta 63)")
	}
	if !slices.Contains(Keyboards, req.Keyboard) {
		return nil, errors.New("distribución de teclado desconocida")
	}
	args := []string{"--disk", req.Disk, "--yes", "--hostname", strings.ToLower(req.Hostname), "--keyboard", req.Keyboard, "--no-reboot", "--progress"}
	switch req.Net.Mode {
	case "dhcp", "":
	case "static":
		pfx, err := netip.ParsePrefix(strings.TrimSpace(req.Net.Address))
		if err != nil || !pfx.Addr().Is4() || pfx.Bits() < 8 || pfx.Bits() > 30 {
			return nil, errors.New("la dirección IP va con su máscara, por ejemplo 192.168.1.50/24")
		}
		gw, err := netip.ParseAddr(strings.TrimSpace(req.Net.Gateway))
		if err != nil || !gw.Is4() || !pfx.Masked().Contains(gw) || gw == pfx.Addr() {
			return nil, errors.New("la puerta de enlace tiene que estar en la misma red que la IP")
		}
		dns := strings.TrimSpace(req.Net.DNS)
		if dns == "" {
			dns = gw.String()
		}
		d, err := netip.ParseAddr(dns)
		if err != nil || !d.Is4() {
			return nil, errors.New("el DNS tiene que ser una dirección IPv4")
		}
		args = append(args, "--ip", pfx.String()+","+gw.String()+","+d.String())
	default:
		return nil, errors.New("modo de red desconocido")
	}
	return args, nil
}

func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	var req Request
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID", "pedido inválido")
		return
	}
	disks, err := s.Disks(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DISKS", err.Error())
		return
	}
	args, err := Validate(req, disks)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID", err.Error())
		return
	}
	if err := s.Start(args, strings.ToLower(req.Hostname)); err != nil {
		writeErr(w, http.StatusConflict, "BUSY", err.Error())
		return
	}
	s.cfg.Log.Info("instalación iniciada", "disk", req.Disk, "remote", r.RemoteAddr)
	writeJSON(w, map[string]bool{"ok": true})
}

// Start lanza el motor una sola vez y sigue su progreso.
func (s *Server) Start(args []string, hostname string) error {
	s.mu.Lock()
	if s.phase != PhaseReady && s.phase != PhaseFailed {
		s.mu.Unlock()
		return errors.New("la instalación ya está en curso o terminó")
	}
	s.phase, s.pct, s.msg, s.errMsg, s.lines, s.hostname = PhaseInstalling, 0, "Preparando…", "", nil, hostname
	s.mu.Unlock()

	cmd := s.cfg.Command(context.Background(), s.cfg.Engine, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return s.finish(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return s.finish(err)
	}
	go func() {
		s.follow(out)
		_ = s.finish(cmd.Wait())
	}()
	return nil
}

// follow interpreta la salida del motor: "@@ <pct> <texto>", "@@ERR <texto>" o log.
func (s *Server) follow(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		s.mu.Lock()
		switch {
		case strings.HasPrefix(line, "@@ERR "):
			s.errMsg = strings.TrimPrefix(line, "@@ERR ")
		case strings.HasPrefix(line, "@@ "):
			f := strings.SplitN(strings.TrimPrefix(line, "@@ "), " ", 2)
			if p, err := strconv.Atoi(f[0]); err == nil && p >= s.pct && p <= 100 {
				s.pct = p
			}
			if len(f) == 2 {
				s.msg = f[1]
			}
		default:
			if t := strings.TrimSpace(line); t != "" {
				s.lines = append(s.lines, t)
				if len(s.lines) > 200 {
					s.lines = s.lines[len(s.lines)-200:]
				}
			}
		}
		s.mu.Unlock()
	}
}

func (s *Server) finish(err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.phase = PhaseFailed
		if s.errMsg == "" {
			s.errMsg = err.Error()
		}
		s.cfg.Log.Error("la instalación falló", "err", s.errMsg)
		return err
	}
	s.phase, s.pct = PhaseDone, 100
	s.cfg.Log.Info("instalación terminada")
	return nil
}

func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	phase := s.phase
	s.mu.Unlock()
	if phase == PhaseInstalling {
		writeErr(w, http.StatusConflict, "BUSY", "esperá a que termine la instalación")
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
	go func() {
		time.Sleep(500 * time.Millisecond) // que la respuesta llegue antes de apagar la red
		if err := s.cfg.Reboot(); err != nil {
			s.cfg.Log.Error("reinicio", "err", err)
		}
	}()
}

// lanAddrs: IPv4 de las interfaces activas (sin loopback).
func lanAddrs() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out = append(out, n.IP.String())
			}
		}
	}
	return out
}
