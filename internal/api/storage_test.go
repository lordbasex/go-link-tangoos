package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/agentclient"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// fakeAgent simula tango-agent: huellas por disco y un volume.create que termina solo.
type fakeAgent struct {
	mu       sync.Mutex
	fp       map[string]string
	created  []agentproto.VolumeCreateArgs
	deleted  []agentproto.VolumeDeleteArgs
	perms    []agentproto.SharePermsArgs
	statuses int
}

func (f *fakeAgent) DiskInspect(_ context.Context, ids []string) ([]agentproto.DiskInspection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agentproto.DiskInspection
	for _, id := range ids {
		out = append(out, agentproto.DiskInspection{DevID: id, Size: 17179869184, Fingerprint: f.fp[id], Content: "vacío", Eligible: true})
	}
	return out, nil
}
func (f *fakeAgent) VolumeCreate(_ context.Context, a agentproto.VolumeCreateArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, a)
	return nil
}
func (f *fakeAgent) ScrubStart(context.Context, string, string) error { return nil }
func (f *fakeAgent) JobStatus(_ context.Context, id string) (*agentclient.JobStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses++
	if f.statuses < 3 {
		return &agentclient.JobStatus{OpID: id, State: "running", Progress: 0.4, Stage: "Creando Btrfs"}, nil
	}
	return &agentclient.JobStatus{OpID: id, State: "succeeded", Progress: 1, Result: json.RawMessage(`{"name":"volume1"}`)}, nil
}
func (f *fakeAgent) SubvolCreate(context.Context, string, string) error           { return nil }
func (f *fakeAgent) SnapshotCreate(context.Context, string, string, string) error { return nil }
func (f *fakeAgent) SambaApply(context.Context, string, string, string) (bool, error) {
	return false, agentproto.Errf(agentproto.CodeNotInstalled, "Samba no está instalado")
}
func (f *fakeAgent) VolumeDelete(_ context.Context, a agentproto.VolumeDeleteArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, a)
	return nil
}
func (f *fakeAgent) ShareDelete(context.Context, string, string) (*agentproto.ShareDeleteResult, error) {
	return &agentproto.ShareDeleteResult{}, nil
}
func (f *fakeAgent) SharePermissions(_ context.Context, a agentproto.SharePermsArgs) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.perms = append(f.perms, a)
	return false, nil
}
func (f *fakeAgent) UserEnsure(context.Context, string, string) error           { return nil }
func (f *fakeAgent) UserPassword(context.Context, string, string, string) error { return nil }

func writeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, root, rel, target string) {
	t.Helper()
	p := filepath.Join(root, rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

// Como la VM: sda sistema (/ en sda1), sdb y sdc libres.
func testInventory(t *testing.T) *storage.Inventory {
	root := t.TempDir()
	for i, d := range []string{"sda", "sdb", "sdc"} {
		writeFile(t, root, "sys/block/"+d+"/device/model", "QEMU HARDDISK\n")
		writeFile(t, root, "sys/block/"+d+"/device/serial", "TANGO-"+d+"\n")
		writeFile(t, root, "sys/block/"+d+"/size", "33554432\n")
		writeFile(t, root, "sys/block/"+d+"/queue/rotational", "1\n")
		_ = os.MkdirAll(filepath.Join(root, "sys/block", d, "holders"), 0o755)
		symlink(t, root, "dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_drive-scsi"+string(rune('0'+i)), "../../"+d)
	}
	writeFile(t, root, "sys/block/sda/sda1/partition", "1\n")
	symlink(t, root, "sys/dev/block/8:1", "../../block/sda/sda1")
	writeFile(t, root, "proc/self/mountinfo", "25 1 8:1 / / rw - ext4 /dev/sda1 rw\n")
	return &storage.Inventory{Sys: filepath.Join(root, "sys"), Dev: filepath.Join(root, "dev"), Proc: filepath.Join(root, "proc")}
}

func newTestStorage(t *testing.T, ag *fakeAgent) *storageAPI {
	dir := t.TempDir()
	s := &storageAPI{a: &API{d: Deps{Hub: rtcrpc.NewHub(Topics...)}}, agent: ag, jobs: jobs.Open(dir, nil), inv: testInventory(t),
		planner: newPlanner(), poll: 2 * time.Millisecond, path: filepath.Join(dir, "storage.json")}
	s.load()
	return s
}

func code(err error) string {
	var e *rtcrpc.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func diskIDs(t *testing.T, s *storageAPI) map[string]string { // dev → Disk.id
	disks, err := s.inv.Disks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range disks {
		out[d.Dev] = d.ID
	}
	return out
}

func TestPlanRejects(t *testing.T) {
	ag := &fakeAgent{fp: map[string]string{}}
	s := newTestStorage(t, ag)
	ids := diskIDs(t, s)
	ctx := context.Background()
	cases := []struct {
		name, profile string
		disks         []string
		want          string
	}{
		{"volume1", "raid1", []string{ids["sda"], ids["sdb"]}, codeNotEligible}, // disco del sistema
		{"volume1", "raid1", []string{ids["sdb"]}, rtcrpc.CodeInvalidParams},    // RAID1 con 1 disco
		{"volume1", "raid1", []string{ids["sdb"], ids["sdb"]}, rtcrpc.CodeInvalidParams},
		{"etc", "basic", []string{ids["sdb"]}, rtcrpc.CodeInvalidParams},
		{"volume1", "raid5", []string{ids["sdb"], ids["sdc"]}, rtcrpc.CodeInvalidParams},
		{"volume1", "basic", []string{"no-existe"}, "DEVICE_NOT_FOUND"},
	}
	for _, c := range cases {
		if _, err := s.plan(ctx, c.name, c.profile, c.disks); code(err) != c.want {
			t.Errorf("%s %s %v: %v (esperado %s)", c.name, c.profile, c.disks, err, c.want)
		}
	}
}

func TestPlanConfirmCreate(t *testing.T) {
	ag := &fakeAgent{fp: map[string]string{
		"scsi-0QEMU_QEMU_HARDDISK_drive-scsi1": strings.Repeat("a", 64),
		"scsi-0QEMU_QEMU_HARDDISK_drive-scsi2": strings.Repeat("b", 64),
	}}
	s := newTestStorage(t, ag)
	ids := diskIDs(t, s)
	ctx := context.Background()
	p, err := s.plan(ctx, "volume1", "raid1", []string{ids["sdb"], ids["sdc"]})
	if err != nil {
		t.Fatal(err)
	}
	if p.Summary.Usable != 17179869184 || len(p.Summary.Disks) != 2 || p.Summary.Disks[0].Content != "vacío" {
		t.Fatalf("resumen %+v", p.Summary)
	}

	// Token alterado o nombre mal escrito: no crea nada y el plan sigue vivo.
	bad := "0"
	if p.ConfirmToken[63:] == "0" { // alterar de verdad: el último carácter podía ya ser "0"
		bad = "1"
	}
	if _, err := s.create(ctx, p.PlanID, p.ConfirmToken[:63]+bad, "volume1"); code(err) != codeMismatch {
		t.Fatalf("token alterado: %v", err)
	}
	if _, err := s.create(ctx, p.PlanID, p.ConfirmToken, "volume2"); code(err) != codeMismatch {
		t.Fatalf("nombre: %v", err)
	}
	if _, err := s.create(ctx, "p_otro", p.ConfirmToken, "volume1"); code(err) != codePlanExpired {
		t.Fatalf("plan inexistente: %v", err)
	}

	id, err := s.create(ctx, p.PlanID, p.ConfirmToken, "volume1")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		if j, _ := s.jobs.Get(id); j.State == jobs.Succeeded || j.State == jobs.Failed {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if j, _ := s.jobs.Get(id); j.State != jobs.Succeeded {
		t.Fatalf("job %+v", j)
	}
	ag.mu.Lock()
	got := ag.created
	ag.mu.Unlock()
	if len(got) != 1 || got[0].OpID != id || got[0].Profile != "raid1" || got[0].Disks[1].Fingerprint != strings.Repeat("b", 64) {
		t.Fatalf("args al agente %+v", got)
	}
	// Un token sirve una sola vez.
	if _, err := s.create(ctx, p.PlanID, p.ConfirmToken, "volume1"); code(err) != codePlanExpired {
		t.Fatalf("reuso del token: %v", err)
	}
}

func TestCreateDetectsChangedDisk(t *testing.T) {
	ag := &fakeAgent{fp: map[string]string{"scsi-0QEMU_QEMU_HARDDISK_drive-scsi1": strings.Repeat("a", 64)}}
	s := newTestStorage(t, ag)
	p, err := s.plan(context.Background(), "volume1", "basic", []string{diskIDs(t, s)["sdb"]})
	if err != nil {
		t.Fatal(err)
	}
	ag.mu.Lock()
	ag.fp["scsi-0QEMU_QEMU_HARDDISK_drive-scsi1"] = strings.Repeat("c", 64) // alguien escribió en el disco
	ag.mu.Unlock()
	if _, err := s.create(context.Background(), p.PlanID, p.ConfirmToken, "volume1"); code(err) != codeChanged {
		t.Fatalf("esperaba DEVICE_CHANGED: %v", err)
	}
	if len(ag.created) != 0 {
		t.Fatal("no debía llamar a volume.create")
	}
}

func TestPlanExpires(t *testing.T) {
	pl := newPlanner()
	now := time.Now()
	pl.now = func() time.Time { return now }
	v := pl.add("volume1", "basic", []planDisk{{ByID: "x", Size: 1, Fingerprint: "f"}})
	if got, _, _ := pl.check(v.ID, v.Token, "volume1"); got == nil {
		t.Fatal("recién creado debería valer")
	}
	pl.now = func() time.Time { return now.Add(PlanTTL + time.Second) }
	if got, c, _ := pl.check(v.ID, v.Token, "volume1"); got != nil || c != codePlanExpired {
		t.Fatalf("vencido: %v %s", got, c)
	}
}
