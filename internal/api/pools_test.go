package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// sizedAgent es fakeAgent con tamaños por disco (el RAID híbrido depende de ellos).
type sizedAgent struct {
	*fakeAgent
	sizes map[string]uint64
}

func (s sizedAgent) DiskInspect(ctx context.Context, ids []string) ([]agentproto.DiskInspection, error) {
	out, err := s.fakeAgent.DiskInspect(ctx, ids)
	for i := range out {
		if n, ok := s.sizes[out[i].DevID]; ok {
			out[i].Size = n
		}
	}
	return out, err
}

type fakePoolsAgent struct {
	mu       sync.Mutex
	created  []agentproto.PoolCreateArgs
	lvs      []agentproto.LVCreateArgs
	unlocks  int
	wrongKey bool
}

func (f *fakePoolsAgent) PoolCreate(_ context.Context, a agentproto.PoolCreateArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, a)
	return nil
}
func (f *fakePoolsAgent) PoolExpand(context.Context, agentproto.PoolExpandArgs) error   { return nil }
func (f *fakePoolsAgent) PoolReplace(context.Context, agentproto.PoolReplaceArgs) error { return nil }
func (f *fakePoolsAgent) PoolDelete(context.Context, agentproto.PoolDeleteArgs) error   { return nil }
func (f *fakePoolsAgent) LVCreate(_ context.Context, a agentproto.LVCreateArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lvs = append(f.lvs, a)
	return nil
}
func (f *fakePoolsAgent) LVResize(context.Context, agentproto.LVResizeArgs) error { return nil }
func (f *fakePoolsAgent) LVDelete(context.Context, agentproto.LVDeleteArgs) error { return nil }
func (f *fakePoolsAgent) LUKSUnlock(context.Context, agentproto.LUKSUnlockArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unlocks++
	if f.wrongKey {
		return agentproto.Errf(agentproto.CodeWrongPassphrase, "la frase no abre este volumen")
	}
	return nil
}
func (f *fakePoolsAgent) LUKSLock(context.Context, string) error { return nil }
func (f *fakePoolsAgent) LUKSPassphrase(context.Context, agentproto.LUKSPassphraseArgs) error {
	return nil
}
func (f *fakePoolsAgent) LUKSKeyDevice(context.Context, agentproto.LUKSKeyDeviceArgs) error {
	return nil
}
func (f *fakePoolsAgent) KeyDevices(context.Context) ([]agentproto.KeyDevice, error) {
	return []agentproto.KeyDevice{{UUID: "ABCD-1234", FS: "vfat", Label: "LLAVE", Disk: "sde", Dev: "sde1"}}, nil
}
func (f *fakePoolsAgent) CacheCreate(context.Context, agentproto.CacheCreateArgs) error { return nil }
func (f *fakePoolsAgent) CacheRemove(context.Context, agentproto.CacheRemoveArgs) error { return nil }

type staticPools struct{ rep *agentproto.PoolReport }

func (s staticPools) PoolReport(context.Context) (*agentproto.PoolReport, error) { return s.rep, nil }

func newPools(t *testing.T, rep *agentproto.PoolReport) (*poolsAPI, *fakePoolsAgent, string) {
	t.Helper()
	inv := raidInventory(t, "raid5", false)
	inv.PoolAgent = staticPools{rep}
	ag := &fakeAgent{fp: map[string]string{}}
	s := newTestStorage(t, ag)
	s.inv = inv
	s.agent = sizedAgent{ag, map[string]uint64{
		"scsi-0QEMU_QEMU_HARDDISK_drive-scsi4": 8 << 30, "scsi-0QEMU_QEMU_HARDDISK_drive-scsi5": 16 << 30, "scsi-0QEMU_QEMU_HARDDISK_drive-scsi6": 16 << 30,
	}}
	f := &fakePoolsAgent{}
	p := &poolsAPI{s: s, agent: f, failures: map[string][]time.Time{}, tried: map[string]bool{}}
	s.a.pools = p
	return p, f, filepath.Dir(s.path)
}

func idOf(t *testing.T, p *poolsAPI, dev string) string {
	t.Helper()
	ds, _ := p.s.inv.Disks(context.Background())
	for _, d := range ds {
		if d.Dev == dev {
			return d.ID
		}
	}
	t.Fatalf("no disk %s", dev)
	return ""
}

func TestPoolCreatePlanHybrid(t *testing.T) {
	ctx := context.Background()
	p, f, _ := newPools(t, &agentproto.PoolReport{})
	ids := []string{idOf(t, p, "sde"), idOf(t, p, "sdf"), idOf(t, p, "sdg")}
	plan, err := p.createPlan(ctx, "pool1", "hybrid1", ids)
	if err != nil {
		t.Fatal(err)
	}
	ts := plan.Summary.Tiers
	if len(ts) != 2 || ts[0].Level != "raid5" || len(ts[0].Members) != 3 || ts[1].Level != "raid1" || len(ts[1].Members) != 2 {
		t.Fatalf("tramos = %+v", ts)
	}
	// 8+16+16 GiB: RAID 5 de 3×8 (16) + RAID 1 de 8 (8) ≈ 24 GiB; sin espacio reservado.
	if g := plan.Summary.Usable >> 30; g != 23 && g != 24 {
		t.Errorf("útil = %d GiB", g)
	}
	if plan.Summary.Unused != 0 {
		t.Errorf("sin usar = %d", plan.Summary.Unused)
	}
	if _, err := p.create(ctx, plan.PlanID, plan.ConfirmToken, "pool2"); code(err) != codeMismatch {
		t.Fatalf("nombre: %v", err)
	}
	id, err := p.create(ctx, plan.PlanID, plan.ConfirmToken, "pool1")
	if err != nil {
		t.Fatal(err)
	}
	waitStorageJob(t, p.s, id)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.created) != 1 || f.created[0].Profile != "hybrid1" || len(f.created[0].Disks) != 3 || f.created[0].Name != "pool1" {
		t.Fatalf("agente: %+v", f.created)
	}
}

func TestPoolCreatePlanRules(t *testing.T) {
	ctx := context.Background()
	p, _, _ := newPools(t, &agentproto.PoolReport{})
	free := []string{idOf(t, p, "sde"), idOf(t, p, "sdf"), idOf(t, p, "sdg")}
	if _, err := p.createPlan(ctx, "volume2", "raid5", free); code(err) != codeVolumeExists {
		t.Errorf("nombre de un volumen: %v", err)
	}
	if _, err := p.createPlan(ctx, "Pool-1", "raid5", free); code(err) != "INVALID_PARAMS" {
		t.Errorf("nombre inválido: %v", err)
	}
	if _, err := p.createPlan(ctx, "pool1", "hybrid2", free); code(err) != "INVALID_PARAMS" {
		t.Errorf("híbrido-2 con 3 discos: %v", err)
	}
	if _, err := p.createPlan(ctx, "pool1", "raid1", []string{idOf(t, p, "sdb"), idOf(t, p, "sdf")}); code(err) != codeNotEligible {
		t.Errorf("disco en uso: %v", err)
	}
	// RAID 5 con tamaños distintos: avisa que el híbrido aprovecharía más.
	plan, err := p.createPlan(ctx, "pool1", "raid5", free)
	if err != nil || !strings.Contains(strings.Join(plan.Summary.Notes, " "), "híbrido") {
		t.Errorf("nota de tamaños: %+v %v", plan.Summary.Notes, err)
	}
}

func poolReport() *agentproto.PoolReport {
	return &agentproto.PoolReport{Pools: []agentproto.PoolInfo{{
		Name: "pool1", UUID: "VG1", Profile: "hybrid1",
		PVs:   []agentproto.PoolPV{{Dev: "/dev/md125", MD: "pool1_md1", Role: "data", Size: 20 << 30, Free: 10 << 30, Disks: []string{"sdf", "sdg"}}},
		Tiers: []agentproto.PoolTier{{Index: 1, Level: "raid1", Size: 20 << 30, Members: []string{"sdf", "sdg"}}},
		LVs: []agentproto.PoolLV{
			{Name: "volume5", UUID: "LV5", Size: 10 << 30, FS: "btrfs", Encrypted: true, KeyDevice: "ABCD-1234"},
		},
	}}}
}

func TestPoolVolumeCreate(t *testing.T) {
	ctx := context.Background()
	p, f, dir := newPools(t, poolReport())
	if _, err := p.createVolume(ctx, "VG1", "volume6", 11<<30, "btrfs", "", ""); code(err) != codeNoSpace {
		t.Errorf("sin espacio: %v", err)
	}
	if _, err := p.createVolume(ctx, "VG1", "volume5", 1<<30, "btrfs", "", ""); code(err) != codeVolumeExists {
		t.Errorf("nombre repetido: %v", err)
	}
	if _, err := p.createVolume(ctx, "VG1", "volume6", 1<<30, "zfs", "", ""); code(err) != "INVALID_PARAMS" {
		t.Errorf("fs: %v", err)
	}
	if _, err := p.createVolume(ctx, "VG1", "volume6", 1<<30, "btrfs", "corta", ""); code(err) != "INVALID_PARAMS" {
		t.Errorf("frase corta: %v", err)
	}
	const secret = "una frase de prueba muy larga"
	id, err := p.createVolume(ctx, "VG1", "volume6", 0, "xfs", secret, "ABCD-1234")
	if err != nil {
		t.Fatal(err)
	}
	waitStorageJob(t, p.s, id)
	f.mu.Lock()
	got := f.lvs
	f.mu.Unlock()
	if len(got) != 1 || got[0].Passphrase != secret || got[0].Size != 10<<30 || got[0].FS != "xfs" || got[0].Pool != "pool1" || got[0].KeyDevice != "ABCD-1234" {
		t.Fatalf("agente: %+v", got)
	}
	// La frase no queda en ningún archivo de estado de tangod (jobs, storage.json).
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, _ := os.ReadFile(path); strings.Contains(string(b), secret) {
				t.Errorf("la frase quedó en %s", path)
			}
		}
		return nil
	})
}

func TestUnlockRateLimit(t *testing.T) {
	ctx := context.Background()
	p, f, _ := newPools(t, poolReport())
	f.wrongKey = true
	for i := 0; i < unlockMaxFails; i++ {
		if err := p.unlock(ctx, "LV5", "frase equivocada 1", false); code(err) != agentproto.CodeWrongPassphrase {
			t.Fatalf("intento %d: %v", i, err)
		}
	}
	if err := p.unlock(ctx, "LV5", "frase equivocada 1", false); code(err) != agentproto.CodeRateLimited {
		t.Fatalf("sexto intento: %v", err)
	}
	if f.unlocks != unlockMaxFails {
		t.Errorf("el agente recibió %d intentos", f.unlocks)
	}
	if err := p.unlock(ctx, "LV5", "", false); code(err) != "INVALID_PARAMS" {
		t.Errorf("sin frase: %v", err)
	}
}

func TestAutoUnlockWithKeyDevice(t *testing.T) {
	ctx := context.Background()
	p, f, _ := newPools(t, poolReport())
	p.autoUnlock(ctx)
	p.autoUnlock(ctx) // una sola vez por volumen y llave
	if f.unlocks != 1 {
		t.Fatalf("desbloqueos automáticos = %d", f.unlocks)
	}
}

func TestCachePlanRules(t *testing.T) {
	ctx := context.Background()
	rep := poolReport()
	rep.Pools[0].LVs[0].Encrypted = false
	p, _, _ := newPools(t, rep)
	if _, err := p.cachePlan(ctx, "LV5", []string{idOf(t, p, "sde")}, "readwrite"); code(err) != "INVALID_PARAMS" {
		t.Errorf("escritura con 1 SSD: %v", err)
	}
	if _, err := p.cachePlan(ctx, "LV5", []string{idOf(t, p, "sde")}, "read"); code(err) != "NOT_SSD" {
		t.Errorf("HDD como caché: %v", err)
	}
}

func TestPoolDeleteNeedsEmpty(t *testing.T) {
	p, _, _ := newPools(t, poolReport())
	if _, err := p.deletePlan(context.Background(), "VG1"); code(err) != agentproto.CodePoolNotEmpty {
		t.Fatalf("grupo con volúmenes: %v", err)
	}
	_ = storage.Pool{}
}
