package rtc

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// Client is a minimal tango/1 client in Go (tangoctl, tests; later the base
// of tango-desk's NativeBridge transport, docs/06).
type Client struct {
	Hello Hello

	ws   *websocket.Conn
	wsMu sync.Mutex
	pc   *webrtc.PeerConnection

	rpc     *webrtc.DataChannel
	nextID  atomic.Int64
	mu      sync.Mutex
	waiting map[int64]chan rpcReply

	files  *clientFiles  // files.N (client_files.go)
	term   *clientTerm   // canal term (client_term.go)
	bundle *clientBundle // canal bundle (client_connect.go)

	// sendSig manda un payload de señalización: WebSocket local o signalhub.
	sendSig func(signalPayload) error

	Events  chan json.RawMessage // frames from `events`
	Metrics chan json.RawMessage // frames from `metrics`
}

// Hello is the server's first signaling message.
type Hello struct {
	Server     string      `json:"server"`
	NASID      string      `json:"nas_id"`
	ICEServers []iceServer `json:"ice_servers"`
	Methods    []string    `json:"methods"`
	Topics     []string    `json:"topics"`
}

type rpcReply struct {
	Type   string          `json:"type"`
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// RPCError is an `err` reply.
type RPCError struct{ Code, Message string }

func (e *RPCError) Error() string { return e.Code + ": " + e.Message }

// Dial connects to wss://host/signal?v=1 (or ws:// in dev) and waits until
// the three DataChannels are open.
func Dial(ctx context.Context, url string, insecure bool) (*Client, error) {
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second, TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}} //nolint:gosec // --insecure is explicit (self-signed first boot)
	ws, _, err := d.DialContext(ctx, url, http.Header{})
	if err != nil {
		return nil, fmt.Errorf("signal: %w", err)
	}
	c := &Client{ws: ws, waiting: map[int64]chan rpcReply{}, Events: make(chan json.RawMessage, 256), Metrics: make(chan json.RawMessage, 64)}
	c.sendSig = c.wsSignal
	var h struct {
		Type string `json:"type"`
		Hello
	}
	if err := ws.ReadJSON(&h); err != nil || h.Type != "hello" {
		ws.Close()
		return nil, fmt.Errorf("signal: no hello (%v)", err)
	}
	c.Hello = h.Hello

	var cfg webrtc.Configuration
	for _, s := range h.ICEServers {
		cfg.ICEServers = append(cfg.ICEServers, webrtc.ICEServer{URLs: s.URLs})
	}
	open, err := c.newPC(cfg)
	if err != nil {
		ws.Close()
		return nil, err
	}
	if err := c.signal(signalPayload{Kind: "hello_client", Client: "web"}); err != nil {
		c.Close()
		return nil, err
	}
	go c.readSignals()
	if err := waitOpen(ctx, open); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// newPC arma la PeerConnection del cliente y devuelve el canal que avisa cada uno de
// los tres canales base abiertos.
func (c *Client) newPC(cfg webrtc.Configuration) (chan struct{}, error) {
	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(cfg)
	if err != nil {
		return nil, err
	}
	c.pc = pc
	open := make(chan struct{}, 3)
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() == "rpc" || dc.Label() == "events" || dc.Label() == "metrics" {
			dc.OnOpen(func() { open <- struct{}{} })
		}
		switch dc.Label() {
		case "rpc":
			c.rpc = dc
			dc.OnMessage(func(m webrtc.DataChannelMessage) { c.onRPC(m.Data) })
		case "events":
			dc.OnMessage(func(m webrtc.DataChannelMessage) { push(c.Events, m.Data) })
		case "metrics":
			dc.OnMessage(func(m webrtc.DataChannelMessage) { push(c.Metrics, m.Data) })
		case "term":
			c.addTermChannel(dc) // client_term.go
		case "bundle":
			c.addBundleChannel(dc) // client_connect.go
		default:
			c.addFileChannel(dc) // files.N (client_files.go)
		}
	})
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		var init *webrtc.ICECandidateInit
		if cand != nil {
			ci := cand.ToJSON()
			init = &ci
		}
		_ = c.signal(signalPayload{Kind: "candidate", Candidate: init})
	})
	return open, nil
}

func waitOpen(ctx context.Context, open chan struct{}) error {
	for i := 0; i < 3; i++ {
		select {
		case <-open:
		case <-ctx.Done():
			return fmt.Errorf("datachannels: %w", ctx.Err())
		}
	}
	return nil
}

func push(ch chan json.RawMessage, b []byte) {
	select {
	case ch <- append(json.RawMessage(nil), b...):
	default: // nobody reading: drop
	}
}

func (c *Client) signal(p signalPayload) error { return c.sendSig(p) }

func (c *Client) wsSignal(p signalPayload) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	c.wsMu.Lock()
	defer c.wsMu.Unlock()
	return c.ws.WriteJSON(wire{Type: "signal", Payload: b})
}

func (c *Client) readSignals() {
	for {
		var m wire
		if err := c.ws.ReadJSON(&m); err != nil {
			return
		}
		if m.Type != "signal" {
			continue
		}
		var sp signalPayload
		if json.Unmarshal(m.Payload, &sp) != nil {
			continue
		}
		c.handlePayload(sp)
	}
}

func (c *Client) handlePayload(sp signalPayload) {
	switch sp.Kind {
	case "offer":
		if err := c.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sp.SDP}); err != nil {
			return
		}
		ans, err := c.pc.CreateAnswer(nil)
		if err != nil {
			return
		}
		if err := c.pc.SetLocalDescription(ans); err != nil {
			return
		}
		_ = c.signal(signalPayload{Kind: "answer", SDP: ans.SDP})
	case "candidate":
		if sp.Candidate != nil && sp.Candidate.Candidate != "" {
			_ = c.pc.AddICECandidate(*sp.Candidate)
		}
	}
}

func (c *Client) onRPC(b []byte) {
	var r rpcReply
	if json.Unmarshal(b, &r) != nil {
		return
	}
	c.mu.Lock()
	ch := c.waiting[r.ID]
	delete(c.waiting, r.ID)
	c.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}

// Call sends a request and waits for its reply.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	pb, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(map[string]any{"type": "req", "id": id, "method": method, "params": json.RawMessage(pb)})
	if err != nil {
		return nil, err
	}
	ch := make(chan rpcReply, 1)
	c.mu.Lock()
	c.waiting[id] = ch
	c.mu.Unlock()
	if err := c.rpc.SendText(string(b)); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if r.Type == "err" && r.Error != nil {
			return nil, &RPCError{Code: r.Error.Code, Message: r.Error.Message}
		}
		return r.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Login calls auth.login.
func (c *Client) Login(ctx context.Context, user, password string) error {
	return c.LoginTOTP(ctx, user, password, "")
}

// ErrNeedTOTP: el usuario tiene verificación en dos pasos y no se pasó código.
var ErrNeedTOTP = errors.New("el usuario tiene verificación en dos pasos: pasá el código (-totp)")

// LoginTOTP inicia sesión y, si el NAS pide código (protocol/README.md §13), lo manda.
func (c *Client) LoginTOTP(ctx context.Context, user, password, code string) error {
	res, err := c.Call(ctx, "auth.login", map[string]string{"user": user, "password": password})
	if err != nil {
		return err
	}
	var r struct {
		Needs string `json:"needs"`
	}
	if json.Unmarshal(res, &r) == nil && r.Needs == "totp" {
		if code == "" {
			return ErrNeedTOTP
		}
		_, err = c.Call(ctx, "auth.totp", map[string]string{"code": code})
	}
	return err
}

// Close tears down the PeerConnection and the WebSocket.
func (c *Client) Close() error {
	var errs []error
	if c.pc != nil {
		errs = append(errs, c.pc.Close())
	}
	if c.ws != nil {
		errs = append(errs, c.ws.Close())
	}
	return errors.Join(errs...)
}
