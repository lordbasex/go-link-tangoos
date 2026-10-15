//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/smart"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// registerM21 agrega las operaciones de la fase M2.1 (protocol/README.md §8): borrar
// volúmenes y carpetas, permisos de carpetas y usuarios Unix/Samba.
func registerM21(r *agentproto.Registry, paths smart.Paths, env diskops.Env, jobs *asyncJobs, log *slog.Logger) {
	agentproto.Register(r, agentproto.OpVolumeDelete, validateVolumeDelete, func(_ context.Context, a *agentproto.VolumeDeleteArgs) (any, *agentproto.Error) {
		devs, md, e := volumeMembers(paths, env, a.Name, a.DevIDs)
		if e != nil {
			return nil, e
		}
		if e := jobs.start(a.OpID, agentproto.OpVolumeDelete, a, func(ctx context.Context, step stepFn) (any, error) {
			return deleteVolume(ctx, step, paths, env, a, devs, md, log)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpShareDelete, validateShareDelete, func(ctx context.Context, a *agentproto.ShareDeleteArgs) (any, *agentproto.Error) {
		if _, ok := zfsVolumeOf(env, a.Mount); ok { // dataset de la carpeta y sus instantáneas (ADR 0013)
			res := agentproto.ShareDeleteResult{}
			res.Reloaded, _ = diskops.RemoveSambaShare(ctx, a.Share)
			ents, _ := os.ReadDir(filepath.Join(a.Mount, "@snapshots", a.Share))
			for _, e := range ents {
				if diskops.ValidSnapshotName(e.Name()) == nil {
					res.Snapshots++
				}
			}
			if err := zfsHelper(ctx, "zfs-sharedel", a); err != nil {
				return nil, zerr(err)
			}
			return res, nil
		}
		if e := mountedBtrfs(env, a.Mount); e != nil {
			return nil, e
		}
		shares := filepath.Join(a.Mount, "@shares")
		if fi, err := os.Stat(filepath.Join(shares, a.Share)); err != nil || !fi.IsDir() {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe la carpeta %s", a.Share)
		}
		res := agentproto.ShareDeleteResult{}
		// Primero que deje de verse en la red.
		reloaded, err := diskops.RemoveSambaShare(ctx, a.Share)
		if err != nil {
			log.Warn("samba", "err", err)
		}
		res.Reloaded = reloaded
		snapDir := filepath.Join(a.Mount, "@snapshots", a.Share)
		ents, _ := os.ReadDir(snapDir)
		for _, e := range ents {
			if diskops.ValidSnapshotName(e.Name()) != nil {
				continue
			}
			if err := diskops.DestroySubvolume(snapDir, e.Name()); err != nil {
				return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo borrar la instantánea %s: %v", e.Name(), err)
			}
			res.Snapshots++
		}
		_ = os.RemoveAll(filepath.Join(snapDir, diskops.ShadowLinkDir)) // solo enlaces
		_ = os.Remove(snapDir)
		if err := diskops.DestroySubvolume(shares, a.Share); err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo borrar la carpeta: %v", err)
		}
		return res, nil
	})

	agentproto.Register(r, agentproto.OpSharePerms, validateSharePerms, func(ctx context.Context, a *agentproto.SharePermsArgs) (any, *agentproto.Error) {
		if _, zfsVol := zfsVolumeOf(env, a.Mount); !zfsVol { // en ZFS los permisos son iguales (acltype=posixacl)
			if e := mountedBtrfs(env, a.Mount); e != nil {
				return nil, e
			}
		}
		path := filepath.Join(a.Mount, "@shares", a.Share)
		if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe la carpeta %s", a.Share)
		}
		for _, u := range append(append([]string{}, a.RW...), a.RO...) {
			if _, err := diskops.EnsureUser(ctx, u, ""); err != nil {
				return nil, userErr(err, log)
			}
		}
		rwG, roG := diskops.ShareGroups(a.Share)
		rwGID, err := diskops.EnsureGroup(ctx, rwG)
		if err != nil {
			return nil, userErr(err, log)
		}
		roGID, err := diskops.EnsureGroup(ctx, roG)
		if err != nil {
			return nil, userErr(err, log)
		}
		if err := diskops.SetGroupMembers(ctx, rwG, sorted(a.RW)); err != nil {
			return nil, userErr(err, log)
		}
		if err := diskops.SetGroupMembers(ctx, roG, sorted(a.RO)); err != nil {
			return nil, userErr(err, log)
		}
		if err := diskops.ApplyShareACL(path, rwGID, roGID); err != nil {
			log.Warn("acl", "share", a.Share, "err", err)
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudieron aplicar los permisos: %v", err)
		}
		res := agentproto.SharePermsResult{}
		// Enlaces de las Versiones anteriores de Windows (también arma los de carpetas viejas).
		if err := diskops.SyncShadowLinks(filepath.Join(a.Mount, "@snapshots", a.Share)); err != nil {
			log.Warn("shadow links", "share", a.Share, "err", err)
		}
		if a.SMB {
			res.Reloaded, err = diskops.ApplySambaShareWithPerms(ctx, a.Share, path, a.Comment, diskops.ShareOpts{TimeMachine: a.TimeMachine, TMMaxBytes: a.TMMaxBytes})
			if errors.Is(err, diskops.ErrSambaMissing) {
				return nil, agentproto.Errf(agentproto.CodeNotInstalled, "Samba no está instalado")
			}
			if err != nil {
				log.Warn("samba", "err", err, "output", execOutput(err))
				return nil, agentproto.Errf(agentproto.CodeFailed, "Samba rechazó la configuración")
			}
		}
		return res, nil
	})

	registerUsersV10(r, log) // user.delete (protocol/README.md §14)

	agentproto.Register(r, agentproto.OpUserEnsure, validateUserEnsure, func(ctx context.Context, a *agentproto.UserEnsureArgs) (any, *agentproto.Error) {
		uid, err := diskops.EnsureUser(ctx, a.Name, a.Full)
		if err != nil {
			return nil, userErr(err, log)
		}
		return agentproto.UserEnsureResult{UID: uid}, nil
	})

	agentproto.Register(r, agentproto.OpUserPassword, validateUserPassword, func(ctx context.Context, a *agentproto.UserPasswordArgs) (any, *agentproto.Error) {
		if _, err := diskops.EnsureUser(ctx, a.Name, a.Full); err != nil {
			return nil, userErr(err, log)
		}
		if err := diskops.SetSambaPassword(ctx, a.Name, a.Password); err != nil {
			if errors.Is(err, diskops.ErrSambaMissing) {
				return nil, agentproto.Errf(agentproto.CodeNotInstalled, "Samba no está instalado")
			}
			// La salida de smbpasswd no incluye la contraseña (va por stdin).
			return nil, userErr(err, log)
		}
		return struct{}{}, nil
	})
}

func sorted(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

func userErr(err error, log *slog.Logger) *agentproto.Error {
	if errors.Is(err, diskops.ErrSystemUser) {
		return agentproto.Errf("SYSTEM_USER", "%v", err)
	}
	log.Warn("usuarios", "err", err, "output", execOutput(err))
	return agentproto.Errf(agentproto.CodeFailed, "no se pudo actualizar los usuarios del sistema")
}

// volumeMembers resuelve los discos que vio el plan y exige que sean exactamente los
// del volumen: los miembros reales del array md, o el disco donde está el Btrfs del
// volumen básico. Nunca se borra un disco que no pertenezca al volumen.
func volumeMembers(paths smart.Paths, env diskops.Env, name string, devIDs []string) ([]string, string, *agentproto.Error) {
	sys := env.SystemDisks()
	var devs []string
	for _, id := range devIDs {
		dev, e := paths.Resolve(id)
		if e != nil {
			return nil, "", agentproto.Errf(agentproto.CodeDeviceChanged, "%s ya no está: %s", id, e.Message)
		}
		if sys[diskops.DevName(dev)] {
			return nil, "", agentproto.Errf(agentproto.CodeNotEligible, "%s es el disco del sistema", id)
		}
		devs = append(devs, dev)
	}
	names := make([]string, len(devs))
	for i, d := range devs {
		names[i] = diskops.DevName(d)
	}
	sort.Strings(names)
	md := ""
	if t, err := filepath.EvalSymlinks("/dev/md/" + name); err == nil {
		md = filepath.Base(t)
	}
	if md != "" {
		members := diskops.MDMembers(md)
		sort.Strings(members)
		if !slices.Equal(members, names) {
			return nil, "", agentproto.Errf(agentproto.CodeDeviceChanged, "los discos del RAID %s no coinciden con el plan", name)
		}
		return devs, md, nil
	}
	// Básico: Btrfs directo en un disco.
	if len(devs) != 1 {
		return nil, "", agentproto.Errf(agentproto.CodeNotFound, "no existe el RAID %s", name)
	}
	if src, fstype, ok := env.MountSource("/" + name); ok {
		real, _ := filepath.EvalSymlinks(src)
		if fstype != "btrfs" || diskops.DevName(real) != names[0] {
			return nil, "", agentproto.Errf(agentproto.CodeDeviceChanged, "/%s no está en el disco del plan", name)
		}
	}
	return devs, "", nil
}

func deleteVolume(ctx context.Context, step stepFn, paths smart.Paths, env diskops.Env, a *agentproto.VolumeDeleteArgs, devs []string, md string, log *slog.Logger) (any, error) {
	mount := "/" + a.Name
	step(0.05, "Despublicando las carpetas compartidas")
	if n, err := diskops.RemoveSambaSharesUnder(ctx, mount); err != nil {
		log.Warn("samba", "err", err)
	} else if n > 0 {
		log.Info("carpetas despublicadas", "volume", a.Name, "count", n)
	}

	step(0.2, "Desmontando "+mount)
	if err := diskops.UninstallMount(ctx, a.Name); err != nil {
		return nil, fmt.Errorf("desmontar: %w", err)
	}
	if _, _, ok := env.MountSource(mount); ok {
		return nil, agentproto.Errf("VOLUME_BUSY", "%s sigue montado (algo lo está usando)", mount)
	}
	_ = os.Remove(mount) // solo si quedó vacío

	// Revalidar justo antes de borrar: siguen siendo los mismos discos.
	if _, _, e := volumeMembers(paths, env, a.Name, a.DevIDs); e != nil && md != "" {
		return nil, e
	}
	if md != "" {
		step(0.45, "Deteniendo el RAID")
		if err := diskops.StopMD(ctx, md); err != nil {
			return nil, fmt.Errorf("detener el RAID: %w", err)
		}
		for range 50 {
			if _, err := os.Stat("/sys/block/" + md + "/md"); err != nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		step(0.6, "Borrando los superbloques del RAID")
		for _, d := range devs {
			if err := diskops.ZeroSuperblock(ctx, d); err != nil {
				return nil, fmt.Errorf("borrar el superbloque de %s: %w", diskops.DevName(d), err)
			}
		}
		if err := diskops.RemoveMDConfLine(a.Name); err != nil {
			log.Warn("mdadm.conf no actualizado", "err", err)
		}
	}
	step(0.8, "Borrando firmas de los discos")
	res := agentproto.VolumeDeleteResult{Wiped: []string{}}
	for i, d := range devs {
		if err := diskops.WipeMember(d); err != nil {
			return nil, fmt.Errorf("borrar firmas de %s: %w", a.DevIDs[i], err)
		}
		res.Wiped = append(res.Wiped, a.DevIDs[i])
	}
	return res, nil
}
