package api

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/clusterpki"
	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Rotación de la CA del clúster (docs/17 §3.3, ADR 0014).
//
// Cada nodo guarda una copia de la clave de la CA (así cualquiera puede sumar a otro), de
// modo que un nodo que sale se la lleva: puede firmarse un certificado con el nombre de
// otro nodo. Una CRL no lo frena (firmaría también otra CRL); lo que lo frena es cambiar de
// CA. La rotación tiene tres fases en todos los nodos, y entre una y otra cada nodo confía
// en las dos CA, así el canal de control nunca queda cortado entre dos nodos:
//
//  1. stage: el nodo recibe su certificado nuevo y la CA nueva (con su clave); confía en
//     las dos y sigue presentando el certificado anterior.
//  2. switch: presenta el certificado nuevo (sigue confiando en las dos).
//  3. finalize: confía solo en la CA nueva y deja los archivos de GlusterFS listos.
//
// Después, si GlusterFS usa TLS (management o E/S), los volúmenes se detienen, glusterd se
// reinicia en cada nodo y los volúmenes vuelven a arrancar y montarse, como al prender el
// TLS. Si algo falla en stage, los nodos vuelven a la CA anterior; desde switch, la
// rotación se puede repetir (los nodos confían en las dos CA hasta terminarla).

// Archivos de la rotación en <StateDir>/cluster.
const (
	rotNextCert  = "next.pem"    // certificado nuevo de este nodo (stage → switch)
	rotNextCA    = "next-ca.pem" // CA nueva (stage → finalize)
	rotNextCAKey = "next-ca.key" // su clave (stage → switch)
	rotPrevCA    = "prev-ca.pem" // confianza antes de stage (para abort)
	revokedFile  = "revoked.json"
)

// ---- Lado de cada nodo -------------------------------------------------------------------

func (p *clusterPKI) remove(names ...string) {
	for _, n := range names {
		_ = os.Remove(p.path(n))
	}
}

// rotateStage guarda el certificado y la CA nuevos y pasa a confiar en las dos CA.
func (p *clusterPKI) rotateStage(cn string, cert, ca, caKey []byte) error {
	key, err := p.nodeKey()
	if err != nil {
		return err
	}
	if _, err := tls.X509KeyPair(cert, key); err != nil {
		return errors.New("el certificado nuevo no corresponde a la clave de este nodo")
	}
	cas, err := clusterpki.ParseCerts(ca)
	if err != nil || len(cas) != 1 || !cas[0].IsCA {
		return errors.New("CA nueva inválida")
	}
	leaf, _ := clusterpki.ParseCert(cert)
	if leaf == nil || leaf.Subject.CommonName != cn || clusterpki.Verify(ca, leaf) != nil {
		return errors.New("certificado nuevo inválido para este nodo")
	}
	k, err := clusterpki.ParseKey(caKey)
	if err != nil || !k.PublicKey.Equal(cas[0].PublicKey) {
		return errors.New("la clave no es la de la CA nueva")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cur := p.read("ca.pem")
	if len(cur) == 0 {
		return errors.New("este nodo no tiene la CA del clúster")
	}
	// Un stage repetido (reintento) conserva la confianza de antes del primero.
	if len(p.read(rotPrevCA)) == 0 {
		if err := p.write(rotPrevCA, cur, 0o644); err != nil {
			return err
		}
	}
	for _, f := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{{rotNextCAKey, caKey, 0o600}, {rotNextCA, ca, 0o644}, {rotNextCert, cert, 0o644}} {
		if err := p.write(f.name, f.data, f.mode); err != nil {
			return err
		}
	}
	return p.write("ca.pem", clusterpki.Bundle(cur, ca), 0o644)
}

// rotateSwitch pasa a presentar el certificado nuevo (y a firmar con la CA nueva).
func (p *clusterPKI) rotateSwitch() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	cert, caKey, ca := p.read(rotNextCert), p.read(rotNextCAKey), p.read(rotNextCA)
	if len(cert) == 0 || len(caKey) == 0 || len(ca) == 0 {
		return errors.New("no hay una rotación preparada en este nodo")
	}
	if err := p.write("ca.key", caKey, 0o600); err != nil {
		return err
	}
	if err := p.write("node.pem", cert, 0o644); err != nil {
		return err
	}
	// La nueva primero: es la que se muestra como CA del clúster.
	if err := p.write("ca.pem", clusterpki.Bundle(ca, p.read("ca.pem")), 0o644); err != nil {
		return err
	}
	p.remove(rotNextCert, rotNextCAKey)
	return nil
}

// rotateFinalize deja de confiar en la CA anterior.
func (p *clusterPKI) rotateFinalize() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ca := p.read(rotNextCA)
	if len(ca) == 0 {
		return errors.New("no hay una rotación en curso en este nodo")
	}
	if len(p.read(rotNextCert)) > 0 {
		return errors.New("este nodo todavía no presenta el certificado nuevo")
	}
	if err := p.write("ca.pem", ca, 0o644); err != nil {
		return err
	}
	p.remove(rotNextCA, rotPrevCA, revokedFile)
	return nil
}

// rotateAbort deshace un stage (solo antes de switch).
func (p *clusterPKI) rotateAbort() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.read(rotNextCert)) == 0 {
		return nil // no había stage, o ya pasó switch: no se toca
	}
	if prev := p.read(rotPrevCA); len(prev) > 0 {
		if err := p.write("ca.pem", prev, 0o644); err != nil {
			return err
		}
	}
	p.remove(rotNextCert, rotNextCA, rotNextCAKey, rotPrevCA)
	return nil
}

// revokedNode es un nodo que salió del clúster después de la última rotación: conserva la
// clave de la CA actual hasta que se rote.
type revokedNode struct {
	Name string    `json:"name"`
	At   time.Time `json:"at"`
}

func (p *clusterPKI) revoked() []revokedNode {
	var out []revokedNode
	_ = json.Unmarshal(p.read(revokedFile), &out)
	return out
}

func (p *clusterPKI) addRevoked(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	list := p.revoked()
	if slices.ContainsFunc(list, func(r revokedNode) bool { return r.Name == name }) {
		return
	}
	list = append(list, revokedNode{Name: name, At: time.Now().UTC()})
	b, _ := json.Marshal(list)
	_ = p.write(revokedFile, b, 0o600)
}

// caSince es la fecha de la CA actual (la primera del archivo), o cero.
func (p *clusterPKI) caSince() time.Time {
	c, err := clusterpki.ParseCert(p.read("ca.pem"))
	if err != nil {
		return time.Time{}
	}
	return c.NotBefore.Add(time.Hour) // NewCA la antedata una hora
}

type ctlRotateStage struct {
	Cert  string `json:"cert"`
	CA    string `json:"ca"`
	CAKey string `json:"caKey"`
}

type ctlRotateStep struct {
	Step   string `json:"step"`   // switch | finalize | abort
	Secure bool   `json:"secure"` // finalize: secure-access de GlusterFS como está
}

// localRotate corre un paso de la rotación en este nodo.
func (c *clusterAPI) localRotate(ctx context.Context, q ctlRotateStep) error {
	var err error
	switch q.Step {
	case "switch":
		err = c.pki.rotateSwitch()
	case "finalize":
		if err = c.pki.rotateFinalize(); err == nil {
			// Los archivos de GlusterFS: certificado nuevo y solo la CA nueva. glusterd y los
			// bricks los leen al arrancar (el reinicio lo hace quien coordina).
			err = c.installTLSFiles(ctx, q.Secure, false)
		}
	case "abort":
		err = c.pki.rotateAbort()
	default:
		err = fmt.Errorf("paso desconocido: %q", q.Step)
	}
	return err
}

// ctlRotateRoutes suma al canal de control los pasos de la rotación.
func (c *clusterAPI) ctlRotateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/pki/csr", c.ctlAuth(func(w http.ResponseWriter, r *http.Request) {
		var q struct{}
		if !ctlDecode(w, r, &q) {
			return
		}
		csr, names, err := c.localCSR()
		ctlReply(w, map[string]any{"cn": c.self(), "csr": string(csr), "names": names}, err)
	}))
	mux.HandleFunc("POST /v1/pki/stage", c.ctlAuth(func(w http.ResponseWriter, r *http.Request) {
		var q ctlRotateStage
		if !ctlDecode(w, r, &q) {
			return
		}
		ctlReply(w, struct{}{}, c.pki.rotateStage(c.self(), []byte(q.Cert), []byte(q.CA), []byte(q.CAKey)))
	}))
	mux.HandleFunc("POST /v1/pki/step", c.ctlAuth(func(w http.ResponseWriter, r *http.Request) {
		var q ctlRotateStep
		if !ctlDecode(w, r, &q) {
			return
		}
		ctlReply(w, struct{}{}, c.localRotate(r.Context(), q))
	}))
}

func (c *clusterAPI) localCSR() ([]byte, []string, error) {
	key, err := c.pki.nodeKey()
	if err != nil {
		return nil, nil, err
	}
	csr, err := clusterpki.CSR(key, c.self())
	return csr, c.selfNames(), err
}

// ---- Coordinación ---------------------------------------------------------------------------

func (c *clusterAPI) nodeCSR(ctx context.Context, host string) (cn string, csr []byte, names []string, err error) {
	if c.isSelf(host) {
		csr, names, err = c.localCSR()
		return c.self(), csr, names, err
	}
	var r struct {
		CN    string   `json:"cn"`
		CSR   string   `json:"csr"`
		Names []string `json:"names"`
	}
	if err := c.ctlCall(ctx, host, "POST", "/v1/pki/csr", struct{}{}, &r); err != nil {
		return "", nil, nil, err
	}
	return r.CN, []byte(r.CSR), r.Names, nil
}

func (c *clusterAPI) nodeStage(ctx context.Context, host string, q ctlRotateStage) error {
	if c.isSelf(host) {
		return c.pki.rotateStage(c.self(), []byte(q.Cert), []byte(q.CA), []byte(q.CAKey))
	}
	return c.ctlCall(ctx, host, "POST", "/v1/pki/stage", q, nil)
}

func (c *clusterAPI) nodeRotate(ctx context.Context, host string, q ctlRotateStep) error {
	if c.isSelf(host) {
		return c.localRotate(ctx, q)
	}
	return c.ctlCall(ctx, host, "POST", "/v1/pki/step", q, nil)
}

// rotateCA cambia la CA del clúster en todos los nodos (ver arriba). Necesita todos los
// nodos conectados: uno apagado se quedaría con la CA anterior y fuera del clúster.
func (c *clusterAPI) rotateCA(ctx context.Context, h *jobs.Handle) error {
	if !c.pki.hasCA() || !c.pki.signed() {
		return rtcrpc.Errorf("NO_CA", "este clúster todavía no tiene CA: se crea al sumar el primer nodo")
	}
	if !c.rotating.CompareAndSwap(false, true) {
		return rtcrpc.Errorf("BUSY", "ya hay una rotación de la CA en curso")
	}
	defer c.rotating.Store(false)

	st, err := c.agentStatus(ctx, true)
	if err != nil {
		return agentErr(err)
	}
	h.Update(0.05, "Comprobando que todos los nodos respondan")
	var remotes []string
	for _, p := range st.Peers {
		if p.Self {
			continue
		}
		if !p.Connected {
			return fmt.Errorf("%s no está conectado: con un nodo apagado no se puede rotar la CA (se quedaría afuera)", p.Host)
		}
		if _, err := c.nodeInfo(ctx, p.Host); err != nil {
			return fmt.Errorf("%s no responde por el canal de control: %w", p.Host, err)
		}
		remotes = append(remotes, p.Host)
	}
	sort.Strings(remotes)
	self := c.self()
	all := append(append([]string{}, remotes...), self) // este nodo, siempre al final

	h.Update(0.1, "Creando la CA nueva y los certificados de cada nodo")
	var id [4]byte
	_, _ = rand.Read(id[:])
	ca, caKey, err := clusterpki.NewCA(hex.EncodeToString(id[:]))
	if err != nil {
		return err
	}
	stages := map[string]ctlRotateStage{}
	for _, host := range all {
		cn, csr, names, err := c.nodeCSR(ctx, host)
		if err != nil {
			return fmt.Errorf("%s: %w", host, err)
		}
		req, err := clusterpki.ParseCSR(csr)
		if err != nil || req.Subject.CommonName != cn {
			return fmt.Errorf("%s mandó un pedido de certificado inválido", host)
		}
		cert, err := clusterpki.Sign(ca, caKey, csr, names)
		if err != nil {
			return err
		}
		stages[host] = ctlRotateStage{Cert: string(cert), CA: string(ca), CAKey: string(caKey)}
	}

	h.Update(0.2, "Repartiendo la CA nueva")
	var staged []string
	for _, host := range all {
		if err := c.nodeStage(ctx, host, stages[host]); err != nil {
			bg := context.WithoutCancel(ctx)
			for _, s := range staged {
				if e := c.nodeRotate(bg, s, ctlRotateStep{Step: "abort"}); e != nil {
					c.m.a.d.Log.Warn("cluster: deshacer la rotación", "host", s, "err", e)
				}
			}
			return fmt.Errorf("%s: %w (los nodos siguen con la CA anterior)", host, err)
		}
		staged = append(staged, host)
	}

	// Desde acá no se vuelve atrás: todos confían en las dos CA y la rotación se puede
	// repetir si algo falla.
	again := " (todos los nodos aceptan las dos CA: volvé a rotar para terminar)"
	h.Update(0.3, "Cambiando el certificado de cada nodo")
	for _, host := range all {
		if err := c.nodeRotate(ctx, host, ctlRotateStep{Step: "switch"}); err != nil {
			return fmt.Errorf("%s: %w%s", host, err, again)
		}
	}
	secure := st.TLS.Secure
	h.Update(0.4, "Retirando la CA anterior")
	for _, host := range all {
		if err := c.nodeRotate(ctx, host, ctlRotateStep{Step: "finalize", Secure: secure}); err != nil {
			return fmt.Errorf("%s: %w%s", host, err, again)
		}
	}
	c.m.a.d.Log.Info("cluster: CA rotada", "ca", c.pki.caFingerprint(), "nodos", len(all))

	tlsVols := slices.ContainsFunc(st.Volumes, func(v gluster.Volume) bool { return v.TLS })
	if !secure && !tlsVols {
		return nil // GlusterFS no usa los certificados: no hace falta reiniciar nada
	}
	mounted, started, err := c.stopVolumes(ctx, h, all, st, 0.5, 0.6)
	if err != nil {
		return err
	}
	h.Update(0.7, "Reiniciando glusterd en cada nodo con el certificado nuevo")
	for _, host := range all {
		if err := c.nodeSecure(ctx, host, secure); err != nil {
			return fmt.Errorf("%s: %w (los volúmenes quedaron detenidos: arreglalo y volvé a intentar)", host, err)
		}
	}
	if err := c.waitPeers(ctx, len(remotes), 90*time.Second); err != nil {
		return err
	}
	h.Update(0.85, "Arrancando los volúmenes")
	if err := c.restartVolumes(ctx, started); err != nil {
		return err
	}
	h.Update(0.95, "Montando de nuevo")
	c.restoreMounts(ctx, mounted)
	return nil
}
