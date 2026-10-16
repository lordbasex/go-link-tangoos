package api

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/clusterpki"
	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Clúster, segunda etapa (protocol/README.md §17, ADR 0012): unión con código y huella,
// TLS entre nodos, bricks en LV dedicado, operaciones de bricks, rebalance, cuotas,
// instantáneas y NFS.

type clusterAgent2 interface {
	GlusterTLS(ctx context.Context, a agentproto.GlusterTLSArgs) (*agentproto.GlusterTLSState, error)
	GlusterVolumeTLS(ctx context.Context, a agentproto.GlusterVolumeTLSArgs) error
	GlusterBrickLV(ctx context.Context, a agentproto.GlusterBrickLVArgs) (*agentproto.GlusterBrickLVResult, error)
	GlusterQuota(ctx context.Context, a agentproto.GlusterQuotaArgs) (*gluster.QuotaResult, error)
	GlusterRebalance(ctx context.Context, a agentproto.GlusterRebalanceArgs) (*gluster.Task, error)
	GlusterBricksTask(ctx context.Context, a agentproto.GlusterBricksArgs) (*gluster.Task, error)
	GlusterSnapshot(ctx context.Context, a agentproto.GlusterSnapshotArgs) ([]gluster.Snapshot, error)
}

// clusterJoin es una unión preparada (paso 1) esperando que el usuario confirme la huella.
type clusterJoin struct {
	Host    string
	Code    string
	Nonce   []byte
	ChanFP  []byte
	CSR     []byte
	Names   []string
	CN      string
	FP      string
	Expires time.Time
}

// ClusterTLSInfo es el TLS del clúster visto desde este nodo.
type ClusterTLSInfo struct {
	CA            bool   `json:"ca"`            // este nodo tiene la CA del clúster
	CAFingerprint string `json:"caFingerprint"` // huella de la CA
	Signed        bool   `json:"signed"`        // este nodo tiene certificado de la CA
	Fingerprint   string `json:"fingerprint"`   // huella de la clave de este nodo
	Files         bool   `json:"files"`         // /etc/ssl/glusterfs.* instalados
	Secure        bool   `json:"secure"`        // management con TLS (secure-access)
	// CASince es la fecha de la CA actual; Revoke, los nodos que salieron después y
	// conservan su clave (se resuelve rotando la CA).
	CASince string        `json:"caSince,omitempty"`
	Revoke  []revokedNode `json:"revoke"`
}

// ClusterBrickSpec es un brick pedido desde la UI: carpeta en un volumen de TangoOS o LV
// dedicado (XFS) en un grupo de almacenamiento.
type ClusterBrickSpec struct {
	Host        string `json:"host"`
	Kind        string `json:"kind"` // dir (por defecto) | lv
	TangoVolume string `json:"tangoVolume,omitempty"`
	Pool        string `json:"pool,omitempty"`
	Size        uint64 `json:"size,omitempty"`
	Thin        bool   `json:"thin,omitempty"`
}

func (c *clusterAPI) ag2() (clusterAgent2, error) {
	a, ok := c.ag.(clusterAgent2)
	if !ok {
		return nil, rtcrpc.Errorf("NOT_SUPPORTED", "el agente no soporta esta operación del clúster")
	}
	return a, nil
}

// tlsInfo arma ClusterTLSInfo con el estado del agente.
func (c *clusterAPI) tlsInfo(st *agentproto.GlusterStatusResult) ClusterTLSInfo {
	if c.pki == nil {
		return ClusterTLSInfo{}
	}
	info := ClusterTLSInfo{CA: c.pki.hasCA(), CAFingerprint: c.pki.caFingerprint(), Signed: c.pki.signed(),
		Fingerprint: c.pki.nodeFingerprint(), Files: st.TLS.Files, Secure: st.TLS.Secure, Revoke: c.pki.revoked()}
	if info.Revoke == nil {
		info.Revoke = []revokedNode{}
	}
	if t := c.pki.caSince(); !t.IsZero() {
		info.CASince = t.Format(time.RFC3339)
	}
	return info
}

// ---- Lado local de los pedidos del canal ----------------------------------------------

func (c *clusterAPI) localInfo(ctx context.Context) (*CtlInfo, error) {
	st, err := c.agentStatus(ctx, false)
	if err != nil {
		return nil, agentErr(err)
	}
	info := &CtlInfo{CN: c.self(), Fingerprint: c.pki.nodeFingerprint(), Names: append([]string{c.self()}, localAddrs()...),
		Secure: st.TLS.Secure, Mounts: st.Mounts, TangoVolumes: c.tangoVolumes(ctx), Pools: c.localPools(ctx)}
	return info, nil
}

func (c *clusterAPI) tangoVolumes(ctx context.Context) []ClusterTangoVolume {
	out := []ClusterTangoVolume{}
	if inv := c.m.a.d.Inventory; inv != nil {
		if vols, err := inv.Volumes(ctx); err == nil {
			for _, v := range vols {
				if v.Mount != "" && (v.FS == "" || v.FS == "btrfs") {
					out = append(out, ClusterTangoVolume{Name: v.Name, Mount: v.Mount, Free: v.Size - v.Used})
				}
			}
		}
	}
	return out
}

func (c *clusterAPI) localPools(ctx context.Context) []CtlPool {
	out := []CtlPool{}
	inv := c.m.a.d.Inventory
	if inv == nil {
		return out
	}
	rep := inv.PoolReport(ctx)
	if rep == nil {
		return out
	}
	for _, p := range rep.Pools {
		var free uint64
		for _, pv := range p.PVs {
			if pv.Role != "cache" {
				free += pv.Free
			}
		}
		out = append(out, CtlPool{Name: p.Name, Free: free})
	}
	return out
}

func (c *clusterAPI) localBrick(ctx context.Context, q ctlBrickReq) (any, error) {
	ag2, err := c.ag2()
	if err != nil {
		return nil, err
	}
	args := agentproto.GlusterBrickLVArgs{Op: q.Op, Pool: q.Pool, Vol: q.Vol, Size: q.Size, Thin: q.Thin}
	if q.Op == "delete" {
		args.Self = c.selfNames()
	}
	r, err := ag2.GlusterBrickLV(ctx, args)
	if err != nil {
		return nil, agentErr(err)
	}
	if r == nil {
		return struct{}{}, nil
	}
	return r, nil
}

func (c *clusterAPI) localSecure(ctx context.Context, secure bool) error {
	if !c.pki.signed() && secure {
		return errors.New("este nodo no tiene certificado del clúster")
	}
	ag2, err := c.ag2()
	if err != nil {
		return err
	}
	if _, err := ag2.GlusterTLS(ctx, agentproto.GlusterTLSArgs{Secure: secure, Restart: true}); err != nil {
		return agentErr(err)
	}
	c.stMu.Lock()
	c.st = nil
	c.stMu.Unlock()
	return nil
}

// localMounts detiene o arranca los montajes de este nodo. stop devuelve los que estaban
// montados (para volver a arrancarlos después).
func (c *clusterAPI) localMounts(ctx context.Context, q ctlMountsReq) ([]string, error) {
	st, err := c.agentStatus(ctx, true)
	if err != nil {
		return nil, agentErr(err)
	}
	var done []string
	for _, m := range st.Mounts {
		switch {
		case q.Op == "stop" && m.Mounted:
		case q.Op == "start" && slices.Contains(q.Names, m.Name):
		case q.Op == "acl" && slices.Contains(q.Names, m.Name) && m.Mounted && m.Share != "":
		default:
			continue
		}
		if err := c.ag.GlusterMount(ctx, agentproto.GlusterMountArgs{Action: q.Op, Name: m.Name}); err != nil {
			return done, agentErr(err)
		}
		done = append(done, m.Name)
	}
	return done, nil
}

// ---- Llamadas a un nodo (local o remoto) ------------------------------------------------

// selfNames son los nombres con los que se conoce a este nodo (nombre e IPs).
func (c *clusterAPI) selfNames() []string { return append([]string{c.self()}, localAddrs()...) }

// lvOrphans son los LV de bricks de este nodo que ningún brick usa: de un volumen borrado,
// o reemplazados, o que quedaron sin uso al restaurar una instantánea.
func (c *clusterAPI) lvOrphans(st *agentproto.GlusterStatusResult) []agentproto.GlusterOrphan {
	out := []agentproto.GlusterOrphan{}
	names := c.selfNames()
	for _, lb := range st.LVBricks {
		used := false
		for _, v := range st.Volumes {
			if v.Name != lb.Vol {
				continue
			}
			for _, b := range v.Bricks {
				if b.Path == gluster.LVBrickPath(lb.Vol) && slices.Contains(names, b.Host) {
					used = true
				}
			}
		}
		if !used {
			out = append(out, agentproto.GlusterOrphan{Kind: "lv", TangoVolume: lb.Pool, Name: lb.Vol, Path: gluster.LVMountPath(lb.Vol)})
		}
	}
	return out
}

func (c *clusterAPI) isSelf(host string) bool {
	if host == c.self() {
		return true
	}
	for _, a := range localAddrs() {
		if a == host {
			return true
		}
	}
	return false
}

func (c *clusterAPI) nodeInfo(ctx context.Context, host string) (*CtlInfo, error) {
	if c.isSelf(host) {
		return c.localInfo(ctx)
	}
	var info CtlInfo
	if err := c.ctlCall(ctx, host, "GET", "/v1/info", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (c *clusterAPI) nodeBrick(ctx context.Context, host string, q ctlBrickReq) (string, error) {
	if c.isSelf(host) {
		r, err := c.localBrick(ctx, q)
		if err != nil {
			return "", err
		}
		if br, ok := r.(*agentproto.GlusterBrickLVResult); ok {
			return br.Path, nil
		}
		return "", nil
	}
	var r agentproto.GlusterBrickLVResult
	if err := c.ctlCall(ctx, host, "POST", "/v1/brick", q, &r); err != nil {
		return "", err
	}
	return r.Path, nil
}

func (c *clusterAPI) nodeSecure(ctx context.Context, host string, secure bool) error {
	if c.isSelf(host) {
		return c.localSecure(ctx, secure)
	}
	return c.ctlCall(ctx, host, "POST", "/v1/tls", map[string]bool{"secure": secure}, nil)
}

func (c *clusterAPI) nodeMounts(ctx context.Context, host string, q ctlMountsReq) ([]string, error) {
	if c.isSelf(host) {
		return c.localMounts(ctx, q)
	}
	var r struct {
		Names []string `json:"names"`
	}
	err := c.ctlCall(ctx, host, "POST", "/v1/mounts", q, &r)
	return r.Names, err
}

// refreshNodeInfo pide /v1/info a cada nodo conectado (para el asistente de bricks).
func (c *clusterAPI) refreshNodeInfo(ctx context.Context, st *agentproto.GlusterStatusResult) {
	if c.pki == nil || !c.pki.signed() {
		return
	}
	out := map[string]*CtlInfo{}
	for _, p := range st.Peers {
		if !p.Connected && !p.Self {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		info, err := c.nodeInfo(cctx, p.Host)
		cancel()
		if err == nil {
			out[p.Host] = info
		}
	}
	c.mu.Lock()
	c.infos = out
	c.mu.Unlock()
}

// ---- Unión con código y huella ------------------------------------------------------------

// prepareJoin es el paso 1 en el nodo que suma: abre este lado, habla con el invitado
// usando el código y devuelve su huella para que el usuario la compare con la pantalla
// del otro equipo.
func (c *clusterAPI) prepareJoin(ctx context.Context, host, code string) (*clusterJoin, error) {
	if c.rotating.Load() {
		return nil, rtcrpc.Errorf("BUSY", "se está rotando la CA del clúster: esperá a que termine")
	}
	if _, err := c.invite(ctx, host); err != nil {
		return nil, err
	}
	if err := c.syncFirewall(ctx, nil); err != nil {
		return nil, err
	}
	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	// El MAC del hello incluye la huella del certificado del canal: primero se lee el
	// certificado (solo el handshake TLS), después va el hello sobre esa misma huella.
	fp, err := peekFP(ctx, host)
	if err != nil {
		return nil, rtcrpc.Errorf("PEER_UNREACHABLE", "%s no responde en el puerto %d: invitalo primero desde la app Clúster de %s", host, gluster.PortCtl, host)
	}
	var resp joinHelloResp
	fp2, err := joinPost(ctx, host, "/join/hello", joinHello{Nonce: b64.EncodeToString(nonce), From: c.self(),
		MAC: b64.EncodeToString(clusterpki.MAC(code, "hello", nonce, fp))}, &resp)
	if err != nil {
		if strings.Contains(err.Error(), "código") {
			return nil, rtcrpc.Errorf("JOIN_CODE", "el código no coincide con el que muestra %s", host)
		}
		return nil, rtcrpc.Errorf("PEER_UNREACHABLE", "%s", err.Error())
	}
	if string(fp2) != string(fp) {
		return nil, rtcrpc.Errorf("JOIN_TAMPERED", "el certificado de %s cambió durante la unión: puede haber alguien en el medio", host)
	}
	nb, _ := json.Marshal(resp.Names)
	mac, _ := b64.DecodeString(resp.MAC)
	if !clusterpki.CheckMAC(clusterpki.MAC(code, "csr", nonce, []byte(resp.CSR), nb, fp), mac) {
		return nil, rtcrpc.Errorf("JOIN_TAMPERED", "la respuesta de %s no está firmada con el código: puede haber alguien en el medio", host)
	}
	req, err := clusterpki.ParseCSR([]byte(resp.CSR))
	if err != nil || req.Subject.CommonName != resp.CN || gluster.ValidCN(resp.CN) != nil {
		return nil, rtcrpc.Errorf("JOIN_TAMPERED", "pedido de certificado inválido de %s", host)
	}
	fpDisplay := clusterpki.Fingerprint(req.RawSubjectPublicKeyInfo)
	j := &clusterJoin{Host: host, Code: code, Nonce: nonce, ChanFP: fp, CSR: []byte(resp.CSR), Names: resp.Names, CN: resp.CN, FP: fpDisplay, Expires: time.Now().Add(10 * time.Minute)}
	return j, nil
}

// finishJoin es el paso 2 (después de que el usuario confirmó la huella): firma el
// certificado del invitado, se lo entrega con la CA y lo suma al pool.
func (c *clusterAPI) finishJoin(ctx context.Context, j *clusterJoin, probe bool) error {
	if c.rotating.Load() {
		return rtcrpc.Errorf("BUSY", "se está rotando la CA del clúster: esperá a que termine")
	}
	st, err := c.agentStatus(ctx, true)
	if err != nil {
		return agentErr(err)
	}
	created, err := c.pki.ensureCA(c.self(), append([]string{c.self()}, localAddrs()...))
	if err != nil {
		return err
	}
	if created || !st.TLS.Files {
		if err := c.installTLSFiles(ctx, st.TLS.Secure, false); err != nil {
			return err
		}
	}
	cert, err := clusterpki.Sign(c.pki.read("ca.pem"), c.pki.read("ca.key"), j.CSR, j.Names)
	if err != nil {
		return err
	}
	ca, caKey := c.pki.read("ca.pem"), c.pki.read("ca.key")
	sec := []byte{0}
	if st.TLS.Secure {
		sec[0] = 1
	}
	var resp joinFinishResp
	fp, err := joinPost(ctx, j.Host, "/join/finish", joinFinish{Nonce: b64.EncodeToString(j.Nonce), Cert: string(cert), CA: string(ca), CAKey: string(caKey),
		Secure: st.TLS.Secure, MAC: b64.EncodeToString(clusterpki.MAC(j.Code, "finish", j.Nonce, cert, ca, caKey, sec, j.ChanFP))}, &resp)
	if err != nil {
		return rtcrpc.Errorf("PEER_UNREACHABLE", "%s", err.Error())
	}
	if string(fp) != string(j.ChanFP) {
		return rtcrpc.Errorf("JOIN_TAMPERED", "el certificado de %s cambió durante la unión", j.Host)
	}
	mac, _ := b64.DecodeString(resp.MAC)
	if !clusterpki.CheckMAC(clusterpki.MAC(j.Code, "done", j.Nonce, []byte(resp.CAFingerprint), j.ChanFP), mac) || resp.CAFingerprint != c.pki.caFingerprint() {
		return rtcrpc.Errorf("JOIN_TAMPERED", "%s no confirmó la CA del clúster", j.Host)
	}
	if probe {
		if err := c.ag.GlusterPeer(ctx, agentproto.GlusterPeerArgs{Action: "probe", Host: j.Host}); err != nil {
			return agentErr(err)
		}
	}
	// Con TLS de E/S, el nodo nuevo entra en auth.ssl-allow de cada volumen.
	if err := c.syncSSLAllow(ctx, map[string]string{j.Host: j.CN}, ""); err != nil {
		c.m.a.d.Log.Warn("cluster: auth.ssl-allow", "err", err)
	}
	return nil
}

// syncSSLAllow recalcula auth.ssl-allow de los volúmenes con TLS: los CN de los nodos que
// responden por el canal, más extra (nodos recién unidos), menos drop (nodo que sale). Un
// nodo que no responde conserva su CN si ya estaba.
func (c *clusterAPI) syncSSLAllow(ctx context.Context, extra map[string]string, drop string) error {
	ag2, err := c.ag2()
	if err != nil {
		return err
	}
	st, err := c.agentStatus(ctx, true)
	if err != nil {
		return agentErr(err)
	}
	cns := map[string]bool{c.self(): true}
	for _, cn := range extra {
		cns[cn] = true
	}
	known := map[string]bool{}
	for _, p := range st.Peers {
		if p.Self {
			continue
		}
		if cn, ok := extra[p.Host]; ok {
			known[cn] = true
			continue
		}
		if info, err := c.nodeInfo(ctx, p.Host); err == nil {
			cns[info.CN] = true
			known[info.CN] = true
		}
	}
	for _, v := range st.Volumes {
		if !v.TLS {
			continue
		}
		set := map[string]bool{}
		for k := range cns {
			set[k] = true
		}
		for _, cn := range strings.Split(v.Options["auth.ssl-allow"], ",") {
			if cn != "" && cn != drop && !known[cn] && cn != "*" {
				set[cn] = true // nodo sin respuesta: se conserva
			}
		}
		delete(set, drop)
		list := make([]string, 0, len(set))
		for k := range set {
			list = append(list, k)
		}
		sort.Strings(list)
		if strings.Join(list, ",") == v.Options["auth.ssl-allow"] {
			continue
		}
		if err := ag2.GlusterVolumeTLS(ctx, agentproto.GlusterVolumeTLSArgs{Name: v.Name, On: true, CNs: list}); err != nil {
			return agentErr(err)
		}
	}
	return nil
}

// syncSSLAllowNew prende el TLS de E/S de un volumen recién creado (sin arrancar) con los
// CN de todos los nodos que responden.
func (c *clusterAPI) syncSSLAllowNew(ctx context.Context, vol string) error {
	ag2, err := c.ag2()
	if err != nil {
		return err
	}
	st, err := c.agentStatus(ctx, true)
	if err != nil {
		return agentErr(err)
	}
	cns := []string{c.self()}
	for _, p := range st.Peers {
		if p.Self {
			continue
		}
		info, err := c.nodeInfo(ctx, p.Host)
		if err != nil {
			return fmt.Errorf("%s no responde por el canal de control: no se puede armar la lista TLS de %s (%w)", p.Host, vol, err)
		}
		cns = append(cns, info.CN)
	}
	sort.Strings(cns)
	if err := ag2.GlusterVolumeTLS(ctx, agentproto.GlusterVolumeTLSArgs{Name: vol, On: true, CNs: cns}); err != nil {
		return agentErr(err)
	}
	return nil
}

// ---- TLS: prender o apagar con parada controlada --------------------------------------------

// setTLS desmonta los volúmenes en cada nodo, los detiene, prende (o apaga) el TLS de
// management en todos los nodos (reinicio de glusterd), aplica el TLS de E/S a cada
// volumen y vuelve a arrancar y montar lo que estaba andando.
func (c *clusterAPI) setTLS(ctx context.Context, h *jobs.Handle, on bool) error {
	ag2, err := c.ag2()
	if err != nil {
		return err
	}
	if on && !c.pki.signed() {
		if _, err := c.pki.ensureCA(c.self(), append([]string{c.self()}, localAddrs()...)); err != nil {
			return err
		}
		if err := c.installTLSFiles(ctx, false, false); err != nil {
			return err
		}
	}
	st, err := c.agentStatus(ctx, true)
	if err != nil {
		return agentErr(err)
	}
	var hosts []string
	cns := []string{c.self()}
	h.Update(0.05, "Comprobando que cada nodo tenga su certificado")
	for _, p := range st.Peers {
		if p.Self {
			continue
		}
		if !p.Connected {
			return fmt.Errorf("%s no está conectado: con un nodo apagado no se puede cambiar el TLS", p.Host)
		}
		info, err := c.nodeInfo(ctx, p.Host)
		if err != nil {
			return fmt.Errorf("%s no tiene certificado del clúster: invitalo desde su app Clúster y certificalo («Certificar nodo») antes de prender el TLS (%v)", p.Host, err)
		}
		hosts = append(hosts, p.Host)
		cns = append(cns, info.CN)
	}
	sort.Strings(cns)
	all := append(append([]string{}, hosts...), c.self())

	mounted, started, err := c.stopVolumes(ctx, h, all, st, 0.15, 0.3)
	if err != nil {
		return err
	}
	h.Update(0.45, map[bool]string{true: "Prendiendo el TLS de management (reinicio de glusterd en cada nodo)", false: "Apagando el TLS de management"}[on])
	for _, host := range all {
		if err := c.nodeSecure(ctx, host, on); err != nil {
			return fmt.Errorf("%s: %w (los volúmenes quedaron detenidos: arreglalo y volvé a intentar)", host, err)
		}
	}
	h.Update(0.6, "Esperando que los nodos se reconecten")
	if err := c.waitPeers(ctx, len(hosts), 90*time.Second); err != nil {
		return err
	}
	h.Update(0.7, "Aplicando el TLS de E/S a cada volumen")
	for _, v := range st.Volumes {
		if err := ag2.GlusterVolumeTLS(ctx, agentproto.GlusterVolumeTLSArgs{Name: v.Name, On: on, CNs: cns}); err != nil {
			return agentErr(err)
		}
	}
	h.Update(0.8, "Arrancando los volúmenes")
	if err := c.restartVolumes(ctx, started); err != nil {
		return err
	}
	h.Update(0.9, "Montando de nuevo")
	c.restoreMounts(ctx, mounted)
	return nil
}

// stopVolumes desmonta los volúmenes del clúster en cada nodo y detiene los que estaban
// arrancados. Devuelve qué había montado en cada nodo y qué estaba arrancado (para
// restartVolumes y restoreMounts); si algo falla, deja todo como estaba.
func (c *clusterAPI) stopVolumes(ctx context.Context, h *jobs.Handle, all []string, st *agentproto.GlusterStatusResult, pUnmount, pStop float64) (map[string][]string, []string, error) {
	h.Update(pUnmount, "Desmontando los volúmenes en cada nodo")
	mounted := map[string][]string{}
	for _, host := range all {
		names, err := c.nodeMounts(ctx, host, ctlMountsReq{Op: "stop"})
		mounted[host] = names
		if err != nil {
			c.restoreMounts(ctx, mounted)
			return nil, nil, fmt.Errorf("no se pudo desmontar en %s (¿archivos abiertos por SMB o NFS?): %w", host, err)
		}
	}
	var started []string
	for _, v := range st.Volumes {
		if v.Status == "Started" {
			started = append(started, v.Name)
		}
	}
	h.Update(pStop, "Deteniendo los volúmenes")
	for _, name := range started {
		if err := c.ag.GlusterVolumeAction(ctx, agentproto.GlusterVolumeActionArgs{Name: name, Action: "stop"}); err != nil {
			c.restartVolumes(ctx, started)
			c.restoreMounts(ctx, mounted)
			return nil, nil, agentErr(err)
		}
	}
	return mounted, started, nil
}

func (c *clusterAPI) restartVolumes(ctx context.Context, names []string) error {
	var first error
	for _, name := range names {
		err := c.ag.GlusterVolumeAction(ctx, agentproto.GlusterVolumeActionArgs{Name: name, Action: "start"})
		if err != nil && !strings.Contains(err.Error(), "already started") && first == nil {
			first = agentErr(err)
		}
	}
	return first
}

func (c *clusterAPI) restoreMounts(ctx context.Context, mounted map[string][]string) {
	for host, names := range mounted {
		if len(names) == 0 {
			continue
		}
		if _, err := c.nodeMounts(ctx, host, ctlMountsReq{Op: "start", Names: names}); err != nil {
			c.m.a.d.Log.Warn("cluster: volver a montar", "host", host, "err", err)
		}
	}
}

// reapplyRootACL: al agregar bricks, GlusterFS cura la raíz del volumen con el modo del
// brick nuevo (000) y deja a los usuarios afuera. Cada nodo que lo tiene montado con una
// carpeta SMB vuelve a poner sus permisos (alcanza con uno: quedan en GlusterFS).
func (c *clusterAPI) reapplyRootACL(ctx context.Context, vol string) {
	st, err := c.agentStatus(ctx, true)
	if err != nil {
		return
	}
	time.Sleep(3 * time.Second) // que los clientes FUSE tomen el volumen con los bricks nuevos
	for _, p := range st.Peers {
		if !p.Connected && !p.Self {
			continue
		}
		q := ctlMountsReq{Op: "acl", Names: []string{vol}}
		if p.Self {
			_, err = c.localMounts(ctx, q)
		} else {
			_, err = c.nodeMounts(ctx, p.Host, q)
		}
		if err != nil {
			c.m.a.d.Log.Warn("cluster: permisos de la raíz", "host", p.Host, "vol", vol, "err", err)
		}
	}
}

// waitPeers espera que n nodos (sin contar este) estén conectados.
func (c *clusterAPI) waitPeers(ctx context.Context, n int, max time.Duration) error {
	deadline := time.Now().Add(max)
	for {
		st, err := c.agentStatus(ctx, true)
		if err == nil && st.Running {
			ok := 0
			for _, p := range st.Peers {
				if !p.Self && p.Connected {
					ok++
				}
			}
			if ok >= n {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("los nodos no se reconectaron a tiempo: revisá que todos tengan el mismo estado de TLS")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// ---- Bricks pedidos desde la UI ----------------------------------------------------------

// resolveBricks prepara los bricks: los "lv" se crean en su nodo (LV con XFS) y devuelven
// su ruta; los "dir" son /<volumen>/@gluster/<vol>. undo borra los LV creados si después
// algo falla.
func (c *clusterAPI) resolveBricks(ctx context.Context, h *jobs.Handle, vol string, specs []ClusterBrickSpec) ([]gluster.Brick, func(), error) {
	var made []ClusterBrickSpec
	undo := func() {
		for _, s := range made {
			if _, err := c.nodeBrick(context.WithoutCancel(ctx), s.Host, ctlBrickReq{Op: "delete", Vol: vol, Pool: s.Pool}); err != nil {
				c.m.a.d.Log.Warn("cluster: borrar brick", "host", s.Host, "err", err)
			}
		}
	}
	out := make([]gluster.Brick, 0, len(specs))
	for i, s := range specs {
		switch s.Kind {
		case "", "dir":
			out = append(out, gluster.Brick{Host: s.Host, Path: gluster.BrickPath(s.TangoVolume, vol)})
		case "lv":
			if h != nil {
				h.Update(0.05+0.4*float64(i)/float64(len(specs)), "Creando el volumen del brick en "+s.Host)
			}
			path, err := c.nodeBrick(ctx, s.Host, ctlBrickReq{Op: "create", Vol: vol, Pool: s.Pool, Size: s.Size, Thin: s.Thin})
			if err != nil {
				undo()
				return nil, nil, fmt.Errorf("%s: %w", s.Host, err)
			}
			made = append(made, s)
			out = append(out, gluster.Brick{Host: s.Host, Path: path})
		default:
			undo()
			return nil, nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "tipo de brick inválido: %q", s.Kind)
		}
	}
	return out, undo, nil
}

// peekFP hace solo el handshake TLS con el canal de un nodo y devuelve la huella de su
// certificado.
func peekFP(ctx context.Context, host string) ([]byte, error) {
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}} // solo para leer la huella
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(ctlPort)))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("sin certificado")
	}
	return clusterpki.TLSFingerprint(certs[0].Raw), nil
}

// ---- Métodos ---------------------------------------------------------------------------

func (c *clusterAPI) register2(r *rtcrpc.Router, admin func(fn func(ctx context.Context, p json.RawMessage) (any, error)) rtcrpc.HandlerFunc,
	decode func(p json.RawMessage, v any) error) {
	a := c.m.a

	// Unión, paso 1: el código que muestra el invitado → su huella para comparar.
	r.Handle("cluster.peer.prepare", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Host string `json:"host"`
			Code string `json:"code"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		if len(clusterpki.NormalizeCode(q.Code)) != 10 {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "el código tiene 10 caracteres (como ABCDE-FGHJK)")
		}
		j, err := c.prepareJoin(ctx, q.Host, q.Code)
		if err != nil {
			return nil, err
		}
		id := make([]byte, 12)
		_, _ = rand.Read(id)
		key := b64.EncodeToString(id)
		c.mu.Lock()
		c.joins[key] = j
		c.mu.Unlock()
		return map[string]string{"id": key, "host": j.Host, "cn": j.CN, "fingerprint": j.FP}, nil
	}))

	// Unión, paso 2: el usuario confirmó la huella. certify = el nodo ya está en el pool
	// (clúster anterior al TLS): solo recibe su certificado.
	r.Handle("cluster.peer.confirm", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			ID      string `json:"id"`
			Certify bool   `json:"certify"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		c.mu.Lock()
		j := c.joins[q.ID]
		delete(c.joins, q.ID)
		c.mu.Unlock()
		if j == nil || time.Now().After(j.Expires) {
			return nil, rtcrpc.Errorf("JOIN_EXPIRED", "la unión venció: empezá de nuevo")
		}
		if err := c.finishJoin(ctx, j, !q.Certify); err != nil {
			return nil, err
		}
		c.publish(ctx)
		return struct{}{}, nil
	}))

	r.Handle("cluster.tls.set", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Enabled bool `json:"enabled"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		title := map[bool]string{true: "Prendiendo TLS en el clúster", false: "Apagando TLS en el clúster"}[q.Enabled]
		j := a.d.Jobs.Start("cluster.tls", title, "Los volúmenes se detienen un momento", func(ctx context.Context, h *jobs.Handle) error {
			defer c.publish(context.WithoutCancel(ctx))
			return c.setTLS(ctx, h, q.Enabled)
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))

	// Rotación de la CA del clúster (cluster_rotate.go): después de sacar un nodo, o cuando
	// se quiera.
	r.Handle("cluster.ca.rotate", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct{}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		if c.rotating.Load() {
			return nil, rtcrpc.Errorf("BUSY", "ya hay una rotación de la CA en curso")
		}
		j := a.d.Jobs.Start("cluster.ca.rotate", "Rotando la CA del clúster", "Si GlusterFS usa TLS, los volúmenes se detienen un momento", func(ctx context.Context, h *jobs.Handle) error {
			defer c.publish(context.WithoutCancel(ctx))
			return c.rotateCA(ctx, h)
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))

	r.Handle("cluster.volume.bricks.add", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Name    string             `json:"name"`
			Replica int                `json:"replica"`
			Bricks  []ClusterBrickSpec `json:"bricks"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		if err := gluster.ValidVolumeName(q.Name); err != nil || len(q.Bricks) == 0 {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "volumen o bricks inválidos")
		}
		j := a.d.Jobs.Start("cluster.bricks.add", "Agregando bricks a "+q.Name, "", func(ctx context.Context, h *jobs.Handle) error {
			defer c.publish(context.WithoutCancel(ctx))
			bricks, undo, err := c.resolveBricks(ctx, h, q.Name, q.Bricks)
			if err != nil {
				return err
			}
			h.Update(0.6, "Agregando los bricks al volumen")
			if err := c.ag.GlusterBricks(ctx, agentproto.GlusterBricksArgs{Name: q.Name, Op: "add", Replica: q.Replica, Bricks: bricks}); err != nil {
				undo()
				return agentErr(err)
			}
			h.Update(0.9, "Restableciendo los permisos de la raíz")
			c.reapplyRootACL(ctx, q.Name)
			return nil
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))

	r.Handle("cluster.volume.bricks.replace", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Name string           `json:"name"`
			From gluster.Brick    `json:"from"`
			To   ClusterBrickSpec `json:"to"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		j := a.d.Jobs.Start("cluster.bricks.replace", "Reemplazando un brick de "+q.Name, q.From.String(), func(ctx context.Context, h *jobs.Handle) error {
			defer c.publish(context.WithoutCancel(ctx))
			bricks, undo, err := c.resolveBricks(ctx, h, q.Name, []ClusterBrickSpec{q.To})
			if err != nil {
				return err
			}
			h.Update(0.6, "Reemplazando: la copia nueva se completa con la curación")
			if err := c.ag.GlusterBricks(ctx, agentproto.GlusterBricksArgs{Name: q.Name, Op: "replace", From: q.From, To: bricks[0]}); err != nil {
				undo()
				return agentErr(err)
			}
			return nil
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))

	// remove: start (migra los datos), status (avance), commit (los saca). Con replica > 0
	// baja la cantidad de copias en un solo paso (commit).
	r.Handle("cluster.volume.bricks.remove", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Name    string          `json:"name"`
			Replica int             `json:"replica"`
			Bricks  []gluster.Brick `json:"bricks"`
			Phase   string          `json:"phase"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		ag2, err := c.ag2()
		if err != nil {
			return nil, err
		}
		t, err := ag2.GlusterBricksTask(ctx, agentproto.GlusterBricksArgs{Name: q.Name, Op: "remove", Replica: q.Replica, Bricks: q.Bricks, Phase: q.Phase})
		if err != nil {
			return nil, agentErr(err)
		}
		if q.Phase != "status" {
			c.publish(ctx)
		}
		return map[string]any{"task": t}, nil
	}))

	r.Handle("cluster.volume.rebalance", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q agentproto.GlusterRebalanceArgs
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		ag2, err := c.ag2()
		if err != nil {
			return nil, err
		}
		t, err := ag2.GlusterRebalance(ctx, q)
		if err != nil {
			return nil, agentErr(err)
		}
		return map[string]any{"task": t}, nil
	}))

	c.quotaRoutes(r, admin, decode)

	r.Handle("cluster.volume.snapshot", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q agentproto.GlusterSnapshotArgs
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		ag2, err := c.ag2()
		if err != nil {
			return nil, err
		}
		if q.Op == "restore" {
			st, err := c.agentStatus(ctx, true)
			if err != nil {
				return nil, agentErr(err)
			}
			for _, v := range st.Volumes {
				if v.Name == q.Name && v.Status == "Started" {
					return nil, rtcrpc.Errorf("VOLUME_STARTED", "para restaurar, primero detené el volumen %s", q.Name)
				}
			}
		}
		list, err := ag2.GlusterSnapshot(ctx, q)
		if err != nil {
			msg := err.Error()
			if strings.Contains(msg, "thinly provisioned") || strings.Contains(msg, "not a thinly") || strings.Contains(msg, "LVM") {
				return nil, rtcrpc.Errorf("SNAPSHOT_UNSUPPORTED", "las instantáneas necesitan que todos los bricks sean volúmenes thin dedicados")
			}
			return nil, agentErr(err)
		}
		if q.Op != "info" {
			c.publish(ctx)
			list, _ = ag2.GlusterSnapshot(ctx, agentproto.GlusterSnapshotArgs{Name: q.Name, Op: "info"})
		}
		if list == nil {
			list = []gluster.Snapshot{}
		}
		return map[string]any{"snapshots": list}, nil
	}))

	// NFS: exporta /cluster/<vol> (montado en este nodo) a los equipos indicados.
	r.Handle("cluster.volume.nfs", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Name    string                 `json:"name"`
			Clients []agentproto.NFSClient `json:"clients"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		if err := gluster.ValidVolumeName(q.Name); err != nil {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%s", err.Error())
		}
		st, err := c.agentStatus(ctx, true)
		if err != nil {
			return nil, agentErr(err)
		}
		mounted := false
		for _, m := range st.Mounts {
			if m.Name == q.Name && m.Mounted {
				mounted = true
			}
		}
		if len(q.Clients) > 0 && !mounted {
			return nil, rtcrpc.Errorf("NOT_MOUNTED", "primero montá %s en este NAS", q.Name)
		}
		m := c.m
		m.mu.Lock()
		if m.cfg.NFS.Cluster == nil {
			m.cfg.NFS.Cluster = map[string][]agentproto.NFSClient{}
		}
		if len(q.Clients) == 0 {
			delete(m.cfg.NFS.Cluster, q.Name)
		} else {
			m.cfg.NFS.Cluster[q.Name] = q.Clients
			m.cfg.NFS.Enabled = true
		}
		err = m.save()
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		j := a.d.Jobs.Start("cluster.volume.nfs", "Exportando "+q.Name+" por NFS", "/cluster/"+q.Name, func(ctx context.Context, h *jobs.Handle) error {
			defer c.publish(context.WithoutCancel(ctx))
			return m.applyNFS(ctx, h)
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))
}

// clusterNFSExports son los exports de volúmenes del clúster montados (los agrega applyNFS).
func (c *clusterAPI) clusterNFSExports(ctx context.Context, cfg map[string][]agentproto.NFSClient) []agentproto.NFSExport {
	if len(cfg) == 0 {
		return nil
	}
	st, err := c.agentStatus(ctx, false)
	if err != nil {
		return nil
	}
	ids := map[string]string{}
	for _, v := range st.Volumes {
		ids[v.Name] = v.ID
	}
	var out []agentproto.NFSExport
	for _, m := range st.Mounts {
		if cl, ok := cfg[m.Name]; ok && m.Mounted && ids[m.Name] != "" {
			out = append(out, agentproto.NFSExport{Path: m.Path, Clients: cl, FSID: ids[m.Name]})
		}
	}
	return out
}
