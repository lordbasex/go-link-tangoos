package api

// Red de la replicación ZFS (ADR 0016): cada NAS tiene una identidad propia (clave
// ECDSA y certificado autofirmado en <estado>/repl). El que recibe escucha en TCP
// replPort solo mientras "Recibir réplicas" está prendido y exige un certificado de
// cliente cuya clave esté vinculada; el que manda fija la clave del destino. La unión
// es como la de un nodo del clúster: el destino muestra un código de un solo uso y su
// huella, el origen ve la huella al conectarse, la confirma y manda el código dentro
// de un MAC atado a los dos certificados (alguien en el medio no puede reenviarlo).

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/clusterpki"
	"github.com/lordbasex/go-link-tangoos/internal/zfs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// replPort es el puerto donde un NAS recibe réplicas (variable para los tests).
var replPort = 24020

const replInviteTTL = 15 * time.Minute

// errPinMismatch: el destino presentó otra clave que la vinculada.
var errPinMismatch = errors.New("la clave del destino cambió: volvé a vincularlo (puede ser otro equipo con su dirección)")

type replInvite struct {
	code    string
	expires time.Time
	fails   int
}

type replProbe struct {
	addr    string
	name    string
	fp      string
	spki    string
	certSHA []byte
	at      time.Time
}

type replNet struct {
	r *zfsRepl

	mu   sync.Mutex
	cert *tls.Certificate
	srv  *http.Server
}

func newReplNet(r *zfsRepl) *replNet { return &replNet{r: r} }

// ---- Identidad ---------------------------------------------------------------------

func hostName() string {
	h, _ := os.Hostname()
	return strings.ToLower(h)
}

// own es la identidad de este NAS (se crea la primera vez).
func (n *replNet) own() (*tls.Certificate, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cert != nil {
		return n.cert, nil
	}
	dir := n.r.dir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "tango-repl")
	}
	_ = os.MkdirAll(dir, 0o700)
	kp, cp := filepath.Join(dir, "id.key"), filepath.Join(dir, "id.pem")
	key, err := os.ReadFile(kp)
	if err != nil || len(key) == 0 {
		if key, err = clusterpki.NewNodeKey(); err != nil {
			return nil, err
		}
		if err := os.WriteFile(kp, key, 0o600); err != nil {
			return nil, err
		}
		_ = os.Remove(cp)
	}
	crt, err := os.ReadFile(cp)
	if err == nil {
		if c, err2 := tls.X509KeyPair(crt, key); err2 == nil && c.Leaf != nil && time.Now().Before(c.Leaf.NotAfter.Add(-30*24*time.Hour)) {
			n.cert = &c
			return n.cert, nil
		}
	}
	// Certificado nuevo con la misma clave: el pin es la clave, no el certificado.
	if crt, err = clusterpki.SelfSigned(key, hostName()); err != nil {
		return nil, err
	}
	if err := os.WriteFile(cp, crt, 0o644); err != nil {
		return nil, err
	}
	c, err := tls.X509KeyPair(crt, key)
	if err != nil {
		return nil, err
	}
	n.cert = &c
	return n.cert, nil
}

// spkiOf es el SHA-256 hex de la clave pública de un certificado (lo que se fija).
func spkiOf(c *x509.Certificate) string {
	s := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(s[:])
}

func (n *replNet) identity() ReplIdentity {
	c, err := n.own()
	if err != nil || c.Leaf == nil {
		return ReplIdentity{Name: hostName()}
	}
	return ReplIdentity{Name: hostName(), Fingerprint: clusterpki.Fingerprint(c.Leaf.RawSubjectPublicKeyInfo)}
}

// ---- Servidor (el que recibe) ------------------------------------------------------------

// sync prende o apaga el servidor según "Recibir réplicas".
func (n *replNet) sync() {
	n.r.mu.Lock()
	on := n.r.st.Receive.Enabled
	n.r.mu.Unlock()
	n.mu.Lock()
	running := n.srv != nil
	n.mu.Unlock()
	switch {
	case on && !running:
		if err := n.listen(); err != nil {
			n.r.log.Warn("replicación: no se pudo escuchar", "port", replPort, "err", err)
		}
	case !on && running:
		n.mu.Lock()
		srv := n.srv
		n.srv = nil
		n.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
}

func (n *replNet) listen() error {
	cert, err := n.own()
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /info", func(w http.ResponseWriter, r *http.Request) {
		replReply(w, map[string]string{"name": hostName()}, nil)
	})
	mux.HandleFunc("POST /pair", n.pair)
	mux.HandleFunc("GET /v1/state", n.auth(n.state))
	mux.HandleFunc("POST /v1/recv", n.auth(n.recv))
	mux.HandleFunc("POST /v1/mirror", n.auth(n.mirror))
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(replPort))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, ErrorLog: slog.NewLogLogger(n.r.log.Handler(), slog.LevelDebug), TLSConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{*cert}, ClientAuth: tls.RequireAnyClientCert}}
	n.mu.Lock()
	n.srv = srv
	n.mu.Unlock()
	go func() {
		if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			n.r.log.Warn("replicación: el servidor terminó", "err", err)
		}
	}()
	return nil
}

func replReply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		code := http.StatusInternalServerError
		var re *rtcrpc.Error
		if errors.As(err, &re) && re.Code == agentproto.CodeBusy {
			code = http.StatusConflict
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": agentMessage(err)})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// peer es el origen vinculado que llama (por la clave de su certificado de cliente).
func (n *replNet) peer(r *http.Request) *ReplSource {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return nil
	}
	spki := spkiOf(r.TLS.PeerCertificates[0])
	n.r.mu.Lock()
	defer n.r.mu.Unlock()
	if !n.r.st.Receive.Enabled {
		return nil
	}
	for _, s := range n.r.st.Sources {
		if s.SPKI == spki {
			c := s
			return &c
		}
	}
	return nil
}

func (n *replNet) auth(h func(w http.ResponseWriter, r *http.Request, src *ReplSource, pool string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		src := n.peer(r)
		if src == nil {
			http.Error(w, "este NAS no está vinculado como origen", http.StatusForbidden)
			return
		}
		n.r.mu.Lock()
		pool := n.r.st.Receive.Pool
		n.r.mu.Unlock()
		if !n.r.poolOK(r.Context(), pool) {
			http.Error(w, "el pool de destino "+pool+" no está disponible", http.StatusServiceUnavailable)
			return
		}
		h(w, r, src, pool)
	}
}

func (n *replNet) state(w http.ResponseWriter, r *http.Request, src *ReplSource, pool string) {
	vol := r.URL.Query().Get("vol")
	if zfs.ValidVolumeName(vol) != nil {
		http.Error(w, "volumen inválido", http.StatusBadRequest)
		return
	}
	st, err := n.r.agent.ZfsReplState(r.Context(), agentproto.ZfsReplStateArgs{Pool: pool, Origin: src.ID, Vol: vol})
	replReply(w, st, err)
}

func (n *replNet) recv(w http.ResponseWriter, r *http.Request, src *ReplSource, pool string) {
	q := r.URL.Query()
	a := agentproto.ZfsReplRecvArgs{Pool: pool, Origin: src.ID, Vol: q.Get("vol"), Rel: q.Get("rel"), BaseGUID: q.Get("base")}
	if zfs.ValidVolumeName(a.Vol) != nil || zfs.ValidRel(a.Rel) != nil || (a.BaseGUID != "" && zfs.ValidGUID(a.BaseGUID) != nil) {
		http.Error(w, "pedido inválido", http.StatusBadRequest)
		return
	}
	err := n.r.receive(r.Context(), a, r.Body)
	if err == nil {
		n.r.changed()
	}
	replReply(w, struct{}{}, err)
}

func (n *replNet) mirror(w http.ResponseWriter, r *http.Request, src *ReplSource, pool string) {
	var q struct {
		Vol  string              `json:"vol"`
		Keep map[string][]string `json:"keep"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&q); err != nil || zfs.ValidVolumeName(q.Vol) != nil {
		http.Error(w, "pedido inválido", http.StatusBadRequest)
		return
	}
	err := n.r.agent.ZfsReplMirror(r.Context(), agentproto.ZfsReplMirrorArgs{Pool: pool, Origin: src.ID, Vol: q.Vol, Keep: q.Keep})
	n.r.changed()
	replReply(w, struct{}{}, err)
}

type replPairReq struct {
	Name  string `json:"name"`
	Nonce string `json:"nonce"`
	MAC   string `json:"mac"`
}

// pair vincula un origen: el MAC con el código cubre la clave del cliente y el
// certificado de este servidor, así que solo vale en esta conexión.
func (n *replNet) pair(w http.ResponseWriter, r *http.Request) {
	var q replPairReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&q); err != nil || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		http.Error(w, "pedido inválido", http.StatusBadRequest)
		return
	}
	cert, err := n.own()
	nonce, err2 := base64.StdEncoding.DecodeString(q.Nonce)
	mac, err3 := base64.StdEncoding.DecodeString(q.MAC)
	if err != nil || err2 != nil || err3 != nil || len(nonce) != 32 {
		http.Error(w, "pedido inválido", http.StatusBadRequest)
		return
	}
	client := r.TLS.PeerCertificates[0]
	spki := spkiOf(client)
	n.r.mu.Lock()
	defer n.r.mu.Unlock()
	inv := n.r.invite
	if !n.r.st.Receive.Enabled || inv == nil || time.Now().After(inv.expires) {
		http.Error(w, "no hay una invitación abierta en este NAS", http.StatusForbidden)
		return
	}
	srvFP := clusterpki.TLSFingerprint(cert.Certificate[0])
	if !clusterpki.CheckMAC(clusterpki.MAC(inv.code, "repl-pair", nonce, client.RawSubjectPublicKeyInfo, srvFP, []byte(q.Name)), mac) {
		inv.fails++
		if inv.fails >= 5 {
			n.r.invite = nil
		}
		http.Error(w, "código incorrecto", http.StatusForbidden)
		return
	}
	n.r.invite = nil // un solo uso
	name := strings.TrimSpace(q.Name)
	if name == "" || len(name) > 64 {
		name = "NAS " + spki[:12]
	}
	src := ReplSource{ID: spki[:12], Name: name, Fingerprint: clusterpki.Fingerprint(client.RawSubjectPublicKeyInfo), SPKI: spki, AddedAt: time.Now().UTC().Format(time.RFC3339)}
	keep := n.r.st.Sources[:0]
	for _, s := range n.r.st.Sources {
		if s.SPKI != spki {
			keep = append(keep, s)
		}
	}
	n.r.st.Sources = append(keep, src)
	n.r.save()
	n.r.log.Info("replicación: origen vinculado", "name", name, "fingerprint", src.Fingerprint)
	go n.r.changed()
	replReply(w, map[string]string{"name": hostName(), "mac": base64.StdEncoding.EncodeToString(clusterpki.MAC(inv.code, "repl-done", nonce, srvFP))}, nil)
}

// ---- Cliente (el que manda) ----------------------------------------------------------

// dial conecta al destino; pin vacío = sondeo (se acepta cualquier certificado y se
// devuelve para mostrar su huella).
func (n *replNet) httpClient(pin string, timeout time.Duration) (*http.Client, error) {
	cert, err := n.own()
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{*cert}, InsecureSkipVerify: true, //nolint:gosec // se verifica la clave fijada abajo
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("el destino no presentó certificado")
			}
			if pin != "" && spkiOf(cs.PeerCertificates[0]) != pin {
				return errPinMismatch
			}
			return nil
		}}
	tr := &http.Transport{TLSClientConfig: cfg, ResponseHeaderTimeout: 0, IdleConnTimeout: 30 * time.Second,
		DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext}
	return &http.Client{Transport: tr, Timeout: timeout}, nil
}

// normAddr agrega el puerto por defecto.
func normAddr(a string) (string, error) {
	a = strings.TrimSpace(a)
	if a == "" || len(a) > 255 || strings.ContainsAny(a, "/?#@ ") {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "dirección inválida")
	}
	if _, _, err := net.SplitHostPort(a); err != nil {
		a = net.JoinHostPort(strings.Trim(a, "[]"), strconv.Itoa(replPort))
	}
	return a, nil
}

// probe se conecta al destino y devuelve su nombre y huella para que el usuario la compare.
func (n *replNet) probe(ctx context.Context, addr string) (string, replProbe, error) {
	addr, err := normAddr(addr)
	if err != nil {
		return "", replProbe{}, err
	}
	c, err := n.httpClient("", 15*time.Second)
	if err != nil {
		return "", replProbe{}, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+"/info", nil)
	resp, err := c.Do(req)
	if err != nil {
		return "", replProbe{}, rtcrpc.Errorf("UNREACHABLE", "no se pudo conectar a %s: ¿tiene \"Recibir réplicas\" prendido? (%v)", addr, shortNetErr(err))
	}
	defer resp.Body.Close()
	var info struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&info)
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return "", replProbe{}, rtcrpc.Errorf("UNREACHABLE", "el destino no presentó certificado")
	}
	leaf := resp.TLS.PeerCertificates[0]
	p := replProbe{addr: addr, name: info.Name, fp: clusterpki.Fingerprint(leaf.RawSubjectPublicKeyInfo), spki: spkiOf(leaf), certSHA: clusterpki.TLSFingerprint(leaf.Raw), at: time.Now()}
	id := newReplID()
	n.r.mu.Lock()
	for k, v := range n.r.probes {
		if time.Since(v.at) > replInviteTTL {
			delete(n.r.probes, k)
		}
	}
	n.r.probes[id] = p
	n.r.mu.Unlock()
	return id, p, nil
}

func shortNetErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i > 0 && i < len(s)-2 {
		s = s[i+2:]
	}
	return s
}

// addTarget termina la vinculación con el código que muestra el destino.
func (n *replNet) addTarget(ctx context.Context, probeID, code string) (ReplTarget, error) {
	n.r.mu.Lock()
	p, ok := n.r.probes[probeID]
	delete(n.r.probes, probeID)
	n.r.mu.Unlock()
	if !ok {
		return ReplTarget{}, rtcrpc.Errorf("NOT_FOUND", "la conexión de prueba venció: empezá de nuevo")
	}
	cert, err := n.own()
	if err != nil {
		return ReplTarget{}, err
	}
	c, err := n.httpClient(p.spki, 20*time.Second)
	if err != nil {
		return ReplTarget{}, err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return ReplTarget{}, err
	}
	me := hostName()
	body, _ := json.Marshal(replPairReq{Name: me, Nonce: base64.StdEncoding.EncodeToString(nonce),
		MAC: base64.StdEncoding.EncodeToString(clusterpki.MAC(code, "repl-pair", nonce, cert.Leaf.RawSubjectPublicKeyInfo, p.certSHA, []byte(me)))})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+p.addr+"/pair", bytes.NewReader(body))
	resp, err := c.Do(req)
	if err != nil {
		return ReplTarget{}, rtcrpc.Errorf("UNREACHABLE", "no se pudo conectar a %s (%s)", p.addr, shortNetErr(err))
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return ReplTarget{}, rtcrpc.Errorf("PAIR_FAILED", "%s", strings.TrimSpace(string(b)))
	}
	var out struct {
		Name string `json:"name"`
		MAC  string `json:"mac"`
	}
	_ = json.Unmarshal(b, &out)
	mac, _ := base64.StdEncoding.DecodeString(out.MAC)
	if !clusterpki.CheckMAC(clusterpki.MAC(code, "repl-done", nonce, p.certSHA), mac) {
		return ReplTarget{}, rtcrpc.Errorf("PAIR_FAILED", "el destino no probó conocer el código")
	}
	name := out.Name
	if name == "" {
		name = p.name
	}
	t := ReplTarget{ID: p.spki[:12], Name: name, Addr: p.addr, Fingerprint: p.fp, SPKI: p.spki, AddedAt: time.Now().UTC().Format(time.RFC3339)}
	n.r.mu.Lock()
	keep := n.r.st.Targets[:0]
	for _, x := range n.r.st.Targets {
		if x.ID != t.ID {
			keep = append(keep, x)
		}
	}
	n.r.st.Targets = append(keep, t)
	n.r.save()
	n.r.mu.Unlock()
	return t, nil
}

// remoteSink manda a otro TangoOS vinculado.
type remoteSink struct {
	n *replNet
	t ReplTarget
}

func (n *replNet) client(t ReplTarget) replSink { return remoteSink{n: n, t: t} }

func (s remoteSink) Label() string { return s.t.Name }

func (s remoteSink) do(ctx context.Context, method, path string, body io.Reader, out any, timeout time.Duration) error {
	c, err := s.n.httpClient(s.t.SPKI, timeout)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, method, "https://"+s.t.Addr+path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := c.Do(req)
	if errors.Is(err, errPinMismatch) {
		return errPinMismatch
	}
	if err != nil {
		return fmt.Errorf("no se pudo conectar a %s (%s)", s.t.Name, shortNetErr(err))
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("%s respondió %d: %s", s.t.Name, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func (s remoteSink) State(ctx context.Context, vol string) (*agentproto.ZfsReplState, error) {
	var st agentproto.ZfsReplState
	err := s.do(ctx, http.MethodGet, "/v1/state?vol="+url.QueryEscape(vol), nil, &st, 60*time.Second)
	return &st, err
}

func (s remoteSink) Recv(ctx context.Context, vol, rel, base string, body io.Reader) error {
	q := url.Values{"vol": {vol}, "rel": {rel}}
	if base != "" {
		q.Set("base", base)
	}
	return s.do(ctx, http.MethodPost, "/v1/recv?"+q.Encode(), io.NopCloser(body), nil, 0)
}

func (s remoteSink) Mirror(ctx context.Context, vol string, keep map[string][]string) error {
	b, _ := json.Marshal(map[string]any{"vol": vol, "keep": keep})
	return s.do(ctx, http.MethodPost, "/v1/mirror", bytes.NewReader(b), nil, 5*time.Minute)
}
