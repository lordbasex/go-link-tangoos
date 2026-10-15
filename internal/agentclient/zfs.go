package agentclient

import (
	"context"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// ---- ZFS (protocol/README.md §18) ------------------------------------------------------
//
// Las asíncronas (enable, pool.*, volume.create/delete) se siguen con JobStatus.

// ZfsReport lee pools, vdevs y datasets; importable=true además busca pools sin importar.
func (c *Client) ZfsReport(ctx context.Context, importable bool) (*agentproto.ZfsReport, error) {
	var r agentproto.ZfsReport
	args := struct {
		Importable bool `json:"importable"`
	}{importable}
	if err := c.Call(ctx, agentproto.OpZfsReport, args, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) ZfsEnable(ctx context.Context, opID string) error {
	return c.CallOnce(ctx, agentproto.OpZfsEnable, agentproto.ZfsEnableArgs{OpID: opID}, nil)
}

func (c *Client) ZfsPoolCreate(ctx context.Context, a agentproto.ZfsPoolCreateArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsCreate, a, nil)
}

func (c *Client) ZfsPoolAdd(ctx context.Context, a agentproto.ZfsPoolAddArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsAdd, a, nil)
}

func (c *Client) ZfsAux(ctx context.Context, a agentproto.ZfsAuxArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsAux, a, nil)
}

func (c *Client) ZfsReplace(ctx context.Context, a agentproto.ZfsReplaceArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsReplace, a, nil)
}

func (c *Client) ZfsScrub(ctx context.Context, a agentproto.ZfsScrubArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsScrub, a, nil)
}

func (c *Client) ZfsImport(ctx context.Context, a agentproto.ZfsImportArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsImport, a, nil)
}

func (c *Client) ZfsExport(ctx context.Context, a agentproto.ZfsPoolArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsExport, a, nil)
}

func (c *Client) ZfsDestroy(ctx context.Context, a agentproto.ZfsPoolArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsDestroy, a, nil)
}

func (c *Client) ZfsVolCreate(ctx context.Context, a agentproto.ZfsVolCreateArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsVolCreate, a, nil)
}

func (c *Client) ZfsVolSet(ctx context.Context, a agentproto.ZfsVolSetArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsVolSet, a, nil)
}

func (c *Client) ZfsVolDelete(ctx context.Context, a agentproto.ZfsVolArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsVolDelete, a, nil)
}

func (c *Client) ZfsUnlock(ctx context.Context, a agentproto.ZfsUnlockArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsUnlock, a, nil)
}

func (c *Client) ZfsLock(ctx context.Context, a agentproto.ZfsVolArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsLock, a, nil)
}

func (c *Client) ZfsPassphrase(ctx context.Context, a agentproto.ZfsPassphraseArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsPassphrase, a, nil)
}

func (c *Client) ZfsZvolCreate(ctx context.Context, a agentproto.ZfsZvolArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsZvolCreate, a, nil)
}

func (c *Client) ZfsZvolDelete(ctx context.Context, a agentproto.ZfsZvolArgs) error {
	return c.CallOnce(ctx, agentproto.OpZfsZvolDelete, a, nil)
}
