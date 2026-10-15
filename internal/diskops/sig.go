// Package diskops implementa las operaciones de almacenamiento que ejecuta
// tango-agent (docs/04, ADR 0003/0009): detección de firmas, huella de disco,
// elegibilidad, borrado de firmas, md + Btrfs, subvolúmenes, instantáneas,
// unidades .mount y Samba.
//
// Las partes puras (este archivo, names.go, units.go, samba.go) no tocan el
// sistema y se prueban con fixtures; las que tocan discos están en *_linux.go.
package diskops

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// HeadSize y TailSize son las zonas que se leen de cada disco: cubren MBR, GPT
// (cabecera + entradas, y su copia al final), superbloques md 1.x (4 KiB) y Btrfs
// (64 KiB), ext2/3/4 (1 KiB), XFS (0) y etiquetas ZFS al final.
const (
	HeadSize = 128 << 10
	TailSize = 1 << 20
)

// Fingerprint resume la identidad y el contenido visible de un disco. Si entre el
// plan y la ejecución cambia el tamaño, la tabla de particiones o algún
// superbloque, la huella cambia y la operación aborta (DEVICE_CHANGED).
func Fingerprint(size uint64, head, tail []byte) string {
	h := sha256.New()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], size)
	h.Write(b[:])
	h.Write(head)
	h.Write([]byte{0xff})
	h.Write(tail)
	return hex.EncodeToString(h.Sum(nil))
}

// MDSuper es lo que interesa de un superbloque md 1.x.
type MDSuper struct {
	UUID  string // formato de mdadm: xxxxxxxx:xxxxxxxx:xxxxxxxx:xxxxxxxx
	Name  string // "host:volume1"
	Level int32
}

const mdMagic = 0xa92b4efc

// ParseMDSuper lee un superbloque md 1.x (struct mdp_superblock_1).
func ParseMDSuper(b []byte) (MDSuper, bool) {
	if len(b) < 96 || binary.LittleEndian.Uint32(b[0:4]) != mdMagic || binary.LittleEndian.Uint32(b[4:8]) != 1 {
		return MDSuper{}, false
	}
	u := b[16:32]
	uuid := fmt.Sprintf("%08x:%08x:%08x:%08x", binary.BigEndian.Uint32(u[0:4]), binary.BigEndian.Uint32(u[4:8]), binary.BigEndian.Uint32(u[8:12]), binary.BigEndian.Uint32(u[12:16]))
	name := string(bytes.TrimRight(b[32:64], "\x00"))
	level := int32(binary.LittleEndian.Uint32(b[72:76]))
	return MDSuper{UUID: uuid, Name: name, Level: level}, true
}

// mdSuperAt busca un superbloque md 1.1 (offset 0) o 1.2 (offset 4 KiB) en la cabecera.
func mdSuperAt(head []byte) (MDSuper, bool) {
	for _, off := range []int{4096, 0} {
		if len(head) >= off+96 {
			if s, ok := ParseMDSuper(head[off:]); ok {
				return s, true
			}
		}
	}
	return MDSuper{}, false
}

const btrfsSuperOff = 0x10000

// BtrfsFSID devuelve el UUID del filesystem Btrfs si la cabecera tiene su superbloque.
func BtrfsFSID(head []byte) (string, bool) {
	if len(head) < btrfsSuperOff+0x48 || string(head[btrfsSuperOff+0x40:btrfsSuperOff+0x48]) != "_BHRfS_M" {
		return "", false
	}
	return uuidString(head[btrfsSuperOff+0x20 : btrfsSuperOff+0x30]), true
}

func uuidString(u []byte) string {
	h := hex.EncodeToString(u)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// DetectContent describe en castellano qué hay en el disco, para el plan
// ("Contenido: …"). Solo mira firmas conocidas; si no hay ninguna es "vacío".
func DetectContent(head, tail []byte) string {
	var found []string
	if s, ok := mdSuperAt(head); ok {
		name := s.Name
		if i := strings.LastIndexByte(name, ':'); i >= 0 {
			name = name[i+1:]
		}
		found = append(found, fmt.Sprintf("miembro de un RAID md («%s»)", name))
	}
	if _, ok := BtrfsFSID(head); ok {
		found = append(found, "Btrfs")
	}
	if len(head) >= 0x438+2 && binary.LittleEndian.Uint16(head[0x438:0x43a]) == 0xef53 {
		found = append(found, "ext4")
	}
	if len(head) >= 4 && string(head[0:4]) == "XFSB" {
		found = append(found, "XFS")
	}
	if _, _, ok := LUKSUUID(head); ok {
		found = append(found, "contenedor cifrado LUKS")
	}
	if len(head) >= 544 && string(head[512:520]) == "LABELONE" && string(head[536:544]) == "LVM2 001" {
		found = append(found, "volumen físico de LVM")
	}
	if len(head) >= 520 && string(head[512:520]) == "EFI PART" {
		found = append(found, "tabla de particiones GPT")
	} else if len(head) >= 512 && head[510] == 0x55 && head[511] == 0xaa && hasMBRPartitions(head) {
		found = append(found, "tabla de particiones MBR")
	}
	if len(head) >= 0x8006 && string(head[0x8001:0x8006]) == "CD001" {
		found = append(found, "imagen ISO 9660")
	}
	if len(tail) > 0 && bytes.Contains(tail, []byte("EFI PART")) && !contains(found, "tabla de particiones GPT") {
		found = append(found, "restos de una tabla GPT")
	}
	if len(found) == 0 {
		if allZero(head) {
			return "vacío"
		}
		return "datos sin formato reconocido"
	}
	return strings.Join(found, " + ")
}

func hasMBRPartitions(head []byte) bool {
	for i := 0; i < 4; i++ {
		e := head[446+16*i : 446+16*(i+1)]
		if e[4] != 0 { // tipo de partición
			return true
		}
	}
	return false
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
