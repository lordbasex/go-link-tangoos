package diskops

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestProfileRules(t *testing.T) {
	ok := map[string][]int{"basic": {1}, "raid1": {2}, "raid5": {3, 4, 8}, "raid6": {4, 5}, "raid10": {4, 6}}
	bad := map[string][]int{"basic": {0, 2}, "raid1": {1, 3}, "raid5": {2}, "raid6": {3}, "raid10": {3, 5, 2}, "raid0": {2}}
	for p, ns := range ok {
		for _, n := range ns {
			if err := CheckProfileDisks(p, n); err != nil {
				t.Errorf("%s con %d discos: %v", p, n, err)
			}
		}
	}
	for p, ns := range bad {
		for _, n := range ns {
			if CheckProfileDisks(p, n) == nil {
				t.Errorf("%s con %d discos debería fallar", p, n)
			}
		}
	}
}

func TestUsableBytes(t *testing.T) {
	const g = uint64(1) << 30
	cases := []struct {
		p    string
		s    []uint64
		want uint64
	}{
		{"basic", []uint64{16 * g}, 16 * g},
		{"raid1", []uint64{16 * g, 16 * g}, 16 * g},
		{"raid5", []uint64{2 * g, 2 * g, 2 * g}, 4 * g},
		{"raid5", []uint64{2 * g, 4 * g, 4 * g}, 4 * g}, // se usa el más chico
		{"raid6", []uint64{2 * g, 2 * g, 2 * g, 2 * g}, 4 * g},
		{"raid10", []uint64{2 * g, 2 * g, 2 * g, 2 * g}, 4 * g},
		{"raid10", []uint64{2 * g, 2 * g, 2 * g, 2 * g, 2 * g, 2 * g}, 6 * g},
	}
	for _, c := range cases {
		if got := UsableBytes(c.p, c.s); got != c.want {
			t.Errorf("%s %v = %d, quiero %d", c.p, c.s, got, c.want)
		}
	}
	if !MixedSizes([]uint64{2 * g, 4 * g}) || MixedSizes([]uint64{2 * g, 2*g + 1024}) {
		t.Error("MixedSizes")
	}
}

func TestShareGroups(t *testing.T) {
	rw, ro := ShareGroups("Familia")
	if rw != "tg-familia-rw" || ro != "tg-familia-ro" {
		t.Fatal(rw, ro)
	}
	rw, _ = ShareGroups("Fotos.2026_de-la-casa-de-campo-larga")
	if len(rw) > 32 || !strings.HasPrefix(rw, "tg-fotos_2026_d") {
		t.Fatalf("grupo largo: %q (%d)", rw, len(rw))
	}
	a, _ := ShareGroups("Fotos.2026_de-la-casa-de-campo-larga")
	if a != rw {
		t.Fatal("no es estable")
	}
}

func TestPosixACL(t *testing.T) {
	b := PosixACL(7, []ACLEntry{{GID: 2001, Perm: 5}})
	if binary.LittleEndian.Uint32(b) != 2 || len(b) != 4+8*5 {
		t.Fatalf("cabecera o largo: %d", len(b))
	}
	type ent struct {
		tag, perm uint16
		id        uint32
	}
	var got []ent
	for o := 4; o < len(b); o += 8 {
		got = append(got, ent{binary.LittleEndian.Uint16(b[o:]), binary.LittleEndian.Uint16(b[o+2:]), binary.LittleEndian.Uint32(b[o+4:])})
	}
	want := []ent{{aclUserObj, 7, aclNoID}, {aclGroupObj, 7, aclNoID}, {aclGroup, 5, 2001}, {aclMask, 7, aclNoID}, {aclOther, 0, aclNoID}}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entrada %d: %+v, quiero %+v", i, got[i], want[i])
		}
	}
	// Archivo: máscara = rw ∪ r = rw.
	f := PosixACL(6, []ACLEntry{{GID: 2001, Perm: 4}})
	if binary.LittleEndian.Uint16(f[4+3*8+2:]) != 6 {
		t.Error("máscara de archivo")
	}
}

func TestSambaPerms(t *testing.T) {
	s := SambaShareWithPerms("Familia", "/volume1/@shares/Familia", "Lo de\n[la casa]")
	for _, want := range []string{"[Familia]", "valid users = @tg-familia-rw @tg-familia-ro", "write list = @tg-familia-rw",
		"read only = yes", "force group = tg-familia-rw", "path = /volume1/@shares/Familia", "comment = Lo de  la casa"} {
		if !strings.Contains(s, want) {
			t.Errorf("falta %q en:\n%s", want, s)
		}
	}
	if !SambaPathUnder(s, "/volume1") || SambaPathUnder(s, "/volume") || SambaPathUnder(s, "/volume2") {
		t.Error("SambaPathUnder")
	}
}

func TestRemoveMDConf(t *testing.T) {
	conf := "# x\nARRAY /dev/md/volume1 metadata=1.2 UUID=a name=any:volume1\nARRAY /dev/md/volume2 metadata=1.2 UUID=b name=any:volume2\n"
	got := RemoveMDConf(conf, "volume2")
	if strings.Contains(got, "volume2") || !strings.Contains(got, "volume1") || !strings.HasPrefix(got, "# x\n") {
		t.Fatal(got)
	}
	if RemoveMDConf(got, "volume1") != "# x\n" {
		t.Fatal(RemoveMDConf(got, "volume1"))
	}
}

func TestValidUserName(t *testing.T) {
	for _, ok := range []string{"sofia", "martin_g", "a1", "tv-living"} {
		if err := ValidUserName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Sofia", "1abc", "root", "tango", "tg-x", "a b", "sofía", strings.Repeat("a", 33)} {
		if ValidUserName(bad) == nil {
			t.Errorf("%q debería fallar", bad)
		}
	}
}
