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
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/smart"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// registerM22 agrega las operaciones de la fase M2.2 (protocol/README.md §11):
// reemplazar un disco de un array, ampliarlo, borrar instantáneas y el LED de un disco.
func registerM22(r *agentproto.Registry, paths smart.Paths, env diskops.Env, jobs *asyncJobs, log *slog.Logger) {
	agentproto.Register(r, agentproto.OpMDReplace, validateMDReplace, func(_ context.Context, a *agentproto.MDReplaceArgs) (any, *agentproto.Error) {
		md, failedDev, e := replaceTargets(paths, env, a)
		if e != nil {
			return nil, e
		}
		if e := jobs.start(a.OpID, agentproto.OpMDReplace, a, func(ctx context.Context, step stepFn) (any, error) {
			return replaceDisk(ctx, step, paths, env, a, md, failedDev, log)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpMDGrow, validateMDGrow, func(_ context.Context, a *agentproto.MDGrowArgs) (any, *agentproto.Error) {
		md, e := growTarget(env, a)
		if e != nil {
			return nil, e
		}
		if _, e := revalidate(paths, env, a.Add); e != nil {
			return nil, e
		}
		if e := jobs.start(a.OpID, agentproto.OpMDGrow, a, func(ctx context.Context, step stepFn) (any, error) {
			return growArray(ctx, step, paths, env, a, md, log)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpSnapshotDelete, validateSnapshotDelete, func(_ context.Context, a *agentproto.SnapshotDeleteArgs) (any, *agentproto.Error) {
		if e := mountedBtrfs(env, a.Mount); e != nil {
			return nil, e
		}
		dir := filepath.Join(a.Mount, "@snapshots", a.Share)
		if _, err := os.Lstat(filepath.Join(dir, a.Name)); err != nil {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe la instantánea %s", a.Name)
		}
		if err := diskops.DestroySubvolume(dir, a.Name); err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo borrar la instantánea: %v", err)
		}
		_ = diskops.SyncShadowLinks(dir) // Versiones anteriores de Windows
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpDiskLocate, validateDiskLocate, func(_ context.Context, a *agentproto.DiskLocateArgs) (any, *agentproto.Error) {
		dev, e := paths.Resolve(a.DevID)
		if e != nil {
			return nil, e
		}
		slot := enclosureSlot(diskops.DevName(dev))
		if slot == "" {
			return nil, agentproto.Errf(agentproto.CodeNotSupported, "este equipo no tiene LEDs de identificación por bahía")
		}
		v := "0"
		if a.On {
			v = "1"
		}
		if err := os.WriteFile(filepath.Join(slot, "locate"), []byte(v), 0); err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo prender el LED: %v", err)
		}
		return struct{}{}, nil
	})
}

// enclosureSlot busca la bahía de /sys/class/enclosure cuyo device es el disco.
func enclosureSlot(disk string) string {
	want, err := filepath.EvalSymlinks(filepath.Join("/sys/block", disk, "device"))
	if err != nil {
		return ""
	}
	slots, _ := filepath.Glob("/sys/class/enclosure/*/*/device")
	for _, s := range slots {
		if got, err := filepath.EvalSymlinks(s); err == nil && got == want {
			return filepath.Dir(s)
		}
	}
	return ""
}

// replaceTargets valida el array y los discos de un reemplazo antes de aceptarlo.
func replaceTargets(paths smart.Paths, env diskops.Env, a *agentproto.MDReplaceArgs) (string, string, *agentproto.Error) {
	md, err := diskops.MDOf(a.Name)
	if err != nil {
		return "", "", agentproto.Errf(agentproto.CodeNotFound, "%v", err)
	}
	info := diskops.ReadMD(md)
	switch info.Level {
	case "raid1", "raid5", "raid6", "raid10":
	default:
		return "", "", agentproto.Errf(agentproto.CodeNotSupported, "el array %s (%s) no admite reemplazo de discos", a.Name, info.Level)
	}
	if info.Action == "reshape" {
		return "", "", agentproto.Errf(agentproto.CodeBusy, "el RAID se está reorganizando: esperá a que termine")
	}
	failedDev := ""
	if a.Failed != "" {
		dev, e := paths.Resolve(a.Failed)
		if e != nil {
			return "", "", agentproto.Errf(agentproto.CodeDeviceChanged, "%s ya no está: %s", a.Failed, e.Message)
		}
		if _, ok := info.Members[diskops.DevName(dev)]; !ok {
			return "", "", agentproto.Errf(agentproto.CodeDeviceChanged, "%s no es miembro del RAID %s", a.Failed, a.Name)
		}
		failedDev = dev
	} else if info.Degraded == 0 {
		return "", "", agentproto.Errf(agentproto.CodeInvalidArgs, "el RAID %s no está degradado: indicá qué disco reemplazar", a.Name)
	}
	repl, e := revalidate(paths, env, []agentproto.VolumeDisk{a.Replacement})
	if e != nil {
		return "", "", e
	}
	if _, member := info.Members[diskops.DevName(repl[0])]; member {
		return "", "", agentproto.Errf(agentproto.CodeNotEligible, "%s ya es miembro del RAID", a.Replacement.DevID)
	}
	return md, failedDev, nil
}

func replaceDisk(ctx context.Context, step stepFn, paths smart.Paths, env diskops.Env, a *agentproto.MDReplaceArgs, md, failedDev string, log *slog.Logger) (any, error) {
	res := agentproto.MDReplaceResult{}
	if failedDev != "" {
		state := diskops.ReadMD(md).Members[diskops.DevName(failedDev)]
		if !strings.Contains(state, "faulty") {
			step(0.05, "Marcando el disco como fallado")
			if err := diskops.MDManage(ctx, a.Name, "fail", failedDev); err != nil {
				return nil, fmt.Errorf("marcar como fallado: %w", err)
			}
		}
		step(0.1, "Sacando el disco del RAID")
		var err error
		for range 20 { // md puede tardar en soltarlo después del fail
			if err = diskops.MDManage(ctx, a.Name, "remove", failedDev); err == nil {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if err != nil {
			return nil, fmt.Errorf("sacar el disco: %w", err)
		}
		res.Removed = a.Failed
		// Que el disco viejo no parezca miembro de nada: si responde, se le borran las
		// firmas (si está muerto de verdad, no importa).
		if err := diskops.WipeMember(failedDev); err != nil {
			log.Info("no se pudieron borrar las firmas del disco reemplazado", "dev", failedDev, "err", err)
		} else {
			res.WipedFailed = true
		}
	}

	// Revalidar el reemplazo justo antes de escribirlo.
	devs, e := revalidate(paths, env, []agentproto.VolumeDisk{a.Replacement})
	if e != nil {
		return nil, e
	}
	step(0.15, "Borrando firmas del disco nuevo")
	if err := diskops.Wipe(devs[0], a.Replacement.Fingerprint); err != nil {
		if errors.Is(err, diskops.ErrChanged) {
			return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "%s cambió desde el plan", a.Replacement.DevID)
		}
		return nil, fmt.Errorf("borrar firmas: %w", err)
	}
	step(0.2, "Agregando el disco nuevo al RAID")
	if err := diskops.MDManage(ctx, a.Name, "add", devs[0]); err != nil {
		return nil, fmt.Errorf("agregar el disco: %w", err)
	}
	res.Added = a.Replacement.DevID

	// Esperar la reconstrucción (recover). Si en 15 s md no empezó y sigue degradado,
	// algo anda mal (el disco quedó como spare de más, por ejemplo).
	idleSince := time.Now()
	for {
		in := diskops.ReadMD(md)
		switch {
		case in.Action == "recover" || in.Action == "resync":
			idleSince = time.Now()
			step(0.2+0.8*in.Progress, "Reconstruyendo el RAID")
		case in.Degraded == 0 && in.Action == "idle":
			step(1, "RAID sano")
			return res, nil
		case time.Since(idleSince) > 15*time.Second:
			return nil, fmt.Errorf("md no empezó a reconstruir (estado %s, degradado %d)", in.Action, in.Degraded)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// growTarget valida el array y su montaje antes de aceptar una ampliación.
func growTarget(env diskops.Env, a *agentproto.MDGrowArgs) (string, *agentproto.Error) {
	md, err := diskops.MDOf(a.Name)
	if err != nil {
		return "", agentproto.Errf(agentproto.CodeNotFound, "%v", err)
	}
	info := diskops.ReadMD(md)
	switch info.Level {
	case "raid5", "raid6", "raid10":
	default:
		return "", agentproto.Errf(agentproto.CodeNotSupported, "solo se amplían RAID 5, 6 y 10 agregando discos (%s es %s)", a.Name, info.Level)
	}
	if info.Degraded > 0 || info.Action != "idle" {
		return "", agentproto.Errf(agentproto.CodeBusy, "el RAID tiene que estar sano y sin tareas en curso (%s)", info.Action)
	}
	if a.RaidDevices != info.RaidDisks+len(a.Add) {
		return "", agentproto.Errf(agentproto.CodeInvalidArgs, "el RAID tiene %d discos: con %d nuevos quedan %d", info.RaidDisks, len(a.Add), info.RaidDisks+len(a.Add))
	}
	if info.Level == "raid10" && a.RaidDevices%2 != 0 {
		return "", agentproto.Errf(agentproto.CodeInvalidArgs, "RAID 10 necesita una cantidad par de discos")
	}
	src, fstype, ok := env.MountSource(a.Mount)
	if !ok || fstype != "btrfs" {
		return "", agentproto.Errf(agentproto.CodeNotFound, "%s no es un volumen Btrfs montado", a.Mount)
	}
	if real, err := filepath.EvalSymlinks(src); err != nil || filepath.Base(real) != md {
		return "", agentproto.Errf(agentproto.CodeDeviceChanged, "%s no está sobre el RAID %s", a.Mount, a.Name)
	}
	return md, nil
}

func growArray(ctx context.Context, step stepFn, paths smart.Paths, env diskops.Env, a *agentproto.MDGrowArgs, md string, log *slog.Logger) (any, error) {
	devs, e := revalidate(paths, env, a.Add)
	if e != nil {
		return nil, e
	}
	before := diskops.ReadMD(md).SizeBytes
	step(0.03, "Borrando firmas de los discos nuevos")
	for i, dev := range devs {
		if err := diskops.Wipe(dev, a.Add[i].Fingerprint); err != nil {
			if errors.Is(err, diskops.ErrChanged) {
				return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "%s cambió desde el plan", a.Add[i].DevID)
			}
			return nil, fmt.Errorf("borrar firmas de %s: %w", a.Add[i].DevID, err)
		}
	}
	step(0.06, "Agregando los discos al RAID")
	for i, dev := range devs {
		if err := diskops.MDManage(ctx, a.Name, "add", dev); err != nil {
			return nil, fmt.Errorf("agregar %s: %w", a.Add[i].DevID, err)
		}
	}
	step(0.08, "Iniciando la reorganización")
	if err := diskops.MDGrow(ctx, a.Name, a.RaidDevices, ""); err != nil {
		// Arrays sin espacio para mover el data offset: mdadm pide un archivo de respaldo.
		if strings.Contains(strings.ToLower(execOutput(err)), "backup") {
			log.Info("mdadm pide archivo de respaldo para el reshape", "name", a.Name)
			backup := filepath.Join("/var/lib/tango-agent", "md-grow-"+a.Name+".bak")
			if err = diskops.MDGrow(ctx, a.Name, a.RaidDevices, backup); err == nil {
				defer os.Remove(backup)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("ampliar el RAID: %s", strings.TrimSpace(firstLine(execOutput(err), err)))
		}
	}

	// Reshape: md mueve todos los datos a la nueva geometría. Al terminar, el kernel
	// agranda el array.
	seen := false
	start := time.Now()
	for {
		in := diskops.ReadMD(md)
		if in.Action == "reshape" {
			seen = true
			step(0.1+0.8*in.Progress, "Reorganizando el RAID (reshape)")
		} else if in.Action == "idle" && (seen || in.RaidDisks == a.RaidDevices) && in.SizeBytes > before {
			break
		} else if in.Action == "idle" && time.Since(start) > 2*time.Minute {
			return nil, fmt.Errorf("el reshape no avanzó (discos %d, tamaño %d)", in.RaidDisks, in.SizeBytes)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	step(0.92, "Agrandando el sistema de archivos")
	if err := diskops.BtrfsResizeMax(a.Mount); err != nil {
		return nil, fmt.Errorf("agrandar Btrfs: %w", err)
	}
	res := agentproto.MDGrowResult{RaidDevices: a.RaidDevices, ArrayBytes: diskops.ReadMD(md).SizeBytes}
	res.FSBytes, _ = fsSize(a.Mount)
	return res, nil
}

func firstLine(out string, err error) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return err.Error()
}
