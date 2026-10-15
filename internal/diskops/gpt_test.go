package diskops

import (
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

func TestBuildGPTRoundTrip(t *testing.T) {
	for _, sector := range []uint64{512, 4096} {
		size := 8*tg + 3*mib
		b := HybridBoundaries([]uint64{2 * tg, 4 * tg, HybridUsable(size)})
		parts := HybridParts("pool1", b, HybridUsable(size))
		if len(parts) != 3 {
			t.Fatalf("particiones = %d", len(parts))
		}
		img, err := BuildGPT(size, sector, "", parts)
		if err != nil {
			t.Fatal(err)
		}
		// Solo importan la cabecera y la cola del disco: se arman sin reservar el disco entero.
		head := make([]byte, HeadSize)
		copy(head, img.Head)
		tailStart := size - TailSize
		tail := make([]byte, TailSize)
		copy(tail[img.TailOff-tailStart:], img.Tail)
		got, err := ParseGPT(head, sector)
		if err != nil {
			t.Fatalf("sector %d: %v", sector, err)
		}
		if len(got) != 3 {
			t.Fatalf("leídas %d", len(got))
		}
		for i, e := range got {
			if e.Start != parts[i].Start || e.Size != parts[i].Size || e.Type != GUIDLinuxRAID || e.Name != parts[i].Name || e.Index != i+1 {
				t.Errorf("partición %d = %+v, quiero %+v", i+1, e, parts[i])
			}
			if pool, tier, ok := ParseHybridPartName(e.Name); !ok || pool != "pool1" || tier != i+1 {
				t.Errorf("nombre %q → %s %d %v", e.Name, pool, tier, ok)
			}
		}
		// Cabecera de respaldo: en el último sector, apunta a la primaria y tiene CRC válido.
		last := size/sector - 1
		bh := tail[last*sector-tailStart : last*sector-tailStart+92]
		if string(bh[0:8]) != "EFI PART" || binary.LittleEndian.Uint64(bh[24:32]) != last || binary.LittleEndian.Uint64(bh[32:40]) != 1 {
			t.Errorf("cabecera de respaldo inválida")
		}
		cp := append([]byte{}, bh...)
		binary.LittleEndian.PutUint32(cp[16:20], 0)
		if crc32.ChecksumIEEE(cp) != binary.LittleEndian.Uint32(bh[16:20]) {
			t.Errorf("CRC de la cabecera de respaldo")
		}
		if !strings.Contains(DetectContent(head, tail), "GPT") {
			t.Errorf("DetectContent no ve la GPT")
		}
		// MBR protector.
		if head[450] != 0xEE || head[510] != 0x55 || head[511] != 0xAA {
			t.Errorf("MBR protector inválido")
		}
	}
}

func TestBuildGPTRejects(t *testing.T) {
	size := 4 * tg
	ok := GPTPart{Start: mib, Size: tg, Type: GUIDLinuxRAID, Name: "x"}
	cases := map[string][]GPTPart{
		"superpuestas": {ok, {Start: mib + tg - mib, Size: mib, Type: GUIDLinuxRAID}},
		"antes de la zona usable": {{Start: 512, Size: mib, Type: GUIDLinuxRAID}},
		"después del final":       {{Start: mib, Size: size, Type: GUIDLinuxRAID}},
		"sin alinear":             {{Start: mib + 1, Size: mib, Type: GUIDLinuxRAID}},
		"tipo inválido":           {{Start: mib, Size: mib, Type: "zz"}},
		"nombre largo":            {{Start: mib, Size: mib, Type: GUIDLinuxRAID, Name: strings.Repeat("a", 37)}},
		"vacía":                   {},
	}
	for name, parts := range cases {
		if _, err := BuildGPT(size, 512, "", parts); err == nil {
			t.Errorf("%s: se aceptó", name)
		}
	}
	if _, err := BuildGPT(size, 1024, "", []GPTPart{ok}); err == nil {
		t.Error("sector de 1024")
	}
}

func TestGUIDRoundTrip(t *testing.T) {
	b, err := guidBytes(GUIDLinuxRAID)
	if err != nil || GUIDString(b[:]) != GUIDLinuxRAID {
		t.Fatalf("%v %s", err, GUIDString(b[:]))
	}
	// Bytes en disco del tipo Linux RAID (little endian en los tres primeros campos).
	if b[0] != 0x0F || b[3] != 0xA1 || b[8] != 0xA0 {
		t.Errorf("codificación mixta: % x", b)
	}
	g := RandomGUID()
	if len(g) != 36 || g[14] != '4' {
		t.Errorf("GUID aleatorio %s", g)
	}
}

func TestParseHybridPartName(t *testing.T) {
	for _, s := range []string{"tg-pool1", "tg--t1", "tg-pool1-t0", "tg-pool1-tx", "pool1-t1", "tg-pool1-t01"} {
		if _, _, ok := ParseHybridPartName(s); ok {
			t.Errorf("%q aceptado", s)
		}
	}
	if p, k, ok := ParseHybridPartName("tg-a-b-t12"); !ok || p != "a-b" || k != 12 {
		t.Errorf("con guiones: %s %d %v", p, k, ok)
	}
}
