package diskops

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
	"unicode/utf16"
)

// Tabla de particiones GPT escrita en Go (UEFI 2.x, cap. 5), sin sgdisk (ADR 0009). Solo
// lo que necesitan los grupos híbridos (ADR 0011): una tabla nueva, entera, con
// particiones de tipo "Linux RAID".

// GUIDLinuxRAID es el tipo de partición de un miembro de md.
const GUIDLinuxRAID = "A19D880F-05FC-4D3B-A006-743F0F84911E"

const (
	gptEntries   = 128
	gptEntrySize = 128
)

// GPTPart es una partición: bytes desde el comienzo del disco (alineados al sector).
type GPTPart struct {
	Start, Size uint64
	Type        string // GUID en texto
	Name        string // hasta 36 caracteres
	GUID        string // vacío = aleatorio
}

// GPTImage son los bytes a escribir: Head en el offset 0 (MBR protector, cabecera y
// entradas) y Tail en TailOff (entradas y cabecera de respaldo).
type GPTImage struct {
	Head    []byte
	Tail    []byte
	TailOff uint64
}

// guidBytes codifica un GUID como lo guarda GPT (los tres primeros campos en little endian).
func guidBytes(s string) ([16]byte, error) {
	var out [16]byte
	h := strings.ReplaceAll(s, "-", "")
	raw, err := hex.DecodeString(h)
	if err != nil || len(raw) != 16 || strings.Count(s, "-") != 4 {
		return out, fmt.Errorf("GUID inválido: %q", s)
	}
	binary.LittleEndian.PutUint32(out[0:4], binary.BigEndian.Uint32(raw[0:4]))
	binary.LittleEndian.PutUint16(out[4:6], binary.BigEndian.Uint16(raw[4:6]))
	binary.LittleEndian.PutUint16(out[6:8], binary.BigEndian.Uint16(raw[6:8]))
	copy(out[8:], raw[8:])
	return out, nil
}

// GUIDString es la inversa de guidBytes.
func GUIDString(b []byte) string {
	if len(b) < 16 {
		return ""
	}
	return fmt.Sprintf("%08X-%04X-%04X-%X-%X", binary.LittleEndian.Uint32(b[0:4]), binary.LittleEndian.Uint16(b[4:6]),
		binary.LittleEndian.Uint16(b[6:8]), b[8:10], b[10:16])
}

// RandomGUID es un GUID versión 4.
func RandomGUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return strings.ToUpper(h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32])
}

// BuildGPT arma una GPT nueva para un disco de size bytes con sectores de sector bytes.
func BuildGPT(size uint64, sector uint64, diskGUID string, parts []GPTPart) (GPTImage, error) {
	if sector != 512 && sector != 4096 {
		return GPTImage{}, fmt.Errorf("tamaño de sector no soportado: %d", sector)
	}
	if size%sector != 0 || size < 4*mib {
		return GPTImage{}, errors.New("tamaño de disco inválido")
	}
	if len(parts) == 0 || len(parts) > gptEntries {
		return GPTImage{}, errors.New("cantidad de particiones inválida")
	}
	lbas := size / sector
	last := lbas - 1
	entSectors := uint64(gptEntries*gptEntrySize) / sector
	firstUsable := 2 + entSectors
	lastUsable := last - 1 - entSectors
	if diskGUID == "" {
		diskGUID = RandomGUID()
	}
	dg, err := guidBytes(diskGUID)
	if err != nil {
		return GPTImage{}, err
	}

	entries := make([]byte, gptEntries*gptEntrySize)
	var prevEnd uint64
	for i, p := range parts {
		if p.Start%sector != 0 || p.Size%sector != 0 || p.Size == 0 {
			return GPTImage{}, fmt.Errorf("partición %d no alineada al sector", i+1)
		}
		first, lastLBA := p.Start/sector, (p.Start+p.Size)/sector-1
		if first < firstUsable || lastLBA > lastUsable || first < prevEnd {
			return GPTImage{}, fmt.Errorf("partición %d fuera de rango o superpuesta", i+1)
		}
		prevEnd = lastLBA + 1
		tg, err := guidBytes(p.Type)
		if err != nil {
			return GPTImage{}, err
		}
		g := p.GUID
		if g == "" {
			g = RandomGUID()
		}
		ug, err := guidBytes(g)
		if err != nil {
			return GPTImage{}, err
		}
		name := utf16.Encode([]rune(p.Name))
		if len(name) > 36 {
			return GPTImage{}, fmt.Errorf("nombre de partición demasiado largo: %q", p.Name)
		}
		e := entries[i*gptEntrySize : (i+1)*gptEntrySize]
		copy(e[0:16], tg[:])
		copy(e[16:32], ug[:])
		binary.LittleEndian.PutUint64(e[32:40], first)
		binary.LittleEndian.PutUint64(e[40:48], lastLBA)
		for j, c := range name {
			binary.LittleEndian.PutUint16(e[56+2*j:], c)
		}
	}
	entCRC := crc32.ChecksumIEEE(entries)

	header := func(my, alt, entLBA uint64) []byte {
		h := make([]byte, sector)
		copy(h[0:8], "EFI PART")
		binary.LittleEndian.PutUint32(h[8:12], 0x00010000)
		binary.LittleEndian.PutUint32(h[12:16], 92)
		binary.LittleEndian.PutUint64(h[24:32], my)
		binary.LittleEndian.PutUint64(h[32:40], alt)
		binary.LittleEndian.PutUint64(h[40:48], firstUsable)
		binary.LittleEndian.PutUint64(h[48:56], lastUsable)
		copy(h[56:72], dg[:])
		binary.LittleEndian.PutUint64(h[72:80], entLBA)
		binary.LittleEndian.PutUint32(h[80:84], gptEntries)
		binary.LittleEndian.PutUint32(h[84:88], gptEntrySize)
		binary.LittleEndian.PutUint32(h[88:92], entCRC)
		binary.LittleEndian.PutUint32(h[16:20], crc32.ChecksumIEEE(h[:92]))
		return h
	}

	// MBR protector: una sola partición 0xEE que cubre el disco.
	mbr := make([]byte, sector)
	pe := mbr[446:462]
	pe[1], pe[2], pe[3] = 0x00, 0x02, 0x00
	pe[4] = 0xEE
	pe[5], pe[6], pe[7] = 0xFF, 0xFF, 0xFF
	binary.LittleEndian.PutUint32(pe[8:12], 1)
	binary.LittleEndian.PutUint32(pe[12:16], uint32(min(lbas-1, 0xFFFFFFFF)))
	mbr[510], mbr[511] = 0x55, 0xAA

	head := make([]byte, 0, (2+entSectors)*sector)
	head = append(head, mbr...)
	head = append(head, header(1, last, 2)...)
	head = append(head, entries...)

	tail := make([]byte, 0, (entSectors+1)*sector)
	tail = append(tail, entries...)
	tail = append(tail, header(last, 1, last-entSectors)...)
	return GPTImage{Head: head, Tail: tail, TailOff: (last - entSectors) * sector}, nil
}

// GPTEntry es una partición leída de una tabla.
type GPTEntry struct {
	Index       int // 1..128
	Type, GUID  string
	Start, Size uint64
	Name        string
}

// ParseGPT lee la tabla primaria (cabecera en el sector 1) de la cabecera de un disco y
// valida sus CRC. head tiene que incluir las entradas (16 KiB + 2 sectores).
func ParseGPT(head []byte, sector uint64) ([]GPTEntry, error) {
	if uint64(len(head)) < 2*sector {
		return nil, errors.New("cabecera corta")
	}
	h := head[sector : sector+92]
	if string(h[0:8]) != "EFI PART" {
		return nil, errors.New("sin GPT")
	}
	hs := binary.LittleEndian.Uint32(h[12:16])
	if hs != 92 {
		return nil, errors.New("tamaño de cabecera GPT inesperado")
	}
	cp := append([]byte{}, h...)
	binary.LittleEndian.PutUint32(cp[16:20], 0)
	if crc32.ChecksumIEEE(cp) != binary.LittleEndian.Uint32(h[16:20]) {
		return nil, errors.New("CRC de la cabecera GPT inválido")
	}
	entLBA := binary.LittleEndian.Uint64(h[72:80])
	n := binary.LittleEndian.Uint32(h[80:84])
	es := binary.LittleEndian.Uint32(h[84:88])
	if es != gptEntrySize || n == 0 || n > 1024 {
		return nil, errors.New("tabla GPT con formato inesperado")
	}
	off := entLBA * sector
	end := off + uint64(n)*uint64(es)
	if end > uint64(len(head)) {
		return nil, errors.New("entradas GPT fuera de la cabecera leída")
	}
	ents := head[off:end]
	if crc32.ChecksumIEEE(ents) != binary.LittleEndian.Uint32(h[88:92]) {
		return nil, errors.New("CRC de las entradas GPT inválido")
	}
	var out []GPTEntry
	for i := 0; i < int(n); i++ {
		e := ents[i*int(es) : (i+1)*int(es)]
		if allZero(e[0:16]) {
			continue
		}
		first, last := binary.LittleEndian.Uint64(e[32:40]), binary.LittleEndian.Uint64(e[40:48])
		var name []uint16
		for j := 0; j < 36; j++ {
			c := binary.LittleEndian.Uint16(e[56+2*j:])
			if c == 0 {
				break
			}
			name = append(name, c)
		}
		out = append(out, GPTEntry{Index: i + 1, Type: GUIDString(e[0:16]), GUID: GUIDString(e[16:32]),
			Start: first * sector, Size: (last - first + 1) * sector, Name: string(utf16.Decode(name))})
	}
	return out, nil
}

// HybridPartName es el nombre GPT de la partición del tramo k de un grupo
// ("tg-pool1-t2"): con él se reconocen los tramos que todavía no tienen md.
func HybridPartName(pool string, tier int) string { return fmt.Sprintf("tg-%s-t%d", pool, tier) }

// ParseHybridPartName es la inversa de HybridPartName.
func ParseHybridPartName(s string) (pool string, tier int, ok bool) {
	rest, found := strings.CutPrefix(s, "tg-")
	if !found {
		return "", 0, false
	}
	i := strings.LastIndex(rest, "-t")
	if i <= 0 {
		return "", 0, false
	}
	if _, err := fmt.Sscanf(rest[i+2:], "%d", &tier); err != nil || tier < 1 || fmt.Sprint(tier) != rest[i+2:] {
		return "", 0, false
	}
	return rest[:i], tier, true
}

// HybridParts arma las particiones de un disco para estos límites de tramos: una por
// tramo que el disco alcanza (usable = HybridUsable del disco).
func HybridParts(pool string, boundaries []uint64, usable uint64) []GPTPart {
	var out []GPTPart
	prev := uint64(0)
	for k, b := range boundaries {
		if usable < b {
			break
		}
		out = append(out, GPTPart{Start: PartAlign + prev, Size: b - prev, Type: GUIDLinuxRAID, Name: HybridPartName(pool, k+1)})
		prev = b
	}
	return out
}
