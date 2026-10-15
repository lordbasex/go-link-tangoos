package api

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// fakeCluster es un agente con un clúster de 3 nodos y un volumen réplica 3.
type fakeCluster struct {
	calls   atomic.Int32
	delay   time.Duration
	allowed []string
}

func (f *fakeCluster) GlusterStatus(ctx context.Context, self string) (*agentproto.GlusterStatusResult, error) {
	f.calls.Add(1)
	time.Sleep(f.delay)
	return &agentproto.GlusterStatusResult{
		Installed: true, Running: true, Allowed: f.allowed,
		Peers: []gluster.Peer{{Host: "10.0.0.2", Connected: true}, {Host: "10.0.0.3", Connected: true}, {Host: self, Self: true, Connected: true}},
		Volumes: []gluster.Volume{{Name: "gv0", Status: "Started", Replica: 3, Online: 3, Bricks: []gluster.BrickStatus{
			{Brick: gluster.Brick{Host: "10.0.0.1", Path: "/volume1/@gluster/gv0"}, Online: true},
			{Brick: gluster.Brick{Host: "10.0.0.2", Path: "/volume1/@gluster/gv0"}, Online: true},
			{Brick: gluster.Brick{Host: "10.0.0.3", Path: "/volume1/@gluster/gv0"}, Online: true},
		}}},
	}, nil
}
func (f *fakeCluster) GlusterHealInfo(context.Context, string) ([]gluster.HealBrick, error) {
	return []gluster.HealBrick{
		{Brick: gluster.Brick{Host: "10.0.0.1", Path: "/volume1/@gluster/gv0"}, Connected: true, Pending: 5},
		{Brick: gluster.Brick{Host: "10.0.0.2", Path: "/volume1/@gluster/gv0"}, Connected: true, Pending: 5, SplitBrain: 1},
	}, nil
}
func (f *fakeCluster) GlusterFirewall(_ context.Context, a []string) error { f.allowed = a; return nil }
func (*fakeCluster) GlusterInstall(context.Context, agentproto.GlusterInstallArgs) error {
	return nil
}
func (*fakeCluster) GlusterPeer(context.Context, agentproto.GlusterPeerArgs) error { return nil }
func (*fakeCluster) GlusterVolumeCreate(context.Context, agentproto.GlusterVolumeCreateArgs) error {
	return nil
}
func (*fakeCluster) GlusterVolumeAction(context.Context, agentproto.GlusterVolumeActionArgs) error {
	return nil
}
func (*fakeCluster) GlusterVolumeOptions(context.Context, agentproto.GlusterVolumeOptionsArgs) error {
	return nil
}
func (*fakeCluster) GlusterHeal(context.Context, agentproto.GlusterHealArgs) error     { return nil }
func (*fakeCluster) GlusterBricks(context.Context, agentproto.GlusterBricksArgs) error { return nil }
func (*fakeCluster) GlusterMount(context.Context, agentproto.GlusterMountArgs) error   { return nil }
func (*fakeCluster) GlusterBrickCleanup(context.Context, agentproto.GlusterBrickCleanArgs) error {
	return nil
}

func newFakeClusterAPI(f *fakeCluster) *clusterAPI {
	return &clusterAPI{ag: f, invites: map[string]clusterInvite{}, latency: map[string]int{}, alerted: map[string]bool{},
		pending: map[string]int{}, heal: map[string][]gluster.HealBrick{}}
}

func TestClusterAllowList(t *testing.T) {
	f := &fakeCluster{}
	c := newFakeClusterAPI(f)
	ctx := context.Background()
	if _, err := c.invite(ctx, "10.0.0.9"); err != nil {
		t.Fatal(err)
	}
	c.invites["10.0.0.8"] = clusterInvite{Host: "10.0.0.8", Addrs: []string{"10.0.0.8"}, Expires: time.Now().Add(-time.Minute)}
	st, _ := f.GlusterStatus(ctx, "10.0.0.1")
	got := c.desiredAllowed(st)
	// Nodos del clúster + invitación vigente; la vencida no, y este nodo tampoco.
	if want := []string{"10.0.0.2", "10.0.0.3", "10.0.0.9"}; !slices.Equal(got, want) {
		t.Fatalf("allow-list %v, esperaba %v", got, want)
	}
	if _, ok := c.invites["10.0.0.8"]; ok {
		t.Error("la invitación vencida tenía que borrarse")
	}
	if err := c.syncFirewall(ctx, st); err != nil || !slices.Equal(f.allowed, got) {
		t.Fatalf("sync: %v %v", f.allowed, err)
	}
	if _, err := c.invite(ctx, "-help"); err == nil {
		t.Error("host inválido")
	}
}

func TestClusterStatusCacheWhileBusy(t *testing.T) {
	f := &fakeCluster{}
	c := newFakeClusterAPI(f)
	ctx := context.Background()
	if _, err := c.agentStatus(ctx, true); err != nil {
		t.Fatal(err)
	}
	c.stAt = time.Now().Add(-10 * time.Second) // vencida para statusTTL
	c.busy.Add(1)                              // hay un heal info en curso
	before := f.calls.Load()
	if _, err := c.agentStatus(ctx, false); err != nil || f.calls.Load() != before {
		t.Fatalf("con el agente ocupado tenía que responder con la caché (llamadas %d→%d)", before, f.calls.Load())
	}
	c.busy.Add(-1)
	if _, err := c.agentStatus(ctx, false); err != nil || f.calls.Load() != before+1 {
		t.Fatal("libre y vencida: tenía que pedir al agente")
	}
}

func TestClusterHealMerge(t *testing.T) {
	f := &fakeCluster{}
	c := newFakeClusterAPI(f)
	ctx := context.Background()
	st, _ := f.GlusterStatus(ctx, "10.0.0.1")
	c.refreshHeal(ctx, "gv0")
	vols := c.withHeal(st.Volumes)
	if vols[0].HealPending != 10 || vols[0].SplitBrain != 1 || vols[0].Bricks[1].SplitBrain != 1 {
		t.Fatalf("%+v", vols[0])
	}
	if st.Volumes[0].HealPending != 0 || st.Volumes[0].Bricks[1].SplitBrain != 0 {
		t.Error("withHeal no tiene que tocar el estado cacheado del agente")
	}
}
