package services

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// ConfigFS es la raíz de LIO.
const ConfigFS = "/sys/kernel/config/target"

// IQNPrefix es el prefijo de los destinos de TangoOS (fecha del dominio: RFC 3720).
const IQNPrefix = "iqn.2026-10.local.tango"

var targetNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`)
var chapUserRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// zvolRe: /dev/zvol/<pool>/zvol/<nombre> (internal/zfs.ZvolDevice).
var zvolRe = regexp.MustCompile(`^/dev/zvol/[a-z][a-z0-9_]{0,31}/zvol/[a-z][a-z0-9_-]{0,31}$`)

// ValidTargetName: minúsculas, números y guiones, 2–32 (entra en el IQN y en el archivo).
func ValidTargetName(n string) error {
	if !targetNameRe.MatchString(n) {
		return errors.New("el nombre del destino admite minúsculas, números y guiones (2 a 32)")
	}
	return nil
}

// IQN del destino.
func IQN(name string) string { return IQNPrefix + ":" + name }

// LUNFile es el archivo de respaldo dentro del volumen (subvolumen @iscsi, sin CoW).
func LUNFile(mount, name string) string { return path.Join(mount, "@iscsi", name+".img") }

// ValidCHAP: LIO limita el secreto a 12–16 caracteres en la práctica de los iniciadores
// (Windows exige 12–16).
func ValidCHAP(user, secret string) error {
	if user == "" && secret == "" {
		return nil
	}
	if !chapUserRe.MatchString(user) {
		return errors.New("usuario CHAP inválido")
	}
	if len(secret) < 12 || len(secret) > 16 || strings.ContainsAny(secret, " \n\t") {
		return errors.New("el secreto CHAP debe tener entre 12 y 16 caracteres, sin espacios")
	}
	return nil
}

// ValidateTarget revisa un destino completo antes de mandarlo al agente.
func ValidateTarget(t agentproto.ISCSITarget) error {
	if err := ValidTargetName(t.Name); err != nil {
		return err
	}
	if t.IQN != IQN(t.Name) {
		return errors.New("IQN inconsistente")
	}
	if t.SizeBytes < 64<<20 || t.SizeBytes > 64<<40 {
		return errors.New("el tamaño debe estar entre 64 MB y 64 TB")
	}
	if t.Zvol != "" { // LUN sobre un volumen de bloque ZFS (ADR 0013)
		if t.File != "" || !zvolRe.MatchString(t.Zvol) || path.Clean(t.Zvol) != t.Zvol {
			return fmt.Errorf("volumen de bloque inválido %q", t.Zvol)
		}
		return ValidCHAP(t.CHAPUser, t.CHAPSecret)
	}
	f := path.Clean(t.File)
	parts := strings.Split(strings.TrimPrefix(f, "/"), "/")
	if f != t.File || len(parts) != 3 || parts[1] != "@iscsi" || parts[2] != t.Name+".img" {
		return fmt.Errorf("archivo de LUN inválido %q", t.File)
	}
	return ValidCHAP(t.CHAPUser, t.CHAPSecret)
}

// Step es una escritura en configfs: Mkdir crea el directorio; si Value no es vacío se
// escribe en Path; Symlink enlaza Path → Target.
type Step struct {
	Mkdir   bool
	Path    string
	Value   string
	Symlink string
}

// Plan arma los pasos de configfs para un destino: backstore fileio, destino iSCSI con
// TPG 1, LUN 0, portal 0.0.0.0:3260 y autenticación (CHAP de demo-mode o por ACL
// genérica). Usa generate_node_acls (cualquier iniciador) con CHAP opcional.
func Plan(t agentproto.ISCSITarget) []Step {
	bs := path.Join(ConfigFS, "core", "fileio_0", t.Name)
	tgt := path.Join(ConfigFS, "iscsi", t.IQN)
	tpg := path.Join(tgt, "tpgt_1")
	control := fmt.Sprintf("fd_dev_name=%s,fd_dev_size=%d", t.File, t.SizeBytes)
	if t.Zvol != "" { // dispositivo de bloque: backstore iblock
		bs = path.Join(ConfigFS, "core", "iblock_0", t.Name)
		control = "udev_path=" + t.Zvol
	}
	steps := []Step{
		{Mkdir: true, Path: bs},
		{Path: path.Join(bs, "control"), Value: control},
	}
	if t.Zvol != "" { // udev_path también como atributo (lo muestran targetcli y el estado)
		steps = append(steps, Step{Path: path.Join(bs, "udev_path"), Value: t.Zvol})
	}
	steps = append(steps, []Step{
		{Path: path.Join(bs, "wwn", "vpd_unit_serial"), Value: "tango-" + t.Name},
		{Path: path.Join(bs, "enable"), Value: "1"},
		{Mkdir: true, Path: tgt},
		{Mkdir: true, Path: tpg},
		{Mkdir: true, Path: path.Join(tpg, "lun", "lun_0")},
		{Path: path.Join(tpg, "lun", "lun_0", "tango_lun"), Symlink: bs},
		{Mkdir: true, Path: path.Join(tpg, "np", "0.0.0.0:3260")},
		{Path: path.Join(tpg, "attrib", "generate_node_acls"), Value: "1"},
		{Path: path.Join(tpg, "attrib", "demo_mode_write_protect"), Value: "0"},
		{Path: path.Join(tpg, "attrib", "cache_dynamic_acls"), Value: "1"},
	}...)
	if t.CHAPUser != "" {
		steps = append(steps,
			Step{Path: path.Join(tpg, "attrib", "authentication"), Value: "1"},
			Step{Path: path.Join(tpg, "auth", "userid"), Value: t.CHAPUser},
			Step{Path: path.Join(tpg, "auth", "password"), Value: t.CHAPSecret},
		)
	} else {
		steps = append(steps, Step{Path: path.Join(tpg, "attrib", "authentication"), Value: "0"})
	}
	steps = append(steps, Step{Path: path.Join(tpg, "enable"), Value: "1"})
	return steps
}

// TeardownPaths son los directorios a quitar (en orden) para eliminar un destino.
func TeardownPaths(t agentproto.ISCSITarget) []string {
	bs := path.Join(ConfigFS, "core", "fileio_0", t.Name)
	tgt := path.Join(ConfigFS, "iscsi", t.IQN)
	tpg := path.Join(tgt, "tpgt_1")
	return []string{
		path.Join(tpg, "lun", "lun_0", "tango_lun"), // symlink
		path.Join(tpg, "lun", "lun_0"),
		path.Join(tpg, "np", "0.0.0.0:3260"),
		tpg,
		tgt,
		bs,
		path.Join(ConfigFS, "core", "iblock_0", t.Name), // LUN sobre zvol (el que no exista se ignora)
	}
}
