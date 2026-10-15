//go:build linux

package diskops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	sd "github.com/coreos/go-systemd/v22/dbus"
)

// Bricks de GlusterFS en un LV dedicado (docs/17 §4): LV gb_<vol> con XFS en un grupo de
// almacenamiento, montado en /srv/gluster/<vol>. Con thin, el LV va en el pool thin gb_thin
// del grupo (las instantáneas de GlusterFS lo necesitan). Los nombres llevan guion bajo:
// los informes de grupos no los muestran como volúmenes de TangoOS.

// TagGlusterBrick marca el LV de un brick (tg.gluster=<vol>).
const TagGlusterBrick = "tg.gluster="

// GlusterThinPool es el nombre del pool thin de bricks de un grupo.
const GlusterThinPool = "gb_thin"

var glusterVolRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func glusterLVPath(vg, vol string) (string, error) {
	if err := ValidPoolName(vg); err != nil {
		return "", err
	}
	if !glusterVolRe.MatchString(vol) {
		return "", fmt.Errorf("volumen del clúster inválido: %q", vol)
	}
	return vg + "/gb_" + vol, nil
}

// GlusterLVDev es el dispositivo del LV de un brick.
func GlusterLVDev(vg, vol string) string { return "/dev/" + vg + "/gb_" + vol }

// GlusterLVCreate crea el LV de un brick: grueso sobre los PV de datos, o thin en el pool
// gb_thin (que se crea o se agranda en size + 25 % para las instantáneas: un pool thin
// lleno deja el volumen sin escritura).
func GlusterLVCreate(ctx context.Context, vg, vol string, size uint64, thin bool, pvs []string) error {
	p, err := glusterLVPath(vg, vol)
	if err != nil {
		return err
	}
	size = AlignExtent(size)
	if size == 0 {
		return errors.New("tamaño inválido")
	}
	for _, d := range pvs {
		if err := validDevPath(d); err != nil {
			return err
		}
	}
	tag := TagGlusterBrick + vol
	if !thin {
		args := []string{"lvcreate", "--yes", "--wipesignatures", "y", "--zero", "y", "--name", "gb_" + vol,
			"--size", strconv.FormatUint(size, 10) + "b", "--addtag", tag, vg}
		_, err = lvm(ctx, append(args, pvs...)...)
		return err
	}
	poolSize := AlignExtent(size + size/4)
	exists := false
	if out, err := lvm(ctx, "lvs", "--noheadings", "-o", "lv_name", vg+"/"+GlusterThinPool); err == nil && strings.TrimSpace(out) == GlusterThinPool {
		exists = true
	}
	if exists {
		args := []string{"lvextend", "--yes", "--size", "+" + strconv.FormatUint(poolSize, 10) + "b", vg + "/" + GlusterThinPool}
		if _, err := lvm(ctx, append(args, pvs...)...); err != nil {
			return fmt.Errorf("agrandar el pool thin: %w", err)
		}
	} else {
		args := []string{"lvcreate", "--yes", "--type", "thin-pool", "--name", GlusterThinPool,
			"--size", strconv.FormatUint(poolSize, 10) + "b", "--addtag", TagGlusterBrick + "thin", vg}
		if _, err := lvm(ctx, append(args, pvs...)...); err != nil {
			return fmt.Errorf("crear el pool thin: %w", err)
		}
	}
	_, err = lvm(ctx, "lvcreate", "--yes", "--type", "thin", "--name", "gb_"+vol, "--virtualsize", strconv.FormatUint(size, 10)+"b",
		"--thinpool", GlusterThinPool, "--addtag", tag, vg)
	_ = p
	return err
}

// GlusterLVRemove borra el LV de un brick y, si era el último thin, el pool gb_thin.
func GlusterLVRemove(ctx context.Context, vg, vol string) error {
	p, err := glusterLVPath(vg, vol)
	if err != nil {
		return err
	}
	if _, err := lvm(ctx, "lvremove", "--yes", p); err != nil {
		return err
	}
	out, err := lvm(ctx, "lvs", "--noheadings", "-o", "lv_name,pool_lv", vg)
	if err != nil {
		return nil
	}
	used := false
	hasPool := false
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 1 && f[0] == GlusterThinPool {
			hasPool = true
		}
		if len(f) == 2 && f[1] == GlusterThinPool {
			used = true
		}
	}
	if hasPool && !used {
		_, _ = lvm(ctx, "lvremove", "--yes", vg+"/"+GlusterThinPool)
	}
	return nil
}

// MkfsXFSBrick formatea un brick con XFS y inodos de 512 bytes (los atributos extendidos de
// GlusterFS entran en el inodo). Devuelve el UUID del filesystem.
func MkfsXFSBrick(ctx context.Context, vol, dev string) (string, error) {
	if err := validDevPath(dev); err != nil {
		return "", err
	}
	if !glusterVolRe.MatchString(vol) {
		return "", fmt.Errorf("volumen inválido: %q", vol)
	}
	label := "gb_" + vol
	if len(label) > 12 {
		label = label[:12]
	}
	if _, err := run(ctx, "mkfs.xfs", "-f", "-i", "size=512", "-L", label, dev); err != nil {
		return "", err
	}
	_, uuid := ReadFSInfo(dev)
	if uuid == "" {
		return "", errors.New("mkfs.xfs terminó pero no hay UUID")
	}
	return uuid, nil
}

// GlusterLVMountUnit es la unidad que monta el brick antes de glusterd.
func GlusterLVMountUnit(vol, fsUUID string) string {
	return fmt.Sprintf(`# Generada por TangoOS (tango-agent). No editar: se regenera.
[Unit]
Description=TangoOS cluster brick %[1]s
Documentation=https://github.com/lordbasex/go-link-tangoos
Before=glusterd.service

[Mount]
What=/dev/disk/by-uuid/%[2]s
Where=/srv/gluster/%[1]s
Type=xfs
Options=noatime,inode64
TimeoutSec=120

[Install]
WantedBy=multi-user.target
RequiredBy=glusterd.service
`, vol, fsUUID)
}

// GlusterLVMountUnitName es el nombre de esa unidad.
func GlusterLVMountUnitName(vol string) string { return "srv-gluster-" + vol + ".mount" }

// InstallGlusterLVMount escribe, habilita y arranca el montaje del brick y crea brick/.
func InstallGlusterLVMount(ctx context.Context, vol, fsUUID string) error {
	if !glusterVolRe.MatchString(vol) {
		return fmt.Errorf("volumen inválido: %q", vol)
	}
	where := "/srv/gluster/" + vol
	if err := os.MkdirAll(where, 0o755); err != nil {
		return err
	}
	unit := GlusterLVMountUnitName(vol)
	path := filepath.Join(unitDir, unit)
	if err := writeAtomic(path, []byte(GlusterLVMountUnit(vol, fsUUID)), 0o644); err != nil {
		return err
	}
	c, err := sdConn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.ReloadContext(ctx); err != nil {
		return err
	}
	if _, _, err := c.EnableUnitFilesContext(ctx, []string{path}, false, true); err != nil {
		return err
	}
	if err := startAndWait(ctx, c, unit, where); err != nil {
		return err
	}
	return os.MkdirAll(where+"/brick", 0o755)
}

// RemoveGlusterLVMount desmonta el brick y borra su unidad.
func RemoveGlusterLVMount(ctx context.Context, vol string) error {
	if !glusterVolRe.MatchString(vol) {
		return fmt.Errorf("volumen inválido: %q", vol)
	}
	unit := GlusterLVMountUnitName(vol)
	path := filepath.Join(unitDir, unit)
	c, err := sdConn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	ch := make(chan string, 1)
	if _, err := c.StopUnitContext(ctx, unit, "replace", ch); err == nil {
		if err := waitJob(ctx, ch); err != nil {
			return err
		}
	}
	if isMountPoint(ctx, "/srv/gluster/"+vol) {
		return errors.New("el brick sigue montado (¿glusterd lo usa?)")
	}
	_, _ = c.DisableUnitFilesContext(ctx, []string{unit}, false)
	_ = os.Remove(path)
	_ = c.ReloadContext(ctx)
	_ = os.Remove("/srv/gluster/" + vol)
	return nil
}

// GlusterLVRow es un LV de brick visto en lvs. Children son los LV de instantáneas de
// GlusterFS que salieron de él (origin = gb_<vol>).
type GlusterLVRow struct {
	VG, Vol  string
	Size     uint64
	Thin     bool
	Children []string
}

// GlusterLVs lista los LV de bricks de este equipo (por la etiqueta) con sus instantáneas.
func GlusterLVs(ctx context.Context) ([]GlusterLVRow, error) {
	out, err := lvm(ctx, "lvs", "--noheadings", "--separator", "|", "--units", "b", "--nosuffix", "-o", "lv_name,vg_name,origin,lv_size,segtype,lv_tags")
	if err != nil {
		return nil, err
	}
	type row struct{ name, vg, origin, size, seg, tags string }
	var rows []row
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(l), "|")
		if len(f) != 6 {
			continue
		}
		rows = append(rows, row{f[0], f[1], f[2], f[3], f[4], f[5]})
	}
	var res []GlusterLVRow
	for _, r := range rows {
		v := TagValue(tagList(r.tags), TagGlusterBrick)
		if v == "" || v == "thin" || !strings.HasPrefix(r.name, "gb_") {
			continue
		}
		size, _ := strconv.ParseUint(r.size, 10, 64)
		gr := GlusterLVRow{VG: r.vg, Vol: v, Size: size, Thin: r.seg == "thin"}
		for _, c := range rows {
			if c.vg == r.vg && c.origin == r.name {
				gr.Children = append(gr.Children, c.name)
			}
		}
		res = append(res, gr)
	}
	return res, nil
}

var snapLVRe = regexp.MustCompile(`^[0-9a-f]{32}_[0-9]{1,3}$`)

var snapMountRe = regexp.MustCompile(`^/run/gluster/snaps/[0-9a-f]{32}/brick[0-9]{1,3}$`)

// RemoveGlusterSnapLV desmonta (con systemd: el agente no puede llamar a umount) y borra un
// LV de instantánea de GlusterFS que quedó de un volumen restaurado y después borrado.
func RemoveGlusterSnapLV(ctx context.Context, vg, lv string) error {
	if err := ValidPoolName(vg); err != nil {
		return err
	}
	if !snapLVRe.MatchString(lv) {
		return fmt.Errorf("LV de instantánea inválido: %q", lv)
	}
	dm := "/dev/mapper/" + strings.ReplaceAll(vg, "-", "--") + "-" + strings.ReplaceAll(lv, "-", "--")
	if b, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) < 10 {
				continue
			}
			src := f[len(f)-2]
			if src != dm || !snapMountRe.MatchString(f[4]) {
				continue
			}
			unit := strings.ReplaceAll(strings.TrimPrefix(f[4], "/"), "/", "-") + ".mount"
			c, err := sdConn(ctx)
			if err != nil {
				return err
			}
			ch := make(chan string, 1)
			if _, err := c.StopUnitContext(ctx, unit, "replace", ch); err == nil {
				_ = waitJob(ctx, ch)
			}
			c.Close()
			if isMountPoint(ctx, f[4]) {
				return fmt.Errorf("%s sigue montado", f[4])
			}
		}
	}
	_, err := lvm(ctx, "lvremove", "--yes", vg+"/"+lv)
	return err
}

var _ = sd.PropDescription
