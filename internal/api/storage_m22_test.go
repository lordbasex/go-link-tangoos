package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

func TestRemapVolumes(t *testing.T) {
	vols := []storage.Volume{
		{ID: "0b8f4c2e-1111-4222-8333-444455556666", Name: "volume1", Mount: "/volume1"},
		{ID: "9a9a9a9a-1111-4222-8333-444455556666", Name: "volume2", Mount: "/volume2"},
	}
	list := []map[string]any{
		{"id": "jellyfin", "volume": "md127", "mount": "/volume1"},
		{"id": "kuma", "volume": "btrfs-9a9a9a9a-1111-4222-8333-444455556666", "mount": ""},
		{"id": "ok", "volume": "0b8f4c2e-1111-4222-8333-444455556666", "mount": "/volume1"},
		{"id": "huérfana", "volume": "md125", "mount": "/volume9"},
	}
	out, changed := remapVolumes(list, vols)
	if !changed || out[0]["volume"] != vols[0].ID || out[1]["volume"] != vols[1].ID || out[2]["volume"] != vols[0].ID || out[3]["volume"] != "md125" {
		t.Fatalf("remap: %v", out)
	}
	// Idempotente.
	if _, again := remapVolumes(out, vols); again {
		t.Fatal("segunda pasada cambió algo")
	}
}

func TestMigrateVolumeIDsBackup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "apps.json")
	_ = os.WriteFile(p, []byte(`[{"id":"jellyfin","volume":"md127","mount":"/volume1"}]`), 0o600)
	inv := fakeInventory(t, dir)
	migrateVolumeIDs(Deps{StateDir: dir, Inventory: inv})
	b, _ := os.ReadFile(p)
	var list []map[string]any
	_ = json.Unmarshal(b, &list)
	if list[0]["volume"] == "md127" {
		t.Fatalf("no migró: %s", b)
	}
	if bak, err := os.ReadFile(p + ".bak-m22"); err != nil || string(bak) == string(b) {
		t.Fatalf("backup: %v", err)
	}
}

// fakeInventory arma un sysfs mínimo con un md montado en /volume1 sobre Btrfs.
func fakeInventory(t *testing.T, dir string) *storage.Inventory {
	t.Helper()
	root := filepath.Join(dir, "root")
	mk := func(rel, data string) {
		p := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(data), 0o644)
	}
	mk("sys/block/md127/md/level", "raid1\n")
	mk("sys/block/md127/md/sync_action", "idle\n")
	mk("sys/block/md127/size", "1000\n")
	_ = os.MkdirAll(filepath.Join(root, "sys/fs/btrfs/0b8f4c2e-1111-4222-8333-444455556666/devices/md127"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "sys/dev/block"), 0o755)
	_ = os.Symlink("../../block/md127", filepath.Join(root, "sys/dev/block/9:127"))
	mk("proc/self/mountinfo", "90 29 9:127 / /volume1 rw - btrfs /dev/md127 rw\n")
	return &storage.Inventory{Sys: filepath.Join(root, "sys"), Dev: filepath.Join(root, "dev"), Proc: filepath.Join(root, "proc")}
}

type fakeM22 struct {
	mu       sync.Mutex
	replaced []agentproto.MDReplaceArgs
	grown    []agentproto.MDGrowArgs
}

func (f *fakeM22) MDReplace(_ context.Context, a agentproto.MDReplaceArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replaced = append(f.replaced, a)
	return nil
}
func (f *fakeM22) MDGrow(_ context.Context, a agentproto.MDGrowArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grown = append(f.grown, a)
	return nil
}
func (f *fakeM22) SnapshotDelete(context.Context, string, string, string) error { return nil }
func (f *fakeM22) DiskLocate(context.Context, string, bool) error {
	return agentproto.Errf(agentproto.CodeNotSupported, "sin LEDs")
}

// raidInventory: volume2 RAID 5 sobre sdb, sdc, sdd (16 GiB c/u, sdd fallado según
// el caso), libres sde (8 GiB, chico), sdf y sdg (16 GiB).
func raidInventory(t *testing.T, level string, faulty bool) *storage.Inventory {
	root := t.TempDir()
	sizes := map[string]string{"sda": "41943040", "sdb": "33554432", "sdc": "33554432", "sdd": "33554432", "sde": "16777216", "sdf": "33554432", "sdg": "33554432"}
	for i, d := range []string{"sda", "sdb", "sdc", "sdd", "sde", "sdf", "sdg"} {
		writeFile(t, root, "sys/block/"+d+"/device/model", "QEMU HARDDISK\n")
		writeFile(t, root, "sys/block/"+d+"/device/serial", "TANGO-"+d+"\n")
		writeFile(t, root, "sys/block/"+d+"/size", sizes[d]+"\n")
		writeFile(t, root, "sys/block/"+d+"/queue/rotational", "1\n")
		_ = os.MkdirAll(filepath.Join(root, "sys/block", d, "holders"), 0o755)
		symlink(t, root, "dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_drive-scsi"+string(rune('0'+i)), "../../"+d)
	}
	writeFile(t, root, "sys/block/sda/sda1/partition", "1\n")
	symlink(t, root, "sys/dev/block/8:1", "../../block/sda/sda1")
	writeFile(t, root, "sys/block/md127/md/level", level+"\n")
	writeFile(t, root, "sys/block/md127/md/sync_action", "idle\n")
	writeFile(t, root, "sys/block/md127/size", "67108864\n")
	deg := "0\n"
	if faulty {
		deg = "1\n"
	}
	writeFile(t, root, "sys/block/md127/md/degraded", deg)
	for _, d := range []string{"sdb", "sdc", "sdd"} {
		symlink(t, root, "sys/block/md127/md/dev-"+d+"/block", "../../../"+d)
		st := "in_sync\n"
		if faulty && d == "sdd" {
			st = "faulty\n"
		}
		writeFile(t, root, "sys/block/md127/md/dev-"+d+"/state", st)
	}
	symlink(t, root, "sys/dev/block/9:127", "../../block/md127")
	symlink(t, root, "dev/md/volume2", "../md127")
	_ = os.MkdirAll(filepath.Join(root, "sys/fs/btrfs/9a9a9a9a-1111-4222-8333-444455556666/devices/md127"), 0o755)
	writeFile(t, root, "proc/self/mountinfo", "25 1 8:1 / / rw - ext4 /dev/sda1 rw\n90 25 9:127 / /volume2 rw - btrfs /dev/md127 rw\n")
	return &storage.Inventory{Sys: filepath.Join(root, "sys"), Dev: filepath.Join(root, "dev"), Proc: filepath.Join(root, "proc")}
}

func newM22(t *testing.T, inv *storage.Inventory) (*m22, *fakeAgent, *fakeM22) {
	ag := &fakeAgent{fp: map[string]string{}}
	s := newTestStorage(t, ag)
	s.inv = inv
	f := &fakeM22{}
	return &m22{s: s, agent: f, unit: time.Hour, schedErr: map[string]bool{}}, ag, f
}

const vol2 = "9a9a9a9a-1111-4222-8333-444455556666"

func diskID(t *testing.T, m *m22, dev string) string {
	t.Helper()
	ds, _ := m.s.inv.Disks(context.Background())
	for _, d := range ds {
		if d.Dev == dev {
			return d.ID
		}
	}
	t.Fatalf("no disk %s", dev)
	return ""
}

func waitStorageJob(t *testing.T, s *storageAPI, id string) jobs.Job {
	t.Helper()
	for range 1000 {
		if j, ok := s.jobs.Get(id); ok && (j.State == jobs.Succeeded || j.State == jobs.Failed) {
			time.Sleep(5 * time.Millisecond)
			return j
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("el job no terminó")
	return jobs.Job{}
}

func TestReplacePlanRules(t *testing.T) {
	ctx := context.Background()
	m, _, f := newM22(t, raidInventory(t, "raid5", true))
	if _, err := m.replacePlan(ctx, vol2, "", diskID(t, m, "sde")); code(err) != codeTooSmall {
		t.Fatalf("chico: %v", err)
	}
	if _, err := m.replacePlan(ctx, vol2, "", diskID(t, m, "sdb")); code(err) != codeNotEligible {
		t.Fatalf("miembro: %v", err)
	}
	// Sin indicar el fallado, toma el que md marca como faulty.
	p, err := m.replacePlan(ctx, vol2, "", diskID(t, m, "sdf"))
	if err != nil || p.Summary.Rebuild.From == nil || *p.Summary.Rebuild.From != diskID(t, m, "sdd") || p.Summary.Rebuild.To != diskID(t, m, "sdf") {
		t.Fatalf("plan: %+v %v", p, err)
	}
	if _, err := m.replace(ctx, p.PlanID, p.ConfirmToken, "volume1"); code(err) != codeMismatch {
		t.Fatalf("nombre: %v", err)
	}
	// Un token de reemplazo no sirve para ampliar.
	if _, err := m.expand(ctx, p.PlanID, p.ConfirmToken, "volume2"); code(err) != codePlanExpired {
		t.Fatalf("tipo de plan: %v", err)
	}
	p, _ = m.replacePlan(ctx, vol2, "", diskID(t, m, "sdf"))
	id, err := m.replace(ctx, p.PlanID, p.ConfirmToken, "volume2")
	if err != nil || id == "" {
		t.Fatal(err)
	}
	waitStorageJob(t, m.s, id)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.replaced) != 1 || f.replaced[0].Failed != "scsi-0QEMU_QEMU_HARDDISK_drive-scsi3" || f.replaced[0].Replacement.DevID != "scsi-0QEMU_QEMU_HARDDISK_drive-scsi5" {
		t.Fatalf("args: %+v", f.replaced)
	}

	// Sano y sin indicar disco: hay que decir cuál.
	m2, _, _ := newM22(t, raidInventory(t, "raid5", false))
	if _, err := m2.replacePlan(ctx, vol2, "", diskID(t, m2, "sdf")); code(err) != rtcrpc.CodeInvalidParams {
		t.Fatalf("sano: %v", err)
	}
}

func TestExpandPlanRules(t *testing.T) {
	ctx := context.Background()
	m, _, f := newM22(t, raidInventory(t, "raid5", false))
	p, err := m.expandPlan(ctx, vol2, []string{diskID(t, m, "sdf")})
	if err != nil || p.Summary.Usable != 3*33554432*512 || len(p.Summary.Disks) != 1 {
		t.Fatalf("plan: %+v %v", p, err)
	}
	id, err := m.expand(ctx, p.PlanID, p.ConfirmToken, "volume2")
	if err != nil {
		t.Fatal(err)
	}
	waitStorageJob(t, m.s, id)
	f.mu.Lock()
	if len(f.grown) != 1 || f.grown[0].RaidDevices != 4 || f.grown[0].Mount != "/volume2" {
		t.Fatalf("grow: %+v", f.grown)
	}
	f.mu.Unlock()
	if _, err := m.expandPlan(ctx, vol2, []string{diskID(t, m, "sde")}); code(err) != codeTooSmall {
		t.Fatalf("chico: %v", err)
	}

	r1, _, _ := newM22(t, raidInventory(t, "raid1", false))
	if _, err := r1.expandPlan(ctx, vol2, []string{diskID(t, r1, "sdf")}); code(err) != codeNotSupported {
		t.Fatalf("raid1: %v", err)
	}
	deg, _, _ := newM22(t, raidInventory(t, "raid5", true))
	if _, err := deg.expandPlan(ctx, vol2, []string{diskID(t, deg, "sdf")}); code(err) != codeVolumeBusy {
		t.Fatalf("degradado: %v", err)
	}
}

func TestLocateNotSupported(t *testing.T) {
	m, _, _ := newM22(t, raidInventory(t, "raid5", false))
	if err := m.locate(context.Background(), diskID(t, m, "sdf")); code(err) != codeNotSupported {
		t.Fatalf("locate: %v", err)
	}
}

func TestSnapshotKind(t *testing.T) {
	if snapshotKind("2026-10-14T15-00-00Z.hourly") != "scheduled" || snapshotKind("2026-10-14T15-00-00Z") != "manual" {
		t.Fatal("kind")
	}
}
