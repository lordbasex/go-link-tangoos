// Package api registers the real tango/1 methods on the rtcrpc router
// (protocol/README.md §5) and runs the publishers: metrics at 4 Hz and
// storage change events.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/agentclient"
	"github.com/lordbasex/go-link-tangoos/internal/auth"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/metrics"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/internal/sysinfo"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Topics the hub advertises in hello.
var Topics = []string{"metrics", "jobs.changed", "storage.changed", "shares.changed", "snapshots.changed", "notice", "apps.changed", "files.changed", "files.xfer", "alerts.changed", "services.changed", "audit.event", "ups.changed", "users.changed", "connect.changed"}

// Notice is exactly the TS Notice.
type Notice struct {
	ID     string  `json:"id"`
	Level  string  `json:"level"`
	App    string  `json:"app"`
	Title  string  `json:"title"`
	Body   string  `json:"body"`
	At     string  `json:"at"`
	Action *Action `json:"action,omitempty"`
}

type Action struct {
	Label string `json:"label"`
	App   string `json:"app"`
	Route string `json:"route,omitempty"`
}

// Job is exactly the TS Job (no real jobs yet: the list is empty).
type Job struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	Title     string  `json:"title"`
	Detail    string  `json:"detail"`
	Progress  float64 `json:"progress"`
	State     string  `json:"state"`
	StartedAt string  `json:"startedAt"`
	Rate      string  `json:"rate,omitempty"`
}

// SessionInfo is exactly the TS Session.
type SessionInfo struct {
	ID      string `json:"id"`
	User    string `json:"user"`
	Client  string `json:"client"`
	IP      string `json:"ip"`
	Since   string `json:"since"`
	Current bool   `json:"current,omitempty"`
}

// Group is exactly the TS Group.
type Group struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Members     []string `json:"members"`
}

// Deps are the services behind the methods.
type Deps struct {
	Auth      *auth.Store
	Inventory *storage.Inventory
	SysInfo   sysinfo.Reader
	Hub       *rtcrpc.Hub
	StateDir  string
	Log       *slog.Logger
	// Fase M2 (opcionales: sin ellos no se registran los métodos que escriben).
	Agent StorageAgent
	Jobs  *jobs.Store
}

// API holds the in-memory stores (notices, jobs).
type API struct {
	d Deps

	mu      sync.Mutex
	notices []Notice
	jobs    []Job
	storage *storageAPI
	files   *filesAPI // Archivos real (files.go)
	m22     *m22      // mantenimiento de volúmenes (storage_m22.go, protocol/README.md §11)
	pools   *poolsAPI // grupos de almacenamiento (pools.go, protocol/README.md §16)
	zfs     *zfsAPI   // pools ZFS (zfs.go, protocol/README.md §18)
	m4      *m4API    // producto (m4_*.go, protocol/README.md §12)
	term    *termMgr  // consolas de administración (m4_term.go)
	// onNotice reenvía cada aviso a los canales externos (notify, m4.go).
	onNotice func(Notice)
}

// Register wires every real method into r.
func Register(r *rtcrpc.Router, d Deps) *API {
	a := &API{d: d, jobs: []Job{}, notices: []Notice{}}
	if _, err := os.Stat(filepath.Join(d.StateDir, "initial-admin-password")); err == nil {
		a.notices = append(a.notices, Notice{
			ID: "first-boot", Level: "info", App: "tango.settings",
			Title: "Cambiá la contraseña de admin",
			Body:  "La contraseña inicial está en " + filepath.Join(d.StateDir, "initial-admin-password") + ". El asistente de primer arranque llega en M4.",
			At:    time.Now().UTC().Format(time.RFC3339), Action: &Action{Label: "Abrir Ajustes", App: "tango.settings", Route: "users"},
		})
	}

	type loginP struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	type tokenP struct {
		Token string `json:"token"`
	}

	r.Handle("auth.users", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		return nonNil(d.Auth.Public()), nil
	}, rtcrpc.Public())
	r.Handle("auth.login", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p loginP) (loginResult, error) {
		u, tok, err := d.Auth.Login(p.User, p.Password, s.RemoteIP)
		switch {
		case errors.Is(err, auth.ErrNeedTOTP): // §13: contraseña bien, falta el código
			s.Set(totpPendingKey, &totpPending{user: u.Name, exp: time.Now().Add(totpPendingTTL)})
			return loginResult{Needs: "totp"}, nil
		case errors.Is(err, auth.ErrLocked):
			d.Log.Warn("login locked", "user", p.User, "ip", s.RemoteIP)
			return loginResult{}, rtcrpc.Errorf(rtcrpc.CodeRateLimited, "%s", err.Error())
		case err != nil:
			d.Log.Warn("login failed", "user", p.User, "ip", s.RemoteIP)
			return loginResult{}, rtcrpc.Errorf(rtcrpc.CodeInvalidCredentials, "%s", err.Error())
		}
		s.Authenticate(u.Name, tok)
		d.Log.Info("login", "user", u.Name, "ip", s.RemoteIP, "client", s.Client)
		return loginResult{User: &u, Token: tok}, nil
	}), rtcrpc.Public())
	r.Handle("auth.resume", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p tokenP) (loginResult, error) {
		u, err := d.Auth.Resume(p.Token)
		if err != nil {
			return loginResult{}, rtcrpc.Errorf(rtcrpc.CodeUnauthenticated, "%s", err.Error())
		}
		s.Authenticate(u.Name, p.Token)
		return loginResult{User: &u}, nil
	}), rtcrpc.Public())
	a.registerTOTP(r)
	r.Handle("auth.logout", func(_ context.Context, s *rtcrpc.Session, _ json.RawMessage) (any, error) {
		d.Auth.Logout(s.Logout())
		return struct{}{}, nil
	})

	r.Handle("system.info", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		return d.SysInfo.Read(), nil
	}, rtcrpc.Public())

	r.Handle("storage.disks.list", func(ctx context.Context, _ *rtcrpc.Session, _ json.RawMessage) (any, error) {
		disks, err := d.Inventory.Disks(ctx)
		if err != nil {
			return nil, err
		}
		return nonNil(disks), nil
	})
	r.Handle("storage.volumes.list", func(ctx context.Context, _ *rtcrpc.Session, _ json.RawMessage) (any, error) {
		vols, err := d.Inventory.Volumes(ctx)
		if err != nil {
			return nil, err
		}
		if a.storage != nil {
			vols = a.storage.decorateVolumes(vols)
		}
		return nonNil(vols), nil
	})
	r.Handle("sessions.list", func(_ context.Context, me *rtcrpc.Session, _ json.RawMessage) (any, error) {
		out := []SessionInfo{}
		for _, s := range d.Hub.Sessions() {
			if !s.Authenticated() {
				continue
			}
			out = append(out, SessionInfo{ID: s.ID, User: s.User(), Client: s.Client, IP: s.RemoteIP, Since: s.Since.UTC().Format(time.RFC3339), Current: s.ID == me.ID})
		}
		return out, nil
	})
	r.Handle("users.list", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		return nonNil(d.Auth.List()), nil
	})
	r.Handle("groups.list", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		admins := []string{}
		for _, u := range d.Auth.List() {
			if u.Admin {
				admins = append(admins, u.Name)
			}
		}
		return []Group{{Name: "administrators", Description: "Administran el NAS", Members: admins}}, nil
	})
	r.Handle("jobs.list", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		if d.Jobs != nil {
			return d.Jobs.List(), nil
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		return append([]Job{}, a.jobs...), nil
	})
	r.Handle("notices.list", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		return append([]Notice{}, a.notices...), nil
	})
	r.Handle("rtc.stats", func(_ context.Context, s *rtcrpc.Session, _ json.RawMessage) (any, error) {
		if f, ok := s.Get("rtc.stats").(func() any); ok {
			return f(), nil
		}
		return nil, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "sin estadísticas para esta sesión")
	})
	if d.Agent != nil && d.Jobs != nil {
		migrateVolumeIDs(d) // IDs de volumen estables (§11): antes de que apps lea apps.json
		a.registerStorage(r, d)
		a.registerApps(r, d) // Centro de apps (protocol/README.md §9)
		a.registerUsers(r)
		a.registerM22(r, d)
		a.registerPools(r, d)
		a.registerZFS(r, d) // pools ZFS (zfs.go, protocol/README.md §18)
		if sp, ok := d.Agent.(WorkerSpawner); ok {
			a.files = a.registerFiles(r, sp) // protocol/README.md §10
		}
	}
	return a
}

// Notify adds a notice and pushes it to the logged-in sessions.
func (a *API) Notify(n Notice) {
	if n.At == "" {
		n.At = time.Now().UTC().Format(time.RFC3339)
	}
	a.mu.Lock()
	a.notices = append([]Notice{n}, a.notices...)
	cb := a.onNotice
	a.mu.Unlock()
	a.d.Hub.Publish("notice", n)
	if cb != nil {
		cb(n)
	}
}

// RunMetrics samples at 4 Hz while somebody is logged in and sends each
// frame on the `metrics` channel of every logged-in session.
func RunMetrics(ctx context.Context, hub *rtcrpc.Hub, c *metrics.Collector) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !hub.AnyAuthenticated() {
			continue
		}
		b, err := json.Marshal(c.Frame())
		if err != nil {
			continue
		}
		for _, s := range hub.Sessions() {
			s.SendMetrics(b)
		}
	}
}

// WatchStorage polls the inventory every 5 s while somebody is logged in and
// publishes storage.changed when disks or volumes change.
// TODO(M2): udev netlink (NETLINK_KOBJECT_UEVENT) instead of polling.
func WatchStorage(ctx context.Context, hub *rtcrpc.Hub, inv *storage.Inventory) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var lastDisks, lastVols [32]byte
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !hub.AnyAuthenticated() {
			continue
		}
		if disks, err := inv.Disks(ctx); err == nil {
			if h := hashJSON(disks); h != lastDisks {
				if lastDisks != ([32]byte{}) {
					hub.Publish("storage.changed", map[string]string{"what": "disks"})
				}
				lastDisks = h
			}
		}
		if vols, err := inv.Volumes(ctx); err == nil {
			if h := hashJSON(vols); h != lastVols {
				if lastVols != ([32]byte{}) {
					hub.Publish("storage.changed", map[string]string{"what": "volumes"})
				}
				lastVols = h
			}
		}
	}
}

func hashJSON(v any) [32]byte {
	b, _ := json.Marshal(v)
	return sha256.Sum256(b)
}

// nonNil turns a nil slice into [] so the client never gets null lists.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// AgentSmart adapts tango-agent's disk.smart to storage.SmartSource.
type AgentSmart struct{ C *agentclient.Client }

func (a AgentSmart) Smart(ctx context.Context, d storage.Disk) (storage.SmartInfo, error) {
	if d.ByID == "" {
		return storage.SmartInfo{}, errors.New("disk without by-id name")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	r, err := a.C.DiskSmart(ctx, d.ByID)
	if err != nil {
		return storage.SmartInfo{}, err // ErrUnavailable included: caller keeps "unknown"
	}
	si := storage.SmartInfo{Health: "unknown"}
	if !r.Supported {
		return si, nil
	}
	if r.Health != "" {
		si.Health = r.Health
	}
	if r.TempC != nil {
		t := float64(*r.TempC)
		si.TempC = &t
	}
	if r.PowerOnH != nil {
		h := int(*r.PowerOnH)
		si.PowerOnHours = &h
	}
	if r.Reallocated != nil {
		si.Reallocated = int(*r.Reallocated)
	}
	if r.Pending != nil {
		si.Pending = int(*r.Pending)
	}
	return si, nil
}
