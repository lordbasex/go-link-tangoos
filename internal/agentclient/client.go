// Package agentclient es el cliente tipado de tango-agent que usa tangod (docs/02).
//
// Mantiene una conexión al socket Unix y la reabre si se corta. Las llamadas son
// serializadas (el agente atiende en orden por conexión) y respetan el contexto.
package agentclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// DefaultSocket es la ruta del socket en el sistema instalado.
const DefaultSocket = "/run/tango/agent.sock"

// ErrUnavailable indica que el agente no está corriendo o no acepta la conexión.
var ErrUnavailable = errors.New("tango-agent no disponible")

// Client habla con tango-agent.
type Client struct {
	socket string
	mu     sync.Mutex
	conn   net.Conn
	rd     *bufio.Reader
	nextID uint64
}

// New crea un cliente; la conexión se abre en la primera llamada.
func New(socket string) *Client {
	if socket == "" {
		socket = DefaultSocket
	}
	return &Client{socket: socket}
}

// Close cierra la conexión.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropLocked()
}

func (c *Client) dropLocked() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn, c.rd = nil, nil
	return err
}

func (c *Client) dialLocked(ctx context.Context) error {
	if c.conn != nil {
		return nil
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	c.conn, c.rd = conn, bufio.NewReaderSize(conn, agentproto.MaxLine)
	return nil
}

// Call ejecuta una op y decodifica el resultado en out (puede ser nil).
// Los errores del agente se devuelven como *agentproto.Error.
func (c *Client) Call(ctx context.Context, op string, args, out any) error {
	return c.call(ctx, op, args, out, true)
}

// CallOnce es Call sin reintento: para ops que escriben, donde repetir a ciegas
// podría duplicar la acción (el journal del agente igual rechaza un op_id repetido).
func (c *Client) CallOnce(ctx context.Context, op string, args, out any) error {
	return c.call(ctx, op, args, out, false)
}

func (c *Client) call(ctx context.Context, op string, args, out any, retry bool) error {
	var raw json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return err
		}
		raw = b
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for attempt := 0; ; attempt++ {
		resp, err := c.roundTripLocked(ctx, op, raw)
		if err != nil {
			_ = c.dropLocked()
			// Un reintento si la conexión vieja estaba muerta: solo para ops de lectura.
			if retry && attempt == 0 && ctx.Err() == nil && !errors.Is(err, ErrUnavailable) {
				continue
			}
			return err
		}
		if !resp.OK {
			if resp.Error == nil {
				return agentproto.Errf(agentproto.CodeInternal, "respuesta sin error ni resultado")
			}
			return resp.Error
		}
		if out != nil {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	}
}

func (c *Client) roundTripLocked(ctx context.Context, op string, args json.RawMessage) (*agentproto.Response, error) {
	if err := c.dialLocked(ctx); err != nil {
		return nil, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(35 * time.Second)
	}
	_ = c.conn.SetDeadline(deadline)
	c.nextID++
	req := agentproto.Request{V: agentproto.Version, ID: c.nextID, Op: op, Args: args}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	line, err := c.rd.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp agentproto.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	if resp.ID != req.ID {
		return nil, fmt.Errorf("respuesta fuera de orden: id %d, esperado %d", resp.ID, req.ID)
	}
	return &resp, nil
}

// Version consulta agent.version.
func (c *Client) Version(ctx context.Context) (*agentproto.VersionResult, error) {
	var r agentproto.VersionResult
	if err := c.Call(ctx, agentproto.OpVersion, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// DiskSmart consulta disk.smart para un nombre de /dev/disk/by-id.
func (c *Client) DiskSmart(ctx context.Context, devID string) (*agentproto.DiskSmartResult, error) {
	var r agentproto.DiskSmartResult
	if err := c.Call(ctx, agentproto.OpDiskSmart, agentproto.DiskSmartArgs{DevID: devID}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ---- Almacenamiento (protocol/README.md §7) --------------------------------------

// DiskInspect revisa discos: huella, contenido y elegibilidad.
func (c *Client) DiskInspect(ctx context.Context, devIDs []string) ([]agentproto.DiskInspection, error) {
	var r agentproto.DiskInspectResult
	if err := c.Call(ctx, agentproto.OpDiskInspect, agentproto.DiskInspectArgs{DevIDs: devIDs}, &r); err != nil {
		return nil, err
	}
	return r.Disks, nil
}

// VolumeCreate arranca la creación de un volumen (asíncrona: seguirla con JobStatus).
func (c *Client) VolumeCreate(ctx context.Context, a agentproto.VolumeCreateArgs) error {
	return c.CallOnce(ctx, agentproto.OpVolumeCreate, a, nil)
}

// ScrubStart arranca la verificación de un volumen (asíncrona).
func (c *Client) ScrubStart(ctx context.Context, opID, mount string) error {
	return c.CallOnce(ctx, agentproto.OpScrubStart, agentproto.ScrubStartArgs{OpID: opID, Mount: mount}, nil)
}

// JobStatus consulta una operación asíncrona. El resultado queda como JSON crudo.
func (c *Client) JobStatus(ctx context.Context, opID string) (*JobStatus, error) {
	var r JobStatus
	if err := c.Call(ctx, agentproto.OpJobStatus, agentproto.OpRef{OpID: opID}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// JobStatus es agentproto.JobStatus con el resultado sin decodificar.
type JobStatus struct {
	OpID     string            `json:"op_id"`
	Op       string            `json:"op"`
	State    string            `json:"state"`
	Progress float64           `json:"progress"`
	Stage    string            `json:"stage"`
	Error    *agentproto.Error `json:"error,omitempty"`
	Result   json.RawMessage   `json:"result,omitempty"`
}

// SubvolCreate crea un subvolumen bajo un volumen montado.
func (c *Client) SubvolCreate(ctx context.Context, mount, path string) error {
	return c.CallOnce(ctx, agentproto.OpSubvolCreate, agentproto.SubvolCreateArgs{Mount: mount, Path: path}, nil)
}

// SnapshotCreate crea una instantánea read-only de una carpeta compartida.
func (c *Client) SnapshotCreate(ctx context.Context, mount, share, name string) error {
	return c.CallOnce(ctx, agentproto.OpSnapshotCreate, agentproto.SnapshotCreateArgs{Mount: mount, Share: share, Name: name}, nil)
}

// SambaApply publica una carpeta por SMB.
func (c *Client) SambaApply(ctx context.Context, name, path, comment string) (bool, error) {
	var r agentproto.SambaShareResult
	if err := c.CallOnce(ctx, agentproto.OpSambaApply, agentproto.SambaShareArgs{Name: name, Path: path, Comment: comment}, &r); err != nil {
		return false, err
	}
	return r.Reloaded, nil
}

// ---- Fase M2.1 --------------------------------------------------------------------

// VolumeDelete arranca volume.delete (asíncrona: seguir con JobStatus).
func (c *Client) VolumeDelete(ctx context.Context, a agentproto.VolumeDeleteArgs) error {
	return c.CallOnce(ctx, agentproto.OpVolumeDelete, a, nil)
}

// ShareDelete borra una carpeta compartida con sus instantáneas.
func (c *Client) ShareDelete(ctx context.Context, mount, share string) (*agentproto.ShareDeleteResult, error) {
	var r agentproto.ShareDeleteResult
	if err := c.CallOnce(ctx, agentproto.OpShareDelete, agentproto.ShareDeleteArgs{Mount: mount, Share: share}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// SharePermissions aplica los permisos de una carpeta.
func (c *Client) SharePermissions(ctx context.Context, a agentproto.SharePermsArgs) (bool, error) {
	var r agentproto.SharePermsResult
	if err := c.CallOnce(ctx, agentproto.OpSharePerms, a, &r); err != nil {
		return false, err
	}
	return r.Reloaded, nil
}

// UserEnsure crea (si falta) el usuario Unix de un usuario de TangoOS.
func (c *Client) UserEnsure(ctx context.Context, name, full string) error {
	return c.CallOnce(ctx, agentproto.OpUserEnsure, agentproto.UserEnsureArgs{Name: name, Full: full}, nil)
}

// UserPassword crea o actualiza la cuenta Samba de un usuario.
func (c *Client) UserPassword(ctx context.Context, name, full, password string) error {
	return c.CallOnce(ctx, agentproto.OpUserPassword, agentproto.UserPasswordArgs{Name: name, Full: full, Password: password}, nil)
}

// UserDelete borra la cuenta Samba y el usuario Unix de un usuario de TangoOS
// (protocol/README.md §14).
func (c *Client) UserDelete(ctx context.Context, a agentproto.UserDeleteArgs) (*agentproto.UserDeleteResult, error) {
	var r agentproto.UserDeleteResult
	if err := c.CallOnce(ctx, agentproto.OpUserDelete, a, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
