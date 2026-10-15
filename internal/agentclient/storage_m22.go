package agentclient

import (
	"context"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// ---- Fase M2.2 (protocol/README.md §11) --------------------------------------------

// MDReplace arranca md.replace (asíncrona: seguir con JobStatus).
func (c *Client) MDReplace(ctx context.Context, a agentproto.MDReplaceArgs) error {
	return c.CallOnce(ctx, agentproto.OpMDReplace, a, nil)
}

// MDGrow arranca md.grow (asíncrona: seguir con JobStatus).
func (c *Client) MDGrow(ctx context.Context, a agentproto.MDGrowArgs) error {
	return c.CallOnce(ctx, agentproto.OpMDGrow, a, nil)
}

// SnapshotDelete borra una instantánea de una carpeta compartida.
func (c *Client) SnapshotDelete(ctx context.Context, mount, share, name string) error {
	return c.CallOnce(ctx, agentproto.OpSnapshotDelete, agentproto.SnapshotDeleteArgs{Mount: mount, Share: share, Name: name}, nil)
}

// DiskLocate prende o apaga el LED de identificación de un disco.
func (c *Client) DiskLocate(ctx context.Context, devID string, on bool) error {
	return c.CallOnce(ctx, agentproto.OpDiskLocate, agentproto.DiskLocateArgs{DevID: devID, On: on}, nil)
}
