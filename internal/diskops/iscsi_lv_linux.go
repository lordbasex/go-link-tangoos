//go:build linux

package diskops

import (
	"context"
	"fmt"
	"regexp"
)

// LUN de iSCSI en un LV de un grupo de almacenamiento (protocol/README.md §12): LV il_<destino>
// sin filesystem, que LIO exporta con un backstore iblock. Con thin va en el pool thin
// il_thin del grupo. El guion bajo lo deja fuera de los volúmenes de TangoOS (lvVisible).

// TagISCSI marca el LV de un LUN (tg.iscsi=<destino>).
const TagISCSI = "tg.iscsi="

// ISCSIThinPool es el pool thin de los LUN de un grupo.
const ISCSIThinPool = "il_thin"

// ISCSILVPrefix es el prefijo de los LV de LUN.
const ISCSILVPrefix = "il_"

var iscsiLUNRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`)

func iscsiLVPath(vg, name string) (string, error) {
	if err := ValidPoolName(vg); err != nil {
		return "", err
	}
	if !iscsiLUNRe.MatchString(name) || name == "thin" {
		return "", fmt.Errorf("nombre de destino inválido: %q", name)
	}
	return vg + "/" + ISCSILVPrefix + name, nil
}

// ISCSILVDev es el dispositivo del LV de un LUN.
func ISCSILVDev(vg, name string) string { return "/dev/" + vg + "/" + ISCSILVPrefix + name }

// ISCSILVCreate crea el LV de un LUN (grueso o thin) sobre los PV de datos.
func ISCSILVCreate(ctx context.Context, vg, name string, size uint64, thin bool, pvs []string) error {
	if _, err := iscsiLVPath(vg, name); err != nil {
		return err
	}
	return blockLVCreate(ctx, vg, ISCSILVPrefix+name, ISCSIThinPool, TagISCSI+name, TagISCSI+"thin", size, thin, pvs)
}

// ISCSILVRemove borra el LV de un LUN (y el pool thin si era el último).
func ISCSILVRemove(ctx context.Context, vg, name string) error {
	p, err := iscsiLVPath(vg, name)
	if err != nil {
		return err
	}
	return blockLVRemove(ctx, vg, p, ISCSIThinPool)
}
