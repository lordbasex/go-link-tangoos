// Copiado de github.com/lordbasex/go-link-desk/backend-device/pkg/signalclient (MIT,
// mismo autor) para no arrastrar el módulo de go-link-desk; se mantiene igual al original.

// Copyright (c) 2026 Federico Pereira <lord.basex@gmail.com>

package signal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// ErrNotConnected is returned by Send while there is no live connection.
var ErrNotConnected = errors.New("signal: not connected")

// Handler receives connection events. All methods are called from the
// client's read goroutine, one at a time, so a Handler does not need its
// own locking for these calls. Send may be called from inside them.
type Handler interface {
	// OnConnect runs after the server's hello arrives.
	OnConnect(hello Envelope)
	// OnMessage runs for every message after hello.
	OnMessage(env Envelope)
	// OnDisconnect runs when a connection attempt fails or a live
	// connection drops. The client then waits and reconnects.
	OnDisconnect(err error)
}

// Config configures a Client. Zero values get defaults.
type Config struct {
	URL        string      // ws:// or wss:// endpoint, e.g. wss://signal.example/ws
	Header     http.Header // extra handshake headers
	MinBackoff time.Duration
	MaxBackoff time.Duration
	PongWait   time.Duration // max silence before the link is considered dead
	// PingEvery is how often the client pings the server itself, so a link
	// that died without a word (a NAT that dropped it) is noticed within
	// PongWait rather than at the server's next ping.
	PingEvery time.Duration
	WriteWait time.Duration
	Logger    *slog.Logger
	// Renewable says whether the connection may be made again now (nobody
	// in session). The TURN credentials of hello last a while (12 h on
	// signalhub) and come only with hello: before they expire the client
	// reconnects for new ones, once Renewable says yes. Nil: any time.
	Renewable func() bool
	// now is the clock (tests).
	now func() time.Time
}

// errRenew ends a connection on purpose, for fresh TURN credentials.
var errRenew = errors.New("signal: renewing the TURN credentials")

// renewMargin is how long before the TURN credentials expire the client
// starts looking for a moment to reconnect.
const renewMargin = time.Hour

// renewRetry is how often the client asks Renewable again while someone is
// in session.
var renewRetry = time.Minute

// renewAt is when to reconnect for fresh TURN credentials: renewMargin
// before the first of them expires (its username is "expiry:peer", the
// TURN REST format), or zero when none says.
func renewAt(servers []ICEServer, now time.Time) time.Time {
	var first time.Time
	for _, s := range servers {
		exp, _, ok := strings.Cut(s.Username, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(exp, 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		if t := time.Unix(n, 0); first.IsZero() || t.Before(first) {
			first = t
		}
	}
	if first.IsZero() {
		return time.Time{}
	}
	margin := min(renewMargin, first.Sub(now)/4)
	return first.Add(-max(margin, 0))
}

// Client keeps a signalhub connection alive.
type Client struct {
	cfg Config
	h   Handler

	mu   sync.Mutex // guards conn and serializes writes
	conn *websocket.Conn
}

// New builds a Client. Call Run to start it.
func New(cfg Config, h Handler) *Client {
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	if cfg.PingEvery <= 0 {
		cfg.PingEvery = 15 * time.Second
	}
	if cfg.PongWait <= 0 {
		// Two of our pings unanswered (the server's own come every 54 s).
		cfg.PongWait = 2*cfg.PingEvery + 5*time.Second
	}
	if cfg.WriteWait <= 0 {
		cfg.WriteWait = 10 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &Client{cfg: cfg, h: h}
}

// CheckURL validates a signalhub URL. It reports whether the link is
// protected: wss:// always is; ws:// is accepted only because loopback
// development servers have no TLS.
func CheckURL(raw string) (encrypted bool, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return false, fmt.Errorf("signal: bad URL: %w", err)
	}
	switch u.Scheme {
	case "wss":
		encrypted = true
	case "ws":
	default:
		return false, fmt.Errorf("signal: URL scheme must be ws or wss, got %q", u.Scheme)
	}
	if u.Host == "" {
		return false, fmt.Errorf("signal: URL %q has no host", raw)
	}
	return encrypted, nil
}

// IsLoopback reports whether the URL points at this machine (where
// ws:// without TLS is fine).
func IsLoopback(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// endpoint adds ?v=1 unless the URL already names a version.
func endpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("signal: bad URL: %w", err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return "", fmt.Errorf("signal: URL scheme must be ws or wss, got %q", u.Scheme)
	}
	q := u.Query()
	if q.Get("v") == "" {
		q.Set("v", ProtocolVersion)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// Run connects and reconnects until ctx is cancelled. It only returns an
// error for a bad configuration or when ctx ends.
func (c *Client) Run(ctx context.Context) error {
	target, err := endpoint(c.cfg.URL)
	if err != nil {
		return err
	}
	backoff := c.cfg.MinBackoff
	for {
		connected, err := c.session(ctx, target)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.h.OnDisconnect(err)
		if errors.Is(err, errRenew) {
			continue // straight back, with new credentials
		}
		if connected {
			backoff = c.cfg.MinBackoff
		}
		// Full jitter: many devices restarting together do not all
		// reconnect at the same instant.
		wait := time.Duration(rand.Int64N(int64(backoff))) + c.cfg.MinBackoff/2
		c.cfg.Logger.Debug("signal reconnect scheduled", "in", wait, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		backoff = min(backoff*2, c.cfg.MaxBackoff)
	}
}

// session runs one connection. connected reports whether hello arrived.
func (c *Client) session(ctx context.Context, target string) (connected bool, err error) {
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	conn, _, err := dialer.DialContext(ctx, target, c.cfg.Header)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	// Close the socket when ctx ends so the blocking read returns.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	conn.SetReadLimit(64 << 10)
	_ = conn.SetReadDeadline(time.Now().Add(c.cfg.PongWait))
	conn.SetPingHandler(func(data string) error {
		_ = conn.SetReadDeadline(time.Now().Add(c.cfg.PongWait))
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(c.cfg.WriteWait))
	})
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(c.cfg.PongWait))
	})
	// Our own pings: the server answers each with a pong, which keeps the
	// link alive here; no answer for PongWait and the read fails.
	pinging := make(chan struct{})
	defer close(pinging)
	go func() {
		t := time.NewTicker(c.cfg.PingEvery)
		defer t.Stop()
		for {
			select {
			case <-pinging:
				return
			case <-t.C:
				// WriteControl may run beside the other writers.
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(c.cfg.WriteWait))
			}
		}
	}()

	var hello Envelope
	if err := conn.ReadJSON(&hello); err != nil {
		return false, err
	}
	if hello.Type != TypeHello {
		return false, fmt.Errorf("signal: expected hello, got %q", hello.Type)
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
	}()

	c.h.OnConnect(hello)
	var renewing atomic.Bool
	if at := renewAt(hello.ICEServers, c.cfg.now()); !at.IsZero() {
		go c.renewWhenIdle(conn, at, pinging, &renewing)
	}
	for {
		var env Envelope
		if err := conn.ReadJSON(&env); err != nil {
			if renewing.Load() {
				return true, errRenew
			}
			return true, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(c.cfg.PongWait))
		c.h.OnMessage(env)
	}
}

// renewWhenIdle closes conn, for Run to make it again with fresh TURN
// credentials, from at on, as soon as nobody is in session (checked each
// minute). done ends it with the connection.
func (c *Client) renewWhenIdle(conn *websocket.Conn, at time.Time, done <-chan struct{}, renewing *atomic.Bool) {
	wait := max(at.Sub(c.cfg.now()), 0)
	for {
		select {
		case <-done:
			return
		case <-time.After(wait):
		}
		if c.cfg.Renewable == nil || c.cfg.Renewable() {
			c.cfg.Logger.Info("reconnecting to the signaling server for fresh TURN credentials")
			renewing.Store(true)
			_ = conn.Close()
			return
		}
		wait = renewRetry
	}
}

// Send writes one message. It is safe for concurrent use.
func (c *Client) Send(env Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return ErrNotConnected
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.cfg.WriteWait))
	return c.conn.WriteJSON(env)
}
