package agentclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// ---- Fase M4 (protocol/README.md §12) -------------------------------------------------

// Power reinicia o apaga (el agente responde antes de actuar).
func (c *Client) Power(ctx context.Context, action string) error {
	return c.CallOnce(ctx, agentproto.OpPower, agentproto.PowerArgs{Action: action}, nil)
}

// SetIdentity cambia hostname y/o zona horaria.
func (c *Client) SetIdentity(ctx context.Context, hostname, tz string) error {
	return c.CallOnce(ctx, agentproto.OpIdentity, agentproto.IdentityArgs{Hostname: hostname, Timezone: tz}, nil)
}

// LogsQuery lee el journal.
func (c *Client) LogsQuery(ctx context.Context, a agentproto.LogsQueryArgs) ([]agentproto.LogLine, error) {
	var r agentproto.LogsQueryResult
	if err := c.Call(ctx, agentproto.OpLogsQuery, a, &r); err != nil {
		return nil, err
	}
	return r.Lines, nil
}

// UpdatesCheck consulta apt.
func (c *Client) UpdatesCheck(ctx context.Context) (*agentproto.UpdatesCheckResult, error) {
	var r agentproto.UpdatesCheckResult
	if err := c.CallOnce(ctx, agentproto.OpUpdatesCheck, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdatesApply arranca la actualización (asíncrona: seguir con M4Job).
func (c *Client) UpdatesApply(ctx context.Context, opID, scope string) error {
	return c.CallOnce(ctx, agentproto.OpUpdatesApply, agentproto.UpdatesApplyArgs{OpID: opID, Scope: scope}, nil)
}

// RollbackList lista los puntos de vuelta atrás.
func (c *Client) RollbackList(ctx context.Context) (*agentproto.RollbackListResult, error) {
	var r agentproto.RollbackListResult
	if err := c.Call(ctx, agentproto.OpRollbackList, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// RollbackApply vuelve a un punto y reinicia (asíncrona).
func (c *Client) RollbackApply(ctx context.Context, opID, id string) error {
	return c.CallOnce(ctx, agentproto.OpRollbackApply, agentproto.RollbackApplyArgs{OpID: opID, ID: id}, nil)
}

// ServicesState consulta unidades de systemd del allow-list.
func (c *Client) ServicesState(ctx context.Context, units ...string) ([]agentproto.ServiceState, error) {
	var r agentproto.ServicesStateResult
	if err := c.Call(ctx, agentproto.OpServicesState, agentproto.ServicesStateArgs{Units: units}, &r); err != nil {
		return nil, err
	}
	return r.Units, nil
}

// ServiceSet habilita o deshabilita Samba.
func (c *Client) ServiceSet(ctx context.Context, unit string, enabled bool) error {
	return c.CallOnce(ctx, agentproto.OpServiceSet, agentproto.ServiceSetArgs{Unit: unit, Enabled: enabled}, nil)
}

// NFSApply aplica los exports (asíncrona).
func (c *Client) NFSApply(ctx context.Context, a agentproto.NFSApplyArgs) error {
	return c.CallOnce(ctx, agentproto.OpNFSApply, a, nil)
}

// ISCSIApply deja LIO con estos destinos (asíncrona).
func (c *Client) ISCSIApply(ctx context.Context, a agentproto.ISCSIApplyArgs) error {
	return c.CallOnce(ctx, agentproto.OpISCSIApply, a, nil)
}

// ISCSIStatus cuenta sesiones por destino.
func (c *Client) ISCSIStatus(ctx context.Context) (*agentproto.ISCSIStatusResult, error) {
	var r agentproto.ISCSIStatusResult
	if err := c.Call(ctx, agentproto.OpISCSIStatus, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// M4Job consulta una operación asíncrona de M4.
func (c *Client) M4Job(ctx context.Context, opID string) (*JobStatus, error) {
	var r JobStatus
	if err := c.Call(ctx, agentproto.OpM4Job, agentproto.M4JobArgs{OpID: opID}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// TermSpawn abre una consola de administración y devuelve el master del PTY.
func (c *Client) TermSpawn(ctx context.Context, cols, rows int, user string) (*os.File, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, ErrUnavailable
	}
	_ = uc.SetDeadline(time.Now().Add(15 * time.Second))
	args, _ := json.Marshal(agentproto.TermSpawnArgs{Cols: cols, Rows: rows, User: user})
	req, _ := json.Marshal(agentproto.Request{V: agentproto.Version, ID: 1, Op: agentproto.OpTermSpawn, Args: args})
	if _, err := uc.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	line, fd, err := readWithFD(uc)
	if err != nil {
		return nil, err
	}
	var resp agentproto.Response
	if err := json.Unmarshal(line, &resp); err != nil || !resp.OK {
		if fd >= 0 {
			unix.Close(fd)
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return nil, agentproto.Errf(agentproto.CodeInternal, "respuesta inválida del agente")
	}
	if fd < 0 {
		return nil, agentproto.Errf(agentproto.CodeInternal, "el agente no entregó la terminal")
	}
	unix.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), "pty"), nil
}

// readWithFD lee una línea JSON y, si vino, el primer fd por SCM_RIGHTS.
func readWithFD(uc *net.UnixConn) ([]byte, int, error) {
	buf := make([]byte, agentproto.MaxLine)
	oob := make([]byte, unix.CmsgSpace(4))
	var line []byte
	fd := -1
	for {
		n, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
		if oobn > 0 && fd < 0 {
			if msgs, perr := unix.ParseSocketControlMessage(oob[:oobn]); perr == nil {
				for _, m := range msgs {
					if fds, err := unix.ParseUnixRights(&m); err == nil {
						for i, f := range fds {
							if i == 0 {
								fd = f
							} else {
								unix.Close(f)
							}
						}
					}
				}
			}
		}
		line = append(line, buf[:n]...)
		if n > 0 && line[len(line)-1] == '\n' {
			return line, fd, nil
		}
		if err != nil || n == 0 {
			if fd >= 0 {
				unix.Close(fd)
			}
			return nil, -1, fmt.Errorf("%w: respuesta incompleta (%v)", ErrUnavailable, err)
		}
	}
}

// UPSApply configura NUT para un UPS USB local (asíncrona: seguir con M4Job).
func (c *Client) UPSApply(ctx context.Context, a agentproto.UPSApplyArgs) error {
	return c.CallOnce(ctx, agentproto.OpUPSApply, a, nil)
}
