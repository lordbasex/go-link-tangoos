package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Cuotas por carpeta con simple-quota (docs/adr/0015-gluster-simple-quota.md).
//
// Poner un límite en una carpeta vacía es inmediato. Si ya tiene contenido, un job lo
// cuenta: nodo por nodo, cada uno reinicia sus bricks del volumen (lo que tenían abierto
// adentro pasa a contar para la carpeta) y deja en disco lo que ocupa la carpeta en ellos;
// en réplica se espera a que termine la curación antes del siguiente. Al final cada brick
// vuelve a leer su uso (reset). Al volver a prender las cuotas se cuenta de nuevo, sin
// reiniciar (lo escrito mientras estaban apagadas no se contó).

func (c *clusterAPI) quotaRoutes(r *rtcrpc.Router, admin func(fn func(ctx context.Context, p json.RawMessage) (any, error)) rtcrpc.HandlerFunc,
	decode func(p json.RawMessage, v any) error) {
	r.Handle("cluster.volume.quota", admin(func(ctx context.Context, p json.RawMessage) (any, error) {
		var q agentproto.GlusterQuotaArgs
		if err := decode(p, &q); err != nil {
			return nil, err
		}
		switch q.Op {
		case "enable", "disable", "limit", "remove", "list":
		default:
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "operación de cuota inválida: %q", q.Op)
		}
		q.Restart = false
		ag2, err := c.ag2()
		if err != nil {
			return nil, err
		}
		if q.Op == "limit" {
			if _, err := c.quotaNodes(ctx, q.Name); err != nil {
				return nil, err
			}
		}
		res, err := ag2.GlusterQuota(ctx, q)
		if err != nil {
			return nil, agentErr(err)
		}
		out := map[string]any{"quotas": res.Quotas}
		if res.Quotas == nil {
			out["quotas"] = []gluster.Quota{}
		}
		switch {
		case q.Op == "limit" && res.NonEmpty:
			out["job"] = c.startQuotaCount(q.Name, q.Path, true)
		case q.Op == "enable" && len(res.Quotas) > 0:
			out["job"] = c.startQuotaCount(q.Name, "", false)
		}
		if q.Op != "list" {
			c.publish(ctx)
		}
		return out, nil
	}))
}

// quotaNodes son los nodos con bricks del volumen; todos tienen que estar conectados
// (cada uno cuenta lo suyo).
func (c *clusterAPI) quotaNodes(ctx context.Context, vol string) ([]gluster.Peer, error) {
	st, err := c.agentStatus(ctx, true)
	if err != nil {
		return nil, agentErr(err)
	}
	var v *gluster.Volume
	for i := range st.Volumes {
		if st.Volumes[i].Name == vol {
			v = &st.Volumes[i]
		}
	}
	if v == nil {
		return nil, rtcrpc.Errorf("NOT_FOUND", "no existe el volumen %s", vol)
	}
	if v.Status != "Started" {
		return nil, rtcrpc.Errorf("VOLUME_STOPPED", "el volumen %s está detenido: arrancalo para manejar sus cuotas", vol)
	}
	var out []gluster.Peer
	seen := map[string]bool{}
	for _, b := range v.Bricks {
		if seen[b.HostUUID] {
			continue
		}
		seen[b.HostUUID] = true
		found := false
		for _, p := range st.Peers {
			if p.UUID == b.HostUUID {
				found = true
				if !p.Connected && !p.Self {
					return nil, rtcrpc.Errorf("PEER_DISCONNECTED", "%s no está conectado: hace falta para contar lo que ocupa en sus bricks", p.Host)
				}
				out = append(out, p)
			}
		}
		if !found {
			return nil, rtcrpc.Errorf("PEER_DISCONNECTED", "un brick de %s está en un nodo desconocido (%s)", vol, b.Host)
		}
	}
	return out, nil
}

func (c *clusterAPI) startQuotaCount(vol, path string, restart bool) string {
	title, detail := "Contando lo que ya ocupa "+path+" en "+vol, "Los bricks reinician de a un nodo"
	if path == "" {
		title, detail = "Contando el uso de las carpetas con límite de "+vol, ""
	}
	j := c.m.a.d.Jobs.Start("cluster.quota.count", title, detail, func(ctx context.Context, h *jobs.Handle) error {
		defer c.publish(context.WithoutCancel(ctx))
		return c.quotaCount(ctx, h, vol, path, restart)
	}, nil)
	return j.ID
}

func (c *clusterAPI) quotaCount(ctx context.Context, h *jobs.Handle, vol, path string, restart bool) error {
	nodes, err := c.quotaNodes(ctx, vol)
	if err != nil {
		return err
	}
	ag2, err := c.ag2()
	if err != nil {
		return err
	}
	q := agentproto.GlusterQuotaArgs{Name: vol, Op: "count", Path: path, Restart: restart}
	for i, p := range nodes {
		h.Update(float64(i)/float64(len(nodes)+1), "Contando en "+p.Host)
		if p.Self {
			_, err = ag2.GlusterQuota(ctx, q)
		} else {
			err = c.ctlCall(ctx, p.Host, "POST", "/v1/quota/count", q, nil)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", p.Host, err)
		}
		if restart && i < len(nodes)-1 {
			h.Update(float64(i+1)/float64(len(nodes)+1), "Esperando la curación de "+vol)
			if err := c.waitHealed(ctx, vol, 5*time.Minute); err != nil {
				return err
			}
		}
	}
	h.Update(float64(len(nodes))/float64(len(nodes)+1), "Aplicando el uso contado")
	_, err = ag2.GlusterQuota(ctx, agentproto.GlusterQuotaArgs{Name: vol, Op: "reset", Path: path})
	return err
}

// waitHealed espera a que ningún brick del volumen tenga entradas por curar.
func (c *clusterAPI) waitHealed(ctx context.Context, vol string, max time.Duration) error {
	deadline := time.Now().Add(max)
	for {
		hb, err := c.ag.GlusterHealInfo(ctx, vol)
		if err == nil {
			ok := len(hb) > 0
			for _, b := range hb {
				if !b.Connected || b.Pending > 0 || b.SplitBrain > 0 {
					ok = false
				}
			}
			if ok {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("la curación de %s no terminó: revisá el volumen y volvé a poner el límite", vol)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// ctlQuotaRoutes: otro nodo pide contar en los bricks de este.
func (c *clusterAPI) ctlQuotaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/quota/count", c.ctlAuth(func(w http.ResponseWriter, r *http.Request) {
		var q agentproto.GlusterQuotaArgs
		if !ctlDecode(w, r, &q) {
			return
		}
		if q.Op != "count" {
			http.Error(w, "pedido inválido", http.StatusBadRequest)
			return
		}
		ag2, err := c.ag2()
		if err == nil {
			_, err = ag2.GlusterQuota(r.Context(), q)
		}
		ctlReply(w, struct{}{}, err)
	}))
}

// syncQuotas lista las cuotas de los volúmenes que las aplican: así cada brick guarda su
// uso en disco y, en los distribuidos, recibe el total. Lo hace un solo nodo: el de UUID
// menor entre los conectados (cada nodo se ve a sí mismo como localhost).
func (c *clusterAPI) syncQuotas(ctx context.Context, st *agentproto.GlusterStatusResult) {
	first, self := "", ""
	for _, p := range st.Peers {
		if (p.Connected || p.Self) && (first == "" || p.UUID < first) {
			first = p.UUID
		}
		if p.Self {
			self = p.UUID
		}
	}
	ag2, err := c.ag2()
	if self == "" || self != first || err != nil {
		return
	}
	for _, v := range st.Volumes {
		if v.Quota && v.Status == "Started" {
			if _, err := ag2.GlusterQuota(ctx, agentproto.GlusterQuotaArgs{Name: v.Name, Op: "list"}); err != nil {
				c.m.a.d.Log.Warn("cluster: cuotas", "vol", v.Name, "err", err)
			}
		}
	}
}
