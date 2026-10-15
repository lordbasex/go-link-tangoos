package rtc

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sync"

	"github.com/pion/webrtc/v4"
)

// clientTerm es el lado cliente del canal term (tangoctl term-exec y tests e2e).
type clientTerm struct {
	mu   sync.Mutex
	dc   *webrtc.DataChannel
	open chan struct{}
	subs map[uint32]chan []byte
}

func (c *Client) termState() *clientTerm {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.term == nil {
		c.term = &clientTerm{open: make(chan struct{}), subs: map[uint32]chan []byte{}}
	}
	return c.term
}

func (c *Client) addTermChannel(dc *webrtc.DataChannel) {
	t := c.termState()
	t.mu.Lock()
	t.dc = dc
	t.mu.Unlock()
	dc.OnOpen(func() { close(t.open) })
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if m.IsString || len(m.Data) < 4 {
			return
		}
		id := binary.BigEndian.Uint32(m.Data)
		t.mu.Lock()
		ch := t.subs[id]
		t.mu.Unlock()
		if ch != nil {
			ch <- append([]byte(nil), m.Data[4:]...)
		}
	})
}

// TermExec abre una terminal, escribe cmd y devuelve lo que se imprimió hasta que la
// shell termina (envía "exit" al final).
func (c *Client) TermExec(ctx context.Context, cmd string) (string, error) {
	t := c.termState()
	select {
	case <-t.open:
	case <-ctx.Done():
		return "", errors.New("el canal term no abrió")
	}
	res, err := c.Call(ctx, "term.open", map[string]int{"cols": 120, "rows": 40})
	if err != nil {
		return "", err
	}
	var o struct {
		ID uint32 `json:"id"`
	}
	_ = json.Unmarshal(res, &o)
	ch := make(chan []byte, 256)
	t.mu.Lock()
	t.subs[o.ID] = ch
	dc := t.dc
	t.mu.Unlock()
	frame := make([]byte, 4, 4+len(cmd)+16)
	binary.BigEndian.PutUint32(frame, o.ID)
	frame = append(frame, []byte(cmd+"\nexit\n")...)
	if err := dc.Send(frame); err != nil {
		return "", err
	}
	var out bytes.Buffer
	for {
		select {
		case b := <-ch:
			if len(b) == 0 {
				return out.String(), nil
			}
			out.Write(b)
		case <-ctx.Done():
			_, _ = c.Call(context.Background(), "term.close", map[string]uint32{"id": o.ID})
			return out.String(), ctx.Err()
		}
	}
}
