package agentclient

import (
	"context"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// ---- Grupos de almacenamiento (protocol/README.md §16) -------------------------------

// PoolReport lee los grupos de TangoOS (LVM sobre md).
func (c *Client) PoolReport(ctx context.Context) (*agentproto.PoolReport, error) {
	var r agentproto.PoolReport
	if err := c.Call(ctx, agentproto.OpPoolReport, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// PoolCreate arranca pool.create (asíncrona: seguir con JobStatus).
func (c *Client) PoolCreate(ctx context.Context, a agentproto.PoolCreateArgs) error {
	return c.CallOnce(ctx, agentproto.OpPoolCreate, a, nil)
}

// PoolExpand arranca pool.expand.
func (c *Client) PoolExpand(ctx context.Context, a agentproto.PoolExpandArgs) error {
	return c.CallOnce(ctx, agentproto.OpPoolExpand, a, nil)
}

// PoolReplace arranca pool.replace.
func (c *Client) PoolReplace(ctx context.Context, a agentproto.PoolReplaceArgs) error {
	return c.CallOnce(ctx, agentproto.OpPoolReplace, a, nil)
}

// PoolDelete arranca pool.delete.
func (c *Client) PoolDelete(ctx context.Context, a agentproto.PoolDeleteArgs) error {
	return c.CallOnce(ctx, agentproto.OpPoolDelete, a, nil)
}

// LVCreate arranca lv.create.
func (c *Client) LVCreate(ctx context.Context, a agentproto.LVCreateArgs) error {
	return c.CallOnce(ctx, agentproto.OpLVCreate, a, nil)
}

// LVResize arranca lv.resize.
func (c *Client) LVResize(ctx context.Context, a agentproto.LVResizeArgs) error {
	return c.CallOnce(ctx, agentproto.OpLVResize, a, nil)
}

// LVDelete arranca lv.delete.
func (c *Client) LVDelete(ctx context.Context, a agentproto.LVDeleteArgs) error {
	return c.CallOnce(ctx, agentproto.OpLVDelete, a, nil)
}

// LUKSUnlock abre y monta un volumen cifrado.
func (c *Client) LUKSUnlock(ctx context.Context, a agentproto.LUKSUnlockArgs) error {
	return c.CallOnce(ctx, agentproto.OpLUKSUnlock, a, nil)
}

// LUKSLock desmonta y cierra un volumen cifrado.
func (c *Client) LUKSLock(ctx context.Context, name string) error {
	return c.CallOnce(ctx, agentproto.OpLUKSLock, agentproto.LUKSLockArgs{Name: name}, nil)
}

// LUKSPassphrase cambia la frase de un volumen cifrado.
func (c *Client) LUKSPassphrase(ctx context.Context, a agentproto.LUKSPassphraseArgs) error {
	return c.CallOnce(ctx, agentproto.OpLUKSPassphrase, a, nil)
}

// LUKSKeyDevice guarda o quita el archivo de clave de un volumen.
func (c *Client) LUKSKeyDevice(ctx context.Context, a agentproto.LUKSKeyDeviceArgs) error {
	return c.CallOnce(ctx, agentproto.OpLUKSKeyDevice, a, nil)
}

// KeyDevices lista los dispositivos donde se puede guardar un archivo de clave.
func (c *Client) KeyDevices(ctx context.Context) ([]agentproto.KeyDevice, error) {
	var r agentproto.KeyDevListResult
	if err := c.Call(ctx, agentproto.OpKeyDevList, nil, &r); err != nil {
		return nil, err
	}
	return r.Devices, nil
}

// CacheCreate arranca cache.create.
func (c *Client) CacheCreate(ctx context.Context, a agentproto.CacheCreateArgs) error {
	return c.CallOnce(ctx, agentproto.OpCacheCreate, a, nil)
}

// CacheRemove arranca cache.remove.
func (c *Client) CacheRemove(ctx context.Context, a agentproto.CacheRemoveArgs) error {
	return c.CallOnce(ctx, agentproto.OpCacheRemove, a, nil)
}
