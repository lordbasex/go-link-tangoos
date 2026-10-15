package api

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/clusterpki"
	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// fakeCluster2 suma las ops de la segunda etapa y registra lo que se le pide.
type fakeCluster2 struct {
	fakeCluster
	tls    []agentproto.GlusterTLSArgs
	probes []string
}

func (f *fakeCluster2) GlusterTLS(_ context.Context, a agentproto.GlusterTLSArgs) (*agentproto.GlusterTLSState, error) {
	f.tls = append(f.tls, a)
	return &agentproto.GlusterTLSState{Files: a.Cert != "", Secure: a.Secure}, nil
}
func (f *fakeCluster2) GlusterPeer(_ context.Context, a agentproto.GlusterPeerArgs) error {
	f.probes = append(f.probes, a.Action+" "+a.Host)
	return nil
}
func (*fakeCluster2) GlusterVolumeTLS(context.Context, agentproto.GlusterVolumeTLSArgs) error { return nil }
func (*fakeCluster2) GlusterBrickLV(context.Context, agentproto.GlusterBrickLVArgs) (*agentproto.GlusterBrickLVResult, error) {
	return &agentproto.GlusterBrickLVResult{Path: "/srv/gluster/gv0/brick"}, nil
}
func (*fakeCluster2) GlusterQuota(context.Context, agentproto.GlusterQuotaArgs) ([]gluster.Quota, error) {
	return nil, nil
}
func (*fakeCluster2) GlusterRebalance(context.Context, agentproto.GlusterRebalanceArgs) (*gluster.Task, error) {
	return nil, nil
}
func (*fakeCluster2) GlusterBricksTask(context.Context, agentproto.GlusterBricksArgs) (*gluster.Task, error) {
	return nil, nil
}
func (*fakeCluster2) GlusterSnapshot(context.Context, agentproto.GlusterSnapshotArgs) ([]gluster.Snapshot, error) {
	return nil, nil
}

func newJoinNode(t *testing.T) (*clusterAPI, *fakeCluster2) {
	f := &fakeCluster2{}
	c := newFakeClusterAPI(&f.fakeCluster)
	c.ag = f
	c.joins = map[string]*clusterJoin{}
	c.infos = map[string]*CtlInfo{}
	c.pki = newClusterPKI(t.TempDir())
	c.m = &m4API{a: &API{d: Deps{Log: slog.Default()}}}
	return c, f
}

// La unión de punta a punta: el invitado (B) abre una invitación con código; el que suma
// (A) prepara con el código, ve la huella de B, confirma; B recibe su certificado de la CA
// de A y A hace el peer probe.
func TestClusterJoinFlow(t *testing.T) {
	a, fa := newJoinNode(t)
	b, fb := newJoinNode(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := b.ctlServer("")
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	old := ctlPort
	ctlPort = ln.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { ctlPort = old })

	code := clusterpki.NewJoinCode()
	b.invites["127.0.0.1"] = clusterInvite{Host: "127.0.0.1", Addrs: []string{"127.0.0.1"}, Expires: time.Now().Add(time.Minute), Code: code, Fingerprint: b.pki.nodeFingerprint()}
	ctx := context.Background()

	// Código equivocado: se rechaza y cuenta un intento.
	if _, err := a.prepareJoin(ctx, "127.0.0.1", "AAAAA-BBBBB"); err == nil || !strings.Contains(err.Error(), "código") {
		t.Fatalf("esperaba rechazo por código: %v", err)
	}
	if b.invites["127.0.0.1"].Fails != 1 {
		t.Fatalf("intentos fallidos: %d", b.invites["127.0.0.1"].Fails)
	}

	j, err := a.prepareJoin(ctx, "127.0.0.1", strings.ToLower(code))
	if err != nil {
		t.Fatal(err)
	}
	// La huella que A muestra es la que B muestra en su invitación.
	if j.FP != b.invites["127.0.0.1"].Fingerprint || j.CN != b.self() {
		t.Fatalf("huella %q vs %q, cn %q", j.FP, b.invites["127.0.0.1"].Fingerprint, j.CN)
	}
	if err := a.finishJoin(ctx, j, true); err != nil {
		t.Fatal(err)
	}
	if !a.pki.hasCA() || !b.pki.signed() || b.pki.caFingerprint() != a.pki.caFingerprint() {
		t.Fatal("B no quedó con la CA de A")
	}
	if b.invites["127.0.0.1"].Joined != a.pki.caFingerprint() {
		t.Fatal("la invitación de B no muestra la CA a la que se unió")
	}
	if len(fb.tls) != 1 || fb.tls[0].Cert == "" || fb.tls[0].Secure {
		t.Fatalf("B no instaló los archivos TLS: %+v", fb.tls)
	}
	if len(fa.probes) != 1 || fa.probes[0] != "probe 127.0.0.1" {
		t.Fatalf("probes de A: %v", fa.probes)
	}
	// Después de unirse, A habla con B por mTLS. B rechaza a A porque en el fake A no
	// figura como nodo del pool de B (el canal exige ser del clúster).
	var info CtlInfo
	if err := a.ctlCall(ctx, "127.0.0.1", "GET", "/v1/info", nil, &info); err == nil || !strings.Contains(err.Error(), "no es un nodo del clúster") {
		t.Fatalf("esperaba el rechazo por no ser del pool: %v", err)
	}
}

// Cinco códigos equivocados cierran la invitación.
func TestClusterJoinLockout(t *testing.T) {
	a, _ := newJoinNode(t)
	b, _ := newJoinNode(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := b.ctlServer("")
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	old := ctlPort
	ctlPort = ln.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { ctlPort = old })
	b.invites["127.0.0.1"] = clusterInvite{Host: "127.0.0.1", Addrs: []string{"127.0.0.1"}, Expires: time.Now().Add(time.Minute), Code: clusterpki.NewJoinCode()}
	for range 5 {
		_, _ = a.prepareJoin(context.Background(), "127.0.0.1", "AAAAA-BBBBB")
	}
	if _, ok := b.invites["127.0.0.1"]; ok {
		t.Fatal("la invitación sigue abierta después de 5 intentos")
	}
}
