package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/agentclient"
	"github.com/lordbasex/go-link-tangoos/internal/appproxy"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

type fakeApps struct {
	mu      sync.Mutex
	docker  agentproto.DockerStatus
	ups     []agentproto.AppUpArgs
	removed []string
	fail    string
}

func (f *fakeApps) DockerStatus(context.Context) (*agentproto.DockerStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.docker
	return &d, nil
}
func (f *fakeApps) DockerInstall(context.Context, string) error { return nil }
func (f *fakeApps) AppUp(_ context.Context, a agentproto.AppUpArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ups = append(f.ups, a)
	return nil
}
func (f *fakeApps) AppAction(context.Context, agentproto.AppActionArgs) error { return nil }
func (f *fakeApps) AppUninstall(_ context.Context, a agentproto.AppUninstallArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, a.ID)
	return nil
}
func (f *fakeApps) AppLogs(context.Context, string, int) ([]string, error) {
	return []string{"hola"}, nil
}
func (f *fakeApps) AppStats(_ context.Context, ids []string) ([]agentproto.AppStat, error) {
	out := []agentproto.AppStat{}
	for _, id := range ids {
		out = append(out, agentproto.AppStat{ID: id, State: "running", Containers: 1, Running: 1})
	}
	return out, nil
}
func (f *fakeApps) AppJob(_ context.Context, id string) (*agentclient.JobStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != "" {
		return &agentclient.JobStatus{OpID: id, State: "failed", Error: &agentproto.Error{Code: "OPERATION_FAILED", Message: f.fail}}, nil
	}
	return &agentclient.JobStatus{OpID: id, State: "succeeded", Progress: 1, Result: json.RawMessage(`{}`)}, nil
}

func newTestApps(t *testing.T, ag *fakeApps) *appsAPI {
	dir := t.TempDir()
	return &appsAPI{a: &API{d: Deps{Hub: rtcrpc.NewHub(Topics...)}}, agent: ag, jobs: jobs.Open(dir, nil), proxy: &appproxy.Manager{},
		path: filepath.Join(dir, "apps.json"), poll: 2 * time.Millisecond, tz: "UTC", apps: map[string]*installed{},
		volumes: func(context.Context) ([]storage.Volume, error) {
			return []storage.Volume{{ID: "vol1", Name: "volume1", Mount: "/volume1"}}, nil
		}}
}

func waitJob(t *testing.T, s *appsAPI, id string) jobs.Job {
	t.Helper()
	for range 500 {
		if j, ok := s.jobs.Get(id); ok && (j.State == jobs.Succeeded || j.State == jobs.Failed) {
			time.Sleep(5 * time.Millisecond) // onEnd
			return j
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("el job no terminó")
	return jobs.Job{}
}

func TestAppsInstallRequiresDocker(t *testing.T) {
	s := newTestApps(t, &fakeApps{})
	if _, err := s.install(context.Background(), "uptime-kuma", "vol1", nil, "nas"); code(err) != agentproto.CodeDockerMissing {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.install(context.Background(), "../etc", "vol1", nil, "nas"); code(err) != rtcrpc.CodeInvalidParams {
		t.Fatalf("id inválido: %v", err)
	}
}

func TestAppsInstallLifecycle(t *testing.T) {
	ag := &fakeApps{docker: agentproto.DockerStatus{Installed: true, Running: true, Compose: true}}
	s := newTestApps(t, ag)
	ctx := context.Background()
	id, err := s.install(ctx, "nextcloud", "vol1", nil, "172.17.0.44")
	if err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, s, id); j.State != jobs.Succeeded {
		t.Fatalf("job %+v", j)
	}
	up := ag.ups[0]
	if up.Mount != "/volume1" || up.Port != 8082 || !up.Pull || len(up.Options["ADMIN_PASSWORD"]) != 24 || up.Host != "172.17.0.44" {
		t.Fatalf("app.up = %+v", up)
	}
	// La contraseña generada queda persistida con permisos 0600.
	st, err := os.Stat(s.path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("apps.json: %v %v", err, st)
	}
	if _, err := s.install(ctx, "nextcloud", "vol1", nil, "x"); code(err) != "APP_EXISTS" {
		t.Fatalf("segunda instalación: %v", err)
	}
	list := s.list(ctx, "nas.local")
	if len(list) != 1 || list[0].State != "running" || list[0].URL == nil || *list[0].URL != "https://nas.local:9100/" {
		t.Fatalf("list = %+v", list)
	}
	if _, err := s.install(ctx, "nextcloud", "nope", nil, "x"); err == nil {
		t.Fatal("volumen inexistente aceptado")
	}
	uid, err := s.uninstall(ctx, "nextcloud", false)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, s, uid)
	if _, ok := s.get("nextcloud"); ok || len(ag.removed) != 1 {
		t.Fatal("no se desinstaló")
	}
}

func TestAppsInstallFailureMarksError(t *testing.T) {
	ag := &fakeApps{docker: agentproto.DockerStatus{Installed: true, Running: true, Compose: true}, fail: "no se pudieron bajar las imágenes"}
	s := newTestApps(t, ag)
	id, err := s.install(context.Background(), "uptime-kuma", "vol1", nil, "nas")
	if err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, s, id); j.State != jobs.Failed {
		t.Fatalf("job %+v", j)
	}
	if it, _ := s.get("uptime-kuma"); it.State != "error" || it.Error == "" {
		t.Fatalf("estado %+v", it)
	}
}
