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

// SpawnFileWorker le pide al agente un file-worker con las credenciales de user y
// devuelve la conexión SOCK_SEQPACKET hacia él (protocol/README.md §10). Usa una
// conexión propia al agente: la respuesta llega con el descriptor por SCM_RIGHTS.
func (c *Client) SpawnFileWorker(ctx context.Context, user string) (*net.UnixConn, agentproto.FileWorkerResult, error) {
	var res agentproto.FileWorkerResult
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, res, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, res, ErrUnavailable
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(15 * time.Second)
	}
	_ = uc.SetDeadline(deadline)
	args, _ := json.Marshal(agentproto.FileWorkerArgs{User: user})
	req, _ := json.Marshal(agentproto.Request{V: agentproto.Version, ID: 1, Op: agentproto.OpFileWorker, Args: args})
	if _, err := uc.Write(append(req, '\n')); err != nil {
		return nil, res, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	buf := make([]byte, agentproto.MaxLine)
	oob := make([]byte, unix.CmsgSpace(4))
	var line []byte
	var fd = -1
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
			break
		}
		if err != nil || n == 0 {
			if fd >= 0 {
				unix.Close(fd)
			}
			return nil, res, fmt.Errorf("%w: respuesta incompleta (%v)", ErrUnavailable, err)
		}
	}
	var resp agentproto.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		if fd >= 0 {
			unix.Close(fd)
		}
		return nil, res, err
	}
	if !resp.OK {
		if fd >= 0 {
			unix.Close(fd)
		}
		if resp.Error == nil {
			return nil, res, agentproto.Errf(agentproto.CodeInternal, "respuesta sin error ni resultado")
		}
		return nil, res, resp.Error
	}
	if fd < 0 {
		return nil, res, agentproto.Errf(agentproto.CodeInternal, "el agente no entregó el socket del file-worker")
	}
	_ = json.Unmarshal(resp.Result, &res)
	unix.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), "fileworker")
	wc, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return nil, res, err
	}
	wuc, ok := wc.(*net.UnixConn)
	if !ok {
		wc.Close()
		return nil, res, agentproto.Errf(agentproto.CodeInternal, "socket del file-worker inválido")
	}
	return wuc, res, nil
}
