package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/ups"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// UPS (protocol/README.md §13): tangod habla NUT por TCP (Go), el agente instala y
// configura NUT solo para un UPS USB local.

type upsAgent interface {
	UPSApply(ctx context.Context, a agentproto.UPSApplyArgs) error
}

type upsAPI struct {
	m   *m4API
	mon *ups.Monitor
	// monPassword: usuario "tango" de upsd en modo local (0600 en ups.json).
	monPassword string
}

type upsFile struct {
	Config      ups.Config `json:"config"`
	MonPassword string     `json:"monPassword,omitempty"`
}

func (u *upsAPI) path() string { return filepath.Join(u.m.a.d.StateDir, "ups.json") }

func (u *upsAPI) save(cfg ups.Config) error {
	b, err := json.MarshalIndent(upsFile{Config: cfg, MonPassword: u.monPassword}, "", "  ")
	if err != nil {
		return err
	}
	tmp := u.path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, u.path())
}

func upsSecret() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// publicConfig es la configuración sin la contraseña (ups.config.get).
func publicConfig(c ups.Config) ups.Config {
	c.Password = ""
	return c
}

func (m *m4API) registerUPS(r *rtcrpc.Router) *upsAPI {
	a := m.a
	u := &upsAPI{m: m}
	cfg := ups.DefaultConfig()
	if b, err := os.ReadFile(u.path()); err == nil {
		var f upsFile
		if json.Unmarshal(b, &f) == nil {
			cfg, u.monPassword = f.Config, f.MonPassword
		}
	}
	u.mon = ups.NewMonitor(cfg)
	u.mon.Detect = func() []ups.Detected { return ups.DetectUSB("/sys") }
	u.mon.OnChange = func(st ups.Status) { a.d.Hub.Publish("ups.changed", st) }
	u.mon.OnEvent = func(e ups.Event) {
		a.Notify(Notice{Level: e.Level, App: "tango.settings", Title: e.Title, Body: e.Body,
			Action: &Action{Label: "Ver el UPS", App: "tango.settings", Route: "hardware"}})
	}
	u.mon.OnShutdown = func(reason string) {
		a.d.Log.Warn("UPS: apagado ordenado", "reason", reason)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.d.Agent.Power(ctx, "poweroff"); err != nil {
			a.d.Log.Error("UPS: no se pudo apagar", "err", err)
		}
	}
	go u.mon.Run(context.Background())

	r.Handle("ups.status", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		return u.mon.Status(), nil
	})
	r.Handle("ups.config.get", func(_ context.Context, s *rtcrpc.Session, _ json.RawMessage) (any, error) {
		if err := a.requireAdmin(s); err != nil {
			return nil, err
		}
		return publicConfig(u.mon.Config()), nil
	})
	r.Handle("ups.config.set", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p ups.Config) (map[string]string, error) {
		if err := a.requireAdmin(s); err != nil {
			return nil, err
		}
		if p.Port == 0 {
			p.Port = ups.DefaultPort
		}
		if p.UPSName == "" {
			p.UPSName = "ups"
		}
		prev := u.mon.Config()
		if p.Password == "" { // vacío = conservar (como los secretos de notificaciones)
			p.Password = prev.Password
		}
		if err := p.Validate(); err != nil {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%s", err.Error())
		}
		title := map[string]string{"none": "Desactivando el UPS", "local": "Configurando el UPS USB", "remote": "Conectando con el UPS de red"}[p.Mode]
		j := a.d.Jobs.Start("ups.config", title, "", func(ctx context.Context, h *jobs.Handle) error {
			defer a.d.Hub.Publish("services.changed", map[string]string{"id": "ups"})
			needAgent := p.Mode == "local" || prev.Mode == "local"
			if needAgent {
				ua, ok := m.d.Agent.(upsAgent)
				if !ok {
					return rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "este agente no maneja UPS locales")
				}
				if u.monPassword == "" {
					u.monPassword = upsSecret()
				}
				if err := m.queued(h, func() error {
					if err := ua.UPSApply(ctx, agentproto.UPSApplyArgs{OpID: h.ID(), Enabled: p.Mode == "local", UPSName: p.UPSName, NetServer: p.NetServer, MonPassword: u.monPassword}); err != nil {
						return agentErr(err)
					}
					return m.follow(ctx, h)
				}); err != nil {
					return err
				}
			}
			if err := u.save(p); err != nil {
				return err
			}
			u.mon.SetConfig(p)
			return nil
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))
	return u
}

// serviceInfo es la fila "ups" de services.list.
func (u *upsAPI) serviceInfo() ServiceInfo {
	st := u.mon.Status()
	detail := "Sin UPS configurado"
	switch st.Mode {
	case "local":
		detail = "UPS por USB en este equipo (NUT)"
	case "remote":
		detail = "UPS de otro equipo por la red (NUT)"
	}
	if st.Configured && st.StatusText != "" {
		detail += " · " + st.StatusText
	}
	port := (*int)(nil)
	if c := u.mon.Config(); c.Mode == "local" && c.NetServer {
		port = intp(ups.DefaultPort)
	}
	return ServiceInfo{ID: "ups", Enabled: st.Configured, Running: st.Online, Port: port, Detail: detail}
}
