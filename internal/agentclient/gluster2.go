package agentclient

import (
	"context"

	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// ---- Clúster, segunda etapa: TLS, bricks en LV, cuotas, rebalance, instantáneas --------

// GlusterTLS instala los archivos TLS de este nodo y prende o apaga secure-access.
func (c *Client) GlusterTLS(ctx context.Context, a agentproto.GlusterTLSArgs) (*agentproto.GlusterTLSState, error) {
	var r agentproto.GlusterTLSState
	if err := c.CallOnce(ctx, agentproto.OpGlusterTLS, a, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// GlusterVolumeTLS prende o apaga el TLS de E/S de un volumen.
func (c *Client) GlusterVolumeTLS(ctx context.Context, a agentproto.GlusterVolumeTLSArgs) error {
	return c.CallOnce(ctx, agentproto.OpGlusterVolumeTLS, a, nil)
}

// GlusterBrickLV crea o borra el LV de un brick dedicado.
func (c *Client) GlusterBrickLV(ctx context.Context, a agentproto.GlusterBrickLVArgs) (*agentproto.GlusterBrickLVResult, error) {
	var r agentproto.GlusterBrickLVResult
	if err := c.CallOnce(ctx, agentproto.OpGlusterBrickLV, a, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// GlusterQuota cambia o lista cuotas; devuelve los límites que quedan.
func (c *Client) GlusterQuota(ctx context.Context, a agentproto.GlusterQuotaArgs) (*gluster.QuotaResult, error) {
	var r gluster.QuotaResult
	call := c.CallOnce
	if a.Op == "list" {
		call = c.Call
	}
	if err := call(ctx, agentproto.OpGlusterQuota, a, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// GlusterRebalance arranca, consulta o detiene un rebalance.
func (c *Client) GlusterRebalance(ctx context.Context, a agentproto.GlusterRebalanceArgs) (*gluster.Task, error) {
	var r agentproto.GlusterTaskResult
	if err := c.CallOnce(ctx, agentproto.OpGlusterRebalance, a, &r); err != nil {
		return nil, err
	}
	return r.Task, nil
}

// GlusterBricksTask es GlusterBricks con resultado (remove status devuelve el avance).
func (c *Client) GlusterBricksTask(ctx context.Context, a agentproto.GlusterBricksArgs) (*gluster.Task, error) {
	var r agentproto.GlusterTaskResult
	if err := c.CallOnce(ctx, agentproto.OpGlusterBricks, a, &r); err != nil {
		return nil, err
	}
	return r.Task, nil
}

// GlusterSnapshot opera instantáneas (info devuelve la lista).
func (c *Client) GlusterSnapshot(ctx context.Context, a agentproto.GlusterSnapshotArgs) ([]gluster.Snapshot, error) {
	if a.Op != "info" {
		return nil, c.CallOnce(ctx, agentproto.OpGlusterSnapshot, a, nil)
	}
	var r []gluster.Snapshot
	if err := c.Call(ctx, agentproto.OpGlusterSnapshot, a, &r); err != nil {
		return nil, err
	}
	return r, nil
}
