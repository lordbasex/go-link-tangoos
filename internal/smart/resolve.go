package smart

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Los nombres de /dev/disk/by-id usan este alfabeto (udev los sanea). Nada de "/".
var devIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+@-]{0,190}$`)

// Paths permite cambiar las raíces en tests.
type Paths struct {
	ByID     string // /dev/disk/by-id
	SysBlock string // /sys/class/block
}

// DefaultPaths son las rutas reales.
var DefaultPaths = Paths{ByID: "/dev/disk/by-id", SysBlock: "/sys/class/block"}

// Resolve traduce un nombre de /dev/disk/by-id al dispositivo de bloque entero
// (nunca una partición). Solo acepta nombres que existen en by-id: no se puede
// apuntar a un path arbitrario.
func (p Paths) Resolve(devID string) (string, *agentproto.Error) {
	if !devIDRe.MatchString(devID) || strings.Contains(devID, "..") {
		return "", agentproto.Errf(agentproto.CodeInvalidArgs, "dev_id inválido")
	}
	link := filepath.Join(p.ByID, devID)
	fi, err := os.Lstat(link)
	if err != nil {
		return "", agentproto.Errf(agentproto.CodeDeviceNotFound, "no existe %s en by-id", devID)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return "", agentproto.Errf(agentproto.CodeDeviceInvalid, "%s no es un enlace de udev", devID)
	}
	dev, err := filepath.EvalSymlinks(link)
	if err != nil {
		return "", agentproto.Errf(agentproto.CodeDeviceNotFound, "el enlace de %s no resuelve", devID)
	}
	st, err := os.Stat(dev)
	if err != nil || st.Mode()&os.ModeDevice == 0 || st.Mode()&os.ModeCharDevice != 0 {
		return "", agentproto.Errf(agentproto.CodeDeviceInvalid, "%s no es un dispositivo de bloque", devID)
	}
	name := filepath.Base(dev)
	if _, err := os.Stat(filepath.Join(p.SysBlock, name, "partition")); err == nil {
		return "", agentproto.Errf(agentproto.CodeDeviceInvalid, "%s es una partición: se pide el disco entero", devID)
	}
	// Tipo SCSI de periférico: 0 = disco, 5 = CD/DVD, etc. Solo se aceptan discos.
	if b, err := os.ReadFile(filepath.Join(p.SysBlock, name, "device", "type")); err == nil {
		if t := strings.TrimSpace(string(b)); t != "0" && t != "14" {
			return "", agentproto.Errf(agentproto.CodeDeviceInvalid, "%s no es un disco (tipo SCSI %s)", devID, t)
		}
	}
	return dev, nil
}
