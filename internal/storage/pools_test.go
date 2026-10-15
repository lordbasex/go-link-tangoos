package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

type fakePools struct{ rep *agentproto.PoolReport }

func (f fakePools) PoolReport(context.Context) (*agentproto.PoolReport, error) { return f.rep, nil }

// poolFixture: un grupo híbrido pool1 con un md (pool1_md1, RAID 5 sobre sdb1, sdc1, sdd1),
// el tramo 2 sin usar en sdd2, volume3 (Btrfs, montado) y volume4 (cifrado, bloqueado),
// y una SSD de caché (sde) en volume3.
func poolFixture(t *testing.T, degraded bool) *Inventory {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, data string) {
		p := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(data), 0o644)
	}
	ln := func(target, rel string) {
		p := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.Symlink(target, p)
	}
	for _, d := range []string{"sdb", "sdc", "sdd", "sde", "sdf"} {
		mk("sys/block/"+d+"/device/model", "QEMU HARDDISK\n")
		mk("sys/block/"+d+"/size", "4194304\n")
		mk("sys/block/"+d+"/"+d+"1/partition", "1\n")
	}
	mk("sys/block/sde/queue/rotational", "0\n")
	mk("sys/block/md125/md/level", "raid5\n")
	mk("sys/block/md125/md/sync_action", "idle\n")
	deg := "0\n"
	if degraded {
		deg = "1\n"
	}
	mk("sys/block/md125/md/degraded", deg)
	mk("sys/block/md125/size", "8388608\n")
	for _, d := range []string{"sdb", "sdc", "sdd"} {
		ln("../../../"+d+"/"+d+"1", "sys/block/md125/md/dev-"+d+"1/block")
		mk("sys/block/md125/md/dev-"+d+"1/state", "in_sync\n")
	}
	mk("sys/block/md125/holders/dm-0", "")
	ln("../md125", "dev/md/pool1_md1")
	mk("proc/self/mountinfo", "90 29 0:40 / /volume3 rw - btrfs /dev/mapper/pool1-volume3 rw\n")
	rep := &agentproto.PoolReport{Pools: []agentproto.PoolInfo{{
		Name: "pool1", UUID: "VG-UUID", Profile: "hybrid1", Size: 6 << 30, Free: 2 << 30,
		PVs: []agentproto.PoolPV{
			{Dev: "/dev/md125", MD: "pool1_md1", Role: "data", Size: 4 << 30, Free: 1 << 30, Disks: []string{"sdb", "sdc", "sdd"}},
			{Dev: "/dev/sde", Role: "cache", Size: 2 << 30, Disks: []string{"sde"}},
		},
		Tiers: []agentproto.PoolTier{
			{Index: 1, MD: "md125", Name: "pool1_md1", Level: "raid5", Size: 2 << 30, Members: []string{"sdb", "sdc", "sdd"}},
			{Index: 2, Level: "unused", Size: 2 << 30, Members: []string{"sdd"}},
			{Index: 1, Level: "basic", Size: 2 << 30, Members: []string{"sde"}, Role: "cache", Volume: "volume3"},
		},
		LVs: []agentproto.PoolLV{
			{Name: "volume3", UUID: "LV3", Size: 3 << 30, FS: "btrfs", Active: true,
				Cache: &agentproto.CacheInfo{Mode: "read", Disks: []string{"sde"}, ReadHits: 75, ReadMisses: 25}},
			{Name: "volume4", UUID: "LV4", Size: 1 << 30, FS: "xfs", Encrypted: true, KeyDevice: "ABCD-1234"},
		},
	}}}
	return &Inventory{Sys: filepath.Join(root, "sys"), Dev: filepath.Join(root, "dev"), Proc: filepath.Join(root, "proc"), PoolAgent: fakePools{rep}}
}

func TestPoolVolumes(t *testing.T) {
	inv := poolFixture(t, false)
	vols, err := inv.Volumes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vols) != 2 {
		t.Fatalf("volúmenes = %+v (el md del grupo no es un volumen)", vols)
	}
	v3, v4 := vols[0], vols[1]
	if v3.Name != "volume3" || v3.Kind != "pool" || v3.Pool != "VG-UUID" || v3.Mount != "/volume3" || v3.Profile != "hybrid1" || v3.State != "healthy" {
		t.Errorf("volume3 = %+v", v3)
	}
	if v3.Cache == nil || v3.Cache.HitRatio == nil || *v3.Cache.HitRatio != 0.75 {
		t.Errorf("caché = %+v", v3.Cache)
	}
	if len(v3.Disks) != 3 {
		t.Errorf("discos de volume3 = %v", v3.Disks)
	}
	if !v4.Locked || v4.State != "locked" || v4.Mount != "" || v4.FS != "xfs" || v4.KeyDevice != "ABCD-1234" {
		t.Errorf("volume4 = %+v", v4)
	}
}

func TestPoolsAndDisks(t *testing.T) {
	inv := poolFixture(t, true)
	pools, _ := inv.Pools(context.Background())
	if len(pools) != 1 {
		t.Fatalf("grupos = %+v", pools)
	}
	p := pools[0]
	if p.State != "degraded" || p.Size != 4<<30 || p.Free != 1<<30 || p.Allocated != 3<<30 || len(p.Disks) != 3 || len(p.CacheDisks) != 1 {
		t.Errorf("grupo = %+v", p)
	}
	if len(p.Tiers) != 2 || p.Tiers[0].Usable != 4<<30 || p.Tiers[1].Level != "unused" || p.Tiers[0].State != "degraded" {
		t.Errorf("tramos = %+v", p.Tiers)
	}
	vols, _ := inv.Volumes(context.Background())
	if vols[0].State != "degraded" {
		t.Errorf("volumen de un grupo degradado = %s", vols[0].State)
	}
	disks, _ := inv.Disks(context.Background())
	got := map[string]Disk{}
	for _, d := range disks {
		got[d.Dev] = d
	}
	if d := got["sdb"]; d.Use != "member" || d.Pool != "VG-UUID" || d.Role != "data" || d.Volume != "" {
		t.Errorf("sdb = %+v", d)
	}
	if d := got["sde"]; d.Use != "member" || d.Role != "cache" || d.Kind != "ssd" {
		t.Errorf("sde = %+v", d)
	}
	if d := got["sdf"]; d.Use != "free" || d.Pool != "" {
		t.Errorf("sdf = %+v", d)
	}
}

func TestStaleMDIsNotAVolume(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "sys/block/md124/md"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "sys/block/md124/md/array_state"), []byte("clear\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "proc/self"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "proc/self/mountinfo"), nil, 0o644)
	inv := &Inventory{Sys: filepath.Join(root, "sys"), Dev: filepath.Join(root, "dev"), Proc: filepath.Join(root, "proc")}
	vols, _ := inv.Volumes(context.Background())
	if len(vols) != 0 {
		t.Fatalf("md vacío listado: %+v", vols)
	}
}
