package agentclient

import (
	"context"

	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// ---- Clúster GlusterFS (protocol/README.md §16) --------------------------------------

// GlusterInstall instala GlusterFS con el firewall (asíncrona: seguir con M4Job).
func (c *Client) GlusterInstall(ctx context.Context, a agentproto.GlusterInstallArgs) error {
	return c.CallOnce(ctx, agentproto.OpGlusterInstall, a, nil)
}

// GlusterFirewall reemplaza la allow-list de nodos.
func (c *Client) GlusterFirewall(ctx context.Context, allowed []string) error {
	return c.CallOnce(ctx, agentproto.OpGlusterFirewall, agentproto.GlusterFirewallArgs{Allowed: allowed}, nil)
}

// GlusterStatus lee nodos, volúmenes y montajes.
func (c *Client) GlusterStatus(ctx context.Context, self string) (*agentproto.GlusterStatusResult, error) {
	var r agentproto.GlusterStatusResult
	if err := c.Call(ctx, agentproto.OpGlusterStatus, agentproto.GlusterStatusArgs{Self: self}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// GlusterPeer une o saca un nodo.
func (c *Client) GlusterPeer(ctx context.Context, a agentproto.GlusterPeerArgs) error {
	return c.CallOnce(ctx, agentproto.OpGlusterPeer, a, nil)
}

// GlusterVolumeCreate crea (y opcionalmente arranca) un volumen.
func (c *Client) GlusterVolumeCreate(ctx context.Context, a agentproto.GlusterVolumeCreateArgs) error {
	return c.CallOnce(ctx, agentproto.OpGlusterVolumeCreate, a, nil)
}

// GlusterVolumeAction arranca, detiene o borra un volumen.
func (c *Client) GlusterVolumeAction(ctx context.Context, a agentproto.GlusterVolumeActionArgs) error {
	return c.CallOnce(ctx, agentproto.OpGlusterVolumeAction, a, nil)
}

// GlusterVolumeOptions cambia opciones.
func (c *Client) GlusterVolumeOptions(ctx context.Context, a agentproto.GlusterVolumeOptionsArgs) error {
	return c.CallOnce(ctx, agentproto.OpGlusterVolumeSet, a, nil)
}

// GlusterHeal lanza una curación.
func (c *Client) GlusterHeal(ctx context.Context, a agentproto.GlusterHealArgs) error {
	return c.CallOnce(ctx, agentproto.OpGlusterHeal, a, nil)
}

// GlusterBricks suma, saca o reemplaza bricks.
func (c *Client) GlusterBricks(ctx context.Context, a agentproto.GlusterBricksArgs) error {
	return c.CallOnce(ctx, agentproto.OpGlusterBricks, a, nil)
}

// GlusterMount monta o desmonta un volumen (y su carpeta SMB).
func (c *Client) GlusterMount(ctx context.Context, a agentproto.GlusterMountArgs) error {
	return c.CallOnce(ctx, agentproto.OpGlusterMount, a, nil)
}

// GlusterHealInfo lee lo pendiente de curar y el split-brain de un volumen.
func (c *Client) GlusterHealInfo(ctx context.Context, name string) ([]gluster.HealBrick, error) {
	var r []gluster.HealBrick
	if err := c.Call(ctx, agentproto.OpGlusterHealInfo, agentproto.GlusterHealInfoArgs{Name: name}, &r); err != nil {
		return nil, err
	}
	return r, nil
}

// GlusterBrickCleanup borra un brick huérfano de este nodo.
func (c *Client) GlusterBrickCleanup(ctx context.Context, a agentproto.GlusterBrickCleanArgs) error {
	return c.CallOnce(ctx, agentproto.OpGlusterBrickClean, a, nil)
}
