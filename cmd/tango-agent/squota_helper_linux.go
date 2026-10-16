//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Cuotas por carpeta con simple-quota (docs/adr/0015-gluster-simple-quota.md). Corre como
// ayudante (root, fuera del sandbox): monta el volumen como ayudante de cuotas para leer
// y poner límites, y cuenta en los bricks de este nodo lo que ya hay en una carpeta.

const (
	squotaRun      = "/run/tango/squota"
	glusterdInfo   = "/var/lib/glusterd/glusterd.info"
	glusterVolsRun = "/var/run/gluster/vols"
	fuseMagic      = 0x65735546
)

func helperSquota(ctx context.Context, a *agentproto.GlusterQuotaArgs) error {
	v, err := squotaVolume(ctx, a.Name, false)
	if err != nil {
		return err
	}
	limits := gluster.ParseQuotaOption(v.Options[gluster.QuotaOption])
	var res gluster.QuotaResult
	if a.Op == "count" {
		if err := squotaCount(ctx, v, pick(limits, a.Path), a.Restart); err != nil {
			return err
		}
	} else {
		err = withQuotaMount(ctx, a.Name, func(mnt string) error {
			var err error
			switch a.Op {
			case "limit":
				if res.NonEmpty, err = squotaLimit(mnt, a.Path, a.Limit); err != nil {
					return err
				}
				limits[a.Path] = a.Limit
				err = saveLimits(ctx, a.Name, limits)
			case "remove":
				if _, ok := limits[a.Path]; !ok {
					return fmt.Errorf("%s no tiene límite", a.Path)
				}
				if err = squotaRemove(mnt, a.Path); err != nil && !errors.Is(err, errNoFolder) {
					return err
				}
				delete(limits, a.Path)
				err = saveLimits(ctx, a.Name, limits)
			case "reset":
				err = squotaReset(mnt, pick(limits, a.Path))
			}
			if err != nil {
				return err
			}
			res.Quotas, err = squotaList(mnt, limits, v.Sets > 1)
			return err
		})
		if err != nil {
			return err
		}
	}
	b, _ := json.Marshal(res)
	fmt.Printf("RESULT %s\n", b)
	return nil
}

// pick: solo la carpeta pedida (si tiene límite) o todas.
func pick(limits map[string]uint64, p string) map[string]uint64 {
	if p == "" {
		return limits
	}
	if l, ok := limits[p]; ok {
		return map[string]uint64{p: l}
	}
	return map[string]uint64{}
}

// saveLimits guarda los límites en la opción del volumen (gluster.QuotaOption). Sin
// límites queda "-": GlusterFS no deja hacer reset de una opción user.*.
func saveLimits(ctx context.Context, vol string, limits map[string]uint64) error {
	v := gluster.FormatQuotaOption(limits)
	if v == "" {
		v = "-"
	}
	return glusterDo(ctx, []string{"volume", "set", vol, gluster.QuotaOption, v})
}

// squotaVolume lee el volumen; con status, también qué bricks están en línea.
func squotaVolume(ctx context.Context, name string, status bool) (*gluster.Volume, error) {
	info, code, err := runGluster(ctx, "volume", "info", name)
	if err != nil {
		return nil, err
	}
	if err := gluster.ParseResult(info, code); err != nil {
		return nil, err
	}
	var st []byte
	if status {
		st, _, _ = runGluster(ctx, "volume", "status", name, "detail")
	}
	vs, err := gluster.ParseVolumes(info, st)
	if err != nil || len(vs) != 1 {
		return nil, fmt.Errorf("no se pudo leer el volumen %s: %v", name, err)
	}
	return &vs[0], nil
}

// withQuotaMount monta el volumen como ayudante de cuotas en /run/tango/squota/<vol>
// (uno a la vez por volumen) y lo desmonta al terminar.
func withQuotaMount(ctx context.Context, vol string, fn func(mnt string) error) error {
	if err := os.MkdirAll(squotaRun, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(squotaRun, vol+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	mnt := filepath.Join(squotaRun, vol)
	_ = unix.Unmount(mnt, unix.MNT_DETACH) // uno que quedó de una corrida cortada
	if err := os.MkdirAll(mnt, 0o700); err != nil {
		return err
	}
	argv := gluster.QuotaHelperMount(vol, mnt)
	if out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput(); err != nil {
		return fmt.Errorf("no se pudo montar %s para las cuotas: %v %s", vol, err, strings.TrimSpace(string(out)))
	}
	defer func() {
		if unix.Unmount(mnt, 0) != nil {
			_ = unix.Unmount(mnt, unix.MNT_DETACH)
		}
		_ = os.Remove(mnt)
	}()
	for i := 0; ; i++ {
		var st unix.Statfs_t
		if unix.Statfs(mnt, &st) == nil && st.Type == fuseMagic {
			// el FUSE responde antes de tener los bricks: se espera a ver la raíz
			if _, err := os.ReadDir(mnt); err == nil {
				break
			}
		}
		if i == 40 {
			return fmt.Errorf("el montaje de %s para las cuotas no respondió", vol)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fn(mnt)
}

// quotaDir es la carpeta de primer nivel dentro del montaje (o del brick).
func quotaDir(root, p string) (string, error) {
	if err := gluster.ValidQuotaPath(p); err != nil {
		return "", err
	}
	d := filepath.Join(root, p)
	fi, err := os.Lstat(d)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%w: %s", errNoFolder, p)
	}
	return d, nil
}

var errNoFolder = errors.New("la carpeta no existe en el volumen")

func setXattrInt(path, key string, v int64) error {
	if err := unix.Setxattr(path, key, []byte(strconv.FormatInt(v, 10)), 0); err != nil {
		return fmt.Errorf("%s en %s: %w", key, path, err)
	}
	return nil
}

func getXattrInt(path, key string) int64 {
	buf := make([]byte, 64)
	n, err := unix.Getxattr(path, key, buf)
	if err != nil {
		return 0
	}
	v, _ := gluster.ParseXattrInt(buf[:n])
	return v
}

// squotaLimit hace de la carpeta un namespace con su límite. Devuelve si ya tenía
// contenido: lo que hay se cuenta aparte (count) y los bricks tienen que reiniciar
// para que lo que ya tenían abierto adentro cuente para el namespace nuevo.
func squotaLimit(mnt, p string, limit uint64) (bool, error) {
	d, err := quotaDir(mnt, p)
	if err != nil {
		return false, err
	}
	if err := unix.Setxattr(d, gluster.NamespaceXattr, []byte("true"), 0); err != nil {
		return false, fmt.Errorf("no se pudo marcar %s: %w", p, err)
	}
	if err := setXattrInt(d, gluster.QuotaLimitXattr, int64(limit)); err != nil {
		return false, err
	}
	f, err := os.Open(d)
	if err != nil {
		return false, err
	}
	defer f.Close()
	names, _ := f.Readdirnames(1)
	return len(names) > 0, nil
}

// squotaRemove quita el límite: en memoria uno inalcanzable (simple-quota ignora el 0
// hasta que el brick reinicia) y en disco 0.
func squotaRemove(mnt, p string) error {
	d, err := quotaDir(mnt, p)
	if err != nil {
		return err
	}
	if err := setXattrInt(d, gluster.QuotaLimitXattr, gluster.QuotaNoLimit); err != nil {
		return err
	}
	return setXattrInt(d, gluster.QuotaLimitXattr, 0)
}

// squotaReset hace que cada brick vuelva a leer de su disco el uso de esas carpetas.
func squotaReset(mnt string, limits map[string]uint64) error {
	for p := range limits {
		d, err := quotaDir(mnt, p)
		if err != nil {
			continue
		}
		if err := unix.Setxattr(d, gluster.QuotaResetXattr, []byte("1"), 0); err != nil {
			return fmt.Errorf("no se pudo recontar %s: %w", p, err)
		}
	}
	return nil
}

// squotaList lee el uso de cada carpeta con límite. Antes vuelve a marcar el namespace y
// el límite: a los bricks que se agregaron después no les llegan. El statfs de un
// namespace devuelve su límite y su uso sumado entre bricks; hecho por el ayudante, además
// cada brick guarda su uso en disco. En un volumen distribuido, cada brick recibe el total
// (sin eso, cada conjunto frenaría recién en el límite entero).
func squotaList(mnt string, limits map[string]uint64, distributed bool) ([]gluster.Quota, error) {
	out := []gluster.Quota{}
	for p, limit := range limits {
		d, err := quotaDir(mnt, p)
		if err != nil {
			out = append(out, gluster.NewQuota(p, limit, 0)) // la carpeta ya no está
			continue
		}
		// EPERM: este montaje ya tiene entradas de la carpeta (la acaba de marcar squotaLimit
		// y la leyó); FUSE no deja marcar una carpeta con entradas a la vista.
		if err := unix.Setxattr(d, gluster.NamespaceXattr, []byte("true"), 0); err != nil && !errors.Is(err, unix.EPERM) {
			return nil, fmt.Errorf("no se pudo marcar %s: %w", p, err)
		}
		if err := setXattrInt(d, gluster.QuotaLimitXattr, int64(limit)); err != nil {
			return nil, err
		}
		var st unix.Statfs_t
		if err := unix.Statfs(d, &st); err != nil {
			return nil, fmt.Errorf("uso de %s: %w", p, err)
		}
		used := (st.Blocks - st.Bfree) * uint64(st.Bsize)
		if distributed {
			if err := setXattrInt(d, gluster.QuotaTotalXattr, int64(used)); err != nil {
				return nil, err
			}
		}
		out = append(out, gluster.NewQuota(p, limit, used))
	}
	slices.SortFunc(out, func(a, b gluster.Quota) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// squotaCount cuenta, en los bricks de este nodo, lo que ya ocupa la carpeta (o cada
// carpeta con límite) y lo deja como su uso en disco. Con restart, antes reinicia esos
// bricks: lo que tenían abierto adentro de una carpeta recién marcada sigue contando para
// la raíz hasta que reinician.
func squotaCount(ctx context.Context, v *gluster.Volume, limits map[string]uint64, restart bool) error {
	b, err := os.ReadFile(glusterdInfo)
	if err != nil {
		return err
	}
	self := gluster.NodeUUID(b)
	var bricks []string
	for _, br := range v.Bricks {
		if br.HostUUID == self && self != "" {
			bricks = append(bricks, br.Path)
		}
	}
	if len(bricks) == 0 {
		return nil
	}
	if restart {
		stage(0.2, "Reiniciando los bricks de "+v.Name+" en este nodo")
		if err := restartLocalBricks(ctx, v.Name, self); err != nil {
			return err
		}
	}
	stage(0.7, "Contando lo que ya hay en las carpetas")
	for _, brick := range bricks {
		for p := range limits {
			d, err := quotaDir(brick, p)
			if err != nil {
				continue // la carpeta todavía no llegó a este brick
			}
			if err := setXattrInt(d, gluster.QuotaSizeXattr, duBlocks(d)); err != nil {
				return err
			}
		}
	}
	return nil
}

// duBlocks suma lo que ocupa en disco lo que hay adentro de d (cada inodo una vez).
func duBlocks(d string) int64 {
	var total int64
	seen := map[uint64]bool{}
	_ = filepath.WalkDir(d, func(path string, e fs.DirEntry, err error) error {
		if err != nil || path == d {
			return nil
		}
		fi, err := e.Info()
		if err != nil {
			return nil
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			if st.Nlink > 1 && !fi.IsDir() {
				if seen[st.Ino] {
					return nil
				}
				seen[st.Ino] = true
			}
			total += st.Blocks * 512
		}
		return nil
	})
	return total
}

// restartLocalBricks detiene los bricks del volumen en este nodo (SIGTERM: guardan su uso)
// y los vuelve a levantar con `volume start force`.
func restartLocalBricks(ctx context.Context, vol, self string) error {
	pids, _ := filepath.Glob(filepath.Join(glusterVolsRun, vol, "*.pid"))
	for _, pf := range pids {
		b, err := os.ReadFile(pf)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || pid <= 1 {
			continue
		}
		cmd, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		args := strings.Split(string(cmd), "\x00")
		if len(args) == 0 || filepath.Base(args[0]) != "glusterfsd" || !slices.ContainsFunc(args, func(s string) bool { return strings.HasPrefix(s, vol+".") }) {
			continue
		}
		_ = syscall.Kill(pid, syscall.SIGTERM)
		for i := 0; i < 60 && syscall.Kill(pid, 0) == nil; i++ {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if err := glusterDo(ctx, []string{"volume", "start", vol, "force"}); err != nil {
		return err
	}
	for i := 0; i < 60; i++ {
		v, err := squotaVolume(ctx, vol, true)
		if err == nil && !slices.ContainsFunc(v.Bricks, func(b gluster.BrickStatus) bool { return b.HostUUID == self && !b.Online }) {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("los bricks de este nodo no volvieron a arrancar")
}
