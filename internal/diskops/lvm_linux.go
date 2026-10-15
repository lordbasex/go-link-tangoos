//go:build linux

package diskops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// LVM2 por exec (ADR 0011): un solo binario (/usr/sbin/lvm) con subcomandos y
// argumentos armados acá. Nunca un string de shell; los nombres ya vienen validados.

func lvm(ctx context.Context, args ...string) (string, error) {
	return run(ctx, "lvm", args...)
}

// runSplit es run con stdout y stderr separados (los reportes JSON de LVM van por
// stdout; las advertencias, por stderr).
func runSplit(ctx context.Context, bin string, args ...string) (string, string, error) {
	path, ok := allowedBins[bin]
	if !ok {
		return "", "", fmt.Errorf("binario no permitido: %s", bin)
	}
	if _, err := os.Stat(path); err != nil {
		return "", "", fmt.Errorf("%s no está instalado", bin)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C", "LVM_SUPPRESS_FD_WARNINGS=1"}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), errb.String(), &ExecError{Bin: bin, Err: err, Output: limit(errb.String(), 4096)}
	}
	return out.String(), errb.String(), nil
}

func validDevPath(dev string) error {
	if !strings.HasPrefix(dev, "/dev/") || strings.Contains(dev, "..") || strings.ContainsAny(dev, " \t\n") {
		return fmt.Errorf("dispositivo inválido: %q", dev)
	}
	return nil
}

func validLVMTag(tag string) error {
	if tag == "" || len(tag) > 128 {
		return errors.New("etiqueta inválida")
	}
	for _, r := range tag {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_+.-/=:", r)
		if !ok {
			return fmt.Errorf("etiqueta inválida: %q", tag)
		}
	}
	return nil
}

// PVCreate inicializa un volumen físico y le pone la etiqueta de rol (data | cache).
func PVCreate(ctx context.Context, dev, role string) error {
	if err := validDevPath(dev); err != nil {
		return err
	}
	if role != "data" && role != "cache" {
		return errors.New("rol de PV inválido")
	}
	if _, err := lvm(ctx, "pvcreate", "--yes", "--force", dev); err != nil {
		return err
	}
	_, err := lvm(ctx, "pvchange", "--addtag", TagRole+role, dev)
	return err
}

// PVRemove borra la etiqueta de LVM de un PV que ya no está en ningún VG.
func PVRemove(ctx context.Context, dev string) error {
	if err := validDevPath(dev); err != nil {
		return err
	}
	_, err := lvm(ctx, "pvremove", "--yes", "--force", dev)
	return err
}

// PVResize agranda un PV al tamaño actual de su dispositivo (después de un reshape).
func PVResize(ctx context.Context, dev string) error {
	if err := validDevPath(dev); err != nil {
		return err
	}
	_, err := lvm(ctx, "pvresize", dev)
	return err
}

// VGCreate crea el volume group de un grupo con sus etiquetas.
func VGCreate(ctx context.Context, name, profile string, pvs []string) error {
	if err := ValidPoolName(name); err != nil {
		return err
	}
	if err := validLVMTag(TagProfile + profile); err != nil {
		return err
	}
	args := []string{"vgcreate", "--yes", "--physicalextentsize", "4m", "--addtag", TagPool, "--addtag", TagProfile + profile, name}
	for _, d := range pvs {
		if err := validDevPath(d); err != nil {
			return err
		}
		args = append(args, d)
	}
	_, err := lvm(ctx, args...)
	return err
}

// VGExtend suma un PV a un grupo.
func VGExtend(ctx context.Context, vg, dev string) error {
	if err := ValidPoolName(vg); err != nil {
		return err
	}
	if err := validDevPath(dev); err != nil {
		return err
	}
	_, err := lvm(ctx, "vgextend", "--yes", vg, dev)
	return err
}

// VGReduce saca un PV (vacío) de un grupo.
func VGReduce(ctx context.Context, vg, dev string) error {
	if err := ValidPoolName(vg); err != nil {
		return err
	}
	if err := validDevPath(dev); err != nil {
		return err
	}
	_, err := lvm(ctx, "vgreduce", "--yes", vg, dev)
	return err
}

// VGRemove borra un grupo sin volúmenes.
func VGRemove(ctx context.Context, vg string) error {
	if err := ValidPoolName(vg); err != nil {
		return err
	}
	_, err := lvm(ctx, "vgremove", "--yes", vg)
	return err
}

func lvPath(vg, lv string) (string, error) {
	if err := ValidPoolName(vg); err != nil {
		return "", err
	}
	if err := ValidVolumeName(strings.TrimSuffix(lv, "_cache")); err != nil {
		return "", err
	}
	return vg + "/" + lv, nil
}

// LVCreate crea un volumen lógico de size bytes solo sobre los PV indicados (los de
// datos: nunca las SSD de caché).
func LVCreate(ctx context.Context, vg, lv string, size uint64, tags []string, pvs []string) error {
	p, err := lvPath(vg, lv)
	if err != nil {
		return err
	}
	if AlignExtent(size) == 0 {
		return errors.New("tamaño de volumen inválido")
	}
	args := []string{"lvcreate", "--yes", "--wipesignatures", "y", "--zero", "y", "--name", lv,
		"--size", strconv.FormatUint(AlignExtent(size), 10) + "b"}
	for _, t := range tags {
		if err := validLVMTag(t); err != nil {
			return err
		}
		args = append(args, "--addtag", t)
	}
	args = append(args, strings.Split(p, "/")[0])
	for _, d := range pvs {
		if err := validDevPath(d); err != nil {
			return err
		}
		args = append(args, d)
	}
	_, err = lvm(ctx, args...)
	return err
}

// LVExtend agranda un volumen lógico a size bytes (sobre los PV de datos).
func LVExtend(ctx context.Context, vg, lv string, size uint64, pvs []string) error {
	p, err := lvPath(vg, lv)
	if err != nil {
		return err
	}
	args := []string{"lvextend", "--yes", "--size", strconv.FormatUint(AlignExtent(size), 10) + "b", p}
	for _, d := range pvs {
		if err := validDevPath(d); err != nil {
			return err
		}
		args = append(args, d)
	}
	_, err = lvm(ctx, args...)
	return err
}

// LVRemove borra un volumen lógico.
func LVRemove(ctx context.Context, vg, lv string) error {
	p, err := lvPath(vg, lv)
	if err != nil {
		return err
	}
	_, err = lvm(ctx, "lvremove", "--yes", p)
	return err
}

// LVTags cambia etiquetas de un volumen lógico.
func LVTags(ctx context.Context, vg, lv string, add, del []string) error {
	p, err := lvPath(vg, lv)
	if err != nil {
		return err
	}
	args := []string{"lvchange"}
	for _, t := range add {
		if err := validLVMTag(t); err != nil {
			return err
		}
		args = append(args, "--addtag", t)
	}
	for _, t := range del {
		if err := validLVMTag(t); err != nil {
			return err
		}
		args = append(args, "--deltag", t)
	}
	if len(args) == 1 {
		return nil
	}
	_, err = lvm(ctx, append(args, p)...)
	return err
}

// LVActivate activa (o desactiva) un volumen lógico.
func LVActivate(ctx context.Context, vg, lv string, on bool) error {
	p, err := lvPath(vg, lv)
	if err != nil {
		return err
	}
	flag := "y"
	if !on {
		flag = "n"
	}
	_, err = lvm(ctx, "lvchange", "--activate", flag, p)
	return err
}

// CacheAttach crea el volumen de caché en el PV de las SSD y lo engancha al volumen
// (lvmcache con --cachevol). mode: read (writethrough) | readwrite (writeback).
func CacheAttach(ctx context.Context, vg, lv, ssdPV, mode string) error {
	cm := map[string]string{"read": "writethrough", "readwrite": "writeback"}[mode]
	if cm == "" {
		return errors.New("modo de caché inválido")
	}
	if err := validDevPath(ssdPV); err != nil {
		return err
	}
	cache := CacheLVName(lv)
	if _, err := lvPath(vg, cache); err != nil {
		return err
	}
	// El volumen de caché ocupa el PV de las SSD (menos lo que LVM reserva para metadatos).
	if _, err := lvm(ctx, "lvcreate", "--yes", "--wipesignatures", "y", "--name", cache, "--extents", "100%PVS", vg, ssdPV); err != nil {
		return err
	}
	if _, err := lvm(ctx, "lvconvert", "--yes", "--type", "cache", "--cachevol", cache, "--cachemode", cm, vg+"/"+lv); err != nil {
		_, _ = lvm(context.Background(), "lvremove", "--yes", vg+"/"+cache)
		return err
	}
	return nil
}

// CacheDetach vacía la caché (los bloques sucios vuelven a los discos) y la separa del
// volumen; después borra el volumen de caché.
func CacheDetach(ctx context.Context, vg, lv string) error {
	p, err := lvPath(vg, lv)
	if err != nil {
		return err
	}
	if _, err := lvm(ctx, "lvconvert", "--yes", "--splitcache", p); err != nil {
		return err
	}
	_, err = lvm(ctx, "lvremove", "--yes", vg+"/"+CacheLVName(lv))
	return err
}

// ---- Reportes ---------------------------------------------------------------------------

type lvmReport struct {
	Report []struct {
		VG []map[string]string `json:"vg"`
		PV []map[string]string `json:"pv"`
		LV []map[string]string `json:"lv"`
	} `json:"report"`
}

func lvmRows(ctx context.Context, kind string, fields string) ([]map[string]string, error) {
	out, _, err := runSplit(ctx, "lvm", kind, "--reportformat", "json", "--units", "b", "--nosuffix", "--all", "-o", fields)
	if err != nil {
		return nil, err
	}
	var r lvmReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return nil, fmt.Errorf("reporte de %s ilegible: %w", kind, err)
	}
	var rows []map[string]string
	for _, x := range r.Report {
		switch kind {
		case "vgs":
			rows = append(rows, x.VG...)
		case "pvs":
			rows = append(rows, x.PV...)
		case "lvs":
			rows = append(rows, x.LV...)
		}
	}
	return rows, nil
}

// VGRow, PVRow y LVRow son filas de los reportes de LVM.
type VGRow struct {
	Name, UUID string
	Size, Free uint64
	Tags       []string
}

type PVRow struct {
	Dev, VG    string
	Size, Free uint64
	Tags       []string
}

type LVRow struct {
	Name, VG, UUID, Attr, Segtype, Pool string
	Size                                uint64
	Tags                                []string
	CacheMode                           string
	CacheTotal, CacheUsed, CacheDirty   uint64
	ReadHits, ReadMisses                uint64
	WriteHits, WriteMisses              uint64
	ChunkSize                           uint64
	Devices                             string
}

func u64(s string) uint64 { n, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64); return n }

func tagList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// LVMReport lee volume groups, volúmenes físicos y lógicos (todos: el que llama filtra
// los de TangoOS por la etiqueta).
func LVMReport(ctx context.Context) ([]VGRow, []PVRow, []LVRow, error) {
	vgs, err := lvmRows(ctx, "vgs", "vg_name,vg_uuid,vg_size,vg_free,vg_tags")
	if err != nil {
		return nil, nil, nil, err
	}
	pvs, err := lvmRows(ctx, "pvs", "pv_name,vg_name,pv_size,pv_free,pv_tags")
	if err != nil {
		return nil, nil, nil, err
	}
	lvs, err := lvmRows(ctx, "lvs", "lv_name,vg_name,lv_uuid,lv_attr,segtype,pool_lv,lv_size,lv_tags,cache_mode,"+
		"cache_total_blocks,cache_used_blocks,cache_dirty_blocks,cache_read_hits,cache_read_misses,"+
		"cache_write_hits,cache_write_misses,chunk_size,devices")
	if err != nil {
		return nil, nil, nil, err
	}
	var outV []VGRow
	for _, r := range vgs {
		outV = append(outV, VGRow{Name: r["vg_name"], UUID: r["vg_uuid"], Size: u64(r["vg_size"]), Free: u64(r["vg_free"]), Tags: tagList(r["vg_tags"])})
	}
	var outP []PVRow
	for _, r := range pvs {
		outP = append(outP, PVRow{Dev: r["pv_name"], VG: r["vg_name"], Size: u64(r["pv_size"]), Free: u64(r["pv_free"]), Tags: tagList(r["pv_tags"])})
	}
	var outL []LVRow
	for _, r := range lvs {
		outL = append(outL, LVRow{Name: r["lv_name"], VG: r["vg_name"], UUID: r["lv_uuid"], Attr: r["lv_attr"], Segtype: r["segtype"],
			Pool: r["pool_lv"], Size: u64(r["lv_size"]), Tags: tagList(r["lv_tags"]), CacheMode: r["cache_mode"],
			CacheTotal: u64(r["cache_total_blocks"]), CacheUsed: u64(r["cache_used_blocks"]), CacheDirty: u64(r["cache_dirty_blocks"]),
			ReadHits: u64(r["cache_read_hits"]), ReadMisses: u64(r["cache_read_misses"]),
			WriteHits: u64(r["cache_write_hits"]), WriteMisses: u64(r["cache_write_misses"]),
			ChunkSize: u64(r["chunk_size"]), Devices: r["devices"]})
	}
	return outV, outP, outL, nil
}
