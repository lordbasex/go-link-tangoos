package connect

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/lordbasex/go-link-tangoos/internal/auth"
	"github.com/lordbasex/go-link-tangoos/internal/connect/signal"
	"github.com/lordbasex/go-link-tangoos/internal/rtc"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Estados de una sesión remota (clave "connect.state" en la sesión rtcrpc).
const (
	StatePaired   = "paired"   // llegó con el código de 9 dígitos (claim): puede vincularse
	StateUnlinked = "unlinked" // llegó con reach: todavía no probó nada
	StateLinked   = "linked"   // presentó un token de vínculo válido: puede iniciar sesión
	StateGuest    = "guest"    // abrió un enlace público
)

// Códigos de error propios (protocol/README.md §15).
const (
	CodeLinkNotFound   = "LINK_NOT_FOUND"
	CodeLinkInvalid    = "LINK_INVALID"
	CodeShareNotFound  = "SHARE_LINK_NOT_FOUND"
	CodeShareExpired   = "SHARE_LINK_EXPIRED"
	CodeShareLocked    = "SHARE_LINK_LOCKED"
	CodeNotPaired      = "NOT_PAIRED"
	CodeConnectOff     = "CONNECT_DISABLED"
	CodeForbiddenState = "FORBIDDEN"
)

// Deps son los servicios que usa TangoConnect.
type Deps struct {
	Store    *Store
	RTC      *rtc.Server
	Log      *slog.Logger
	Version  string
	OnChange func() // estado/links cambiaron (evento connect.changed)
}

// Service mantiene la conexión saliente a signalhub y las PCs remotas.
type Service struct {
	d Deps

	mu          sync.Mutex
	cancel      context.CancelFunc
	client      *signal.Client
	connected   bool
	lastErr     string
	ice         []webrtc.ICEServer
	fingerprint string
	remotes     map[string]*remote // por peer_id de signalhub (ruteo de señalización)
	live        []*remote          // todas las PCs remotas hasta que se cierran
	codeWait    chan codeOrErr
	code        string
	codeExp     time.Time
	baseCtx     context.Context
}

type remote struct {
	peerID string
	rp     *rtc.RemotePeer
	since  time.Time
	fails  int
}

type codeOrErr struct {
	code string
	err  error
}

// New arma el servicio (no conecta todavía: Start).
func New(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Service{d: d, remotes: map[string]*remote{}}
}

// Start prepara la API remota (certificado DTLS persistente en dir) y conecta si
// está habilitado.
func (sv *Service) Start(ctx context.Context, dir string) error {
	fp, err := sv.d.RTC.EnableRemote(dir)
	if err != nil {
		return err
	}
	sv.mu.Lock()
	sv.fingerprint = fp
	sv.baseCtx = ctx
	sv.mu.Unlock()
	if sv.d.Store.Get().Enabled {
		sv.startLocked()
	}
	go sv.prune(ctx)
	return nil
}

// Fingerprint es el fingerprint DTLS de las PCs remotas (pinning en el portal).
func (sv *Service) Fingerprint() string {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	return sv.fingerprint
}

func (sv *Service) startLocked() {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.cancel != nil || sv.baseCtx == nil {
		return
	}
	c := sv.d.Store.Get()
	ctx, cancel := context.WithCancel(sv.baseCtx)
	sv.cancel = cancel
	sv.client = signal.New(signal.Config{
		URL:    c.SignalURL,
		Logger: sv.d.Log.With("component", "connect"),
		Header: nil,
		// Renovar credenciales TURN solo sin navegadores en vuelo de señalización.
		Renewable: func() bool {
			sv.mu.Lock()
			defer sv.mu.Unlock()
			return len(sv.remotes) == 0
		},
	}, sv)
	client := sv.client
	go func() {
		err := client.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			sv.d.Log.Warn("connect: señalización detenida", "err", err)
			sv.mu.Lock()
			sv.lastErr = err.Error()
			sv.mu.Unlock()
		}
	}()
}

func (sv *Service) stop() {
	sv.mu.Lock()
	cancel := sv.cancel
	sv.cancel, sv.client, sv.connected = nil, nil, false
	rs := sv.live
	sv.live = nil
	sv.remotes = map[string]*remote{}
	sv.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, r := range rs {
		r.rp.Close("TangoConnect desactivado")
	}
}

// SetConfig cambia enabled/URLs y reconecta.
func (sv *Service) SetConfig(enabled bool, signalURL, portalURL string) error {
	if signalURL != "" {
		if _, err := signal.CheckURL(signalURL); err != nil {
			return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "URL de señalización inválida: %v", err)
		}
	}
	if portalURL != "" {
		u, err := url.Parse(portalURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "URL del portal inválida")
		}
	}
	err := sv.d.Store.Update(func(c *Config) error {
		c.Enabled = enabled
		if signalURL != "" {
			c.SignalURL = signalURL
		}
		if portalURL != "" {
			c.PortalURL = strings.TrimRight(portalURL, "/")
		}
		return nil
	})
	if err != nil {
		return err
	}
	sv.stop()
	if enabled {
		sv.startLocked()
	}
	sv.changed()
	return nil
}

func (sv *Service) changed() {
	if sv.d.OnChange != nil {
		sv.d.OnChange()
	}
}

// Status es connect.status.
type Status struct {
	Enabled     bool   `json:"enabled"`
	Connected   bool   `json:"connected"`
	Signal      string `json:"signal"`
	Portal      string `json:"portal"`
	DeviceID    string `json:"deviceId"`
	Fingerprint string `json:"fingerprint"`
	RelayOnly   bool   `json:"relayOnly"`
	HasTURN     bool   `json:"hasTurn"`
	Remotes     int    `json:"remotes"`
	LastError   string `json:"lastError,omitempty"`
}

// Status describe la conexión a signalhub.
func (sv *Service) Status() Status {
	c := sv.d.Store.Get()
	sv.mu.Lock()
	defer sv.mu.Unlock()
	turn := false
	for _, s := range sv.ice {
		for _, u := range s.URLs {
			if strings.HasPrefix(u, "turn") {
				turn = true
			}
		}
	}
	n := 0
	for _, r := range sv.live {
		if !r.rp.Closed() {
			n++
		}
	}
	return Status{
		Enabled: c.Enabled, Connected: sv.connected, Signal: c.SignalURL, Portal: c.PortalURL,
		DeviceID: c.DeviceID, Fingerprint: sv.fingerprint, HasTURN: turn, Remotes: n, LastError: sv.lastErr,
	}
}

// ---- signal.Handler ------------------------------------------------------------------

// OnConnect: llegó el hello de signalhub; se registra como dueño de su device_id.
func (sv *Service) OnConnect(hello signal.Envelope) {
	ice := make([]webrtc.ICEServer, 0, len(hello.ICEServers))
	for _, s := range hello.ICEServers {
		ice = append(ice, webrtc.ICEServer{URLs: s.URLs, Username: s.Username, Credential: s.Credential})
	}
	c := sv.d.Store.Get()
	sv.mu.Lock()
	sv.connected, sv.lastErr, sv.ice = true, "", ice
	client := sv.client
	sv.mu.Unlock()
	if client != nil {
		_ = client.Send(signal.Envelope{Type: signal.TypeRegister, App: App, DeviceID: c.DeviceID, DeviceSecret: c.DeviceSecret})
	}
	sv.d.Log.Info("connect: conectado a signalhub", "signal", c.SignalURL)
	sv.changed()
}

// OnDisconnect: se cayó signalhub. Las PCs ya conectadas siguen.
func (sv *Service) OnDisconnect(err error) {
	sv.mu.Lock()
	was := sv.connected
	sv.connected = false
	if err != nil {
		sv.lastErr = err.Error()
	}
	for id, r := range sv.remotes {
		r.rp.Detach()
		delete(sv.remotes, id)
	}
	sv.mu.Unlock()
	if was {
		sv.changed()
	}
}

// OnMessage atiende un mensaje de signalhub.
func (sv *Service) OnMessage(env signal.Envelope) {
	switch env.Type {
	case signal.TypeCode:
		sv.mu.Lock()
		w := sv.codeWait
		sv.codeWait = nil
		if w != nil {
			sv.code, sv.codeExp = env.Code, time.Now().Add(10*time.Minute)
		}
		sv.mu.Unlock()
		if w != nil {
			w <- codeOrErr{code: env.Code}
		}
	case signal.TypePaired:
		// Solo llega al dueño después de un claim (el navegador escribió el código).
		sv.newRemote(env.Remote, StatePaired)
	case signal.TypeReached:
		sv.newRemote(env.Remote, StateUnlinked)
	case signal.TypeSignal:
		sv.mu.Lock()
		r := sv.remotes[env.From]
		sv.mu.Unlock()
		if r == nil {
			return
		}
		var sp rtc.SignalPayload
		if json.Unmarshal(env.Payload, &sp) == nil {
			r.rp.HandleSignal(sp)
		}
	case signal.TypePeerLeft:
		sv.mu.Lock()
		r := sv.remotes[env.From]
		delete(sv.remotes, env.From)
		sv.mu.Unlock()
		if r != nil {
			r.rp.Detach()
		}
	case signal.TypeError:
		sv.d.Log.Warn("connect: signalhub", "error", env.Error)
		sv.mu.Lock()
		w := sv.codeWait
		sv.codeWait = nil
		sv.lastErr = env.Error
		sv.mu.Unlock()
		if w != nil {
			w <- codeOrErr{err: errors.New(env.Error)}
		}
	}
}

type toPeer struct {
	sv *Service
	to string
}

func (t toPeer) Signal(p rtc.SignalPayload) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	t.sv.mu.Lock()
	c := t.sv.client
	t.sv.mu.Unlock()
	if c == nil {
		return signal.ErrNotConnected
	}
	return c.Send(signal.Envelope{Type: signal.TypeSignal, To: t.to, Payload: b})
}

const (
	maxRemotes       = 32
	handshakeTimeout = 2 * time.Minute
)

func (sv *Service) newRemote(peerID, state string) {
	if peerID == "" {
		return
	}
	sv.mu.Lock()
	ice := append([]webrtc.ICEServer(nil), sv.ice...)
	n := 0
	for _, r := range sv.live {
		if !r.rp.Closed() {
			n++
		}
	}
	sv.mu.Unlock()
	if n >= maxRemotes {
		sv.d.Log.Warn("connect: demasiados navegadores remotos a la vez", "max", maxRemotes)
		return
	}
	rp, err := sv.d.RTC.NewRemotePeer(toPeer{sv: sv, to: peerID}, rtc.RemoteOptions{ICEServers: ice, Client: "remote", Bundle: true})
	if err != nil {
		sv.d.Log.Error("connect: nueva PC remota", "err", err)
		return
	}
	s := rp.Session()
	s.Set("connect.state", state)
	s.Set("connect.peer", peerID)
	r := &remote{peerID: peerID, rp: rp, since: time.Now()}
	sv.mu.Lock()
	sv.remotes[peerID] = r
	sv.live = append(sv.live, r)
	sv.mu.Unlock()
	sv.d.Log.Info("connect: navegador remoto", "state", state, "peer", rp.ID())
}

// prune limpia PCs remotas cerradas cada minuto.
func (sv *Service) prune(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		sv.mu.Lock()
		for id, r := range sv.remotes {
			if r.rp.Closed() {
				delete(sv.remotes, id)
			}
		}
		alive := sv.live[:0]
		var idle []*remote
		for _, r := range sv.live {
			if r.rp.Closed() {
				continue
			}
			// Quien no se vinculó ni abrió un enlace en 2 minutos no tiene nada que hacer acá.
			if st, _ := r.rp.Session().Get("connect.state").(string); (st == StatePaired || st == StateUnlinked) && time.Since(r.since) > handshakeTimeout {
				idle = append(idle, r)
				continue
			}
			alive = append(alive, r)
		}
		defer func() {
			for _, r := range idle {
				r.rp.Close("sin vincularse a tiempo")
			}
		}()
		sv.live = alive
		sv.mu.Unlock()
	}
}

// ---- compuerta -----------------------------------------------------------------------

// Gate decide qué puede llamar cada sesión remota según su estado (rtcrpc.Router.Gate).
// Las sesiones locales (LAN) no tienen estado: solo se les niegan los métodos que
// existen únicamente para remotas.
func (sv *Service) Gate(s *rtcrpc.Session, method string) *rtcrpc.Error {
	st, _ := s.Get("connect.state").(string)
	deny := rtcrpc.Errorf(CodeForbiddenState, "esta conexión no puede usar %s", method)
	remoteOnly := method == "connect.link" || method == "connect.challenge" || method == "connect.resume" ||
		strings.HasPrefix(method, "guest.") || strings.HasPrefix(method, "bundle.")
	if st == "" {
		if remoteOnly {
			return deny
		}
		return nil
	}
	if method == "connect.hello" {
		return nil
	}
	switch st {
	case StatePaired:
		if method == "connect.link" {
			return nil
		}
	case StateUnlinked:
		if method == "connect.challenge" || method == "connect.resume" || method == "guest.open" {
			return nil
		}
	case StateLinked:
		if s.Authenticated() {
			if method == "connect.link" || method == "connect.challenge" || method == "connect.resume" || strings.HasPrefix(method, "guest.") {
				return deny
			}
			return nil
		}
		if strings.HasPrefix(method, "auth.") || method == "system.info" || strings.HasPrefix(method, "bundle.") {
			return nil
		}
		return rtcrpc.Errorf(rtcrpc.CodeUnauthenticated, "iniciá sesión primero")
	case StateGuest:
		if strings.HasPrefix(method, "guest.") && method != "guest.open" {
			return nil
		}
	}
	return deny
}

// ---- vincular navegadores -------------------------------------------------------------

// PairStart pide a signalhub un código nuevo de 9 dígitos (10 min, un uso).
func (sv *Service) PairStart(ctx context.Context) (code string, expires time.Time, err error) {
	c := sv.d.Store.Get()
	if !c.Enabled {
		return "", time.Time{}, rtcrpc.Errorf(CodeConnectOff, "activá TangoConnect primero")
	}
	sv.mu.Lock()
	client, connected := sv.client, sv.connected
	if client == nil || !connected {
		sv.mu.Unlock()
		return "", time.Time{}, rtcrpc.Errorf("SIGNAL_UNAVAILABLE", "el NAS no está conectado al servidor de señalización")
	}
	w := make(chan codeOrErr, 1)
	sv.codeWait = w
	sv.mu.Unlock()
	// Un register repetido reemplaza el código pendiente y mantiene la sesión.
	if err := client.Send(signal.Envelope{Type: signal.TypeRegister, App: App, DeviceID: c.DeviceID, DeviceSecret: c.DeviceSecret}); err != nil {
		return "", time.Time{}, rtcrpc.Errorf("SIGNAL_UNAVAILABLE", "no se pudo pedir el código: %v", err)
	}
	select {
	case r := <-w:
		if r.err != nil {
			return "", time.Time{}, rtcrpc.Errorf("SIGNAL_ERROR", "signalhub: %v", r.err)
		}
		sv.mu.Lock()
		exp := sv.codeExp
		sv.mu.Unlock()
		return r.code, exp, nil
	case <-time.After(10 * time.Second):
		return "", time.Time{}, rtcrpc.Errorf("SIGNAL_UNAVAILABLE", "signalhub no respondió")
	case <-ctx.Done():
		return "", time.Time{}, ctx.Err()
	}
}

// LinkResult es connect.link.
type LinkResult struct {
	LinkID      string `json:"linkId"`
	Token       string `json:"token"`
	DeviceID    string `json:"deviceId"`
	Fingerprint string `json:"fingerprint"`
}

// Link vincula al navegador que llegó con el código (estado paired).
func (sv *Service) Link(s *rtcrpc.Session, label string) (LinkResult, error) {
	if st, _ := s.Get("connect.state").(string); st != StatePaired {
		return LinkResult{}, rtcrpc.Errorf(CodeNotPaired, "este navegador no llegó con un código de vinculación")
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "Navegador"
	}
	if len([]rune(label)) > 64 {
		label = string([]rune(label)[:64])
	}
	token := randB64(32)
	l := Link{ID: newID(), Label: label, TokenHash: hashHex(token), CreatedAt: now(), LastSeen: now(), IP: s.RemoteIP}
	if err := sv.d.Store.Update(func(c *Config) error {
		if len(c.Links) >= 64 {
			return rtcrpc.Errorf(rtcrpc.CodeRateLimited, "hay demasiados navegadores vinculados: revocá alguno")
		}
		c.Links = append(c.Links, l)
		return nil
	}); err != nil {
		return LinkResult{}, err
	}
	s.Set("connect.state", StateLinked)
	s.Set("connect.link", l.ID)
	sv.changed()
	return LinkResult{LinkID: l.ID, Token: token, DeviceID: sv.d.Store.Get().DeviceID, Fingerprint: sv.Fingerprint()}, nil
}

// Challenge: el NAS prueba que conoce el vínculo antes de que el navegador mande el token.
func (sv *Service) Challenge(s *rtcrpc.Session, linkID, nonce string) (string, error) {
	if len(nonce) < 16 || len(nonce) > 128 {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "nonce inválido")
	}
	if sv.tooManyFails(s) {
		return "", rtcrpc.Errorf(rtcrpc.CodeRateLimited, "demasiados intentos")
	}
	l, ok := sv.findLink(linkID)
	if !ok {
		sv.fail(s)
		return "", rtcrpc.Errorf(CodeLinkNotFound, "este navegador ya no está vinculado a este NAS")
	}
	p, _ := LinkProof(l.TokenHash, nonce)
	return p, nil
}

// Resume: el navegador presenta su token; queda linked.
func (sv *Service) Resume(s *rtcrpc.Session, linkID, token string) error {
	if sv.tooManyFails(s) {
		return rtcrpc.Errorf(rtcrpc.CodeRateLimited, "demasiados intentos")
	}
	l, ok := sv.findLink(linkID)
	if !ok || !equalHex(hashHex(token), l.TokenHash) {
		sv.fail(s)
		return rtcrpc.Errorf(CodeLinkInvalid, "el vínculo de este navegador no es válido")
	}
	_ = sv.d.Store.Update(func(c *Config) error {
		for i := range c.Links {
			if c.Links[i].ID == linkID {
				c.Links[i].LastSeen, c.Links[i].IP = now(), s.RemoteIP
			}
		}
		return nil
	})
	s.Set("connect.state", StateLinked)
	s.Set("connect.link", linkID)
	return nil
}

func (sv *Service) findLink(id string) (Link, bool) {
	for _, l := range sv.d.Store.Get().Links {
		if l.ID == id {
			return l, true
		}
	}
	return Link{}, false
}

// Links lista los navegadores vinculados (sin el hash).
func (sv *Service) Links() []Link {
	out := []Link{}
	for _, l := range sv.d.Store.Get().Links {
		l.TokenHash = ""
		out = append(out, l)
	}
	return out
}

// RevokeLink borra un vínculo y cierra las sesiones que lo usan.
func (sv *Service) RevokeLink(id string) error {
	found := false
	if err := sv.d.Store.Update(func(c *Config) error {
		out := c.Links[:0]
		for _, l := range c.Links {
			if l.ID == id {
				found = true
				continue
			}
			out = append(out, l)
		}
		c.Links = out
		return nil
	}); err != nil {
		return err
	}
	if !found {
		return rtcrpc.Errorf(CodeLinkNotFound, "no existe ese vínculo")
	}
	sv.closeWhere(func(s *rtcrpc.Session) bool { return s.Get("connect.link") == id }, "vínculo revocado")
	sv.changed()
	return nil
}

// closeWhere corta las PCs remotas cuya sesión cumple match (aunque ya hayan salido
// de signalhub con peer_left: siguen conectadas por WebRTC).
func (sv *Service) closeWhere(match func(*rtcrpc.Session) bool, reason string) {
	sv.mu.Lock()
	rs := append([]*remote(nil), sv.live...)
	sv.mu.Unlock()
	for _, r := range rs {
		if !r.rp.Closed() && match(r.rp.Session()) {
			r.rp.Close(reason)
		}
	}
}

func (sv *Service) fail(s *rtcrpc.Session) {
	n, _ := s.Get("connect.fails").(int)
	s.Set("connect.fails", n+1)
}

func (sv *Service) tooManyFails(s *rtcrpc.Session) bool {
	n, _ := s.Get("connect.fails").(int)
	return n >= 5
}

// ---- enlaces públicos ------------------------------------------------------------------

// ShareParams son los datos de un enlace nuevo.
type ShareParams struct {
	Owner, Path, Name, Kind string
	Size                    int64
	ExpiresAt               string
	Password                string
	MaxDownloads            int
}

// ShareInfo es un enlace tal como lo ve su dueño.
type ShareInfo struct {
	ID           string `json:"id"`
	URL          string `json:"url"`
	Invite       string `json:"invite"`
	Code         string `json:"code"`
	Owner        string `json:"owner"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Size         int64  `json:"size"`
	CreatedAt    string `json:"createdAt"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	HasPassword  bool   `json:"hasPassword"`
	MaxDownloads int    `json:"maxDownloads,omitempty"`
	Downloads    int    `json:"downloads"`
	Expired      bool   `json:"expired"`
}

// El secreto del enlace va en el fragmento (#…): el portal nunca lo ve en sus logs.
//
//	<portal>/s/<device_id>.<link_id>#<secret>
func (sv *Service) info(l ShareLink, c Config) ShareInfo {
	invite := c.DeviceID + "." + l.ID
	return ShareInfo{
		ID: l.ID, URL: strings.TrimRight(c.PortalURL, "/") + "/s/" + invite + "#" + l.Secret,
		Invite: invite, Owner: l.Owner, Path: l.Path, Name: l.Name, Kind: l.Kind, Size: l.Size,
		CreatedAt: l.CreatedAt, ExpiresAt: l.ExpiresAt, HasPassword: l.PasswordHash != "",
		MaxDownloads: l.MaxDownloads, Downloads: l.Downloads, Expired: l.Expired(time.Now()),
	}
}

// CreateShare crea un enlace público.
func (sv *Service) CreateShare(p ShareParams) (ShareInfo, error) {
	if p.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, p.ExpiresAt)
		if err != nil {
			return ShareInfo{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "fecha de vencimiento inválida (RFC 3339)")
		}
		if !t.After(time.Now()) {
			return ShareInfo{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "la fecha de vencimiento ya pasó")
		}
		p.ExpiresAt = t.UTC().Format(time.RFC3339)
	}
	if p.MaxDownloads < 0 || p.MaxDownloads > 100000 {
		return ShareInfo{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "cantidad de descargas inválida")
	}
	if len(p.Password) > 256 {
		return ShareInfo{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "contraseña demasiado larga")
	}
	l := ShareLink{
		ID: newID(), Owner: p.Owner, Path: p.Path, Name: p.Name, Kind: p.Kind, Size: p.Size,
		Secret: randB64(18), CreatedAt: now(), ExpiresAt: p.ExpiresAt, MaxDownloads: p.MaxDownloads,
	}
	if p.Password != "" {
		l.PasswordHash = auth.HashSecret(p.Password)
	}
	if err := sv.d.Store.Update(func(c *Config) error {
		if len(c.ShareLinks) >= 1000 {
			return rtcrpc.Errorf(rtcrpc.CodeRateLimited, "hay demasiados enlaces: revocá alguno")
		}
		c.ShareLinks = append(c.ShareLinks, l)
		return nil
	}); err != nil {
		return ShareInfo{}, err
	}
	return sv.info(l, sv.d.Store.Get()), nil
}

// Shares lista enlaces: los propios, o todos si all.
func (sv *Service) Shares(owner string, all bool) []ShareInfo {
	c := sv.d.Store.Get()
	out := []ShareInfo{}
	for _, l := range c.ShareLinks {
		if all || l.Owner == owner {
			out = append(out, sv.info(l, c))
		}
	}
	return out
}

// RevokeShare borra un enlace (del dueño, o cualquiera si admin) y corta a sus invitados.
func (sv *Service) RevokeShare(id, owner string, admin bool) error {
	found := false
	if err := sv.d.Store.Update(func(c *Config) error {
		out := c.ShareLinks[:0]
		for _, l := range c.ShareLinks {
			if l.ID == id && (admin || l.Owner == owner) {
				found = true
				continue
			}
			out = append(out, l)
		}
		c.ShareLinks = out
		return nil
	}); err != nil {
		return err
	}
	if !found {
		return rtcrpc.Errorf(CodeShareNotFound, "no existe ese enlace")
	}
	sv.closeWhere(func(s *rtcrpc.Session) bool { return s.Get("guest.link") == id }, "enlace revocado")
	return nil
}

// GuestOpen abre un enlace desde una sesión remota (estado unlinked → guest).
func (sv *Service) GuestOpen(s *rtcrpc.Session, linkID, secret string) (ShareLink, error) {
	if sv.tooManyFails(s) {
		return ShareLink{}, rtcrpc.Errorf(rtcrpc.CodeRateLimited, "demasiados intentos")
	}
	l, ok := sv.findShare(linkID)
	if !ok || !equalHex(secret, l.Secret) {
		sv.fail(s)
		return ShareLink{}, rtcrpc.Errorf(CodeShareNotFound, "este enlace no existe o lo revocaron")
	}
	if l.Expired(time.Now()) {
		return ShareLink{}, rtcrpc.Errorf(CodeShareExpired, "este enlace venció")
	}
	s.Set("connect.state", StateGuest)
	s.Set("guest.link", l.ID)
	s.Set("guest.unlocked", l.PasswordHash == "")
	return l, nil
}

// GuestUnlock verifica la contraseña del enlace (5 intentos por conexión).
func (sv *Service) GuestUnlock(s *rtcrpc.Session, password string) error {
	l, err := sv.guestLink(s, false)
	if err != nil {
		return err
	}
	n, _ := s.Get("guest.tries").(int)
	if n >= 5 {
		return rtcrpc.Errorf(rtcrpc.CodeRateLimited, "demasiados intentos: volvé a abrir el enlace más tarde")
	}
	if l.PasswordHash == "" || auth.VerifySecret(l.PasswordHash, password) {
		s.Set("guest.unlocked", true)
		return nil
	}
	s.Set("guest.tries", n+1)
	return rtcrpc.Errorf(rtcrpc.CodeInvalidCredentials, "contraseña incorrecta")
}

// GuestPeek es el enlace de la sesión invitada aunque todavía pida contraseña
// (para mostrar nombre y tamaño en la página del enlace).
func (sv *Service) GuestPeek(s *rtcrpc.Session) (ShareLink, error) { return sv.guestLink(s, false) }

// GuestLink es el enlace de la sesión invitada (desbloqueado si unlocked).
func (sv *Service) GuestLink(s *rtcrpc.Session) (ShareLink, error) { return sv.guestLink(s, true) }

func (sv *Service) guestLink(s *rtcrpc.Session, needUnlocked bool) (ShareLink, error) {
	id, _ := s.Get("guest.link").(string)
	l, ok := sv.findShare(id)
	if !ok {
		return ShareLink{}, rtcrpc.Errorf(CodeShareNotFound, "este enlace no existe o lo revocaron")
	}
	if l.Expired(time.Now()) {
		return ShareLink{}, rtcrpc.Errorf(CodeShareExpired, "este enlace venció")
	}
	if needUnlocked {
		if ok, _ := s.Get("guest.unlocked").(bool); !ok {
			return ShareLink{}, rtcrpc.Errorf(CodeShareLocked, "este enlace pide contraseña")
		}
	}
	return l, nil
}

// CountDownload suma una descarga (y vuelve a chequear el límite).
func (sv *Service) CountDownload(id string) error {
	return sv.d.Store.Update(func(c *Config) error {
		for i := range c.ShareLinks {
			if c.ShareLinks[i].ID == id {
				if c.ShareLinks[i].Expired(time.Now()) {
					return rtcrpc.Errorf(CodeShareExpired, "este enlace venció")
				}
				c.ShareLinks[i].Downloads++
				return nil
			}
		}
		return rtcrpc.Errorf(CodeShareNotFound, "este enlace no existe o lo revocaron")
	})
}

func (sv *Service) findShare(id string) (ShareLink, bool) {
	for _, l := range sv.d.Store.Get().ShareLinks {
		if l.ID == id {
			return l, true
		}
	}
	return ShareLink{}, false
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }
