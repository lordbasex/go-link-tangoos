package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/agentclient"
	"github.com/lordbasex/go-link-tangoos/internal/appproxy"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
	appcatalog "github.com/lordbasex/go-link-tangoos/system/apps"
)

// Centro de apps (protocol/README.md §9, ADR 0010).

// AppsAgent son las operaciones de tango-agent que usa el Centro de apps.
type AppsAgent interface {
	DockerStatus(ctx context.Context) (*agentproto.DockerStatus, error)
	DockerInstall(ctx context.Context, opID string) error
	AppUp(ctx context.Context, a agentproto.AppUpArgs) error
	AppAction(ctx context.Context, a agentproto.AppActionArgs) error
	AppUninstall(ctx context.Context, a agentproto.AppUninstallArgs) error
	AppLogs(ctx context.Context, id string, tail int) ([]string, error)
	AppStats(ctx context.Context, ids []string) ([]agentproto.AppStat, error)
	AppJob(ctx context.Context, opID string) (*agentclient.JobStatus, error)
}

// InstalledApp es exactamente el InstalledApp de TS.
type InstalledApp struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Icon     string   `json:"icon"`
	Version  string   `json:"version"`
	Volume   string   `json:"volume"`
	DataPath string   `json:"dataPath"`
	State    string   `json:"state"` // installing | running | stopped | updating | error
	URL      *string  `json:"url"`
	CPU      *float64 `json:"cpu"`
	Mem      *uint64  `json:"mem"`
	Error    string   `json:"error,omitempty"`
}

// installed es lo que se persiste de cada app (apps.json, 0600: tiene contraseñas).
type installed struct {
	ID          string            `json:"id"`
	Volume      string            `json:"volume"`
	Mount       string            `json:"mount"`
	Port        int               `json:"port"`
	ProxyPort   int               `json:"proxy_port"`
	Options     map[string]string `json:"options"`
	Version     string            `json:"version"`
	State       string            `json:"state"`
	Error       string            `json:"error,omitempty"`
	InstalledAt string            `json:"installed_at"`
}

type appsAPI struct {
	a     *API
	agent AppsAgent
	jobs  *jobs.Store
	proxy *appproxy.Manager
	path  string
	poll  time.Duration
	tz    string
	// volumes lista los volúmenes (en los tests se reemplaza).
	volumes func(context.Context) ([]storage.Volume, error)

	mu   sync.Mutex
	apps map[string]*installed
}

func (a *API) registerApps(r *rtcrpc.Router, d Deps) {
	ag, ok := d.Agent.(AppsAgent)
	if !ok || d.Jobs == nil {
		return
	}
	s := &appsAPI{a: a, agent: ag, jobs: d.Jobs, proxy: appproxy.Default,
		path: filepath.Join(d.StateDir, "apps.json"), poll: 250 * time.Millisecond, tz: localTZ(), apps: map[string]*installed{}}
	s.volumes = d.Inventory.Volumes
	s.proxy.Log = d.Log
	s.load()

	type idP struct {
		ID string `json:"id"`
	}
	type installP struct {
		ID      string            `json:"id"`
		Volume  string            `json:"volume"`
		Options map[string]string `json:"options"`
	}
	type uninstallP struct {
		ID       string `json:"id"`
		KeepData bool   `json:"keepData"`
	}
	type logsP struct {
		ID   string `json:"id"`
		Tail int    `json:"tail"`
	}
	type jobR struct {
		Job string `json:"job"`
	}
	type dockerR struct {
		Installed bool    `json:"installed"`
		Running   bool    `json:"running"`
		Version   *string `json:"version"`
	}

	r.Handle("apps.docker.status", func(ctx context.Context, _ *rtcrpc.Session, _ json.RawMessage) (any, error) {
		st, err := s.agent.DockerStatus(ctx)
		if err != nil {
			return nil, agentErr(err)
		}
		return dockerR{Installed: st.Installed && st.Compose, Running: st.Running, Version: st.Version}, nil
	})
	r.Handle("apps.docker.install", func(ctx context.Context, sess *rtcrpc.Session, _ json.RawMessage) (any, error) {
		if err := a.requireAdmin(sess); err != nil {
			return nil, err
		}
		id, err := s.installDocker(ctx)
		return jobR{id}, err
	})
	r.Handle("apps.catalog", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		all, err := appcatalog.All()
		if err != nil {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInternal, "catálogo inválido: %v", err)
		}
		return all, nil
	})
	r.Handle("apps.list", func(ctx context.Context, sess *rtcrpc.Session, _ json.RawMessage) (any, error) {
		return s.list(ctx, sessHost(sess)), nil
	})
	r.Handle("apps.install", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p installP) (jobR, error) {
		if err := a.requireAdmin(sess); err != nil {
			return jobR{}, err
		}
		id, err := s.install(ctx, p.ID, p.Volume, p.Options, sessHost(sess))
		return jobR{id}, err
	}))
	for _, act := range []string{"start", "stop", "restart"} {
		act := act
		r.Handle("apps."+act, rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p idP) (jobR, error) {
			if err := a.requireAdmin(sess); err != nil {
				return jobR{}, err
			}
			id, err := s.action(ctx, p.ID, act)
			return jobR{id}, err
		}))
	}
	r.Handle("apps.update", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p idP) (jobR, error) {
		if err := a.requireAdmin(sess); err != nil {
			return jobR{}, err
		}
		id, err := s.update(ctx, p.ID, sessHost(sess))
		return jobR{id}, err
	}))
	r.Handle("apps.uninstall", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p uninstallP) (jobR, error) {
		if err := a.requireAdmin(sess); err != nil {
			return jobR{}, err
		}
		id, err := s.uninstall(ctx, p.ID, p.KeepData)
		return jobR{id}, err
	}))
	r.Handle("apps.logs", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p logsP) (map[string][]string, error) {
		if err := a.requireAdmin(sess); err != nil {
			return nil, err
		}
		if _, ok := s.get(p.ID); !ok {
			return nil, rtcrpc.Errorf("APP_NOT_INSTALLED", "%s no está instalada", p.ID)
		}
		tail := p.Tail
		if tail <= 0 || tail > 2000 {
			tail = 200
		}
		lines, err := s.agent.AppLogs(ctx, p.ID, tail)
		if err != nil {
			return nil, agentErr(err)
		}
		if lines == nil {
			lines = []string{}
		}
		return map[string][]string{"lines": lines}, nil
	}))

	// Al arrancar, los proxies de las apps ya instaladas (se abren cuando main fija el TLS).
	s.mu.Lock()
	for _, it := range s.apps {
		if it.ProxyPort > 0 && it.Port > 0 {
			_ = s.proxy.Ensure(it.ID, it.ProxyPort, it.Port)
		}
	}
	s.mu.Unlock()
}

// ---- Persistencia ------------------------------------------------------------------

func (s *appsAPI) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var list []*installed
	if json.Unmarshal(b, &list) != nil {
		return
	}
	for _, it := range list {
		if appcatalog.ValidID(it.ID) {
			if it.State == "installing" || it.State == "updating" {
				it.State, it.Error = "error", "tangod se reinició durante la operación"
			}
			s.apps[it.ID] = it
		}
	}
}

// saveLocked persiste apps.json (0600). Llamar con s.mu tomado.
func (s *appsAPI) saveLocked() {
	list := make([]*installed, 0, len(s.apps))
	for _, it := range s.apps {
		list = append(list, it)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	b, _ := json.MarshalIndent(list, "", "  ")
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

func (s *appsAPI) get(id string) (installed, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.apps[id]
	if !ok {
		return installed{}, false
	}
	return *it, true
}

func (s *appsAPI) setState(id, state, errMsg string) {
	s.mu.Lock()
	if it, ok := s.apps[id]; ok {
		it.State, it.Error = state, errMsg
		s.saveLocked()
	}
	s.mu.Unlock()
	s.a.d.Hub.Publish("apps.changed", map[string]string{"id": id})
}

// ---- Lista --------------------------------------------------------------------------

func (s *appsAPI) list(ctx context.Context, host string) []InstalledApp {
	s.mu.Lock()
	items := make([]installed, 0, len(s.apps))
	for _, it := range s.apps {
		items = append(items, *it)
	}
	s.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })

	stats := map[string]agentproto.AppStat{}
	if len(items) > 0 {
		ids := make([]string, len(items))
		for i, it := range items {
			ids[i] = it.ID
		}
		sctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		if st, err := s.agent.AppStats(sctx, ids); err == nil {
			for _, x := range st {
				stats[x.ID] = x
			}
		}
		cancel()
	}
	out := make([]InstalledApp, 0, len(items))
	for _, it := range items {
		app, _ := appcatalog.Get(it.ID)
		ia := InstalledApp{ID: it.ID, Name: app.Name, Icon: app.Icon, Version: it.Version, Volume: it.Volume,
			DataPath: filepath.Join(it.Mount, "@apps", it.ID, "data"), State: it.State, Error: it.Error}
		if st, ok := stats[it.ID]; ok && it.State != "installing" && it.State != "updating" {
			switch st.State {
			case "running", "partial":
				ia.State = "running"
				if st.Unhealthy > 0 {
					ia.Error = "Un contenedor no pasa su chequeo de salud"
				}
			case "stopped":
				ia.State = "stopped"
			case "missing":
				if it.State != "error" {
					ia.State = "stopped"
				}
			}
			ia.CPU, ia.Mem = st.CPU, st.Mem
		}
		if it.ProxyPort > 0 && host != "" {
			u := fmt.Sprintf("https://%s:%d/", host, it.ProxyPort)
			ia.URL = &u
		}
		out = append(out, ia)
	}
	return out
}

// ---- Operaciones --------------------------------------------------------------------

func (s *appsAPI) installDocker(ctx context.Context) (string, error) {
	if st, err := s.agent.DockerStatus(ctx); err == nil && st.Installed && st.Compose && st.Running {
		return "", rtcrpc.Errorf("ALREADY_INSTALLED", "Docker ya está instalado")
	}
	j := s.jobs.Start("apps.docker.install", "Instalando Docker", "docker.io y docker-compose de Debian",
		func(ctx context.Context, h *jobs.Handle) error {
			if err := s.agent.DockerInstall(ctx, h.ID()); err != nil {
				return errors.New(agentMessage(err))
			}
			_, err := s.follow(ctx, h)
			return err
		}, func(j jobs.Job) {
			s.a.d.Hub.Publish("apps.changed", map[string]string{"id": ""})
			if j.State == jobs.Succeeded {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.apps", Title: "Docker está listo", Body: "Ya podés instalar apps del catálogo."})
			} else {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.apps", Title: "No se pudo instalar Docker", Body: j.Error})
			}
		})
	return j.ID, nil
}

// follow sigue app.job en el agente y copia cada etapa al job (un jobs.changed por línea).
func (s *appsAPI) follow(ctx context.Context, h *jobs.Handle) (json.RawMessage, error) {
	misses := 0
	lastStage, lastProg := "", -1.0
	for {
		st, err := s.agent.AppJob(ctx, h.ID())
		if err != nil {
			misses++
			if misses > 40 {
				return nil, errors.New("se perdió el contacto con el agente del sistema")
			}
		} else {
			misses = 0
			switch st.State {
			case "succeeded":
				return st.Result, nil
			case "failed", "interrupted":
				if st.Error != nil {
					return nil, errors.New(st.Error.Message)
				}
				return nil, errors.New("la operación falló")
			default:
				if st.Stage != lastStage || st.Progress-lastProg > 0.01 {
					h.Update(st.Progress, st.Stage)
					lastStage, lastProg = st.Stage, st.Progress
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.poll):
		}
	}
}

func (s *appsAPI) volumeMount(ctx context.Context, volID string) (string, string, error) {
	vols, _ := s.volumes(ctx)
	for _, v := range vols {
		if v.ID == volID || v.Name == volID {
			if v.Mount == "" {
				return "", "", rtcrpc.Errorf("VOLUME_NOT_MOUNTED", "%s no está montado", v.Name)
			}
			return v.ID, v.Mount, nil
		}
	}
	return "", "", rtcrpc.Errorf("VOLUME_NOT_FOUND", "No existe el volumen %s", volID)
}

func (s *appsAPI) install(ctx context.Context, id, volID string, opts map[string]string, host string) (string, error) {
	app, ok := appcatalog.Get(id)
	if !ok {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "app desconocida: %s", id)
	}
	if _, ok := s.get(id); ok {
		return "", rtcrpc.Errorf("APP_EXISTS", "%s ya está instalada", app.Name)
	}
	st, err := s.agent.DockerStatus(ctx)
	if err != nil {
		return "", agentErr(err)
	}
	if !st.Installed || !st.Compose || !st.Running {
		return "", rtcrpc.Errorf(agentproto.CodeDockerMissing, "Primero instalá Docker")
	}
	vid, mount, err := s.volumeMount(ctx, volID)
	if err != nil {
		return "", err
	}
	if opts == nil {
		opts = map[string]string{}
	}
	for _, o := range app.Options {
		if o.Generate && opts[o.Key] == "" {
			opts[o.Key] = randomSecret(24)
		}
	}
	if _, err := appcatalog.Resolve(app, opts); err != nil {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	if host == "" {
		host = "localhost"
	}

	s.mu.Lock()
	port := 0
	if app.WebPort != nil {
		port = *app.WebPort
		for _, it := range s.apps {
			if it.Port == port {
				s.mu.Unlock()
				return "", rtcrpc.Errorf("PORT_IN_USE", "El puerto %d ya lo usa %s", port, it.ID)
			}
		}
	}
	proxyPort := 0
	if port > 0 {
		used := map[int]bool{}
		for _, it := range s.apps {
			used[it.ProxyPort] = true
		}
		for p := appproxy.FirstPort; p <= appproxy.LastPort; p++ {
			if !used[p] {
				proxyPort = p
				break
			}
		}
	}
	it := &installed{ID: id, Volume: vid, Mount: mount, Port: port, ProxyPort: proxyPort, Options: opts, Version: app.Version,
		State: "installing", InstalledAt: time.Now().UTC().Format(time.RFC3339)}
	s.apps[id] = it
	s.saveLocked()
	s.mu.Unlock()
	s.a.d.Hub.Publish("apps.changed", map[string]string{"id": id})

	args := agentproto.AppUpArgs{ID: id, Mount: mount, Port: max(port, 1024), TZ: s.tz, Host: host, Options: opts, Pull: true}
	j := s.jobs.Start("apps.install", "Instalando "+app.Name, "Preparando", func(ctx context.Context, h *jobs.Handle) error {
		args.OpID = h.ID()
		if err := s.agent.AppUp(ctx, args); err != nil {
			return errors.New(agentMessage(err))
		}
		_, err := s.follow(ctx, h)
		return err
	}, func(j jobs.Job) {
		if j.State == jobs.Succeeded {
			s.setState(id, "running", "")
			if proxyPort > 0 {
				if err := s.proxy.Ensure(id, proxyPort, port); err != nil {
					s.a.d.Log.Warn("proxy de app", "app", id, "err", err)
				}
			}
			s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.apps", Title: app.Name + " está instalada", Body: "Abrila desde el Centro de apps.",
				Action: &Action{Label: "Abrir", App: "tango.apps", Route: id}})
		} else {
			s.setState(id, "error", j.Error)
			s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.apps", Title: "No se pudo instalar " + app.Name, Body: j.Error})
		}
	})
	return j.ID, nil
}

func (s *appsAPI) action(ctx context.Context, id, act string) (string, error) {
	it, ok := s.get(id)
	if !ok {
		return "", rtcrpc.Errorf("APP_NOT_INSTALLED", "%s no está instalada", id)
	}
	if it.State == "installing" || it.State == "updating" {
		return "", rtcrpc.Errorf("APP_BUSY", "%s está ocupada", id)
	}
	app, _ := appcatalog.Get(id)
	title := map[string]string{"start": "Iniciando ", "stop": "Deteniendo ", "restart": "Reiniciando "}[act] + app.Name
	j := s.jobs.Start("apps."+act, title, "", func(ctx context.Context, h *jobs.Handle) error {
		if err := s.agent.AppAction(ctx, agentproto.AppActionArgs{OpID: h.ID(), ID: id, Mount: it.Mount, Action: act}); err != nil {
			return errors.New(agentMessage(err))
		}
		_, err := s.follow(ctx, h)
		return err
	}, func(j jobs.Job) {
		state := map[string]string{"start": "running", "stop": "stopped", "restart": "running"}[act]
		if j.State != jobs.Succeeded {
			s.setState(id, "error", j.Error)
			return
		}
		s.setState(id, state, "")
	})
	return j.ID, nil
}

func (s *appsAPI) update(ctx context.Context, id, host string) (string, error) {
	it, ok := s.get(id)
	if !ok {
		return "", rtcrpc.Errorf("APP_NOT_INSTALLED", "%s no está instalada", id)
	}
	if it.State == "installing" || it.State == "updating" {
		return "", rtcrpc.Errorf("APP_BUSY", "%s está ocupada", id)
	}
	app, _ := appcatalog.Get(id)
	if host == "" {
		host = "localhost"
	}
	s.mu.Lock()
	if p := s.apps[id]; p != nil {
		p.State = "updating"
		s.saveLocked()
	}
	s.mu.Unlock()
	s.a.d.Hub.Publish("apps.changed", map[string]string{"id": id})
	args := agentproto.AppUpArgs{ID: id, Mount: it.Mount, Port: max(it.Port, 1024), TZ: s.tz, Host: host, Options: it.Options, Pull: true}
	j := s.jobs.Start("apps.update", "Actualizando "+app.Name, "A la versión "+app.Version, func(ctx context.Context, h *jobs.Handle) error {
		args.OpID = h.ID()
		if err := s.agent.AppUp(ctx, args); err != nil {
			return errors.New(agentMessage(err))
		}
		_, err := s.follow(ctx, h)
		return err
	}, func(j jobs.Job) {
		if j.State == jobs.Succeeded {
			s.mu.Lock()
			if p := s.apps[id]; p != nil {
				p.Version = app.Version
			}
			s.mu.Unlock()
			s.setState(id, "running", "")
			s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.apps", Title: app.Name + " actualizada", Body: "Versión " + app.Version + "."})
		} else {
			s.setState(id, "error", j.Error)
		}
	})
	return j.ID, nil
}

func (s *appsAPI) uninstall(ctx context.Context, id string, keepData bool) (string, error) {
	it, ok := s.get(id)
	if !ok {
		return "", rtcrpc.Errorf("APP_NOT_INSTALLED", "%s no está instalada", id)
	}
	if it.State == "installing" || it.State == "updating" {
		return "", rtcrpc.Errorf("APP_BUSY", "%s está ocupada", id)
	}
	app, _ := appcatalog.Get(id)
	detail := "Se conservan sus datos"
	if !keepData {
		detail = "Se borran sus datos"
	}
	j := s.jobs.Start("apps.uninstall", "Desinstalando "+app.Name, detail, func(ctx context.Context, h *jobs.Handle) error {
		if err := s.agent.AppUninstall(ctx, agentproto.AppUninstallArgs{OpID: h.ID(), ID: id, Mount: it.Mount, KeepData: keepData}); err != nil {
			return errors.New(agentMessage(err))
		}
		_, err := s.follow(ctx, h)
		return err
	}, func(j jobs.Job) {
		if j.State != jobs.Succeeded {
			s.setState(id, "error", j.Error)
			return
		}
		s.proxy.Remove(id)
		s.mu.Lock()
		delete(s.apps, id)
		s.saveLocked()
		s.mu.Unlock()
		s.a.d.Hub.Publish("apps.changed", map[string]string{"id": id})
		s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.apps", Title: app.Name + " desinstalada", Body: detail + "."})
	})
	return j.ID, nil
}

// ---- Utilidades ---------------------------------------------------------------------

// sessHost es el host con el que el navegador llegó al NAS (lo guarda la señalización).
func sessHost(s *rtcrpc.Session) string {
	if s == nil {
		return ""
	}
	if h, ok := s.Get("host").(string); ok {
		return h
	}
	return ""
}

const secretChars = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomSecret(n int) string {
	b := make([]byte, n)
	for i := range b {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(secretChars))))
		b[i] = secretChars[k.Int64()]
	}
	return string(b)
}

// localTZ lee la zona horaria del sistema (Debian: /etc/timezone o el link de /etc/localtime).
func localTZ() string {
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		if tz := strings.TrimSpace(string(b)); tz != "" {
			return tz
		}
	}
	if p, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(p, "zoneinfo/"); i >= 0 {
			return p[i+len("zoneinfo/"):]
		}
	}
	return "UTC"
}
