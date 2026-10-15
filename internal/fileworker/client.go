package fileworker

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// Client es el extremo de tangod de un file-worker. Las llamadas son concurrentes:
// el worker atiende hasta 8 a la vez y las respuestas se emparejan por id.
type Client struct {
	conn   *net.UnixConn
	nextID atomic.Uint64

	mu      sync.Mutex
	waiting map[uint64]chan reply
	closed  bool
	lastUse atomic.Int64
	done    chan struct{}
}

type reply struct {
	resp Response
	fd   *os.File
}

// NewClient arranca la lectura de respuestas sobre conn (SOCK_SEQPACKET).
func NewClient(conn *net.UnixConn) *Client {
	c := &Client{conn: conn, waiting: map[uint64]chan reply{}, done: make(chan struct{})}
	c.lastUse.Store(time.Now().UnixNano())
	go c.readLoop()
	return c
}

// Done se cierra cuando el worker termina o la conexión se corta.
func (c *Client) Done() <-chan struct{} { return c.done }

// Idle es el tiempo desde la última llamada.
func (c *Client) Idle() time.Duration { return time.Since(time.Unix(0, c.lastUse.Load())) }

// Close termina el worker (al cerrar su socket, el worker sale).
func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) readLoop() {
	defer func() {
		c.mu.Lock()
		c.closed = true
		for id, ch := range c.waiting {
			delete(c.waiting, id)
			close(ch)
		}
		c.mu.Unlock()
		close(c.done)
	}()
	buf := make([]byte, maxPacketClient)
	oob := make([]byte, unix.CmsgSpace(4*4))
	for {
		n, oobn, _, _, err := c.conn.ReadMsgUnix(buf, oob)
		if err != nil || n == 0 {
			return
		}
		var fd *os.File
		if oobn > 0 {
			if msgs, err := unix.ParseSocketControlMessage(oob[:oobn]); err == nil {
				for _, m := range msgs {
					fds, err := unix.ParseUnixRights(&m)
					if err != nil {
						continue
					}
					for i, f := range fds {
						if i == 0 && fd == nil {
							unix.CloseOnExec(f)
							fd = os.NewFile(uintptr(f), "worker-fd")
						} else {
							unix.Close(f)
						}
					}
				}
			}
		}
		var r Response
		if json.Unmarshal(buf[:n], &r) != nil {
			if fd != nil {
				fd.Close()
			}
			continue
		}
		c.mu.Lock()
		ch := c.waiting[r.ID]
		delete(c.waiting, r.ID)
		c.mu.Unlock()
		if ch == nil {
			if fd != nil {
				fd.Close()
			}
			continue
		}
		ch <- reply{resp: r, fd: fd}
	}
}

// maxPacketClient deja margen sobre el límite del worker.
const maxPacketClient = 256 << 10

// Call envía un pedido y espera la respuesta. Si out no es nil se decodifica el
// resultado ahí; el fd (si vino) lo devuelve y es del llamador cerrarlo.
func (c *Client) Call(ctx context.Context, req Request, out any) (*os.File, error) {
	c.lastUse.Store(time.Now().UnixNano())
	req.ID = c.nextID.Add(1)
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, &Error{Code: CodeUnavailable, Message: "el file-worker no está disponible"}
	}
	c.waiting[req.ID] = ch
	c.mu.Unlock()
	if _, _, err := c.conn.WriteMsgUnix(b, nil, nil); err != nil {
		c.mu.Lock()
		delete(c.waiting, req.ID)
		c.mu.Unlock()
		return nil, &Error{Code: CodeUnavailable, Message: "el file-worker no está disponible"}
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, &Error{Code: CodeUnavailable, Message: "el file-worker se cerró"}
		}
		if !r.resp.OK {
			if r.fd != nil {
				r.fd.Close()
			}
			if r.resp.Error == nil {
				r.resp.Error = &Error{Code: CodeInternal, Message: "error del file-worker"}
			}
			return nil, r.resp.Error
		}
		if out != nil && len(r.resp.Result) > 0 {
			if err := json.Unmarshal(r.resp.Result, out); err != nil {
				if r.fd != nil {
					r.fd.Close()
				}
				return nil, err
			}
		}
		return r.fd, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waiting, req.ID)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}
