package rtc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/lordbasex/go-link-tangoos/internal/connect/signal"
	"github.com/lordbasex/go-link-tangoos/internal/fileworker"
)

// Cliente por signalhub (TangoConnect, protocol/README.md §15): lo que hace el portal,
// en Go, para tangoctl y los e2e. Se une a la sesión del NAS con claim (código de 9
// dígitos) o reach (device_id), y desde ahí es el mismo cliente que el local.

// HubMode elige cómo unirse a la sesión del NAS.
type HubMode struct {
	App      string // "go-link-tangoos"
	Code     string // claim con el código de vinculación
	DeviceID string // reach (navegador vinculado o invitado de un enlace)
}

// DialSignalhub se conecta a signalhub, entra a la sesión del NAS y espera los
// canales base abiertos.
func DialSignalhub(ctx context.Context, signalURL string, m HubMode) (*Client, error) {
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	ws, _, err := d.DialContext(ctx, signalURL, http.Header{})
	if err != nil {
		return nil, fmt.Errorf("signalhub: %w", err)
	}
	var hello signal.Envelope
	if err := ws.ReadJSON(&hello); err != nil || hello.Type != signal.TypeHello {
		ws.Close()
		return nil, fmt.Errorf("signalhub: no hello (%v)", err)
	}
	join := signal.Envelope{App: m.App}
	switch {
	case m.Code != "":
		join.Type, join.Code = signal.TypeClaim, m.Code
	case m.DeviceID != "":
		join.Type, join.DeviceID = signal.TypeReach, m.DeviceID
	default:
		ws.Close()
		return nil, errors.New("signalhub: falta el código o el device_id")
	}
	if err := ws.WriteJSON(join); err != nil {
		ws.Close()
		return nil, err
	}
	var remote string
	for remote == "" {
		_ = ws.SetReadDeadline(time.Now().Add(15 * time.Second))
		var env signal.Envelope
		if err := ws.ReadJSON(&env); err != nil {
			ws.Close()
			return nil, fmt.Errorf("signalhub: %w", err)
		}
		switch env.Type {
		case signal.TypePaired:
			remote = env.Remote
		case signal.TypeError:
			ws.Close()
			return nil, fmt.Errorf("signalhub: %s", env.Error)
		}
	}
	_ = ws.SetReadDeadline(time.Time{})

	c := &Client{ws: ws, waiting: map[int64]chan rpcReply{}, Events: make(chan json.RawMessage, 256), Metrics: make(chan json.RawMessage, 64)}
	c.Hello.ICEServers = nil
	c.sendSig = func(p signalPayload) error {
		b, err := json.Marshal(p)
		if err != nil {
			return err
		}
		c.wsMu.Lock()
		defer c.wsMu.Unlock()
		return ws.WriteJSON(signal.Envelope{Type: signal.TypeSignal, To: remote, Payload: b})
	}
	var cfg webrtc.Configuration
	for _, s := range hello.ICEServers {
		cfg.ICEServers = append(cfg.ICEServers, webrtc.ICEServer{URLs: s.URLs, Username: s.Username, Credential: s.Credential})
	}
	open, err := c.newPC(cfg)
	if err != nil {
		ws.Close()
		return nil, err
	}
	go func() {
		for {
			var env signal.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				return
			}
			if env.Type != signal.TypeSignal || env.From != remote {
				continue
			}
			var sp signalPayload
			if json.Unmarshal(env.Payload, &sp) == nil {
				c.handlePayload(sp)
			}
		}
	}()
	if err := waitOpen(ctx, open); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// RemoteFingerprint es el fingerprint DTLS que presentó el NAS (pinning).
func (c *Client) RemoteFingerprint() string {
	rd := c.pc.RemoteDescription()
	if rd == nil {
		return ""
	}
	for _, line := range splitLines(rd.SDP) {
		const p = "a=fingerprint:"
		if len(line) > len(p) && line[:len(p)] == p {
			return line[len(p):]
		}
	}
	return ""
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, trimCR(s[start:i]))
			start = i + 1
		}
	}
	return append(out, trimCR(s[start:]))
}

func trimCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}

// ---- canal bundle -------------------------------------------------------------------------

type clientBundle struct {
	mu    sync.Mutex
	parts map[uint32]map[uint64][]byte
	sizes map[uint32]int
	wait  map[uint32]chan struct{}
}

func (c *Client) addBundleChannel(dc *webrtc.DataChannel) {
	b := &clientBundle{parts: map[uint32]map[uint64][]byte{}, sizes: map[uint32]int{}, wait: map[uint32]chan struct{}{}}
	c.bundle = b
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if m.IsString {
			return
		}
		id, off, data, err := fileworker.DecodeFrame(m.Data)
		if err != nil {
			return
		}
		b.mu.Lock()
		if b.parts[id] == nil {
			b.parts[id] = map[uint64][]byte{}
		}
		b.parts[id][off] = append([]byte(nil), data...)
		b.sizes[id] += len(data)
		if b.wait[id] == nil {
			b.wait[id] = make(chan struct{}, 1)
		}
		w := b.wait[id]
		b.mu.Unlock()
		select {
		case w <- struct{}{}:
		default:
		}
	})
}

// BundleFile pide un archivo del escritorio por bundle.get y verifica su sha256.
func (c *Client) BundleFile(ctx context.Context, path string) ([]byte, error) {
	if c.bundle == nil {
		return nil, errors.New("esta conexión no tiene canal bundle")
	}
	res, err := c.Call(ctx, "bundle.get", map[string]string{"path": path})
	if err != nil {
		return nil, err
	}
	var g struct {
		ID     uint32 `json:"id"`
		Size   int    `json:"size"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(res, &g); err != nil {
		return nil, err
	}
	b := c.bundle
	for {
		b.mu.Lock()
		got := b.sizes[g.ID]
		if b.wait[g.ID] == nil {
			b.wait[g.ID] = make(chan struct{}, 1)
		}
		w := b.wait[g.ID]
		b.mu.Unlock()
		if got >= g.Size {
			break
		}
		select {
		case <-w:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	b.mu.Lock()
	parts := b.parts[g.ID]
	delete(b.parts, g.ID)
	b.mu.Unlock()
	out := make([]byte, g.Size)
	for off, p := range parts {
		copy(out[off:], p)
	}
	sum := sha256.Sum256(out)
	if hex.EncodeToString(sum[:]) != g.SHA256 {
		return nil, errors.New("bundle: el sha256 no coincide")
	}
	return out, nil
}
