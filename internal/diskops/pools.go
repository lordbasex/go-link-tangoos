package diskops

import (
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Partes puras de los grupos de almacenamiento (ADR 0011): nombres, etiquetas de LVM y
// lectura de los UUID de XFS, ext4 y LUKS2 en Go.

var poolNameRe = regexp.MustCompile(`^[a-z][a-z0-9]{0,23}$`)

// ValidPoolName valida el nombre de un grupo (también es el del volume group de LVM y
// el prefijo de sus md: "<grupo>_md1", así que es más corto que un volumen).
func ValidPoolName(name string) error {
	if !poolNameRe.MatchString(name) {
		return fmt.Errorf("el nombre del grupo debe empezar con una letra y tener solo minúsculas y números (máx. 24)")
	}
	if reservedNames[name] {
		return fmt.Errorf("«%s» es un nombre reservado", name)
	}
	return nil
}

// PoolMDName es el nombre del md del tramo k de un grupo ("pool1_md1").
func PoolMDName(pool string, k int) string { return fmt.Sprintf("%s_md%d", pool, k) }

// PoolCacheMDName es el md RAID 1 de las SSD de caché de un volumen.
func PoolCacheMDName(pool string, k int) string { return fmt.Sprintf("%s_cache%d", pool, k) }

// ParsePoolMDName separa "pool1_md2" en ("pool1", "md", 2) y "pool1_cache1" en ("pool1", "cache", 1).
func ParsePoolMDName(s string) (pool, kind string, k int, ok bool) {
	i := strings.LastIndexByte(s, '_')
	if i <= 0 {
		return "", "", 0, false
	}
	pool, rest := s[:i], s[i+1:]
	for _, kd := range []string{"md", "cache"} {
		if num, found := strings.CutPrefix(rest, kd); found {
			n, err := strconv.Atoi(num)
			if err != nil || n < 1 || strconv.Itoa(n) != num || ValidPoolName(pool) != nil {
				return "", "", 0, false
			}
			return pool, kd, n, true
		}
	}
	return "", "", 0, false
}

// CryptName es el nombre del dispositivo abierto de un volumen cifrado (/dev/mapper/tgc_<vol>).
func CryptName(volume string) string { return "tgc_" + volume }

// CacheLVName es el volumen lógico que hace de caché de un volumen.
func CacheLVName(volume string) string { return volume + "_cache" }

// Etiquetas de LVM: el grupo y sus volúmenes se describen solos (sobreviven a perder
// /var/lib/tango).
const (
	TagPool    = "tangoos"
	TagProfile = "tg.profile="
	TagRole    = "tg.role="  // data | cache (en los PV)
	TagFS      = "tg.fs="    // btrfs | xfs | ext4 (en los LV)
	TagEnc     = "tg.enc="   // luks2
	TagKey     = "tg.key="   // UUID del filesystem del dispositivo con el archivo de clave
	TagKeySlot = "tg.slot="  // ranura LUKS del archivo de clave
	TagCache   = "tg.cache=" // read | readwrite (en el LV de datos)
)

// TagValue busca una etiqueta "clave=valor" en una lista de etiquetas de LVM.
func TagValue(tags []string, prefix string) string {
	for _, t := range tags {
		if v, ok := strings.CutPrefix(t, prefix); ok {
			return v
		}
	}
	return ""
}

// PoolFS son los filesystems de un volumen dentro de un grupo.
var PoolFS = map[string]bool{"btrfs": true, "xfs": true, "ext4": true}

// ExtentSize es el tamaño de extent de los volume groups de TangoOS (el de LVM por defecto).
const ExtentSize = 4 << 20

// AlignExtent redondea hacia abajo a extents.
func AlignExtent(n uint64) uint64 { return n / ExtentSize * ExtentSize }

// ---- UUID de filesystems y de LUKS ------------------------------------------------------

// XFSUUID devuelve el UUID de un XFS (superbloque al comienzo: "XFSB", uuid en 32).
func XFSUUID(head []byte) (string, bool) {
	if len(head) < 48 || string(head[0:4]) != "XFSB" {
		return "", false
	}
	return uuidString(head[32:48]), true
}

// Ext4UUID devuelve el UUID de un ext2/3/4 (superbloque en 1024: magia en 0x38, uuid en 0x68).
func Ext4UUID(head []byte) (string, bool) {
	const sb = 1024
	if len(head) < sb+0x78 || binary.LittleEndian.Uint16(head[sb+0x38:sb+0x3a]) != 0xef53 {
		return "", false
	}
	return uuidString(head[sb+0x68 : sb+0x78]), true
}

// LUKSUUID devuelve la versión y el UUID de una cabecera LUKS (magia "LUKS\xba\xbe",
// versión en 6, UUID como texto en 168).
func LUKSUUID(head []byte) (version int, uuid string, ok bool) {
	if len(head) < 208 || string(head[0:6]) != "LUKS\xba\xbe" {
		return 0, "", false
	}
	v := int(binary.BigEndian.Uint16(head[6:8]))
	u := strings.TrimRight(string(head[168:208]), "\x00")
	return v, u, u != ""
}

// FSInfo identifica el filesystem (o contenedor) al comienzo de un dispositivo.
func FSInfo(head []byte) (fstype, uuid string) {
	if u, ok := BtrfsFSID(head); ok {
		return "btrfs", u
	}
	if u, ok := XFSUUID(head); ok {
		return "xfs", u
	}
	if u, ok := Ext4UUID(head); ok {
		return "ext4", u
	}
	if _, u, ok := LUKSUUID(head); ok {
		return "crypto_LUKS", u
	}
	if len(head) >= 0x5a && (string(head[0x52:0x5a]) == "FAT32   " || string(head[0x36:0x3e]) == "FAT16   " || string(head[0x36:0x3e]) == "FAT12   ") {
		return "vfat", vfatSerial(head)
	}
	if len(head) >= 11 && string(head[3:11]) == "EXFAT   " && len(head) >= 0x68 {
		s := binary.LittleEndian.Uint32(head[0x64:0x68])
		return "exfat", fmt.Sprintf("%04X-%04X", s>>16, s&0xffff)
	}
	return "", ""
}

// vfatSerial es el "UUID" de un FAT como lo muestra udev (/dev/disk/by-uuid/ABCD-1234).
func vfatSerial(head []byte) string {
	off := 0x27 // FAT12/16
	if string(head[0x52:0x5a]) == "FAT32   " {
		off = 0x43
	}
	s := binary.LittleEndian.Uint32(head[off : off+4])
	return fmt.Sprintf("%04X-%04X", s>>16, s&0xffff)
}

// keyDeviceUUIDRe valida el UUID de un dispositivo de clave (vfat "ABCD-1234" o UUID completo).
var keyDeviceUUIDRe = regexp.MustCompile(`^([0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)

// ValidKeyDeviceUUID valida el UUID del filesystem de un dispositivo de clave.
func ValidKeyDeviceUUID(s string) error {
	if !keyDeviceUUIDRe.MatchString(s) {
		return fmt.Errorf("UUID de dispositivo inválido")
	}
	return nil
}

// ValidPassphrase es la regla de las frases de cifrado: entre 10 y 512 bytes, sin NUL ni
// saltos de línea (cryptsetup las lee de stdin tal cual).
func ValidPassphrase(p string) error {
	if len(p) < 10 || len(p) > 512 {
		return fmt.Errorf("la frase tiene que tener entre 10 y 512 caracteres")
	}
	if strings.ContainsAny(p, "\x00\n\r") {
		return fmt.Errorf("la frase no puede tener saltos de línea")
	}
	return nil
}
