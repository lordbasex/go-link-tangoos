//go:build linux

package main

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/lordbasex/go-link-tangoos/internal/ups"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// UPS por USB en este equipo (protocol/README.md §13): instala NUT, escribe /etc/nut a
// partir de una configuración tipada y deja nut-server corriendo.

func upsConfig(a *agentproto.UPSApplyArgs) ups.Config {
	c := ups.DefaultConfig()
	c.Mode, c.UPSName, c.NetServer = "local", a.UPSName, a.NetServer
	return c
}

func registerUPS(r *agentproto.Registry, jobs *asyncJobs) {
	agentproto.Register(r, agentproto.OpUPSApply, func(a *agentproto.UPSApplyArgs) *agentproto.Error {
		if e := validOpID(a.OpID); e != nil {
			return e
		}
		if !a.Enabled {
			return nil
		}
		if _, err := ups.NUTFiles(upsConfig(a), a.MonPassword); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		return nil
	}, func(_ context.Context, a *agentproto.UPSApplyArgs) (any, *agentproto.Error) {
		redacted := map[string]any{"enabled": a.Enabled, "ups": a.UPSName, "net_server": a.NetServer}
		if e := jobs.start(a.OpID, agentproto.OpUPSApply, redacted, func(ctx context.Context, step stepFn) (any, error) {
			return nil, runHelperJSON(ctx, step, "ups-apply", a.OpID, a)
		}); e != nil {
			return nil, e
		}
		return struct{}{}, nil
	})
}

// helperUPS corre en la unidad transitoria (root, fuera del sandbox).
func helperUPS(ctx context.Context, a *agentproto.UPSApplyArgs) error {
	if !a.Enabled {
		stage(0.5, "Deteniendo NUT")
		if fileExists("/lib/systemd/system/nut-server.service") || fileExists("/usr/lib/systemd/system/nut-server.service") {
			_ = setUnit(ctx, "nut-server.service", false)
		}
		return nil
	}
	files, err := ups.NUTFiles(upsConfig(a), a.MonPassword)
	if err != nil {
		return err
	}
	if !fileExists("/usr/sbin/upsd") && !fileExists("/sbin/upsd") {
		stage(0.15, "Instalando NUT (nut-server, nut-client)")
		if err := runCmd(ctx, "/usr/bin/apt-get", "-q", "update"); err != nil {
			return err
		}
		if err := runCmd(ctx, "/usr/bin/apt-get", "-q", "-y", "--no-install-recommends", "install", "nut-server", "nut-client"); err != nil {
			return err
		}
	}
	stage(0.6, "Escribiendo /etc/nut")
	if err := os.MkdirAll("/etc/nut", 0o755); err != nil {
		return err
	}
	gid := -1
	if g, err := user.LookupGroup("nut"); err == nil {
		gid, _ = strconv.Atoi(g.Gid)
	}
	for name, body := range files {
		mode := os.FileMode(0o640) // upsd.users lleva una contraseña
		p := filepath.Join("/etc/nut", name)
		if err := writeFileAtomic(p, []byte(body), mode); err != nil {
			return err
		}
		if gid >= 0 {
			_ = os.Chown(p, 0, gid)
		}
	}
	stage(0.8, "Arrancando el driver y upsd")
	for _, u := range []string{"nut-driver.target", "nut-server.service"} {
		if err := setUnit(ctx, u, true); err != nil && u == "nut-server.service" {
			return err
		}
	}
	return nil
}
