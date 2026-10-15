package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/agentclient"
	"github.com/lordbasex/go-link-tangoos/internal/audit"
	"github.com/lordbasex/go-link-tangoos/internal/auth"
	"github.com/lordbasex/go-link-tangoos/internal/certs"
	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/notify"
	"github.com/lordbasex/go-link-tangoos/internal/services"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Fase M4 (protocol/README.md §12 y §12.1): primer arranque, resume "el host prueba
// primero", actualizaciones y vuelta atrás, energía, certificado, servicios (SMB, NFS,
// SFTP, WebDAV, iSCSI), notificaciones, auditoría, registro del sistema y terminal.

// M4Agent son las ops del agente que usa M4.
type M4Agent interface {
	Power(ctx context.Context, action string) error
	SetIdentity(ctx context.Context, hostname, tz string) error
	LogsQuery(ctx context.Context, a agentproto.LogsQueryArgs) ([]agentproto.LogLine, error)
	UpdatesCheck(ctx context.Context) (*agentproto.UpdatesCheckResult, error)
	UpdatesApply(ctx context.Context, opID, scope string) error
	RollbackList(ctx context.Context) (*agentproto.RollbackListResult, error)
	RollbackApply(ctx context.Context, opID, id string) error
	ServicesState(ctx context.Context, units ...string) ([]agentproto.ServiceState, error)
	ServiceSet(ctx context.Context, unit string, enabled bool) error
	NFSApply(ctx context.Context, a agentproto.NFSApplyArgs) error
	ISCSIApply(ctx context.Context, a agentproto.ISCSIApplyArgs) error
	ISCSIStatus(ctx context.Context) (*agentproto.ISCSIStatusResult, error)
	M4Job(ctx context.Context, opID string) (*agentclient.JobStatus, error)
	UserEnsure(ctx context.Context, name, full string) error
	UserPassword(ctx context.Context, name, full, password string) error
	TermSpawn(ctx context.Context, cols, rows int, user string) (*os.File, error)
}

// M4Deps son los servicios de M4.
type M4Deps struct {
	Agent   M4Agent
	Certs   *certs.Manager
	Audit   *audit.Log
	Notify  *notify.Service
	Version string
}

// svcConfig es lo que se persiste en services.json (0600: lleva secretos CHAP).
type svcConfig struct {
	NFS struct {
		Enabled bool                              `json:"enabled"`
		Exports map[string][]agentproto.NFSClient `json:"exports"` // Share.ID → clientes
	} `json:"nfs"`
	SFTP struct {
		Enabled bool `json:"enabled"`
		Port    int  `json:"port"`
	} `json:"sftp"`
	WebDAV struct {
		Enabled bool `json:"enabled"`
	} `json:"webdav"`
	ISCSI struct {
		Enabled bool          `json:"enabled"`
		Targets []iscsiConfig `json:"targets"`
	} `json:"iscsi"`
}

type iscsiConfig struct {
	Name       string `json:"name"`
	Volume     string `json:"volume"` // Volume.ID (UUID del Btrfs)
	SizeBytes  int64  `json:"sizeBytes"`
	CHAPUser   string `json:"chapUser,omitempty"`
	CHAPSecret string `json:"chapSecret,omitempty"`
}

type m4API struct {
	a    *API
	d    M4Deps
	sftp *sftpServer
	ups  *upsAPI // UPS por NUT (ups.go, protocol/README.md §13)

	mu  sync.Mutex
	cfg svcConfig
	// opMu encola las ops asíncronas de M4 en el agente (acepta una a la vez): un segundo
	// cambio espera en vez de fallar con BUSY.
	opMu sync.Mutex
}

// queued corre fn con el turno de las ops de M4 del agente; mientras espera lo muestra.
func (m *m4API) queued(h *jobs.Handle, fn func() error) error {
	if !m.opMu.TryLock() {
		if h != nil {
			h.Update(0, "En cola: esperando otra operación del sistema")
		}
		m.opMu.Lock()
	}
	defer m.opMu.Unlock()
	return fn()
}

// RegisterM4 registra los métodos de M4. Llamarlo después de Register.
func (a *API) RegisterM4(r *rtcrpc.Router, d M4Deps) *m4API {
	m := &m4API{a: a, d: d, sftp: &sftpServer{a: a}}
	m.cfg.SFTP.Port = 2222
	m.cfg.NFS.Exports = map[string][]agentproto.NFSClient{}
	if b, err := os.ReadFile(m.cfgPath()); err == nil {
		_ = json.Unmarshal(b, &m.cfg)
	}
	if m.cfg.NFS.Exports == nil {
		m.cfg.NFS.Exports = map[string][]agentproto.NFSClient{}
	}
	a.m4 = m

	// Auditoría: toda llamada que modifica algo y todo login (§12).
	if d.Audit != nil {
		d.Audit.OnEvent = func(e audit.Event) { m.publishAdmins("audit.event", e) }
		r.OnCall = func(s *rtcrpc.Session, method string, params json.RawMessage, err error) {
			var re *rtcrpc.Error
			denied := errors.As(err, &re) && (re.Code == rtcrpc.CodeUnauthenticated || re.Code == codePermissionDenied || re.Code == rtcrpc.CodeInvalidCredentials || re.Code == rtcrpc.CodeRateLimited)
			if !audit.ShouldRecord(method, denied) {
				return
			}
			result := "ok"
			switch {
			case denied:
				result = "denied"
			case err != nil:
				result = "error"
			}
			actor := s.User()
			if prev, ok := s.Get("audit.actor").(string); ok && actor == "" {
				actor = prev
				s.Set("audit.actor", nil)
			}
			if method == "auth.login" || actor == "" {
				if u := audit.LoginActor(params); u != "" {
					actor = u
				}
			}
			ev := audit.Event{Actor: actor, IP: s.RemoteIP, Client: s.Client, Method: method, Target: audit.Target(params), Result: result}
			d.Audit.Record(ev)
		}
	}
	// Avisos → canales externos.
	if d.Notify != nil {
		a.onNotice = func(n Notice) {
			go d.Notify.Dispatch(context.Background(), notify.Message{Level: n.Level, Title: n.Title, Body: n.Body}, func(msg string, kv ...any) { a.d.Log.Warn(msg, kv...) })
		}
	}

	m.registerSetup(r)
	m.registerSystem(r)
	m.registerServices(r)
	m.registerNotify(r)
	m.ups = m.registerUPS(r)
	m.registerCluster(r) // clúster GlusterFS (cluster.go, protocol/README.md §16)
	m.registerTicket(r)
	a.term = a.registerTerm(r, d.Agent)

	go m.restore()
	return m
}

func (m *m4API) cfgPath() string { return filepath.Join(m.a.d.StateDir, "services.json") }

func (m *m4API) save() error {
	b, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.cfgPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.cfgPath())
}

// audit registra un evento de un servicio que no pasa por el router (WebDAV, SFTP).
func (m *m4API) audit(actor, ip, client, method, target, result string) {
	if m.d.Audit != nil {
		m.d.Audit.Record(audit.Event{Actor: actor, IP: ip, Client: client, Method: method, Target: target, Result: result})
	}
}

// publishAdmins manda un evento solo a sesiones de administradores.
func (m *m4API) publishAdmins(topic string, data any) {
	for _, s := range m.a.d.Hub.Sessions() {
		if u, ok := m.a.d.Auth.Get(s.User()); ok && u.Admin {
			s.Emit(topic, data)
		}
	}
}

func (m *m4API) serviceOn(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch id {
	case "webdav":
		return m.cfg.WebDAV.Enabled
	case "sftp":
		return m.cfg.SFTP.Enabled
	}
	return false
}

// restore reaplica al arrancar lo que no persiste solo (SFTP propio, iSCSI en configfs).
func (m *m4API) restore() {
	time.Sleep(3 * time.Second)
	m.mu.Lock()
	sftpOn, port, iscsiOn := m.cfg.SFTP.Enabled, m.cfg.SFTP.Port, m.cfg.ISCSI.Enabled && len(m.cfg.ISCSI.Targets) > 0
	m.mu.Unlock()
	if sftpOn && m.a.files != nil {
		if err := m.sftp.start(port); err != nil {
			m.a.d.Log.Warn("SFTP no arrancó", "err", err)
		}
	}
	if iscsiOn {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := m.applyISCSI(ctx, nil, "restore"); err != nil {
			m.a.d.Log.Warn("iSCSI no se pudo restaurar", "err", err)
		}
	}
}

// follow sigue una op asíncrona de M4 en el agente.
func (m *m4API) follow(ctx context.Context, h *jobs.Handle) error {
	misses := 0
	for {
		st, err := m.d.Agent.M4Job(ctx, h.ID())
		if err != nil {
			if misses++; misses > 60 {
				return errors.New("se perdió el contacto con el agente del sistema")
			}
		} else {
			misses = 0
			switch st.State {
			case "succeeded":
				return nil
			case "failed", "interrupted":
				if st.Error != nil {
					return errors.New(st.Error.Message)
				}
				return errors.New("la operación falló")
			default:
				h.Update(st.Progress, st.Stage)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
	}
}

func typedCheck(typed, want string) error {
	if strings.TrimSpace(typed) != want {
		return rtcrpc.Errorf("CONFIRMATION_MISMATCH", "escribí %s para confirmar", want)
	}
	return nil
}

// ---- Primer arranque y resume seguro ------------------------------------------------------

func (m *m4API) registerSetup(r *rtcrpc.Router) {
	a := m.a
	type setupState struct {
		State             string `json:"state"`
		Hostname          string `json:"hostname"`
		SuggestedHostname string `json:"suggestedHostname"`
	}
	r.Handle("setup.state", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		host, _ := os.Hostname()
		if b, err := os.ReadFile("/etc/hostname"); err == nil && strings.TrimSpace(string(b)) != "" {
			host = strings.TrimSpace(string(b)) // tangod tiene su propio UTS namespace
		}
		st := "done"
		if !a.d.Auth.SetupDone(a.d.StateDir) {
			st = "pending"
		}
		sug := host
		if sug == "" || sug == "localhost" || strings.HasPrefix(sug, "debian") {
			sug = "tango"
		}
		return setupState{State: st, Hostname: host, SuggestedHostname: sug}, nil
	}, rtcrpc.Public())

	type adminP struct {
		Name     string `json:"name"`
		Full     string `json:"full"`
		Password string `json:"password"`
	}
	type completeP struct {
		Hostname    string `json:"hostname"`
		Admin       adminP `json:"admin"`
		Timezone    string `json:"timezone"`
		Certificate string `json:"certificate"`
	}
	type completeR struct {
		User  auth.User `json:"user"`
		Token string    `json:"token"`
	}
	var setupMu sync.Mutex
	r.Handle("setup.complete", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p completeP) (completeR, error) {
		setupMu.Lock()
		defer setupMu.Unlock()
		if a.d.Auth.SetupDone(a.d.StateDir) {
			return completeR{}, rtcrpc.Errorf("SETUP_DONE", "el NAS ya está configurado")
		}
		if err := diskops.ValidUserName(p.Admin.Name); err != nil {
			return completeR{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
		if auth.WeakPassword(p.Admin.Password, p.Admin.Name, p.Admin.Full, p.Hostname) {
			return completeR{}, rtcrpc.Errorf("WEAK_PASSWORD", "la contraseña es débil: usá al menos 10 caracteres y evitá palabras comunes o tu nombre")
		}
		if p.Certificate != "selfsigned" && p.Certificate != "localca" {
			return completeR{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "certificado inválido")
		}
		full := strings.TrimSpace(p.Admin.Full)
		if full == "" {
			full = p.Admin.Name
		}
		// Orden: primero lo que valida (usuario), al final lo que cambia el sistema
		// (nombre y hora), para no dejar cambios a medias si algo falla.
		if err := m.d.Agent.UserEnsure(ctx, p.Admin.Name, full); err != nil {
			return completeR{}, agentErr(err)
		}
		if err := m.d.Agent.UserPassword(ctx, p.Admin.Name, full, p.Admin.Password); err != nil {
			var pe *agentproto.Error
			if !errors.As(err, &pe) || pe.Code != "NOT_INSTALLED" {
				return completeR{}, agentErr(err)
			}
			// Sin Samba no hay cuenta SMB todavía: se crea al cambiar la contraseña con Samba instalado.
			a.d.Log.Warn("primer arranque sin Samba: la cuenta SMB del administrador queda pendiente", "user", p.Admin.Name)
		}
		if err := m.d.Agent.SetIdentity(ctx, p.Hostname, p.Timezone); err != nil {
			return completeR{}, agentErr(err)
		}
		if err := a.d.Auth.ReplaceInitialAdmin(a.d.StateDir, auth.User{Name: p.Admin.Name, Full: full, Groups: []string{"administrators"}, Color: "#2F7CF6"}, p.Admin.Password); err != nil {
			return completeR{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
		}
		if p.Certificate == "localca" && m.d.Certs != nil {
			if err := m.d.Certs.UseLocalCA(); err != nil {
				a.d.Log.Warn("CA local en el primer arranque", "err", err)
			}
		}
		if err := auth.MarkSetupDone(a.d.StateDir); err != nil {
			return completeR{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
		}
		tok, err := a.d.Auth.IssueToken(p.Admin.Name)
		if err != nil {
			return completeR{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
		}
		u, _ := a.d.Auth.Get(p.Admin.Name)
		s.Authenticate(u.Name, tok)
		a.mu.Lock()
		a.notices = slices.DeleteFunc(a.notices, func(n Notice) bool { return n.ID == "first-boot" })
		a.mu.Unlock()
		return completeR{User: u, Token: tok}, nil
	}), rtcrpc.Public())

	// Resume seguro (§12, docs/07 §2): el NAS prueba que conoce el token antes de que el
	// cliente lo revele. tokenId = hex(SHA-256(SHA-256(token)))[:32]; proof = HMAC.
	type challengeP struct {
		TokenID string `json:"tokenId"`
		Nonce   string `json:"nonce"`
	}
	r.Handle("auth.challenge", rtcrpc.Typed(func(_ context.Context, _ *rtcrpc.Session, p challengeP) (map[string]string, error) {
		proof, err := a.d.Auth.ProveToken(p.TokenID, p.Nonce)
		if err != nil {
			return nil, rtcrpc.Errorf(rtcrpc.CodeUnauthenticated, "%s", err.Error())
		}
		return map[string]string{"proof": proof}, nil
	}), rtcrpc.Public())
}

// ---- Sistema: actualizaciones, energía, certificado, auditoría, registro ---------------------

func (m *m4API) registerSystem(r *rtcrpc.Router) {
	a := m.a
	admin := func(s *rtcrpc.Session) error { return a.requireAdmin(s) }

	type updatesR struct {
		TangoOS struct {
			Current   string  `json:"current"`
			Available *string `json:"available"`
		} `json:"tangoos"`
		Debian    []agentproto.PackageUpdate `json:"debian"`
		CheckedAt string                     `json:"checkedAt"`
	}
	r.Handle("system.updates.check", func(ctx context.Context, s *rtcrpc.Session, _ json.RawMessage) (any, error) {
		if err := admin(s); err != nil {
			return nil, err
		}
		res, err := m.d.Agent.UpdatesCheck(ctx)
		if err != nil {
			return nil, agentErr(err)
		}
		var out updatesR
		out.TangoOS.Current = res.TangoOSCurrent
		if out.TangoOS.Current == "" {
			out.TangoOS.Current = m.d.Version
		}
		if res.TangoOSAvailable != "" {
			v := res.TangoOSAvailable
			out.TangoOS.Available = &v
		}
		out.Debian = []agentproto.PackageUpdate{}
		for _, p := range res.Debian {
			if p.Name != "tangoos" {
				out.Debian = append(out.Debian, p)
			}
		}
		out.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		return out, nil
	})
	type scopeP struct {
		Scope string `json:"scope"`
	}
	r.Handle("system.updates.apply", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p scopeP) (map[string]string, error) {
		if err := admin(s); err != nil {
			return nil, err
		}
		if p.Scope != "all" && p.Scope != "security" {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "alcance inválido")
		}
		title := map[string]string{"all": "Actualizando el sistema", "security": "Instalando parches de seguridad"}[p.Scope]
		j := a.d.Jobs.Start("system.updates", title, "Preparando", func(ctx context.Context, h *jobs.Handle) error {
			return m.queued(h, func() error {
				if err := m.d.Agent.UpdatesApply(ctx, h.ID(), p.Scope); err != nil {
					return agentErr(err)
				}
				return m.follow(ctx, h)
			})
		}, func(j jobs.Job) {
			lvl, body := "ok", "El sistema quedó actualizado. Si algo falla, podés volver a la versión anterior desde Ajustes."
			if j.State != jobs.Succeeded {
				lvl, body = "crit", j.Error
			}
			a.Notify(Notice{ID: "upd-" + j.ID, Level: lvl, App: "tango.settings", Title: title, Body: body, Action: &Action{Label: "Abrir", App: "tango.settings", Route: "updates"}})
		})
		return map[string]string{"job": j.ID}, nil
	}))
	r.Handle("system.rollback.list", func(ctx context.Context, s *rtcrpc.Session, _ json.RawMessage) (any, error) {
		if err := admin(s); err != nil {
			return nil, err
		}
		res, err := m.d.Agent.RollbackList(ctx)
		if err != nil {
			return nil, agentErr(err)
		}
		if !res.Supported {
			return nil, rtcrpc.Errorf(agentproto.CodeNotSupported, "%s", res.Reason)
		}
		return nonNil(res.Points), nil
	})
	type rollbackP struct {
		ID    string `json:"id"`
		Typed string `json:"typed"`
	}
	r.Handle("system.rollback.apply", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p rollbackP) (map[string]string, error) {
		if err := admin(s); err != nil {
			return nil, err
		}
		if err := typedCheck(p.Typed, "VOLVER"); err != nil {
			return nil, err
		}
		// Validar antes de crear el job: un punto inexistente (o un sistema sin Btrfs) se
		// rechaza en la respuesta, no como un job fallido (hallazgo e2e 2026-10-14).
		lst, err := m.d.Agent.RollbackList(ctx)
		if err != nil {
			return nil, agentErr(err)
		}
		if !lst.Supported {
			return nil, rtcrpc.Errorf(agentproto.CodeNotSupported, "%s", lst.Reason)
		}
		known := false
		for _, pt := range lst.Points {
			if pt.ID == p.ID && !pt.Current {
				known = true
			}
		}
		if !known {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "no existe el punto de vuelta atrás %q", p.ID)
		}
		j := a.d.Jobs.Start("system.rollback", "Volviendo a la versión anterior", p.ID, func(ctx context.Context, h *jobs.Handle) error {
			return m.queued(h, func() error {
				if err := m.d.Agent.RollbackApply(ctx, h.ID(), p.ID); err != nil {
					return agentErr(err)
				}
				return m.follow(ctx, h)
			})
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))
	type typedP struct {
		Typed string `json:"typed"`
	}
	power := func(action, word string) rtcrpc.HandlerFunc {
		return rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p typedP) (struct{}, error) {
			if err := admin(s); err != nil {
				return struct{}{}, err
			}
			if err := typedCheck(p.Typed, word); err != nil {
				return struct{}{}, err
			}
			if err := m.d.Agent.Power(ctx, action); err != nil {
				return struct{}{}, agentErr(err)
			}
			return struct{}{}, nil
		})
	}
	r.Handle("system.reboot", power("reboot", "REINICIAR"))
	r.Handle("system.shutdown", power("poweroff", "APAGAR"))

	r.Handle("cert.info", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		if m.d.Certs == nil {
			return nil, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "sin TLS (modo desarrollo)")
		}
		return m.d.Certs.Info(), nil
	})
	r.Handle("cert.localca", func(_ context.Context, s *rtcrpc.Session, _ json.RawMessage) (any, error) {
		if err := admin(s); err != nil {
			return nil, err
		}
		if m.d.Certs == nil {
			return nil, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "sin TLS (modo desarrollo)")
		}
		j := a.d.Jobs.Start("cert.localca", "Activando la CA local", "Generando la CA y el certificado del NAS", func(context.Context, *jobs.Handle) error {
			return m.d.Certs.UseLocalCA()
		}, nil)
		return map[string]string{"job": j.ID}, nil
	})
	r.Handle("cert.caDownload", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		if m.d.Certs == nil {
			return nil, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "sin TLS (modo desarrollo)")
		}
		pem, err := m.d.Certs.CAPEM()
		if err != nil {
			return nil, rtcrpc.Errorf("NO_LOCAL_CA", "%s", err.Error())
		}
		return map[string]string{"filename": "tangoos-ca.crt", "pem": pem}, nil
	})

	r.Handle("audit.list", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, q audit.Query) ([]audit.Event, error) {
		if err := admin(s); err != nil {
			return nil, err
		}
		if m.d.Audit == nil {
			return []audit.Event{}, nil
		}
		return m.d.Audit.List(q)
	}))
	r.Handle("logs.query", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, q agentproto.LogsQueryArgs) ([]agentproto.LogLine, error) {
		if err := admin(s); err != nil {
			return nil, err
		}
		l, err := m.d.Agent.LogsQuery(ctx, q)
		if err != nil {
			return nil, agentErr(err)
		}
		return nonNil(l), nil
	}))
}

// ---- Servicios de archivos -----------------------------------------------------------------

// ServiceInfo es exactamente el ServiceInfo del cliente.
type ServiceInfo struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
	Port    *int   `json:"port"`
	Detail  string `json:"detail"`
}

// IscsiTarget es exactamente el IscsiTarget del cliente.
type IscsiTarget struct {
	Name      string `json:"name"`
	IQN       string `json:"iqn"`
	Volume    string `json:"volume"`
	SizeBytes int64  `json:"sizeBytes"`
	CHAP      bool   `json:"chap"`
	Sessions  int    `json:"sessions"`
}

func intp(n int) *int { return &n }

func (m *m4API) volumes(ctx context.Context) []storage.Volume {
	vols, _ := m.a.d.Inventory.Volumes(ctx)
	if m.a.storage != nil {
		vols = m.a.storage.decorateVolumes(vols)
	}
	return vols
}

func (m *m4API) shareByID(ctx context.Context, id string) (Share, bool) {
	if m.a.storage == nil {
		return Share{}, false
	}
	for _, sh := range m.a.storage.shares(ctx) {
		if sh.ID == id || strings.EqualFold(sh.Name, id) {
			return sh, true
		}
	}
	return Share{}, false
}

func (m *m4API) registerServices(r *rtcrpc.Router) {
	a := m.a
	admin := func(s *rtcrpc.Session) error { return a.requireAdmin(s) }

	r.Handle("services.list", func(ctx context.Context, _ *rtcrpc.Session, _ json.RawMessage) (any, error) {
		st, _ := m.d.Agent.ServicesState(ctx, "smbd.service", "nfs-server.service")
		state := map[string]agentproto.ServiceState{}
		for _, u := range st {
			state[u.Unit] = u
		}
		m.mu.Lock()
		cfg := m.cfg
		m.mu.Unlock()
		smb := state["smbd.service"]
		nfs := state["nfs-server.service"]
		smbDetail := map[bool]string{true: "Samba (SMB 3) para Windows, macOS y Linux", false: "Samba no está instalado"}[smb.Installed]
		if a.storage != nil && smb.Installed {
			switch n := a.storage.tmCount(ctx); n {
			case 0:
			case 1:
				smbDetail += " · Time Machine: 1 carpeta"
			default:
				smbDetail += " · Time Machine: " + strconv.Itoa(n) + " carpetas"
			}
		}
		out := []ServiceInfo{
			{ID: "smb", Enabled: smb.Enabled, Running: smb.Active, Port: intp(445), Detail: smbDetail},
			{ID: "nfs", Enabled: cfg.NFS.Enabled, Running: nfs.Active, Port: intp(2049), Detail: map[bool]string{true: "NFS v4 (nfs-kernel-server)", false: "Se instala nfs-kernel-server al activarlo"}[nfs.Installed]},
			{ID: "sftp", Enabled: cfg.SFTP.Enabled, Running: cfg.SFTP.Enabled && m.sftp.running(), Port: intp(cfg.SFTP.Port), Detail: "SFTP de TangoOS: solo archivos, sin shell"},
			{ID: "webdav", Enabled: cfg.WebDAV.Enabled, Running: cfg.WebDAV.Enabled, Port: intp(443), Detail: "WebDAV en https://<nas>/dav/"},
		}
		ist, _ := m.d.Agent.ISCSIStatus(ctx)
		running := ist != nil && ist.Loaded && cfg.ISCSI.Enabled
		out = append(out, ServiceInfo{ID: "iscsi", Enabled: cfg.ISCSI.Enabled, Running: running, Port: intp(3260), Detail: "iSCSI (LIO) con LUN en archivos sin copia en escritura"})
		if m.ups != nil {
			out = append(out, m.ups.serviceInfo())
		}
		return out, nil
	})

	type setP struct {
		ID      string         `json:"id"`
		Enabled bool           `json:"enabled"`
		Options map[string]any `json:"options,omitempty"`
	}
	r.Handle("services.set", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p setP) (map[string]string, error) {
		if err := admin(s); err != nil {
			return nil, err
		}
		names := map[string]string{"smb": "SMB", "nfs": "NFS", "sftp": "SFTP", "webdav": "WebDAV", "iscsi": "iSCSI"}
		label, ok := names[p.ID]
		if !ok {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "servicio desconocido")
		}
		verb := map[bool]string{true: "Activando ", false: "Desactivando "}[p.Enabled]
		j := a.d.Jobs.Start("services.set", verb+label, "", func(ctx context.Context, h *jobs.Handle) error {
			defer a.d.Hub.Publish("services.changed", map[string]string{"id": p.ID})
			switch p.ID {
			case "smb":
				for _, u := range []string{"smbd.service", "nmbd.service"} {
					if err := m.d.Agent.ServiceSet(ctx, u, p.Enabled); err != nil && u == "smbd.service" {
						return agentErr(err)
					}
				}
				return nil
			case "nfs":
				m.mu.Lock()
				m.cfg.NFS.Enabled = p.Enabled
				_ = m.save()
				m.mu.Unlock()
				return m.applyNFS(ctx, h)
			case "sftp":
				port := 2222
				if v, ok := p.Options["port"].(float64); ok && v >= 1024 && v <= 65535 {
					port = int(v)
				}
				m.mu.Lock()
				m.cfg.SFTP.Enabled, m.cfg.SFTP.Port = p.Enabled, port
				_ = m.save()
				m.mu.Unlock()
				if p.Enabled {
					return m.sftp.start(port)
				}
				m.sftp.stop()
				return nil
			case "webdav":
				m.mu.Lock()
				m.cfg.WebDAV.Enabled = p.Enabled
				err := m.save()
				m.mu.Unlock()
				return err
			case "iscsi":
				m.mu.Lock()
				m.cfg.ISCSI.Enabled = p.Enabled
				_ = m.save()
				m.mu.Unlock()
				_, err := m.applyISCSI(ctx, h, "")
				return err
			}
			return nil
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))

	type shareP struct {
		Share string `json:"share"`
	}
	r.Handle("nfs.exports.get", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p shareP) (map[string][]agentproto.NFSClient, error) {
		if err := admin(s); err != nil {
			return nil, err
		}
		sh, ok := m.shareByID(ctx, p.Share)
		if !ok {
			return nil, rtcrpc.Errorf("SHARE_NOT_FOUND", "no existe la carpeta compartida")
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		return map[string][]agentproto.NFSClient{"clients": nonNil(m.cfg.NFS.Exports[sh.ID])}, nil
	}))
	type exportsP struct {
		Share   string                 `json:"share"`
		Clients []agentproto.NFSClient `json:"clients"`
	}
	r.Handle("nfs.exports.set", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p exportsP) (struct{}, error) {
		if err := admin(s); err != nil {
			return struct{}{}, err
		}
		sh, ok := m.shareByID(ctx, p.Share)
		if !ok {
			return struct{}{}, rtcrpc.Errorf("SHARE_NOT_FOUND", "no existe la carpeta compartida")
		}
		if _, err := services.RenderExports([]agentproto.NFSExport{{Path: sh.Path, Clients: p.Clients}}); err != nil {
			return struct{}{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
		m.mu.Lock()
		if len(p.Clients) == 0 {
			delete(m.cfg.NFS.Exports, sh.ID)
		} else {
			m.cfg.NFS.Exports[sh.ID] = p.Clients
		}
		err := m.save()
		on := m.cfg.NFS.Enabled
		m.mu.Unlock()
		if err != nil {
			return struct{}{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
		}
		if on {
			a.d.Jobs.Start("nfs.exports", "Actualizando el acceso NFS", sh.Name, func(ctx context.Context, h *jobs.Handle) error {
				return m.applyNFS(ctx, h)
			}, nil)
		}
		a.d.Hub.Publish("services.changed", map[string]string{"id": "nfs"})
		return struct{}{}, nil
	}))

	r.Handle("iscsi.targets.list", func(ctx context.Context, s *rtcrpc.Session, _ json.RawMessage) (any, error) {
		m.mu.Lock()
		cfg := append([]iscsiConfig(nil), m.cfg.ISCSI.Targets...)
		m.mu.Unlock()
		ist, _ := m.d.Agent.ISCSIStatus(ctx)
		out := []IscsiTarget{}
		for _, t := range cfg {
			n := 0
			if ist != nil {
				n = ist.Sessions[services.IQN(t.Name)]
			}
			out = append(out, IscsiTarget{Name: t.Name, IQN: services.IQN(t.Name), Volume: t.Volume, SizeBytes: t.SizeBytes, CHAP: t.CHAPUser != "", Sessions: n})
		}
		return out, nil
	})
	type chapP struct {
		User   string `json:"user"`
		Secret string `json:"secret"`
	}
	type createP struct {
		Name      string `json:"name"`
		Volume    string `json:"volume"`
		SizeBytes int64  `json:"sizeBytes"`
		CHAP      *chapP `json:"chap,omitempty"`
	}
	r.Handle("iscsi.targets.create", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p createP) (map[string]string, error) {
		if err := admin(s); err != nil {
			return nil, err
		}
		if err := services.ValidTargetName(p.Name); err != nil {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
		c := iscsiConfig{Name: p.Name, SizeBytes: p.SizeBytes}
		if p.CHAP != nil && (p.CHAP.User != "" || p.CHAP.Secret != "") {
			if err := services.ValidCHAP(p.CHAP.User, p.CHAP.Secret); err != nil {
				return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
			}
			c.CHAPUser, c.CHAPSecret = p.CHAP.User, p.CHAP.Secret
		}
		var vol *storage.Volume
		for _, v := range m.volumes(ctx) {
			if v.ID == p.Volume || v.Name == p.Volume {
				v := v
				vol = &v
			}
		}
		if vol == nil || vol.Mount == "" {
			return nil, rtcrpc.Errorf("VOLUME_NOT_FOUND", "elegí un volumen montado")
		}
		c.Volume = vol.ID
		free := int64(vol.Size) - int64(vol.Used)
		if p.SizeBytes > free {
			return nil, rtcrpc.Errorf("NO_SPACE", "no entra en %s: quedan %d bytes libres", vol.Name, free)
		}
		if err := services.ValidateTarget(agentproto.ISCSITarget{Name: c.Name, IQN: services.IQN(c.Name), File: services.LUNFile(vol.Mount, c.Name), SizeBytes: c.SizeBytes, CHAPUser: c.CHAPUser, CHAPSecret: c.CHAPSecret}); err != nil {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
		m.mu.Lock()
		for _, t := range m.cfg.ISCSI.Targets {
			if t.Name == c.Name {
				m.mu.Unlock()
				return nil, rtcrpc.Errorf("TARGET_EXISTS", "ya existe el destino %s", c.Name)
			}
		}
		m.cfg.ISCSI.Targets = append(m.cfg.ISCSI.Targets, c)
		m.cfg.ISCSI.Enabled = true
		_ = m.save()
		m.mu.Unlock()
		j := a.d.Jobs.Start("iscsi.create", "Creando el destino iSCSI "+c.Name, "", func(ctx context.Context, h *jobs.Handle) error {
			defer a.d.Hub.Publish("services.changed", map[string]string{"id": "iscsi"})
			if _, err := m.applyISCSI(ctx, h, ""); err != nil {
				m.mu.Lock() // no dejar en la config un destino que no se pudo crear
				m.cfg.ISCSI.Targets = slices.DeleteFunc(m.cfg.ISCSI.Targets, func(t iscsiConfig) bool { return t.Name == c.Name })
				_ = m.save()
				m.mu.Unlock()
				return err
			}
			return nil
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))
	type deleteP struct {
		Name  string `json:"name"`
		Typed string `json:"typed"`
	}
	r.Handle("iscsi.targets.delete", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p deleteP) (map[string]string, error) {
		if err := admin(s); err != nil {
			return nil, err
		}
		if err := typedCheck(p.Typed, p.Name); err != nil {
			return nil, err
		}
		m.mu.Lock()
		var gone *iscsiConfig
		for i, t := range m.cfg.ISCSI.Targets {
			if t.Name == p.Name {
				t := t
				gone = &t
				m.cfg.ISCSI.Targets = append(m.cfg.ISCSI.Targets[:i], m.cfg.ISCSI.Targets[i+1:]...)
				break
			}
		}
		_ = m.save()
		m.mu.Unlock()
		if gone == nil {
			return nil, rtcrpc.Errorf("TARGET_NOT_FOUND", "no existe el destino %s", p.Name)
		}
		mount := ""
		for _, v := range m.volumes(ctx) {
			if v.ID == gone.Volume {
				mount = v.Mount
			}
		}
		j := a.d.Jobs.Start("iscsi.delete", "Eliminando el destino iSCSI "+p.Name, "", func(ctx context.Context, h *jobs.Handle) error {
			defer a.d.Hub.Publish("services.changed", map[string]string{"id": "iscsi"})
			del := []string{}
			if mount != "" {
				del = append(del, services.LUNFile(mount, gone.Name))
			}
			_, err := m.applyISCSIWith(ctx, h, del)
			return err
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))
}

func (s *sftpServer) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ln != nil
}

func (m *m4API) applyNFS(ctx context.Context, h *jobs.Handle) error {
	return m.queued(h, func() error { return m.applyNFSNow(ctx, h) })
}

func (m *m4API) applyNFSNow(ctx context.Context, h *jobs.Handle) error {
	m.mu.Lock()
	on := m.cfg.NFS.Enabled
	exp := map[string][]agentproto.NFSClient{}
	for k, v := range m.cfg.NFS.Exports {
		exp[k] = v
	}
	m.mu.Unlock()
	args := agentproto.NFSApplyArgs{OpID: h.ID(), Enabled: on, Exports: []agentproto.NFSExport{}}
	for id, clients := range exp {
		if sh, ok := m.shareByID(ctx, id); ok {
			args.Exports = append(args.Exports, agentproto.NFSExport{Path: sh.Path, Clients: clients})
		}
	}
	if err := m.d.Agent.NFSApply(ctx, args); err != nil {
		return agentErr(err)
	}
	return m.follow(ctx, h)
}

func (m *m4API) applyISCSI(ctx context.Context, h *jobs.Handle, _ string) (struct{}, error) {
	return m.applyISCSIWith(ctx, h, nil)
}

func (m *m4API) applyISCSIWith(ctx context.Context, h *jobs.Handle, del []string) (struct{}, error) {
	var err error
	_ = m.queued(h, func() error {
		_, err = m.applyISCSINow(ctx, h, del)
		return err
	})
	return struct{}{}, err
}

func (m *m4API) applyISCSINow(ctx context.Context, h *jobs.Handle, del []string) (struct{}, error) {
	m.mu.Lock()
	on := m.cfg.ISCSI.Enabled
	cfg := append([]iscsiConfig(nil), m.cfg.ISCSI.Targets...)
	m.mu.Unlock()
	vols := m.volumes(ctx)
	targets := []agentproto.ISCSITarget{}
	for _, t := range cfg {
		for _, v := range vols {
			if v.ID == t.Volume && v.Mount != "" {
				targets = append(targets, agentproto.ISCSITarget{Name: t.Name, IQN: services.IQN(t.Name), File: services.LUNFile(v.Mount, t.Name), SizeBytes: t.SizeBytes, CHAPUser: t.CHAPUser, CHAPSecret: t.CHAPSecret})
			}
		}
	}
	if h == nil { // restauración al arrancar: job propio sin mostrarlo como acción del usuario
		var err error
		done := make(chan struct{})
		m.a.d.Jobs.Start("iscsi.restore", "Restaurando los destinos iSCSI", "", func(ctx context.Context, jh *jobs.Handle) error {
			defer close(done)
			if e := m.d.Agent.ISCSIApply(ctx, agentproto.ISCSIApplyArgs{OpID: jh.ID(), Enabled: on, Targets: targets, Delete: del}); e != nil {
				err = agentErr(e)
				return err
			}
			err = m.follow(ctx, jh)
			return err
		}, nil)
		<-done
		return struct{}{}, err
	}
	if err := m.d.Agent.ISCSIApply(ctx, agentproto.ISCSIApplyArgs{OpID: h.ID(), Enabled: on, Targets: targets, Delete: del}); err != nil {
		return struct{}{}, agentErr(err)
	}
	return struct{}{}, m.follow(ctx, h)
}

// ---- Notificaciones ---------------------------------------------------------------

func (m *m4API) registerNotify(r *rtcrpc.Router) {
	a := m.a
	r.Handle("notify.channels.get", func(_ context.Context, s *rtcrpc.Session, _ json.RawMessage) (any, error) {
		if err := a.requireAdmin(s); err != nil {
			return nil, err
		}
		if m.d.Notify == nil {
			return nil, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "sin notificaciones")
		}
		return m.d.Notify.Get(), nil
	})
	r.Handle("notify.channels.set", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, c notify.Channels) (struct{}, error) {
		if err := a.requireAdmin(s); err != nil {
			return struct{}{}, err
		}
		if err := m.d.Notify.Set(c); err != nil {
			return struct{}{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
		return struct{}{}, nil
	}))
	type testP struct {
		Channel string `json:"channel"`
	}
	r.Handle("notify.test", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p testP) (struct{}, error) {
		if err := a.requireAdmin(s); err != nil {
			return struct{}{}, err
		}
		if err := m.d.Notify.Test(ctx, p.Channel); err != nil {
			return struct{}{}, rtcrpc.Errorf("NOTIFY_FAILED", "no se pudo enviar: %v", err)
		}
		return struct{}{}, nil
	}))
}
