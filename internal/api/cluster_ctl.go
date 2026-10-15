package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/clusterpki"
	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Canal de control entre los tangod de un clúster (docs/17 §3, ADR 0012): HTTPS en el
// puerto 24010 (mismo firewall que GlusterFS: solo nodos e invitaciones).
//
//   - /join/hello y /join/finish: la unión de un nodo nuevo. La autentica el código que
//     muestra el invitado (MAC con el código y la huella del certificado TLS que ve el que
//     suma: nadie en el medio puede reenviar los mensajes). El nodo que suma firma el
//     certificado del nuevo con la CA del clúster y se lo entrega junto con la CA.
//   - /v1/*: pedidos entre nodos ya unidos, con certificado de cliente firmado por la CA
//     del clúster (mTLS) y solo desde un nodo del clúster.

// ---- PKI de este nodo ----------------------------------------------------------------

// clusterPKI guarda la CA del clúster, la clave y el certificado de este nodo en
// <StateDir>/cluster (0700, dueño tango).
type clusterPKI struct {
	dir string
	mu  sync.Mutex
	// certificado provisorio del canal antes de unirse (autofirmado con la clave del nodo)
	self []byte
}

func newClusterPKI(stateDir string) *clusterPKI {
	return &clusterPKI{dir: filepath.Join(stateDir, "cluster")}
}

func (p *clusterPKI) path(name string) string { return filepath.Join(p.dir, name) }

func (p *clusterPKI) read(name string) []byte {
	b, _ := os.ReadFile(p.path(name))
	return b
}

func (p *clusterPKI) write(name string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return err
	}
	tmp := p.path(name + ".tmp")
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, p.path(name))
}

// nodeKey devuelve la clave de este nodo (la crea la primera vez).
func (p *clusterPKI) nodeKey() ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if k := p.read("node.key"); len(k) > 0 {
		return k, nil
	}
	k, err := clusterpki.NewNodeKey()
	if err != nil {
		return nil, err
	}
	return k, p.write("node.key", k, 0o600)
}

func (p *clusterPKI) hasCA() bool { return len(p.read("ca.pem")) > 0 && len(p.read("ca.key")) > 0 }

// signed dice si este nodo tiene certificado de la CA del clúster.
func (p *clusterPKI) signed() bool { return len(p.read("node.pem")) > 0 && len(p.read("ca.pem")) > 0 }

// ensureCA crea la CA del clúster y el certificado de este nodo si todavía no hay.
func (p *clusterPKI) ensureCA(cn string, names []string) (created bool, err error) {
	if p.hasCA() && p.signed() {
		return false, nil
	}
	key, err := p.nodeKey()
	if err != nil {
		return false, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	caC, caK := p.read("ca.pem"), p.read("ca.key")
	if len(caC) == 0 || len(caK) == 0 {
		var id [4]byte
		_, _ = rand.Read(id[:])
		if caC, caK, err = clusterpki.NewCA(hex.EncodeToString(id[:])); err != nil {
			return false, err
		}
		if err := p.write("ca.key", caK, 0o600); err != nil {
			return false, err
		}
		if err := p.write("ca.pem", caC, 0o644); err != nil {
			return false, err
		}
		created = true
	}
	csr, err := clusterpki.CSR(key, cn)
	if err != nil {
		return created, err
	}
	cert, err := clusterpki.Sign(caC, caK, csr, names)
	if err != nil {
		return created, err
	}
	return created, p.write("node.pem", cert, 0o644)
}

// install guarda lo que entrega el nodo que suma a este.
func (p *clusterPKI) install(cert, ca, caKey []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.write("ca.key", caKey, 0o600); err != nil {
		return err
	}
	if err := p.write("ca.pem", ca, 0o644); err != nil {
		return err
	}
	return p.write("node.pem", cert, 0o644)
}

// serverCert es el certificado que presenta el canal: el de la CA si este nodo ya está
// unido, o uno autofirmado con la misma clave.
func (p *clusterPKI) serverCert(cn string) (*tls.Certificate, error) {
	key, err := p.nodeKey()
	if err != nil {
		return nil, err
	}
	cert := p.read("node.pem")
	if len(cert) == 0 {
		p.mu.Lock()
		if p.self == nil {
			p.self, err = clusterpki.SelfSigned(key, cn)
		}
		cert = p.self
		p.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	c, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// caFingerprint es la huella de la CA (para mostrar), o "".
func (p *clusterPKI) caFingerprint() string {
	fp, _ := clusterpki.CertFingerprint(p.read("ca.pem"))
	return fp
}

func (p *clusterPKI) nodeFingerprint() string {
	k, err := p.nodeKey()
	if err != nil {
		return ""
	}
	fp, _ := clusterpki.KeyFingerprint(k)
	return fp
}

// clientConfig es la configuración TLS para hablar con otro nodo: certificado de cliente
// de la CA y verificación del servidor contra la misma CA (sin mirar el nombre: el canal
// se identifica por la CA del clúster, no por DNS).
func (p *clusterPKI) clientConfig() (*tls.Config, error) {
	if !p.signed() {
		return nil, errors.New("este nodo no tiene certificado del clúster")
	}
	key, err := p.nodeKey()
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(p.read("node.pem"), key)
	if err != nil {
		return nil, err
	}
	ca := p.read("ca.pem")
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("el nodo no presentó certificado")
			}
			c, err := x509.ParseCertificate(raw[0])
			if err != nil {
				return err
			}
			return clusterpki.Verify(ca, c)
		}}, nil
}

// ---- Servidor ---------------------------------------------------------------------------

// CtlInfo es lo que un nodo cuenta de sí mismo por el canal de control.
type CtlInfo struct {
	CN           string                        `json:"cn"`
	Fingerprint  string                        `json:"fingerprint"`
	Names        []string                      `json:"names"`
	Secure       bool                          `json:"secure"`
	TangoVolumes []ClusterTangoVolume          `json:"tangoVolumes"`
	Pools        []CtlPool                     `json:"pools"`
	Mounts       []agentproto.GlusterMountInfo `json:"mounts"`
}

// CtlPool es un grupo de almacenamiento con espacio libre para bricks dedicados.
type CtlPool struct {
	Name string `json:"name"`
	Free uint64 `json:"free"`
}

type ctlBrickReq struct {
	Op   string `json:"op"` // create | delete
	Vol  string `json:"vol"`
	Pool string `json:"pool"`
	Size uint64 `json:"size"`
	Thin bool   `json:"thin"`
}

type ctlMountsReq struct {
	Op    string   `json:"op"` // stop | start
	Names []string `json:"names"`
}

type joinHello struct {
	Nonce string `json:"nonce"`
	From  string `json:"from"`
	MAC   string `json:"mac"`
}

type joinHelloResp struct {
	CN          string   `json:"cn"`
	Names       []string `json:"names"`
	CSR         string   `json:"csr"`
	Fingerprint string   `json:"fingerprint"`
	MAC         string   `json:"mac"`
}

type joinFinish struct {
	Nonce  string `json:"nonce"`
	Cert   string `json:"cert"`
	CA     string `json:"ca"`
	CAKey  string `json:"caKey"`
	Secure bool   `json:"secure"`
	MAC    string `json:"mac"`
}

type joinFinishResp struct {
	CAFingerprint string `json:"caFingerprint"`
	MAC           string `json:"mac"`
}

var b64 = base64.StdEncoding

// ctlPort es el puerto del canal en los otros nodos (variable para los tests).
var ctlPort = gluster.PortCtl

// serveCtl escucha el canal de control. Corre mientras viva tangod.
func (c *clusterAPI) serveCtl(addr string) {
	if err := c.ctlServer(addr).ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		c.m.a.d.Log.Warn("cluster: canal de control", "addr", addr, "err", err)
	}
}

// ctlServer arma el servidor del canal de control.
func (c *clusterAPI) ctlServer(addr string) *http.Server {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequestClientCert,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return c.pki.serverCert(c.self()) }}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /join/hello", c.ctlJoinHello)
	mux.HandleFunc("POST /join/finish", c.ctlJoinFinish)
	mux.HandleFunc("GET /v1/info", c.ctlAuth(func(w http.ResponseWriter, r *http.Request) {
		info, err := c.localInfo(r.Context())
		ctlReply(w, info, err)
	}))
	mux.HandleFunc("POST /v1/brick", c.ctlAuth(func(w http.ResponseWriter, r *http.Request) {
		var q ctlBrickReq
		if !ctlDecode(w, r, &q) {
			return
		}
		res, err := c.localBrick(r.Context(), q)
		ctlReply(w, res, err)
	}))
	mux.HandleFunc("POST /v1/tls", c.ctlAuth(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Secure bool `json:"secure"`
		}
		if !ctlDecode(w, r, &q) {
			return
		}
		ctlReply(w, struct{}{}, c.localSecure(r.Context(), q.Secure))
	}))
	mux.HandleFunc("POST /v1/mounts", c.ctlAuth(func(w http.ResponseWriter, r *http.Request) {
		var q ctlMountsReq
		if !ctlDecode(w, r, &q) {
			return
		}
		names, err := c.localMounts(r.Context(), q)
		ctlReply(w, map[string][]string{"names": names}, err)
	}))
	return &http.Server{Addr: addr, Handler: mux, TLSConfig: cfg, ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 5 * time.Minute, MaxHeaderBytes: 16 << 10}
}

func ctlDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 128<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		http.Error(w, "pedido inválido", http.StatusBadRequest)
		return false
	}
	return true
}

func ctlReply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func remoteIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	if ip := net.ParseIP(h); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
		return ip.String()
	}
	return h
}

// ctlAuth exige un certificado de cliente firmado por la CA del clúster y que el pedido
// venga de un nodo del clúster (un nodo que salió conserva su certificado, pero ya no está
// en el pool ni en el firewall).
func (c *clusterAPI) ctlAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || !c.pki.signed() {
			http.Error(w, "se necesita el certificado del clúster", http.StatusUnauthorized)
			return
		}
		if err := clusterpki.Verify(c.pki.read("ca.pem"), r.TLS.PeerCertificates[0]); err != nil {
			http.Error(w, "certificado de otro clúster", http.StatusForbidden)
			return
		}
		if !c.isPeerAddr(r.Context(), remoteIP(r)) {
			http.Error(w, "este equipo no es un nodo del clúster", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

// isPeerAddr dice si ip es de un nodo del clúster.
func (c *clusterAPI) isPeerAddr(ctx context.Context, ip string) bool {
	st, err := c.agentStatus(ctx, false)
	if err != nil {
		return false
	}
	for _, p := range st.Peers {
		if p.Self {
			continue
		}
		for _, h := range append([]string{p.Host}, p.Hostnames...) {
			if addrs, err := resolve(ctx, h); err == nil {
				for _, a := range addrs {
					if a == ip {
						return true
					}
				}
			}
		}
	}
	return false
}

// ownServerFP es la huella del certificado que presenta este canal ahora (vinculación).
func (c *clusterAPI) ownServerFP() ([]byte, error) {
	cert, err := c.pki.serverCert(c.self())
	if err != nil {
		return nil, err
	}
	return clusterpki.TLSFingerprint(cert.Certificate[0]), nil
}

// inviteFor busca la invitación abierta para la IP que llama.
func (c *clusterAPI) inviteFor(ip string) (string, *clusterInvite) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for h, inv := range c.invites {
		if now.After(inv.Expires) || inv.Code == "" {
			continue
		}
		for _, a := range inv.Addrs {
			if a == ip {
				cp := inv
				return h, &cp
			}
		}
	}
	return "", nil
}

// failInvite cuenta un intento con código equivocado: al quinto, la invitación se cierra.
func (c *clusterAPI) failInvite(host string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	inv, ok := c.invites[host]
	if !ok {
		return
	}
	inv.Fails++
	if inv.Fails >= 5 {
		delete(c.invites, host)
		return
	}
	c.invites[host] = inv
}

func (c *clusterAPI) ctlJoinHello(w http.ResponseWriter, r *http.Request) {
	var q joinHello
	if !ctlDecode(w, r, &q) {
		return
	}
	host, inv := c.inviteFor(remoteIP(r))
	if inv == nil {
		http.Error(w, "no hay una invitación abierta para este equipo", http.StatusForbidden)
		return
	}
	fp, err := c.ownServerFP()
	nonce, err2 := b64.DecodeString(q.Nonce)
	mac, err3 := b64.DecodeString(q.MAC)
	if err != nil || err2 != nil || err3 != nil || len(nonce) != 32 {
		http.Error(w, "pedido inválido", http.StatusBadRequest)
		return
	}
	if !clusterpki.CheckMAC(clusterpki.MAC(inv.Code, "hello", nonce, fp), mac) {
		c.failInvite(host)
		http.Error(w, "código de unión incorrecto", http.StatusForbidden)
		return
	}
	key, err := c.pki.nodeKey()
	if err != nil {
		ctlReply(w, nil, err)
		return
	}
	csr, err := clusterpki.CSR(key, c.self())
	if err != nil {
		ctlReply(w, nil, err)
		return
	}
	names := append([]string{c.self()}, localAddrs()...)
	nb, _ := json.Marshal(names)
	ctlReply(w, joinHelloResp{CN: c.self(), Names: names, CSR: string(csr), Fingerprint: c.pki.nodeFingerprint(),
		MAC: b64.EncodeToString(clusterpki.MAC(inv.Code, "csr", nonce, csr, nb, fp))}, nil)
}

func (c *clusterAPI) ctlJoinFinish(w http.ResponseWriter, r *http.Request) {
	var q joinFinish
	if !ctlDecode(w, r, &q) {
		return
	}
	host, inv := c.inviteFor(remoteIP(r))
	if inv == nil {
		http.Error(w, "no hay una invitación abierta para este equipo", http.StatusForbidden)
		return
	}
	fp, err := c.ownServerFP()
	nonce, err2 := b64.DecodeString(q.Nonce)
	mac, err3 := b64.DecodeString(q.MAC)
	if err != nil || err2 != nil || err3 != nil || len(nonce) != 32 {
		http.Error(w, "pedido inválido", http.StatusBadRequest)
		return
	}
	sec := []byte{0}
	if q.Secure {
		sec[0] = 1
	}
	if !clusterpki.CheckMAC(clusterpki.MAC(inv.Code, "finish", nonce, []byte(q.Cert), []byte(q.CA), []byte(q.CAKey), sec, fp), mac) {
		c.failInvite(host)
		http.Error(w, "código de unión incorrecto", http.StatusForbidden)
		return
	}
	key, err := c.pki.nodeKey()
	if err != nil {
		ctlReply(w, nil, err)
		return
	}
	// El certificado tiene que ser para esta clave, con este nombre y de esa CA.
	if _, err := tls.X509KeyPair([]byte(q.Cert), key); err != nil {
		ctlReply(w, nil, errors.New("el certificado no corresponde a la clave de este nodo"))
		return
	}
	leaf, _ := clusterpki.ParseCert([]byte(q.Cert))
	if leaf == nil || leaf.Subject.CommonName != c.self() || clusterpki.Verify([]byte(q.CA), leaf) != nil {
		ctlReply(w, nil, errors.New("certificado inválido para este nodo"))
		return
	}
	if _, err := clusterpki.ParseKey([]byte(q.CAKey)); err != nil {
		ctlReply(w, nil, errors.New("clave de la CA inválida"))
		return
	}
	if err := c.pki.install([]byte(q.Cert), []byte(q.CA), []byte(q.CAKey)); err != nil {
		ctlReply(w, nil, err)
		return
	}
	if err := c.installTLSFiles(r.Context(), q.Secure, q.Secure); err != nil {
		ctlReply(w, nil, err)
		return
	}
	cafp := c.pki.caFingerprint()
	c.mu.Lock()
	if cur, ok := c.invites[host]; ok {
		cur.Joined = cafp
		c.invites[host] = cur
	}
	c.mu.Unlock()
	c.m.a.d.Log.Info("cluster: unido a un clúster", "por", host, "ca", cafp)
	ctlReply(w, joinFinishResp{CAFingerprint: cafp, MAC: b64.EncodeToString(clusterpki.MAC(inv.Code, "done", nonce, []byte(cafp), fp))}, nil)
}

// installTLSFiles lleva el certificado de este nodo a GlusterFS (/etc/ssl/glusterfs.*) y
// deja secure-access como se pida; con restart, reinicia glusterd.
func (c *clusterAPI) installTLSFiles(ctx context.Context, secure, restart bool) error {
	ag2, ok := c.ag.(clusterAgent2)
	if !ok {
		return errors.New("el agente no soporta TLS del clúster")
	}
	key, err := c.pki.nodeKey()
	if err != nil {
		return err
	}
	_, err = ag2.GlusterTLS(ctx, agentproto.GlusterTLSArgs{Cert: string(c.pki.read("node.pem")), Key: string(key), CA: string(c.pki.read("ca.pem")), Secure: secure, Restart: restart})
	if err != nil {
		return agentErr(err)
	}
	return nil
}

// ---- Cliente --------------------------------------------------------------------------

// ctlURL arma la URL del canal de un nodo (host: IP o nombre del pool).
func ctlURL(host, path string) string {
	return "https://" + net.JoinHostPort(host, strconv.Itoa(ctlPort)) + path
}

// ctlCall habla con otro nodo ya unido (mTLS).
func (c *clusterAPI) ctlCall(ctx context.Context, host, method, path string, in, out any) error {
	cfg, err := c.pki.clientConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, ctlURL(host, path), body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	cl := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}}
	defer cl.CloseIdleConnections()
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("%s no responde por el canal de control: %w", host, err)
	}
	defer resp.Body.Close()
	return ctlParse(host, resp, out)
}

func ctlParse(host string, resp *http.Response, out any) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s: %s", host, e.Error)
		}
		return fmt.Errorf("%s: %s", host, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// joinPost habla con el canal de un nodo que todavía no tiene certificado del clúster. Su
// certificado es autofirmado: no hay CA contra la cual verificarlo, así que en lugar de
// eso se devuelve su huella y los MAC con el código de unión la incluyen (vinculación al
// canal). Si alguien en el medio presenta otro certificado, el MAC no coincide y la unión
// se corta antes de mandar nada secreto.
func joinPost(ctx context.Context, host, path string, in, out any) ([]byte, error) {
	var fp []byte
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, // verificado por MAC (ver arriba)
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("el nodo no presentó certificado")
			}
			fp = clusterpki.TLSFingerprint(raw[0])
			return nil
		}}
	cl := &http.Client{Timeout: 3 * time.Minute, Transport: &http.Transport{TLSClientConfig: cfg,
		DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, DisableKeepAlives: true}}
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ctlURL(host, path), bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s no responde por el canal de control (puerto %d): ¿lo invitaste desde su app Clúster? (%w)", host, gluster.PortCtl, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fp, fmt.Errorf("%s rechazó la unión: %s", host, strings.TrimSpace(string(b)))
	}
	return fp, ctlParse(host, resp, out)
}
