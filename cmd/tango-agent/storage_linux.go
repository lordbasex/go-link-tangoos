//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/smart"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// registerStorage agrega las operaciones de almacenamiento (protocol/README.md §7).
// Toda operación que escribe revalida justo antes: disco entero, no del sistema,
// sin montajes ni holders, abierto con O_EXCL y con la misma huella que vio el plan.
func registerStorage(r *agentproto.Registry, paths smart.Paths, env diskops.Env, jobs *asyncJobs, log *slog.Logger) {
	agentproto.Register(r, agentproto.OpDiskInspect, func(a *agentproto.DiskInspectArgs) *agentproto.Error {
		if len(a.DevIDs) == 0 || len(a.DevIDs) > 16 {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "se esperan entre 1 y 16 discos")
		}
		return nil
	}, func(_ context.Context, a *agentproto.DiskInspectArgs) (any, *agentproto.Error) {
		out := agentproto.DiskInspectResult{Disks: []agentproto.DiskInspection{}}
		for _, id := range a.DevIDs {
			out.Disks = append(out.Disks, inspect(paths, env, id))
		}
		return out, nil
	})

	agentproto.Register(r, agentproto.OpVolumeCreate, validateVolumeCreate, func(_ context.Context, a *agentproto.VolumeCreateArgs) (any, *agentproto.Error) {
		// Revalidación síncrona antes de aceptar: si algo cambió, el error vuelve
		// enseguida y no queda un job fallido en el journal.
		devs, e := revalidate(paths, env, a.Disks)
		if e != nil {
			return nil, e
		}
		if e := volumeFree(a.Name); e != nil {
			return nil, e
		}
		if e := jobs.start(a.OpID, agentproto.OpVolumeCreate, a, func(ctx context.Context, step stepFn) (any, error) {
			return createVolume(ctx, step, paths, env, a, devs, log)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpScrubStart, func(a *agentproto.ScrubStartArgs) *agentproto.Error {
		if err := diskops.ValidMount(a.Mount); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		return nil
	}, func(_ context.Context, a *agentproto.ScrubStartArgs) (any, *agentproto.Error) {
		src, fstype, ok := env.MountSource(a.Mount)
		if !ok || fstype != "btrfs" {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "%s no es un volumen Btrfs montado", a.Mount)
		}
		if e := jobs.start(a.OpID, agentproto.OpScrubStart, a, func(ctx context.Context, step stepFn) (any, error) {
			return scrub(ctx, step, a.Mount, src)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpJobStatus, func(a *agentproto.OpRef) *agentproto.Error {
		if a.OpID == "" {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "falta op_id")
		}
		return nil
	}, func(_ context.Context, a *agentproto.OpRef) (any, *agentproto.Error) {
		return jobs.status(a.OpID)
	})

	agentproto.Register(r, agentproto.OpSubvolCreate, func(a *agentproto.SubvolCreateArgs) *agentproto.Error {
		if err := diskops.ValidMount(a.Mount); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		if err := diskops.ValidSubvolPath(a.Path); err != nil || strings.Count(a.Path, "/") != 1 {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "ruta de subvolumen inválida")
		}
		return nil
	}, func(_ context.Context, a *agentproto.SubvolCreateArgs) (any, *agentproto.Error) {
		if e := mountedBtrfs(env, a.Mount); e != nil {
			return nil, e
		}
		parent, name := filepath.Split(a.Path)
		if err := diskops.CreateSubvolume(filepath.Join(a.Mount, parent), name); err != nil {
			if errors.Is(err, os.ErrExist) {
				return nil, agentproto.Errf(agentproto.CodeExists, "ya existe %s", a.Path)
			}
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo crear el subvolumen: %v", err)
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpSnapshotCreate, func(a *agentproto.SnapshotCreateArgs) *agentproto.Error {
		if err := diskops.ValidMount(a.Mount); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		if err := diskops.ValidShareName(a.Share); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		if err := diskops.ValidSnapshotName(a.Name); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		return nil
	}, func(_ context.Context, a *agentproto.SnapshotCreateArgs) (any, *agentproto.Error) {
		if e := mountedBtrfs(env, a.Mount); e != nil {
			return nil, e
		}
		src := filepath.Join(a.Mount, "@shares", a.Share)
		if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe la carpeta %s", a.Share)
		}
		dst := filepath.Join(a.Mount, "@snapshots", a.Share)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
		}
		if err := diskops.CreateSnapshot(src, dst, a.Name); err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo crear la instantánea: %v", err)
		}
		if err := diskops.SyncShadowLinks(dst); err != nil { // Versiones anteriores de Windows
			log.Warn("shadow links", "share", a.Share, "err", err)
		}
		return struct{}{}, nil
	})

	registerM21(r, paths, env, jobs, log)
	registerM22(r, paths, env, jobs, log) // mantenimiento de volúmenes (protocol/README.md §11)

	agentproto.Register(r, agentproto.OpSambaApply, validateSamba, func(ctx context.Context, a *agentproto.SambaShareArgs) (any, *agentproto.Error) {
		if fi, err := os.Stat(a.Path); err != nil || !fi.IsDir() {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe %s", a.Path)
		}
		reloaded, err := diskops.ApplySambaShare(ctx, a.Name, a.Path, a.Comment, diskops.ShareOpts{TimeMachine: a.TimeMachine, TMMaxBytes: a.TMMaxBytes})
		if errors.Is(err, diskops.ErrSambaMissing) {
			return nil, agentproto.Errf(agentproto.CodeNotInstalled, "Samba no está instalado")
		}
		if err != nil {
			log.Warn("samba", "err", err, "output", execOutput(err))
			return nil, agentproto.Errf(agentproto.CodeFailed, "Samba rechazó la configuración")
		}
		return agentproto.SambaShareResult{Reloaded: reloaded}, nil
	})
}

func inspect(paths smart.Paths, env diskops.Env, id string) agentproto.DiskInspection {
	out := agentproto.DiskInspection{DevID: id}
	dev, e := paths.Resolve(id)
	if e != nil {
		out.Reason = e.Message
		return out
	}
	out.Dev = dev
	ok, reason := env.Eligible(diskops.DevName(dev))
	if !ok {
		out.Reason = reason
		return out
	}
	snap, err := diskops.ReadDisk(dev)
	switch {
	case errors.Is(err, diskops.ErrInUse):
		out.Reason = "el kernel lo tiene en uso"
		return out
	case err != nil:
		out.Reason = "no se pudo leer el disco"
		return out
	}
	out.Size, out.Fingerprint, out.Content, out.Eligible = snap.Size, snap.Fingerprint, snap.Content, true
	return out
}

// revalidate resuelve los discos y exige que sigan elegibles y con la huella del plan.
func revalidate(paths smart.Paths, env diskops.Env, disks []agentproto.VolumeDisk) ([]string, *agentproto.Error) {
	sys := env.SystemDisks()
	var devs []string
	for _, d := range disks {
		dev, e := paths.Resolve(d.DevID)
		if e != nil {
			return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "%s ya no está: %s", d.DevID, e.Message)
		}
		if sys[diskops.DevName(dev)] {
			return nil, agentproto.Errf(agentproto.CodeNotEligible, "%s es el disco del sistema", d.DevID)
		}
		if ok, reason := env.Eligible(diskops.DevName(dev)); !ok {
			return nil, agentproto.Errf(agentproto.CodeNotEligible, "%s: %s", d.DevID, reason)
		}
		snap, err := diskops.ReadDisk(dev)
		if err != nil {
			return nil, agentproto.Errf(agentproto.CodeNotEligible, "%s: %v", d.DevID, err)
		}
		if snap.Fingerprint != d.Fingerprint {
			return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "%s cambió desde el plan", d.DevID)
		}
		devs = append(devs, dev)
	}
	return devs, nil
}

func volumeFree(name string) *agentproto.Error {
	if _, err := os.Lstat("/dev/md/" + name); err == nil {
		return agentproto.Errf(agentproto.CodeExists, "ya existe un RAID llamado %s", name)
	}
	if _, err := os.Lstat(filepath.Join("/etc/systemd/system", diskops.MountUnitName(name))); err == nil {
		return agentproto.Errf(agentproto.CodeExists, "ya existe un volumen %s", name)
	}
	if ents, err := os.ReadDir("/" + name); err == nil && len(ents) > 0 {
		return agentproto.Errf(agentproto.CodeExists, "/%s ya existe y no está vacío", name)
	}
	return nil
}

func createVolume(ctx context.Context, step stepFn, paths smart.Paths, env diskops.Env, a *agentproto.VolumeCreateArgs, devs []string, log *slog.Logger) (any, error) {
	// Última revalidación: entre la aceptación y este punto pudo pasar algo.
	if _, e := revalidate(paths, env, a.Disks); e != nil {
		return nil, e
	}
	step(0.08, "Borrando firmas de los discos")
	for i, dev := range devs {
		if err := diskops.Wipe(dev, a.Disks[i].Fingerprint); err != nil {
			if errors.Is(err, diskops.ErrChanged) {
				return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "%s cambió desde el plan", a.Disks[i].DevID)
			}
			return nil, fmt.Errorf("borrar firmas de %s: %w", a.Disks[i].DevID, err)
		}
	}
	target, md := devs[0], ""
	if level := diskops.RAIDLevel[a.Profile]; level != "" {
		step(0.2, "Creando el RAID "+level+" con mdadm")
		var err error
		md, err = diskops.CreateMD(ctx, a.Name, level, devs)
		if err != nil {
			return nil, fmt.Errorf("crear el RAID: %w", err)
		}
		target = "/dev/" + md
	}
	undoMD := func() {
		if md != "" {
			if err := diskops.StopMD(context.Background(), md); err != nil {
				log.Warn("no se pudo detener el array tras el error", "md", md, "err", err)
			}
		}
	}
	step(0.4, "Creando el sistema de archivos Btrfs")
	uuid, err := diskops.MkfsBtrfs(ctx, a.Name, target)
	if err != nil {
		undoMD()
		return nil, fmt.Errorf("crear Btrfs: %w", err)
	}
	if md != "" {
		step(0.55, "Guardando la configuración del RAID")
		s, err := diskops.MDSuperOf(devs[0])
		if err == nil {
			err = diskops.WriteMDConf(a.Name, s)
		}
		if err != nil {
			log.Warn("mdadm.conf no actualizado (el array igual se ensambla por udev)", "err", err)
		}
	}
	step(0.65, "Montando /"+a.Name)
	if err := diskops.InstallMount(ctx, a.Name, uuid); err != nil {
		diskops.RemoveMount(context.Background(), a.Name)
		undoMD()
		return nil, fmt.Errorf("montar: %w", err)
	}
	step(0.85, "Creando subvolúmenes")
	for _, sv := range []string{"@shares", "@snapshots", "@apps"} {
		if err := diskops.CreateSubvolume("/"+a.Name, sv); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("crear %s: %w", sv, err)
		}
	}
	return agentproto.VolumeCreateResult{Name: a.Name, Mount: "/" + a.Name, MD: md, FSUUID: uuid}, nil
}

func scrub(ctx context.Context, step stepFn, mount, source string) (any, error) {
	var res agentproto.ScrubResult
	md := ""
	if strings.HasPrefix(source, "/dev/md") {
		if t, err := filepath.EvalSymlinks(source); err == nil {
			md = filepath.Base(t)
		}
	}
	btrfsFrom := 0.0
	if md != "" {
		step(0.01, "Verificando el espejo (md check)")
		n, err := diskops.MDCheck(ctx, md, func(p float64) { step(p*0.5, "Verificando el espejo (md check)") })
		if err != nil {
			return nil, fmt.Errorf("md check: %w", err)
		}
		res.MDMismatches = n
		btrfsFrom = 0.5
	}
	used := usedBytes(mount)
	step(btrfsFrom, "Verificando checksums (Btrfs scrub)")
	p, err := diskops.Scrub(mount, func(p diskops.ScrubProgress) {
		if used > 0 {
			f := float64(p.DataBytesScrubbed+p.TreeBytesScrubbed) / float64(used)
			step(btrfsFrom+min(f, 1)*(1-btrfsFrom), "Verificando checksums (Btrfs scrub)")
		}
	})
	if err != nil {
		return nil, err
	}
	res.CsumErrors, res.ReadErrors = p.CsumErrors, p.ReadErrors
	res.Corrected, res.Uncorrectable = p.CorrectedErrors, p.UncorrectableErrors
	res.BytesScrubbed = p.DataBytesScrubbed + p.TreeBytesScrubbed
	return res, nil
}

func mountedBtrfs(env diskops.Env, mount string) *agentproto.Error {
	if _, fstype, ok := env.MountSource(mount); !ok || fstype != "btrfs" {
		return agentproto.Errf(agentproto.CodeNotFound, "%s no es un volumen Btrfs montado", mount)
	}
	return nil
}

func execOutput(err error) string {
	var xe *diskops.ExecError
	if errors.As(err, &xe) {
		return xe.Output
	}
	return ""
}
