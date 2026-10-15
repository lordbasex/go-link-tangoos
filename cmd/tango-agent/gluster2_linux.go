//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	sd "github.com/coreos/go-systemd/v22/dbus"

	"github.com/lordbasex/go-link-tangoos/internal/appsys"
	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Segunda etapa del clúster (protocol/README.md §17, ADR 0012): TLS, bricks en LV
// dedicado, cuotas, rebalance e instantáneas.

func registerGluster2(r *agentproto.Registry, log *slog.Logger) {
	// Al arrancar (por ejemplo después de una actualización) el ruleset se regenera con la
	// versión actual de las reglas (puertos nuevos) y la misma allow-list.
	if fileExists(gluster.FirewallFile) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := writeGlusterFirewall(readGlusterAllowed()); err != nil {
				log.Warn("gluster firewall", "err", err)
				return
			}
			if fileExists(filepath.Join("/etc/systemd/system", gluster.FirewallUnitName)) {
				if err := reloadUnit(ctx, gluster.FirewallUnitName); err != nil {
					log.Warn("gluster firewall", "err", err)
				}
			}
		}()
	}

	agentproto.Register(r, agentproto.OpGlusterTLS, validateGlusterTLS, func(ctx context.Context, a *agentproto.GlusterTLSArgs) (any, *agentproto.Error) {
		if a.Cert != "" {
			if err := os.MkdirAll(filepath.Dir(gluster.TLSCertFile), 0o755); err != nil {
				return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
			}
			for _, f := range []struct {
				path string
				data string
				mode os.FileMode
			}{{gluster.TLSKeyFile, a.Key, 0o600}, {gluster.TLSCertFile, a.Cert, 0o644}, {gluster.TLSCAFile, a.CA, 0o644}} {
				if err := writeFileAtomic(f.path, []byte(f.data), f.mode); err != nil {
					return nil, agentproto.Errf(agentproto.CodeFailed, "%s: %v", f.path, err)
				}
			}
		}
		if a.Secure {
			if err := os.MkdirAll(filepath.Dir(gluster.SecureAccess), 0o755); err != nil {
				return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
			}
			if err := writeFileAtomic(gluster.SecureAccess, nil, 0o644); err != nil {
				return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
			}
		} else if err := os.Remove(gluster.SecureAccess); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
		}
		if a.Restart && glusterInstalled() {
			if err := restartUnit(ctx, "glusterd.service"); err != nil {
				log.Warn("gluster tls: reinicio de glusterd", "err", err)
				return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo reiniciar glusterd: %v", err)
			}
		}
		return glusterTLSState(), nil
	})

	agentproto.Register(r, agentproto.OpGlusterVolumeTLS, validateGlusterVolumeTLS, func(ctx context.Context, a *agentproto.GlusterVolumeTLSArgs) (any, *agentproto.Error) {
		opts := gluster.TLSOptions(a.On, a.CNs)
		// auth.ssl-allow antes de prender server.ssl: el volumen nunca queda abierto a
		// cualquier certificado de la CA.
		for _, k := range []string{"auth.ssl-allow", "server.ssl", "client.ssl"} {
			v, ok := opts[k]
			if !ok {
				continue
			}
			args, err := gluster.ArgsSetManaged(a.Name, k, v)
			if err != nil {
				return nil, badArgs(err)
			}
			if err := glusterDo(ctx, args); err != nil {
				return nil, glusterAgentErr(err)
			}
		}
		if !a.On {
			_ = glusterDo(ctx, []string{"volume", "reset", a.Name, "auth.ssl-allow"})
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpGlusterBrickLV, validateGlusterBrickLV, func(ctx context.Context, a *agentproto.GlusterBrickLVArgs) (any, *agentproto.Error) {
		if a.Op == "delete" {
			return glusterBrickLVDelete(ctx, a)
		}
		return glusterBrickLVCreate(ctx, a, log)
	})

	agentproto.Register(r, agentproto.OpGlusterQuota, validateGlusterQuota, func(ctx context.Context, a *agentproto.GlusterQuotaArgs) (any, *agentproto.Error) {
		args, _ := gluster.ArgsQuota(a.Name, a.Op, a.Path, a.Limit)
		// Las cuotas usan un montaje auxiliar del volumen (red y mount): fuera del sandbox.
		out, err := runGlusterUnit(ctx, "tango-gluster-quota", args)
		if err != nil {
			return nil, glusterAgentErr(err)
		}
		if a.Op == "list" {
			q, err := gluster.ParseQuota(out)
			if err != nil {
				if strings.Contains(err.Error(), "not enabled") {
					return []gluster.Quota{}, nil
				}
				return nil, glusterAgentErr(err)
			}
			return q, nil
		}
		if err := gluster.ParseResult(out, 0); err != nil {
			return nil, glusterAgentErr(err)
		}
		// GlusterFS 11 cuenta el uso pero deja pasar las escrituras
		// (feature.simple-quota-pass-through=true por defecto): sin esto, la cuota no frena.
		if a.Op == "enable" {
			if err := glusterDo(ctx, gluster.ArgsQuotaEnforce(a.Name)); err != nil {
				return nil, glusterAgentErr(err)
			}
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpGlusterRebalance, validateGlusterRebalance, func(ctx context.Context, a *agentproto.GlusterRebalanceArgs) (any, *agentproto.Error) {
		args, _ := gluster.ArgsRebalance(a.Name, a.Action)
		out, code, err := runGluster(ctx, args...)
		if err != nil {
			return nil, glusterAgentErr(err)
		}
		if a.Action != "status" {
			if err := gluster.ParseResult(out, code); err != nil {
				return nil, glusterAgentErr(err)
			}
			return agentproto.GlusterTaskResult{}, nil
		}
		t, err := gluster.ParseTask(out, hostnameOr(""))
		if err != nil {
			if strings.Contains(err.Error(), "not started") {
				return agentproto.GlusterTaskResult{}, nil
			}
			return nil, glusterAgentErr(err)
		}
		return agentproto.GlusterTaskResult{Task: t}, nil
	})

	agentproto.Register(r, agentproto.OpGlusterSnapshot, validateGlusterSnapshot, func(ctx context.Context, a *agentproto.GlusterSnapshotArgs) (any, *agentproto.Error) {
		args, _ := gluster.ArgsSnapshot(a.Op, a.Snap, a.Name, a.Desc)
		out, code, err := runGlusterT(ctx, 0, args...)
		if err != nil {
			return nil, glusterAgentErr(err)
		}
		if a.Op == "info" {
			s, err := gluster.ParseSnapshots(out)
			if err != nil {
				return nil, glusterAgentErr(err)
			}
			return s, nil
		}
		if err := gluster.ParseResult(out, code); err != nil {
			return nil, glusterAgentErr(err)
		}
		return struct{}{}, nil
	})
}

func hostnameOr(def string) string {
	if h, err := os.Hostname(); err == nil {
		return strings.ToLower(h)
	}
	return def
}

func glusterTLSState() agentproto.GlusterTLSState {
	return agentproto.GlusterTLSState{
		Files:  fileExists(gluster.TLSCertFile) && fileExists(gluster.TLSKeyFile) && fileExists(gluster.TLSCAFile),
		Secure: fileExists(gluster.SecureAccess),
	}
}

func restartUnit(ctx context.Context, unit string) error {
	c, err := sd.NewSystemConnectionContext(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	ch := make(chan string, 1)
	if _, err := c.RestartUnitContext(ctx, unit, "replace", ch); err != nil {
		return err
	}
	return sdWait(ctx, ch, unit)
}

// runGlusterUnit corre `gluster --mode=script --xml <args>` en una unidad transitoria
// (red del host y montajes auxiliares) y devuelve su salida XML.
func runGlusterUnit(ctx context.Context, unit string, args []string) ([]byte, error) {
	if !glusterInstalled() {
		return nil, errGlusterMissing
	}
	var rb [4]byte
	_, _ = rand.Read(rb[:])
	out := filepath.Join(m4LogDir, unit+"-"+hex.EncodeToString(rb[:])+".xml")
	defer os.Remove(out)
	argv := append([]string{gluster.Binary, "--mode=script", "--xml"}, args...)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_ = appsys.RunUnit(ctx, unit, argv, nil, out, nil)
	b, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	if i := bytes.Index(b, []byte("<?xml")); i > 0 {
		b = b[i:]
	}
	return b, nil
}

// ---- Bricks en LV --------------------------------------------------------------------

func glusterBrickLVCreate(ctx context.Context, a *agentproto.GlusterBrickLVArgs, log *slog.Logger) (any, *agentproto.Error) {
	if poolsInst == nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "los grupos de almacenamiento no están disponibles")
	}
	if isMountedAt(gluster.LVMountPath(a.Vol)) {
		return nil, agentproto.Errf(agentproto.CodeBusy, "ya hay un brick montado en %s", gluster.LVMountPath(a.Vol))
	}
	rep, err := poolsInst.report(ctx)
	if err != nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
	}
	var pi *agentproto.PoolInfo
	for i := range rep.Pools {
		if rep.Pools[i].Name == a.Pool {
			pi = &rep.Pools[i]
		}
	}
	if pi == nil {
		return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe el grupo %s", a.Pool)
	}
	pvs, free := dataPVs(pi)
	need := a.Size
	if a.Thin {
		need += a.Size / 4
	}
	if need > free {
		return nil, agentproto.Errf(agentproto.CodeNoSpace, "el grupo %s tiene %d MB libres (hacen falta %d MB)", a.Pool, free>>20, need>>20)
	}
	if err := diskops.GlusterLVCreate(ctx, a.Pool, a.Vol, a.Size, a.Thin, pvs); err != nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "lvcreate: %v", err)
	}
	dev := diskops.GlusterLVDev(a.Pool, a.Vol)
	undo := func() { _ = diskops.GlusterLVRemove(context.Background(), a.Pool, a.Vol) }
	if err := diskops.WaitDev(ctx, dev); err != nil {
		undo()
		return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
	}
	uuid, err := diskops.MkfsXFSBrick(ctx, a.Vol, dev)
	if err != nil {
		undo()
		return nil, agentproto.Errf(agentproto.CodeFailed, "mkfs.xfs: %v", err)
	}
	if err := diskops.InstallGlusterLVMount(ctx, a.Vol, uuid); err != nil {
		_ = diskops.RemoveGlusterLVMount(context.Background(), a.Vol)
		undo()
		return nil, agentproto.Errf(agentproto.CodeFailed, "montar el brick: %v", err)
	}
	log.Info("gluster: brick en LV", "pool", a.Pool, "vol", a.Vol, "size", a.Size, "thin", a.Thin)
	return agentproto.GlusterBrickLVResult{Path: gluster.LVBrickPath(a.Vol), Thin: a.Thin}, nil
}

func glusterBrickLVDelete(ctx context.Context, a *agentproto.GlusterBrickLVArgs) (any, *agentproto.Error) {
	// Si el volumen todavía existe, solo se borra el LV cuando ningún brick de este nodo lo
	// usa (por ejemplo después de reemplazarlo o de restaurar una instantánea). Las
	// instantáneas que salieron de él solo se borran con el volumen ya borrado.
	exists := false
	if glusterInstalled() {
		info, code, err := runGluster(ctx, "volume", "info", a.Vol)
		if err == nil {
			perr := gluster.ParseResult(info, code)
			switch {
			case perr == nil:
				exists = true
				vols, err := gluster.ParseVolumes(info, nil)
				if err != nil {
					return nil, glusterAgentErr(err)
				}
				for _, v := range vols {
					for _, b := range v.Bricks {
						if b.Path == gluster.LVBrickPath(a.Vol) && slices.Contains(a.Self, b.Host) {
							return nil, agentproto.Errf(agentproto.CodeBusy, "el volumen %s todavía usa este brick (%s)", a.Vol, b.Host)
						}
					}
				}
			case !gluster.IsNotFound(perr):
				return nil, glusterAgentErr(perr)
			}
		}
	}
	if !exists {
		rows, _ := diskops.GlusterLVs(ctx)
		for _, r := range rows {
			if r.VG != a.Pool || r.Vol != a.Vol {
				continue
			}
			for _, child := range r.Children {
				if err := diskops.RemoveGlusterSnapLV(ctx, a.Pool, child); err != nil {
					return nil, agentproto.Errf(agentproto.CodeBusy, "instantánea %s: %v", child, err)
				}
			}
		}
	}
	if err := diskops.RemoveGlusterLVMount(ctx, a.Vol); err != nil {
		return nil, agentproto.Errf(agentproto.CodeBusy, "%v", err)
	}
	if err := diskops.GlusterLVRemove(ctx, a.Pool, a.Vol); err != nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "lvremove: %v", err)
	}
	return struct{}{}, nil
}

// glusterLVBricks son los LV de bricks de este nodo (para el estado).
func glusterLVBricks(ctx context.Context) []agentproto.GlusterLVBrick {
	out := []agentproto.GlusterLVBrick{}
	rows, err := diskops.GlusterLVs(ctx)
	if err != nil {
		return out
	}
	for _, r := range rows {
		out = append(out, agentproto.GlusterLVBrick{Vol: r.Vol, Pool: r.VG, Size: r.Size, Thin: r.Thin, Mounted: isMountedAt(gluster.LVMountPath(r.Vol)), Snaps: len(r.Children)})
	}
	return out
}

var _ = strconv.Itoa
