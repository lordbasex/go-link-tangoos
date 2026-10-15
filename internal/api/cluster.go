package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/clusterpki"
	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Clúster GlusterFS (protocol/README.md §17, ADR 0012). tangod decide quién puede
// hablar con glusterd (invitaciones + nodos del clúster → firewall del agente),
// resuelve nombres (el agente no tiene red), mide la latencia entre nodos y avisa de
// split-brain, curación pendiente y nodos caídos.

type clusterAgent interface {
	GlusterInstall(ctx context.Context, a agentproto.GlusterInstallArgs) error
	GlusterFirewall(ctx context.Context, allowed []string) error
	GlusterStatus(ctx context.Context, self string) (*agentproto.GlusterStatusResult, error)
	GlusterPeer(ctx context.Context, a agentproto.GlusterPeerArgs) error
	GlusterVolumeCreate(ctx context.Context, a agentproto.GlusterVolumeCreateArgs) error
	GlusterVolumeAction(ctx context.Context, a agentproto.GlusterVolumeActionArgs) error
	GlusterVolumeOptions(ctx context.Context, a agentproto.GlusterVolumeOptionsArgs) error
	GlusterHeal(ctx context.Context, a agentproto.GlusterHealArgs) error
	GlusterBricks(ctx context.Context, a agentproto.GlusterBricksArgs) error
	GlusterMount(ctx context.Context, a agentproto.GlusterMountArgs) error
	GlusterBrickCleanup(ctx context.Context, a agentproto.GlusterBrickCleanArgs) error
	GlusterHealInfo(ctx context.Context, name string) ([]gluster.HealBrick, error)
}

// InviteTTL es cuánto queda abierta una invitación (la confirmación de este lado).
const InviteTTL = 15 * time.Minute

type clusterInvite struct {
	Host    string    `json:"host"`
	Addrs   []string  `json:"addrs"`
	Expires time.Time `json:"expires"`
	// Code es el código de unión que muestra este NAS (lo escribe el otro). Fingerprint es
	// la huella de la clave de este NAS: el otro la muestra al unirlo, para comparar.
	Code        string `json:"code,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// Joined es la huella de la CA del clúster al que se unió por esta invitación.
	Joined string `json:"joined,omitempty"`
	Fails  int    `json:"-"`
}

type clusterAPI struct {
	m  *m4API
	ag clusterAgent

	mu      sync.Mutex
	invites map[string]clusterInvite // host → invitación
	latency map[string]int           // host → ms (-1 sin respuesta)
	alerted map[string]bool          // avisos ya mandados (se limpian al resolverse)
	pending map[string]int           // vueltas seguidas con curación pendiente
	last    string                   // último estado publicado (para cluster.changed)

	// Caché corta del estado del agente: la UI, el bucle y cada método lo piden a la vez
	// y el agente atiende de a uno (con un nodo caído, una lectura puede tardar segundos).
	stMu sync.Mutex
	st   *agentproto.GlusterStatusResult
	stAt time.Time
	// busy cuenta las llamadas al agente en curso que pueden tardar (heal info, status):
	// el cliente del agente atiende de a una, así que mientras tanto se responde con la
	// caché en vez de hacer esperar a la UI.
	busy atomic.Int32

	// heal: último `heal info` por volumen (lo pide el bucle; tarda segundos).
	heal map[string][]gluster.HealBrick

	pki   *clusterPKI             // CA y certificado de este nodo (cluster_ctl.go)
	joins map[string]*clusterJoin // uniones preparadas esperando la confirmación de la huella
	infos map[string]*CtlInfo     // lo que cuenta cada nodo por el canal de control
}

const (
	statusTTL = 3 * time.Second
	staleMax  = 90 * time.Second // máximo de antigüedad para responder con caché mientras el agente está ocupado
)

// agentStatus devuelve el estado del agente: cacheado statusTTL salvo fresh, y
// cacheado (hasta staleMax) si hay otra llamada lenta en curso.
func (c *clusterAPI) agentStatus(ctx context.Context, fresh bool) (*agentproto.GlusterStatusResult, error) {
	c.stMu.Lock()
	st, at := c.st, c.stAt
	c.stMu.Unlock()
	if !fresh && st != nil && (time.Since(at) < statusTTL || c.busy.Load() > 0 && time.Since(at) < staleMax) {
		return st, nil
	}
	c.busy.Add(1)
	st, err := c.ag.GlusterStatus(ctx, c.self())
	c.busy.Add(-1)
	if err != nil {
		return nil, err
	}
	c.stMu.Lock()
	c.st, c.stAt = st, time.Now()
	c.stMu.Unlock()
	return st, nil
}

// ClusterNode es un nodo como lo ve la app (el peer de GlusterFS + latencia).
type ClusterNode struct {
	gluster.Peer
	LatencyMs int `json:"latencyMs"`
}

// ClusterTangoVolume es un volumen de este NAS donde puede ir un brick.
type ClusterTangoVolume struct {
	Name  string `json:"name"`
	Mount string `json:"mount"`
	Free  uint64 `json:"free"`
}

// ClusterStatus es la respuesta de cluster.status.
type ClusterStatus struct {
	Installed    bool                              `json:"installed"`
	Running      bool                              `json:"running"`
	Version      string                            `json:"version,omitempty"`
	Error        string                            `json:"error,omitempty"`
	Self         string                            `json:"self"`
	SelfAddrs    []string                          `json:"selfAddrs"`
	Nodes        []ClusterNode                     `json:"nodes"`
	Volumes      []gluster.Volume                  `json:"volumes"`
	Orphans      []agentproto.GlusterOrphan        `json:"orphans"`
	Mounts       []agentproto.GlusterMountInfo     `json:"mounts"`
	Allowed      []string                          `json:"allowed"`
	Invites      []clusterInvite                   `json:"invites"`
	TangoVolumes []ClusterTangoVolume              `json:"tangoVolumes"`
	Presets      []gluster.Preset                  `json:"presets"`
	Options      []gluster.Option                  `json:"options"`
	TLS          ClusterTLSInfo                    `json:"tls"`
	NodeInfo     map[string]*CtlInfo               `json:"nodeInfo"`
	NFS          map[string][]agentproto.NFSClient `json:"nfs"`
	LVBricks     []agentproto.GlusterLVBrick       `json:"lvBricks"`
	Pools        []CtlPool                         `json:"pools"`
}

func (m *m4API) registerCluster(r *rtcrpc.Router) {
	ag, ok := m.d.Agent.(clusterAgent)
	if !ok {
		return
	}
	c := &clusterAPI{m: m, ag: ag, invites: map[string]clusterInvite{}, latency: map[string]int{}, alerted: map[string]bool{}, pending: map[string]int{}, heal: map[string][]gluster.HealBrick{},
		joins: map[string]*clusterJoin{}, infos: map[string]*CtlInfo{}}
	if m.a.d.StateDir != "" {
		c.pki = newClusterPKI(m.a.d.StateDir)
	}
	m.cluster = c
	a := m.a
	admin := func(fn func(ctx context.Context, p json.RawMessage) (any, error)) rtcrpc.HandlerFunc {
		return func(ctx context.Context, s *rtcrpc.Session, p json.RawMessage) (any, error) {
			if err := a.requireAdmin(s); err != nil {
				return nil, err
			}
			return fn(ctx, p)
		}
	}
	decode := func(p json.RawMessage, v any) error {
		dec := json.NewDecoder(strings.NewReader(string(p)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "parámetros inválidos: %v", err)
		}
		return nil
	}

	r.Handle("cluster.status", admin(func(ctx context.Context, _ json.RawMessage) (any, error) {
		return c.status(ctx, false)
	}))
	r.Handle("cluster.install", admin(func(ctx context.Context, _ json.RawMessage) (any, error) {
		j := a.d.Jobs.Start("cluster.install", "Instalando GlusterFS", "", func(ctx context.Context, h *jobs.Handle) error {
			defer c.publish(context.WithoutCancel(ctx))
			return m.queued(h, func() error {
				if err := c.ag.GlusterInstall(ctx, agentproto.GlusterInstallArgs{OpID: h.ID(), Allowed: c.desiredAllowed(nil)}); err != nil {
					return agentErr(err)
				}
				return m.follow(ctx, h)
			})
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))

	// Invitar: este nodo acepta que el otro le hable (la mitad de la confirmación).
	r.Handle("cluster.invite", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Host string `json:"host"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		inv, err := c.invite(ctx, q.Host)
		if err != nil {
			return nil, err
		}
		if c.pki != nil {
			inv.Code = clusterpki.NewJoinCode()
			inv.Fingerprint = c.pki.nodeFingerprint()
			c.mu.Lock()
			c.invites[q.Host] = inv
			c.mu.Unlock()
		}
		return inv, c.syncFirewall(ctx, nil)
	}))
	r.Handle("cluster.invite.cancel", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Host string `json:"host"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		c.mu.Lock()
		delete(c.invites, q.Host)
		c.mu.Unlock()
		return struct{}{}, c.syncFirewall(ctx, nil)
	}))

	// Agregar un nodo: cluster.peer.prepare (código → huella) y cluster.peer.confirm
	// (cluster2.go). No hay forma de sumar un nodo sin el código que muestra el invitado.

	r.Handle("cluster.peer.remove", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Host string `json:"host"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		// El CN del nodo que sale (si responde) para sacarlo de auth.ssl-allow.
		drop := ""
		if c.pki != nil && c.pki.signed() {
			if info, err := c.nodeInfo(ctx, q.Host); err == nil {
				drop = info.CN
			}
		}
		if err := c.ag.GlusterPeer(ctx, agentproto.GlusterPeerArgs{Action: "detach", Host: q.Host}); err != nil {
			return nil, agentErr(err)
		}
		c.mu.Lock()
		delete(c.invites, q.Host)
		delete(c.infos, q.Host)
		c.mu.Unlock()
		err := c.syncFirewall(ctx, nil)
		if c.pki != nil && c.pki.signed() {
			if e := c.syncSSLAllow(ctx, nil, drop); e != nil && err == nil {
				err = e
			}
		}
		c.publish(ctx)
		return struct{}{}, err
	}))

	r.Handle("cluster.volume.create", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Name    string             `json:"name"`
			Replica int                `json:"replica"`
			Arbiter bool               `json:"arbiter"`
			Preset  string             `json:"preset"`
			Options map[string]string  `json:"options"`
			Bricks  []ClusterBrickSpec `json:"bricks"`
			Start   bool               `json:"start"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		opts := map[string]string{}
		if q.Preset != "" {
			pr, ok := gluster.PresetByID(q.Preset)
			if !ok {
				return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "preset desconocido: %q", q.Preset)
			}
			for k, v := range pr.Options {
				opts[k] = v
			}
		}
		for k, v := range q.Options {
			opts[k] = v
		}
		args := agentproto.GlusterVolumeCreateArgs{Name: q.Name, Layout: gluster.Layout{Replica: q.Replica, Arbiter: q.Arbiter}, Options: opts}
		// Validación previa con rutas provisorias (los LV dedicados todavía no existen).
		var draft []gluster.Brick
		for _, b := range q.Bricks {
			path := gluster.BrickPath(b.TangoVolume, q.Name)
			if b.Kind == "lv" {
				path = gluster.LVBrickPath(q.Name)
				if b.Pool == "" || b.Size == 0 {
					return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "brick dedicado en %s: falta el grupo o el tamaño", b.Host)
				}
			}
			draft = append(draft, gluster.Brick{Host: b.Host, Path: path})
		}
		if err := gluster.ValidateCreate(args.Name, args.Layout, draft); err != nil {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%s", err.Error())
		}
		if err := gluster.ValidOptions(opts); err != nil {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%s", err.Error())
		}
		// Un nodo recién unido pasa unos segundos en "Accepted peer request": GlusterFS rechaza
		// crear volúmenes hasta que todos están en "Peer in Cluster".
		if st, err := c.agentStatus(ctx, true); err == nil {
			for _, p := range st.Peers {
				if !p.Self && p.State != "" && p.State != "Peer in Cluster" {
					return nil, rtcrpc.Errorf("PEER_JOINING", "%s todavía se está uniendo al clúster (%s): probá en unos segundos", p.Host, p.State)
				}
			}
		}
		j := a.d.Jobs.Start("cluster.volume.create", "Creando el volumen "+q.Name, args.Layout.TypeName(len(args.Bricks)), func(ctx context.Context, h *jobs.Handle) error {
			defer c.publish(context.WithoutCancel(ctx))
			bricks, undo, err := c.resolveBricks(ctx, h, q.Name, q.Bricks)
			if err != nil {
				return err
			}
			args.Bricks = bricks
			h.Update(0.5, "Creando el volumen en el clúster")
			if err := c.ag.GlusterVolumeCreate(ctx, args); err != nil {
				undo()
				return agentErr(err)
			}
			// Con TLS en el clúster, el volumen nace con TLS de E/S y solo para sus nodos.
			if st, err := c.agentStatus(ctx, true); err == nil && st.TLS.Secure {
				h.Update(0.75, "Aplicando TLS al volumen")
				if err := c.syncSSLAllowNew(ctx, q.Name); err != nil {
					return err
				}
			}
			if q.Start {
				h.Update(0.85, "Arrancando el volumen")
				if err := c.ag.GlusterVolumeAction(ctx, agentproto.GlusterVolumeActionArgs{Name: q.Name, Action: "start"}); err != nil {
					return agentErr(err)
				}
			}
			return nil
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))

	r.Handle("cluster.volume.action", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q agentproto.GlusterVolumeActionArgs
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		if err := c.ag.GlusterVolumeAction(ctx, q); err != nil {
			return nil, agentErr(err)
		}
		c.publish(ctx)
		return struct{}{}, nil
	}))
	r.Handle("cluster.volume.options", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q agentproto.GlusterVolumeOptionsArgs
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		if err := c.ag.GlusterVolumeOptions(ctx, q); err != nil {
			return nil, agentErr(err)
		}
		c.publish(ctx)
		return struct{}{}, nil
	}))
	r.Handle("cluster.volume.heal", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q agentproto.GlusterHealArgs
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		if err := c.ag.GlusterHeal(ctx, q); err != nil {
			// Justo después de que vuelve un nodo, su demonio de curación todavía no arrancó
			// y GlusterFS rechaza el pedido manual; la curación automática sigue igual.
			if strings.Contains(err.Error(), "has been unsuccessful") {
				return nil, rtcrpc.Errorf("HEAL_NOT_READY", "el demonio de curación de algún nodo todavía está arrancando: probá en unos segundos (la curación automática no se detiene)")
			}
			return nil, agentErr(err)
		}
		go func() { // la curación arrancó: refrescar lo pendiente en unos segundos
			time.Sleep(3 * time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			c.refreshHeal(ctx, q.Name)
			c.publish(ctx)
		}()
		return struct{}{}, nil
	}))
	r.Handle("cluster.volume.bricks", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q agentproto.GlusterBricksArgs
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		if err := c.ag.GlusterBricks(ctx, q); err != nil {
			return nil, agentErr(err)
		}
		c.publish(ctx)
		return struct{}{}, nil
	}))
	r.Handle("cluster.volume.info", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Name string `json:"name"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		st, err := c.agentStatus(ctx, false)
		if err != nil {
			return nil, agentErr(err)
		}
		for _, v := range st.Volumes {
			if v.Name == q.Name {
				return map[string]string{"text": gluster.InfoText(v)}, nil
			}
		}
		return nil, rtcrpc.Errorf("CLUSTER_VOLUME_NOT_FOUND", "no existe el volumen %s", q.Name)
	}))

	// Montar en /cluster/<vol> y, opcional, compartirlo por SMB.
	r.Handle("cluster.volume.mount", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Name    string   `json:"name"`
			Enabled bool     `json:"enabled"`
			Share   string   `json:"share"`
			Comment string   `json:"comment"`
			RW      []string `json:"rw"`
			RO      []string `json:"ro"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		if q.Share != "" {
			for _, sh := range a.listShares(ctx) {
				if strings.EqualFold(sh.Name, q.Share) {
					return nil, rtcrpc.Errorf("SHARE_EXISTS", "ya hay una carpeta compartida llamada %s", sh.Name)
				}
			}
		}
		args := agentproto.GlusterMountArgs{Name: q.Name, Enabled: q.Enabled, Share: q.Share, Comment: q.Comment, RW: q.RW, RO: q.RO}
		if q.Enabled {
			args.Backups = c.backupServers(ctx)
		}
		title := "Montando " + q.Name
		if !q.Enabled {
			title = "Desmontando " + q.Name
		}
		j := a.d.Jobs.Start("cluster.volume.mount", title, "/cluster/"+q.Name, func(ctx context.Context, h *jobs.Handle) error {
			defer c.publish(context.WithoutCancel(ctx))
			if err := c.ag.GlusterMount(ctx, args); err != nil {
				return agentErr(err)
			}
			return nil
		}, nil)
		return map[string]string{"job": j.ID}, nil
	}))
	r.Handle("cluster.brick.cleanup", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q struct {
			Kind        string `json:"kind"` // "" (carpeta) | lv (TangoVolume = grupo)
			TangoVolume string `json:"tangoVolume"`
			Name        string `json:"name"`
		}
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		if q.Kind == "lv" {
			ag2, err := c.ag2()
			if err != nil {
				return nil, err
			}
			if _, err := ag2.GlusterBrickLV(ctx, agentproto.GlusterBrickLVArgs{Op: "delete", Pool: q.TangoVolume, Vol: q.Name, Self: c.selfNames()}); err != nil {
				return nil, agentErr(err)
			}
		} else if err := c.ag.GlusterBrickCleanup(ctx, agentproto.GlusterBrickCleanArgs{TangoVolume: strings.TrimPrefix(q.TangoVolume, "/"), Name: q.Name}); err != nil {
			return nil, agentErr(err)
		}
		c.publish(ctx)
		return struct{}{}, nil
	}))

	c.register2(r, admin, decode)
	if c.pki != nil && m.d.ClusterCtlAddr != "" {
		go c.serveCtl(m.d.ClusterCtlAddr)
	}
	go c.loop()
}

func (c *clusterAPI) self() string {
	h, _ := os.Hostname()
	return strings.ToLower(h)
}

// listShares es la lista de carpetas compartidas (para no repetir nombres).
func (a *API) listShares(ctx context.Context) []Share {
	if a.storage == nil {
		return nil
	}
	return a.storage.shares(ctx)
}

// resolve devuelve las IP de un nodo (una IP se devuelve tal cual).
func resolve(ctx context.Context, host string) ([]string, error) {
	if err := gluster.ValidHost(host); err != nil {
		return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%s", err.Error())
	}
	if ip := net.ParseIP(host); ip != nil {
		return []string{ip.String()}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "no se encontró %s en la red (usá su IP o agregalo a /etc/hosts)", host)
	}
	var out []string
	for _, ip := range ips {
		if !ip.IP.IsLoopback() {
			out = append(out, ip.IP.String())
		}
	}
	return out, nil
}

func (c *clusterAPI) invite(ctx context.Context, host string) (clusterInvite, error) {
	addrs, err := resolve(ctx, host)
	if err != nil {
		return clusterInvite{}, err
	}
	inv := clusterInvite{Host: host, Addrs: addrs, Expires: time.Now().Add(InviteTTL).UTC()}
	c.mu.Lock()
	c.invites[host] = inv
	c.mu.Unlock()
	return inv, nil
}

// desiredAllowed: invitaciones vigentes + nodos del clúster (resueltos).
func (c *clusterAPI) desiredAllowed(st *agentproto.GlusterStatusResult) []string {
	set := map[string]bool{}
	now := time.Now()
	c.mu.Lock()
	for h, inv := range c.invites {
		if now.After(inv.Expires) {
			delete(c.invites, h)
			continue
		}
		for _, a := range inv.Addrs {
			set[a] = true
		}
	}
	c.mu.Unlock()
	if st != nil {
		for _, p := range st.Peers {
			if p.Self {
				continue
			}
			for _, h := range append([]string{p.Host}, p.Hostnames...) {
				if addrs, err := resolve(context.Background(), h); err == nil {
					for _, a := range addrs {
						set[a] = true
					}
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	v4, v6 := gluster.SplitAddrs(out)
	return append(v4, v6...)
}

// syncFirewall lleva la allow-list del agente a la deseada (si GlusterFS ya está).
func (c *clusterAPI) syncFirewall(ctx context.Context, st *agentproto.GlusterStatusResult) error {
	if st == nil {
		var err error
		if st, err = c.agentStatus(ctx, true); err != nil {
			return agentErr(err)
		}
	}
	if !st.Installed {
		return nil
	}
	want := c.desiredAllowed(st)
	have := append([]string{}, st.Allowed...)
	v4, v6 := gluster.SplitAddrs(have)
	if slices.Equal(want, append(v4, v6...)) {
		return nil
	}
	if err := c.ag.GlusterFirewall(ctx, want); err != nil {
		return agentErr(err)
	}
	return nil
}

func (c *clusterAPI) backupServers(ctx context.Context) []string {
	st, err := c.agentStatus(ctx, false)
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range st.Peers {
		if !p.Self {
			out = append(out, p.Host)
		}
	}
	sort.Strings(out)
	return out
}

func (c *clusterAPI) status(ctx context.Context, fresh bool) (*ClusterStatus, error) {
	st, err := c.agentStatus(ctx, fresh)
	if err != nil {
		return nil, agentErr(err)
	}
	out := &ClusterStatus{Installed: st.Installed, Running: st.Running, Version: st.Version, Error: st.Error, Self: c.self(),
		SelfAddrs: localAddrs(), Volumes: c.withHeal(st.Volumes), Orphans: append(append([]agentproto.GlusterOrphan{}, st.Orphans...), c.lvOrphans(st)...), Mounts: st.Mounts, Allowed: st.Allowed,
		Invites: []clusterInvite{}, Nodes: []ClusterNode{}, TangoVolumes: []ClusterTangoVolume{},
		Presets: gluster.Presets(), Options: gluster.Options(), TLS: c.tlsInfo(st), NodeInfo: map[string]*CtlInfo{},
		NFS: map[string][]agentproto.NFSClient{}, LVBricks: st.LVBricks, Pools: c.localPools(ctx)}
	if out.LVBricks == nil {
		out.LVBricks = []agentproto.GlusterLVBrick{}
	}
	c.m.mu.Lock()
	for k, v := range c.m.cfg.NFS.Cluster {
		out.NFS[k] = v
	}
	c.m.mu.Unlock()
	c.mu.Lock()
	now := time.Now()
	for _, inv := range c.invites {
		if now.Before(inv.Expires) {
			out.Invites = append(out.Invites, inv)
		}
	}
	for _, p := range st.Peers {
		lat, ok := c.latency[p.Host]
		if p.Self {
			lat = 0
		} else if !ok {
			lat = -1
		}
		out.Nodes = append(out.Nodes, ClusterNode{Peer: p, LatencyMs: lat})
	}
	c.mu.Unlock()
	sort.Slice(out.Invites, func(i, j int) bool { return out.Invites[i].Host < out.Invites[j].Host })
	c.mu.Lock()
	for k, v := range c.infos {
		out.NodeInfo[k] = v
	}
	c.mu.Unlock()
	if inv := c.m.a.d.Inventory; inv != nil {
		if vols, err := inv.Volumes(ctx); err == nil {
			for _, v := range vols {
				if v.Mount != "" {
					out.TangoVolumes = append(out.TangoVolumes, ClusterTangoVolume{Name: v.Name, Mount: v.Mount, Free: v.Size - v.Used})
				}
			}
		}
	}
	return out, nil
}

// withHeal copia los volúmenes y les suma la última curación conocida.
func (c *clusterAPI) withHeal(vols []gluster.Volume) []gluster.Volume {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]gluster.Volume, len(vols))
	for i, v := range vols {
		v.Bricks = slices.Clone(v.Bricks)
		if h, ok := c.heal[v.Name]; ok && v.Status == "Started" {
			gluster.MergeHeal(&v, h)
		}
		out[i] = v
	}
	return out
}

// refreshHeal pide `heal info` de un volumen al agente y lo guarda.
func (c *clusterAPI) refreshHeal(ctx context.Context, vol string) {
	c.busy.Add(1)
	h, err := c.ag.GlusterHealInfo(ctx, vol)
	c.busy.Add(-1)
	if err != nil {
		c.m.a.d.Log.Warn("cluster: heal info", "vol", vol, "err", err)
		return
	}
	c.mu.Lock()
	c.heal[vol] = h
	c.mu.Unlock()
}

func localAddrs() []string {
	var out []string
	ifs, _ := net.InterfaceAddrs()
	for _, a := range ifs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			out = append(out, n.IP.String())
		}
	}
	return out
}

// publish manda cluster.changed si el estado cambió.
func (c *clusterAPI) publish(ctx context.Context) {
	st, err := c.status(ctx, true)
	if err != nil {
		return
	}
	b, _ := json.Marshal(st)
	c.mu.Lock()
	changed := string(b) != c.last
	c.last = string(b)
	c.mu.Unlock()
	if changed {
		c.m.a.d.Hub.Publish("cluster.changed", st)
	}
}

// loop: firewall al día, latencia y avisos. Solo trabaja si GlusterFS está instalado.
func (c *clusterAPI) loop() {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for range t.C {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		c.tick(ctx)
		cancel()
	}
}

func (c *clusterAPI) tick(ctx context.Context) {
	st, err := c.agentStatus(ctx, true)
	if err != nil || !st.Installed {
		return
	}
	if err := c.syncFirewall(ctx, st); err != nil {
		c.m.a.d.Log.Warn("cluster: firewall", "err", err)
	}
	if !st.Running {
		return
	}
	lat := map[string]int{}
	for _, p := range st.Peers {
		if p.Self {
			continue
		}
		start := time.Now()
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(p.Host, strconv.Itoa(gluster.PortGlusterd)), 2*time.Second)
		if err != nil {
			lat[p.Host] = -1
			continue
		}
		_ = conn.Close()
		lat[p.Host] = int(time.Since(start).Milliseconds())
	}
	c.mu.Lock()
	c.latency = lat
	c.mu.Unlock()
	known := map[string]bool{}
	for _, v := range st.Volumes {
		known[v.Name] = true
		if v.Status == "Started" && v.Replica > 1 {
			c.refreshHeal(ctx, v.Name)
		}
	}
	c.mu.Lock()
	for name := range c.heal {
		if !known[name] {
			delete(c.heal, name)
		}
	}
	c.mu.Unlock()
	c.refreshNodeInfo(ctx, st)
	withHeal := *st
	withHeal.Volumes = c.withHeal(st.Volumes)
	c.alerts(&withHeal)
	c.publish(ctx)
}

// alerts avisa una vez por problema y vuelve a avisar si se repite después de resolverse.
func (c *clusterAPI) alerts(st *agentproto.GlusterStatusResult) {
	notify := func(key, level, title, body string, active bool) {
		c.mu.Lock()
		was := c.alerted[key]
		c.alerted[key] = active
		c.mu.Unlock()
		if active && !was {
			c.m.a.Notify(Notice{Level: level, App: "tango.cluster", Title: title, Body: body,
				Action: &Action{Label: "Abrir Clúster", App: "tango.cluster"}})
		}
	}
	for _, p := range st.Peers {
		if !p.Self {
			notify("peer:"+p.Host, "warning", "Nodo del clúster sin conexión", fmt.Sprintf("%s no responde. Los volúmenes siguen andando mientras haya quórum.", p.Host), !p.Connected)
		}
	}
	for _, v := range st.Volumes {
		notify("split:"+v.Name, "error", "Split-brain en "+v.Name, fmt.Sprintf("%d archivos tienen versiones distintas en cada copia y GlusterFS no puede elegir sola. Revisalos en la app Clúster.", v.SplitBrain), v.SplitBrain > 0)
		c.mu.Lock()
		if v.HealPending > 0 {
			c.pending[v.Name]++
		} else {
			c.pending[v.Name] = 0
		}
		long := c.pending[v.Name] >= 9 // ~3 minutos seguidos
		c.mu.Unlock()
		notify("heal:"+v.Name, "warning", "Curación pendiente en "+v.Name, fmt.Sprintf("%d entradas siguen sin copiarse a todas las réplicas.", v.HealPending), long)
		offline := len(v.Bricks) - v.Online
		notify("bricks:"+v.Name, "warning", "Bricks sin conexión en "+v.Name, fmt.Sprintf("%d de %d bricks no responden.", offline, len(v.Bricks)), v.Status == "Started" && offline > 0)
	}
}
