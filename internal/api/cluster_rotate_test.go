package api

import (
	"testing"

	"github.com/lordbasex/go-link-tangoos/internal/clusterpki"
)

// rotNode es la PKI de un nodo para los tests de la rotación.
type rotNode struct {
	cn  string
	pki *clusterPKI
}

func (n rotNode) presents(t *testing.T) []byte {
	t.Helper()
	return n.pki.read("node.pem")
}

// accepts dice si la confianza de n acepta el certificado que presenta otro nodo.
func (n rotNode) accepts(t *testing.T, cert []byte) bool {
	t.Helper()
	c, err := clusterpki.ParseCert(cert)
	if err != nil {
		t.Fatal(err)
	}
	return clusterpki.Verify(n.pki.read("ca.pem"), c) == nil
}

// Tres nodos con la misma CA (como después de unirse).
func rotCluster(t *testing.T) []rotNode {
	t.Helper()
	ca, caKey, err := clusterpki.NewCA("0001")
	if err != nil {
		t.Fatal(err)
	}
	var out []rotNode
	for _, cn := range []string{"n1", "n2", "n3"} {
		p := newClusterPKI(t.TempDir())
		key, _ := p.nodeKey()
		csr, _ := clusterpki.CSR(key, cn)
		cert, err := clusterpki.Sign(ca, caKey, csr, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.install(cert, ca, caKey); err != nil {
			t.Fatal(err)
		}
		out = append(out, rotNode{cn, p})
	}
	return out
}

func stageAll(t *testing.T, nodes []rotNode) []byte {
	t.Helper()
	ca, caKey, _ := clusterpki.NewCA("0002")
	for _, n := range nodes {
		key, _ := n.pki.nodeKey()
		csr, _ := clusterpki.CSR(key, n.cn)
		cert, _ := clusterpki.Sign(ca, caKey, csr, nil)
		if err := n.pki.rotateStage(n.cn, cert, ca, caKey); err != nil {
			t.Fatal(err)
		}
	}
	return ca
}

// Entre dos nodos cualesquiera, en cualquier punto de la rotación (cada fase se aplica
// nodo por nodo), el certificado de uno vale para el otro. Al terminar, solo valen los
// certificados de la CA nueva: el de un nodo que salió antes ya no abre nada.
func TestRotateNeverCutsTheChannel(t *testing.T) {
	nodes := rotCluster(t)
	removed := nodes[2].presents(t) // n3 sale del clúster con su certificado (y la clave de la CA)
	nodes = nodes[:2]
	check := func(when string) {
		t.Helper()
		for _, a := range nodes {
			for _, b := range nodes {
				if !a.accepts(t, b.presents(t)) {
					t.Fatalf("%s: %s no acepta el certificado de %s", when, a.cn, b.cn)
				}
			}
		}
	}
	check("antes")
	oldFP := nodes[0].pki.caFingerprint()
	newCA := stageAll(t, nodes)
	check("después de stage")
	if nodes[0].pki.caFingerprint() != oldFP {
		t.Fatal("stage no debería cambiar la CA que se muestra")
	}
	for _, n := range nodes {
		if err := n.pki.rotateSwitch(); err != nil {
			t.Fatal(err)
		}
		check("switch de " + n.cn)
	}
	newFP, _ := clusterpki.CertFingerprint(newCA)
	if nodes[0].pki.caFingerprint() != newFP {
		t.Fatal("después de switch se muestra la CA nueva")
	}
	nodes[0].pki.addRevoked("n3")
	for _, n := range nodes {
		if err := n.pki.rotateFinalize(); err != nil {
			t.Fatal(err)
		}
		check("finalize de " + n.cn)
	}
	for _, n := range nodes {
		if n.accepts(t, removed) {
			t.Fatalf("%s todavía acepta el certificado del nodo que salió", n.cn)
		}
		for _, f := range []string{rotNextCert, rotNextCA, rotNextCAKey, rotPrevCA} {
			if len(n.pki.read(f)) > 0 {
				t.Fatalf("%s: quedó %s", n.cn, f)
			}
		}
	}
	if len(nodes[0].pki.revoked()) != 0 {
		t.Fatal("la lista de nodos pendientes no se limpió")
	}
	// La clave de la CA ahora es la nueva: lo que firme sirve con la confianza nueva.
	key, _ := nodes[0].pki.nodeKey()
	csr, _ := clusterpki.CSR(key, "n9")
	cert, err := clusterpki.Sign(nodes[0].pki.read("ca.pem"), nodes[0].pki.read("ca.key"), csr, nil)
	if err != nil || !nodes[1].accepts(t, cert) {
		t.Fatalf("la clave de la CA no quedó actualizada: %v", err)
	}
}

func TestRotateAbortRestoresTrust(t *testing.T) {
	nodes := rotCluster(t)
	before := string(nodes[0].pki.read("ca.pem"))
	stageAll(t, nodes)
	if string(nodes[0].pki.read("ca.pem")) == before {
		t.Fatal("stage no sumó la CA nueva")
	}
	if err := nodes[0].pki.rotateAbort(); err != nil {
		t.Fatal(err)
	}
	if string(nodes[0].pki.read("ca.pem")) != before || len(nodes[0].pki.read(rotNextCert)) > 0 {
		t.Fatal("abort no dejó la confianza como estaba")
	}
	// Después de switch, abort no toca nada (la rotación se completa repitiéndola).
	if err := nodes[1].pki.rotateSwitch(); err != nil {
		t.Fatal(err)
	}
	after := string(nodes[1].pki.read("ca.pem"))
	if err := nodes[1].pki.rotateAbort(); err != nil || string(nodes[1].pki.read("ca.pem")) != after {
		t.Fatal("abort después de switch cambió la confianza")
	}
	if err := nodes[1].pki.rotateFinalize(); err != nil {
		t.Fatal(err)
	}
}

func TestRotateStageRejectsForeignMaterial(t *testing.T) {
	nodes := rotCluster(t)
	n := nodes[0]
	ca, caKey, _ := clusterpki.NewCA("0002")
	other, otherKey, _ := clusterpki.NewCA("0003")
	key, _ := n.pki.nodeKey()
	csr, _ := clusterpki.CSR(key, n.cn)
	cert, _ := clusterpki.Sign(ca, caKey, csr, nil)
	wrongCN, _ := clusterpki.CSR(key, "otro")
	certOtherCN, _ := clusterpki.Sign(ca, caKey, wrongCN, nil)
	foreignKey, _ := clusterpki.NewNodeKey()
	foreignCSR, _ := clusterpki.CSR(foreignKey, n.cn)
	certForeign, _ := clusterpki.Sign(ca, caKey, foreignCSR, nil)
	for name, args := range map[string][3][]byte{
		"CN de otro nodo":        {certOtherCN, ca, caKey},
		"clave de otro nodo":     {certForeign, ca, caKey},
		"clave de otra CA":       {cert, ca, otherKey},
		"certificado de otra CA": {cert, other, otherKey},
		"dos CA en el pedido":    {cert, clusterpki.Bundle(ca, other), caKey},
	} {
		if err := n.pki.rotateStage(n.cn, args[0], args[1], args[2]); err == nil {
			t.Fatalf("%s: se aceptó", name)
		}
	}
	if len(n.pki.read(rotNextCert)) > 0 {
		t.Fatal("un stage rechazado dejó archivos")
	}
}
