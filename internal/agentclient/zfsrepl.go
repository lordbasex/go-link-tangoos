package agentclient

import (
	"context"
	"os"
	"time"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Replicación ZFS (protocol/README.md §18.1, ADR 0016).

func (c *Client) ZfsReplState(ctx context.Context, a agentproto.ZfsReplStateArgs) (*agentproto.ZfsReplState, error) {
	var st agentproto.ZfsReplState
	if err := c.CallOnce(ctx, agentproto.OpZfsReplState, a, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (c *Client) ZfsReplSnapshot(ctx context.Context, a agentproto.ZfsReplSnapshotArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsReplSnapshot, a, nil)
}

// ZfsReplSend devuelve el extremo de lectura del zfs send.
func (c *Client) ZfsReplSend(ctx context.Context, a agentproto.ZfsReplSendArgs) (*os.File, agentproto.ZfsStreamResult, error) {
	var res agentproto.ZfsStreamResult
	f, err := c.CallFD(ctx, agentproto.OpZfsReplSend, a, &res)
	return f, res, err
}

// ZfsReplRecv devuelve el extremo de escritura del zfs receive.
func (c *Client) ZfsReplRecv(ctx context.Context, a agentproto.ZfsReplRecvArgs) (*os.File, agentproto.ZfsStreamResult, error) {
	var res agentproto.ZfsStreamResult
	f, err := c.CallFD(ctx, agentproto.OpZfsReplRecv, a, &res)
	return f, res, err
}

// ZfsReplWait espera a que termine el proceso de un stream. Usa una conexión propia
// (la compartida quedaría tomada mientras dura el envío) y pregunta de a 20 s.
func (c *Client) ZfsReplWait(ctx context.Context, id string) error {
	own := New(c.socket)
	defer own.Close()
	for {
		var res struct {
			Done bool `json:"done"`
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := own.CallOnce(cctx, agentproto.OpZfsReplWait, agentproto.ZfsReplWaitArgs{ID: id}, &res)
		cancel()
		if err != nil || res.Done {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (c *Client) ZfsReplPrune(ctx context.Context, a agentproto.ZfsReplPruneArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsReplPrune, a, nil)
}

func (c *Client) ZfsReplMirror(ctx context.Context, a agentproto.ZfsReplMirrorArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsReplMirror, a, nil)
}

func (c *Client) ZfsReplAbort(ctx context.Context, a agentproto.ZfsReplTargetArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsReplAbort, a, nil)
}

func (c *Client) ZfsReplDestroy(ctx context.Context, a agentproto.ZfsReplTargetArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsReplDestroy, a, nil)
}

// ZfsReplPromote convierte una réplica en volumen; locked: quedó cifrado sin la clave.
func (c *Client) ZfsReplPromote(ctx context.Context, a agentproto.ZfsReplPromoteArgs) (bool, error) {
	var res struct {
		Locked bool `json:"locked"`
	}
	err := c.CallOnce(ctx, agentproto.OpZfsReplPromote, a, &res)
	return res.Locked, err
}
