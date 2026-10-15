package diskops

import (
	"encoding/binary"
	"testing"
)

func TestValidPoolName(t *testing.T) {
	for _, ok := range []string{"pool1", "a", "datos2026"} {
		if err := ValidPoolName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Pool1", "1pool", "pool-1", "pool_1", "abcdefghijklmnopqrstuvwxy", "etc", "pool 1"} {
		if ValidPoolName(bad) == nil {
			t.Errorf("%q aceptado", bad)
		}
	}
}

func TestPoolMDNames(t *testing.T) {
	if PoolMDName("pool1", 2) != "pool1_md2" || PoolCacheMDName("pool1", 1) != "pool1_cache1" {
		t.Fatal("nombres")
	}
	for s, want := range map[string]struct {
		pool, kind string
		k          int
	}{"pool1_md2": {"pool1", "md", 2}, "p_cache10": {"p", "cache", 10}} {
		p, kd, k, ok := ParsePoolMDName(s)
		if !ok || p != want.pool || kd != want.kind || k != want.k {
			t.Errorf("%s → %s %s %d %v", s, p, kd, k, ok)
		}
	}
	for _, bad := range []string{"volume1", "pool1_md0", "pool1_md01", "pool1_mdx", "_md1", "Pool_md1", "pool1_raid1"} {
		if _, _, _, ok := ParsePoolMDName(bad); ok {
			t.Errorf("%q aceptado", bad)
		}
	}
}

func TestTagValue(t *testing.T) {
	tags := []string{"tangoos", "tg.profile=hybrid1", "tg.key=ABCD-1234"}
	if TagValue(tags, TagProfile) != "hybrid1" || TagValue(tags, TagKey) != "ABCD-1234" || TagValue(tags, TagFS) != "" {
		t.Fatal("TagValue")
	}
}

func TestFSInfo(t *testing.T) {
	uuid := []byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	want := "12345678-9abc-def0-1122-334455667788"

	xfs := make([]byte, 4096)
	copy(xfs, "XFSB")
	copy(xfs[32:], uuid)
	if fs, u := FSInfo(xfs); fs != "xfs" || u != want {
		t.Errorf("xfs: %s %s", fs, u)
	}

	ext := make([]byte, 4096)
	binary.LittleEndian.PutUint16(ext[1024+0x38:], 0xef53)
	copy(ext[1024+0x68:], uuid)
	if fs, u := FSInfo(ext); fs != "ext4" || u != want {
		t.Errorf("ext4: %s %s", fs, u)
	}

	luks := make([]byte, 4096)
	copy(luks, "LUKS\xba\xbe")
	binary.BigEndian.PutUint16(luks[6:], 2)
	copy(luks[168:], want)
	if v, u, ok := LUKSUUID(luks); !ok || v != 2 || u != want {
		t.Errorf("luks: %d %s %v", v, u, ok)
	}
	if fs, u := FSInfo(luks); fs != "crypto_LUKS" || u != want {
		t.Errorf("luks fsinfo: %s %s", fs, u)
	}

	fat := make([]byte, 512)
	copy(fat[0x52:], "FAT32   ")
	binary.LittleEndian.PutUint32(fat[0x43:], 0xABCD1234)
	if fs, u := FSInfo(fat); fs != "vfat" || u != "ABCD-1234" {
		t.Errorf("vfat: %s %s", fs, u)
	}
	if fs, _ := FSInfo(make([]byte, 4096)); fs != "" {
		t.Errorf("vacío: %s", fs)
	}
}

func TestValidKeyDeviceAndPassphrase(t *testing.T) {
	for _, ok := range []string{"ABCD-1234", "12345678-9abc-def0-1122-334455667788"} {
		if ValidKeyDeviceUUID(ok) != nil {
			t.Errorf("%s rechazado", ok)
		}
	}
	for _, bad := range []string{"", "ABCD1234", "../x", "ABCD-1234/.."} {
		if ValidKeyDeviceUUID(bad) == nil {
			t.Errorf("%q aceptado", bad)
		}
	}
	if ValidPassphrase("corta") == nil || ValidPassphrase("una frase\nlarga") == nil || ValidPassphrase("una frase larga y segura") != nil {
		t.Error("ValidPassphrase")
	}
}

func TestAlignExtent(t *testing.T) {
	if AlignExtent(10*mib) != 8*mib || AlignExtent(3*mib) != 0 {
		t.Fatal("AlignExtent")
	}
}
