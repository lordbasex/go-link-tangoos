//go:build linux

package main

// ZFS (protocol/README.md §18, ADR 0013). Las lecturas (zpool status/list, zfs list)
// corren acá; todo lo que crea, monta o desmonta corre en una unidad transitoria
// ("zfs-*" en runM4Helper), en el namespace de montaje del sistema: un mount desde el
// namespace del agente no llegaría a smbd ni a los demás. Las frases de cifrado nunca
// salen del agente: zfs create -u / load-key / change-key las leen por stdin acá y la
// unidad transitoria solo monta.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/smart"
	"github.com/lordbasex/go-link-tangoos/internal/zfs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// zfsAvailable: zfsutils instalado y el módulo cargado.
func zfsInstalled() bool { return fileExists(zfs.ZpoolBin) && fileExists(zfs.ZfsBin) }
func zfsAvailable() bool { return zfsInstalled() && fileExists("/dev/zfs") }

// zrun corre zpool/zfs con argv armado por internal/zfs. stdin solo para frases.
func zrun(ctx context.Context, bin string, stdin []byte, args ...string) ([]byte, error) {
	if bin != zfs.ZpoolBin && bin != zfs.ZfsBin {
		return nil, errors.New("binario no permitido")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return out.Bytes(), fmt.Errorf("%s %s: %s", filepath.Base(bin), args[0], firstLineOf(msg))
	}
	return out.Bytes(), nil
}

// zfsResolve: /dev/disk/by-id/…-part1 → disco entero del kernel (sdb).
func zfsResolve(p string) string {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	name := filepath.Base(real)
	if _, err := os.Stat(filepath.Join("/sys/class/block", name, "partition")); err == nil {
		// /sys/class/block/sdj1 → /sys/devices/…/block/sdj/sdj1: el padre es el disco.
		// (EvalSymlinks primero: filepath.Join(…, "..") es léxico.)
		if real, err := filepath.EvalSymlinks(filepath.Join("/sys/class/block", name)); err == nil {
			return filepath.Base(filepath.Dir(real))
		}
	}
	return name
}

// zfsReport arma el estado completo. importable=true además busca pools sin importar
// (lee todos los discos: solo cuando la UI lo pide).
func zfsReport(ctx context.Context, importable bool) (*agentproto.ZfsReport, error) {
	rep := &agentproto.ZfsReport{Installed: zfsInstalled(), Available: zfsAvailable(),
		Pools: []agentproto.ZfsPoolInfo{}, Importable: []agentproto.ZfsImportable{}}
	if !rep.Available {
		return rep, nil
	}
	if v, err := os.ReadFile("/sys/module/zfs/version"); err == nil {
		rep.Version = strings.TrimSpace(string(v))
	}
	st, err := zrun(ctx, zfs.ZpoolBin, nil, "status", "-j", "-p")
	if err != nil {
		return nil, err
	}
	pools, err := zfs.ParseStatus(st, zfsResolve)
	if err != nil {
		return nil, err
	}
	if len(pools) > 0 {
		ls, err := zrun(ctx, zfs.ZpoolBin, nil, "list", "-j", "-p", "-o", "name,size,allocated,free,fragmentation,readonly")
		if err != nil {
			return nil, err
		}
		if err := zfs.MergeList(pools, ls); err != nil {
			return nil, err
		}
		ds, err := zrun(ctx, zfs.ZfsBin, nil, "list", "-j", "-p", "-t", "filesystem,volume", "-o", zfs.DatasetProps)
		if err != nil {
			return nil, err
		}
		byPool, err := zfs.ParseDatasets(ds)
		if err != nil {
			return nil, err
		}
		for i := range pools {
			if d := byPool[pools[i].Name]; d != nil {
				pools[i].Datasets = d
			}
		}
	}
	rep.Pools = pools
	if importable {
		out, _ := zrun(ctx, zfs.ZpoolBin, nil, "import", "-d", "/dev/disk/by-id")
		rep.Importable = zfs.ParseImport(string(out), zfsResolve)
	}
	return rep, nil
}

// zfsCache: el inventario pregunta seguido (disks.list, volumes.list).
var zfsCache struct {
	sync.Mutex
	at  time.Time
	rep *agentproto.ZfsReport
}

func zfsReportCached(ctx context.Context) (*agentproto.ZfsReport, error) {
	zfsCache.Lock()
	defer zfsCache.Unlock()
	if zfsCache.rep != nil && time.Since(zfsCache.at) < 2*time.Second {
		return zfsCache.rep, nil
	}
	rep, err := zfsReport(ctx, false)
	if err != nil {
		return nil, err
	}
	zfsCache.rep, zfsCache.at = rep, time.Now()
	return rep, nil
}

func zfsInvalidate() {
	zfsCache.Lock()
	zfsCache.rep = nil
	zfsCache.Unlock()
}

func zfsPool(ctx context.Context, name string) (*agentproto.ZfsPoolInfo, *agentproto.Error) {
	rep, err := zfsReport(ctx, false)
	if err != nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
	}
	for i := range rep.Pools {
		if rep.Pools[i].Name == name {
			return &rep.Pools[i], nil
		}
	}
	return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe el pool %s", name)
}

func dataset(p *agentproto.ZfsPoolInfo, name string) *agentproto.ZfsDataset {
	for i := range p.Datasets {
		if p.Datasets[i].Name == name {
			return &p.Datasets[i]
		}
	}
	return nil
}

// zfsVolumeOf dice si un punto de montaje de TangoOS es un volumen ZFS y su dataset.
func zfsVolumeOf(env diskops.Env, mount string) (string, bool) {
	src, fstype, ok := env.MountSource(mount)
	if !ok || fstype != "zfs" {
		return "", false
	}
	pool, vol, ok := strings.Cut(src, "/")
	if !ok || zfs.ValidPoolName(pool) != nil || "/"+vol != mount {
		return "", false
	}
	return src, true
}

func zerr(err error) *agentproto.Error {
	if err == nil {
		return nil
	}
	var ae *agentproto.Error
	if errors.As(err, &ae) {
		return ae
	}
	return agentproto.Errf(agentproto.CodeFailed, "%v", err)
}

// zfsHelper corre una tarea "zfs-*" en una unidad transitoria y espera.
func zfsHelper(ctx context.Context, task string, args any) error {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return runHelperJSON(ctx, func(float64, string) {}, task, "zfs-"+hex.EncodeToString(b[:]), args)
}

func registerZFS(r *agentproto.Registry, paths smart.Paths, env diskops.Env, jobs *asyncJobs, log *slog.Logger) {
	need := func() *agentproto.Error {
		if !zfsAvailable() {
			return agentproto.Errf(agentproto.CodeZfsUnavailable, "ZFS no está habilitado en este equipo")
		}
		return nil
	}
	async := func(op, task string, a any, opID string) (any, *agentproto.Error) {
		if e := jobs.start(opID, op, map[string]any{"task": task}, func(ctx context.Context, step stepFn) (any, error) {
			defer zfsInvalidate()
			return nil, runHelperJSON(ctx, step, task, opID, a)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: opID}, nil
	}

	agentproto.Register(r, agentproto.OpZfsReport, nil, func(ctx context.Context, a *struct {
		Importable bool `json:"importable"`
	}) (any, *agentproto.Error) {
		var rep *agentproto.ZfsReport
		var err error
		if a.Importable {
			rep, err = zfsReport(ctx, true)
		} else {
			rep, err = zfsReportCached(ctx)
		}
		if err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
		}
		return rep, nil
	})

	agentproto.Register(r, agentproto.OpZfsEnable, nil, func(_ context.Context, a *agentproto.ZfsEnableArgs) (any, *agentproto.Error) {
		return async(agentproto.OpZfsEnable, "zfs-enable", a, a.OpID)
	})

	agentproto.Register(r, agentproto.OpZfsCreate, validateZfsCreate, func(ctx context.Context, a *agentproto.ZfsPoolCreateArgs) (any, *agentproto.Error) {
		if e := need(); e != nil {
			return nil, e
		}
		if _, e := revalidate(paths, env, a.Disks); e != nil {
			return nil, e
		}
		if _, e := zfsPool(ctx, a.Name); e == nil {
			return nil, agentproto.Errf(agentproto.CodeExists, "ya existe el pool %s", a.Name)
		}
		return async(agentproto.OpZfsCreate, "zfs-create", a, a.OpID)
	})

	agentproto.Register(r, agentproto.OpZfsAdd, func(a *agentproto.ZfsPoolAddArgs) *agentproto.Error {
		return validateZfsVdev(a.Name, a.Layout, a.Disks)
	}, func(ctx context.Context, a *agentproto.ZfsPoolAddArgs) (any, *agentproto.Error) {
		if _, e := zfsPool(ctx, a.Name); e != nil {
			return nil, e
		}
		if _, e := revalidate(paths, env, a.Disks); e != nil {
			return nil, e
		}
		return async(agentproto.OpZfsAdd, "zfs-add", a, a.OpID)
	})

	agentproto.Register(r, agentproto.OpZfsAux, validateZfsAux, func(ctx context.Context, a *agentproto.ZfsAuxArgs) (any, *agentproto.Error) {
		if _, e := zfsPool(ctx, a.Name); e != nil {
			return nil, e
		}
		if _, e := revalidate(paths, env, a.Disks); e != nil {
			return nil, e
		}
		return async(agentproto.OpZfsAux, "zfs-aux", a, a.OpID)
	})

	agentproto.Register(r, agentproto.OpZfsReplace, func(a *agentproto.ZfsReplaceArgs) *agentproto.Error {
		if err := zfs.ValidPoolName(a.Name); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		if err := zfs.ValidVdevName(a.Old); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		return nil
	}, func(ctx context.Context, a *agentproto.ZfsReplaceArgs) (any, *agentproto.Error) {
		pi, e := zfsPool(ctx, a.Name)
		if e != nil {
			return nil, e
		}
		if !hasLeaf(pi, a.Old) {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "%s no es un disco de %s", a.Old, a.Name)
		}
		if _, e := revalidate(paths, env, []agentproto.VolumeDisk{a.New}); e != nil {
			return nil, e
		}
		return async(agentproto.OpZfsReplace, "zfs-replace", a, a.OpID)
	})

	agentproto.Register(r, agentproto.OpZfsScrub, func(a *agentproto.ZfsScrubArgs) *agentproto.Error {
		if err := zfs.ValidPoolName(a.Name); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		return nil
	}, func(ctx context.Context, a *agentproto.ZfsScrubArgs) (any, *agentproto.Error) {
		args, _ := zfs.ArgsScrub(a.Name, a.Stop)
		if _, err := zrun(ctx, zfs.ZpoolBin, nil, args...); err != nil {
			return nil, zerr(err)
		}
		zfsInvalidate()
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpZfsImport, func(a *agentproto.ZfsImportArgs) *agentproto.Error {
		if zfs.ValidPoolName(a.Pool) != nil && zfs.ValidGUID(a.Pool) != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "pool inválido")
		}
		return nil
	}, func(_ context.Context, a *agentproto.ZfsImportArgs) (any, *agentproto.Error) {
		if e := need(); e != nil {
			return nil, e
		}
		return async(agentproto.OpZfsImport, "zfs-import", a, a.OpID)
	})

	poolOp := func(op, task string) {
		agentproto.Register(r, op, func(a *agentproto.ZfsPoolArgs) *agentproto.Error {
			if err := zfs.ValidPoolName(a.Name); err != nil {
				return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
			}
			return nil
		}, func(ctx context.Context, a *agentproto.ZfsPoolArgs) (any, *agentproto.Error) {
			if _, e := zfsPool(ctx, a.Name); e != nil {
				return nil, e
			}
			return async(op, task, a, a.OpID)
		})
	}
	poolOp(agentproto.OpZfsExport, "zfs-export")
	poolOp(agentproto.OpZfsDestroy, "zfs-destroy")

	agentproto.Register(r, agentproto.OpZfsVolCreate, func(a *agentproto.ZfsVolCreateArgs) *agentproto.Error {
		if err := zfs.ValidPoolName(a.Pool); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		if err := zfs.ValidVolumeName(a.Name); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		if a.Passphrase != "" && (len(a.Passphrase) < 8 || len(a.Passphrase) > 512) {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "la frase tiene que tener entre 8 y 512 caracteres")
		}
		return nil
	}, func(ctx context.Context, a *agentproto.ZfsVolCreateArgs) (any, *agentproto.Error) {
		pi, e := zfsPool(ctx, a.Pool)
		if e != nil {
			return nil, e
		}
		if dataset(pi, zfs.VolumeDataset(a.Pool, a.Name)) != nil {
			return nil, agentproto.Errf(agentproto.CodeExists, "ya existe el volumen %s", a.Name)
		}
		if _, _, ok := env.MountSource("/" + a.Name); ok || fileExists("/"+a.Name) && !dirEmpty("/"+a.Name) {
			return nil, agentproto.Errf(agentproto.CodeExists, "/%s ya está en uso", a.Name)
		}
		// El cifrado se crea acá (la frase por stdin, sin montar); la unidad transitoria
		// monta y arma @shares/@apps/@snapshots. Lo que viaja al helper no lleva la frase.
		pass := a.Passphrase
		helperArgs := *a
		helperArgs.Passphrase = ""
		if pass != "" {
			args, err := zfs.ArgsVolumeCreate(a.Pool, a.Name, zfs.VolumeOpts{Quota: a.Quota, Reservation: a.Reservation, Encrypted: true})
			if err != nil {
				return nil, agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
			}
			if _, err := zrun(ctx, zfs.ZfsBin, []byte(pass+"\n"), args...); err != nil {
				return nil, zerr(err)
			}
		}
		return async(agentproto.OpZfsVolCreate, "zfs-volcreate", &helperArgs, a.OpID)
	})

	agentproto.Register(r, agentproto.OpZfsVolSet, func(a *agentproto.ZfsVolSetArgs) *agentproto.Error {
		return validZfsVol(a.Pool, a.Name)
	}, func(ctx context.Context, a *agentproto.ZfsVolSetArgs) (any, *agentproto.Error) {
		ds := zfs.VolumeDataset(a.Pool, a.Name)
		if _, err := zrun(ctx, zfs.ZfsBin, nil, zfs.ArgsSetSize(ds, a.Quota, a.Reservation)...); err != nil {
			return nil, zerr(err)
		}
		zfsInvalidate()
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpZfsVolDelete, func(a *agentproto.ZfsVolArgs) *agentproto.Error {
		return validZfsVol(a.Pool, a.Name)
	}, func(ctx context.Context, a *agentproto.ZfsVolArgs) (any, *agentproto.Error) {
		pi, e := zfsPool(ctx, a.Pool)
		if e != nil {
			return nil, e
		}
		if dataset(pi, zfs.VolumeDataset(a.Pool, a.Name)) == nil {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe el volumen %s", a.Name)
		}
		return async(agentproto.OpZfsVolDelete, "zfs-voldelete", a, a.OpID)
	})

	agentproto.Register(r, agentproto.OpZfsUnlock, func(a *agentproto.ZfsUnlockArgs) *agentproto.Error {
		return validZfsVol(a.Pool, a.Name)
	}, func(ctx context.Context, a *agentproto.ZfsUnlockArgs) (any, *agentproto.Error) {
		ds := zfs.VolumeDataset(a.Pool, a.Name)
		if _, err := zrun(ctx, zfs.ZfsBin, []byte(a.Passphrase+"\n"), "load-key", ds); err != nil {
			if strings.Contains(err.Error(), "already loaded") {
				// sigue: puede faltar montar
			} else if strings.Contains(err.Error(), "Incorrect key") || strings.Contains(err.Error(), "incorrect") {
				return nil, agentproto.Errf(agentproto.CodeWrongPassphrase, "la frase no es correcta")
			} else {
				log.Warn("zfs load-key", "volume", a.Name, "err", firstLineOf(err.Error()))
				return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo desbloquear el volumen")
			}
		}
		zfsInvalidate()
		if err := zfsHelper(ctx, "zfs-mount", &agentproto.ZfsVolArgs{Pool: a.Pool, Name: a.Name}); err != nil {
			return nil, zerr(err)
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpZfsLock, func(a *agentproto.ZfsVolArgs) *agentproto.Error {
		return validZfsVol(a.Pool, a.Name)
	}, func(ctx context.Context, a *agentproto.ZfsVolArgs) (any, *agentproto.Error) {
		if err := zfsHelper(ctx, "zfs-unmount", a); err != nil {
			return nil, zerr(err)
		}
		if _, err := zrun(ctx, zfs.ZfsBin, nil, "unload-key", zfs.VolumeDataset(a.Pool, a.Name)); err != nil && !strings.Contains(err.Error(), "already unloaded") {
			return nil, zerr(err)
		}
		zfsInvalidate()
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpZfsPassphrase, func(a *agentproto.ZfsPassphraseArgs) *agentproto.Error {
		if e := validZfsVol(a.Pool, a.Name); e != nil {
			return e
		}
		if len(a.New) < 8 || len(a.New) > 512 {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "la frase nueva tiene que tener entre 8 y 512 caracteres")
		}
		return nil
	}, func(ctx context.Context, a *agentproto.ZfsPassphraseArgs) (any, *agentproto.Error) {
		ds := zfs.VolumeDataset(a.Pool, a.Name)
		// Verificar la vieja sin tocar nada: load-key -n (dry run).
		if _, err := zrun(ctx, zfs.ZfsBin, []byte(a.Old+"\n"), "load-key", "-n", ds); err != nil && !strings.Contains(err.Error(), "already loaded") {
			return nil, agentproto.Errf(agentproto.CodeWrongPassphrase, "la frase actual no es correcta")
		}
		if _, err := zrun(ctx, zfs.ZfsBin, []byte(a.New+"\n"), "change-key", "-o", "keyformat=passphrase", "-o", "keylocation=prompt", ds); err != nil {
			log.Warn("zfs change-key", "volume", a.Name, "err", firstLineOf(err.Error()))
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo cambiar la frase")
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpZfsZvolCreate, func(a *agentproto.ZfsZvolArgs) *agentproto.Error {
		if _, err := zfs.ArgsZvolCreate(a.Pool, a.Name, a.Size, a.Sparse); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		return nil
	}, func(ctx context.Context, a *agentproto.ZfsZvolArgs) (any, *agentproto.Error) {
		if err := zfsHelper(ctx, "zfs-zvol", a); err != nil {
			return nil, zerr(err)
		}
		zfsInvalidate()
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpZfsZvolDelete, func(a *agentproto.ZfsZvolArgs) *agentproto.Error {
		if err := zfs.ValidPoolName(a.Pool); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		if err := zfs.ValidZvolName(a.Name); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		return nil
	}, func(ctx context.Context, a *agentproto.ZfsZvolArgs) (any, *agentproto.Error) {
		a.Size = 0
		if err := zfsHelper(ctx, "zfs-zvoldel", a); err != nil {
			return nil, zerr(err)
		}
		zfsInvalidate()
		return struct{}{}, nil
	})
}

func dirEmpty(p string) bool {
	ents, err := os.ReadDir(p)
	return err == nil && len(ents) == 0
}

func hasLeaf(p *agentproto.ZfsPoolInfo, name string) bool {
	for _, v := range append(append([]agentproto.ZfsVdev{}, p.Vdevs...), p.Logs...) {
		for _, d := range v.Disks {
			if d.Name == name || d.GUID == name {
				return true
			}
		}
	}
	return false
}

// ---- Validación ------------------------------------------------------------------------

func validZfsVol(pool, name string) *agentproto.Error {
	if err := zfs.ValidPoolName(pool); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if err := zfs.ValidVolumeName(name); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	return nil
}

func validateZfsVdev(pool, layout string, disks []agentproto.VolumeDisk) *agentproto.Error {
	if err := zfs.ValidPoolName(pool); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if err := zfs.CheckLayout(layout, len(disks)); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	seen := map[string]bool{}
	for _, d := range disks {
		if err := zfs.ValidByID(d.DevID); err != nil || seen[d.DevID] {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "disco inválido o repetido")
		}
		seen[d.DevID] = true
	}
	return nil
}

func validateZfsCreate(a *agentproto.ZfsPoolCreateArgs) *agentproto.Error {
	return validateZfsVdev(a.Name, a.Layout, a.Disks)
}

func validateZfsAux(a *agentproto.ZfsAuxArgs) *agentproto.Error {
	if err := zfs.ValidPoolName(a.Name); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if a.Kind != "cache" && a.Kind != "log" {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "tipo inválido")
	}
	if (len(a.Disks) == 0) == (len(a.Remove) == 0) {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "o se agregan discos o se quitan, no las dos cosas")
	}
	if len(a.Disks) > 2 || len(a.Remove) > 2 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "uno o dos dispositivos")
	}
	for _, d := range a.Disks {
		if err := zfs.ValidByID(d.DevID); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
	}
	for _, v := range a.Remove {
		if err := zfs.ValidVdevName(v); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
	}
	return nil
}
