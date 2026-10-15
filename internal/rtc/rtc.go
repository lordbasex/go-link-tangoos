// Package rtc is the WebRTC side of tangod (protocol/README.md §1–2,
// docs/03): the /signal WebSocket, one RTCPeerConnection per client with the
// rpc/events/metrics DataChannels, ICE on a single UDP+TCP port and the
// embedded STUN server.
//
// Lifetime: the WebSocket stays bound to its PeerConnection. While it is
// open the NAS can run ICE restarts (3 s after "disconnected", at once on
// "failed", up to 3 in a row). If the WebSocket is gone when ICE fails, the
// session ends and the client reconnects with a new PeerConnection and
// auth.resume.
package rtc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"

	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Config tunes the WebRTC side.
type Config struct {
	ICEPort  int      // single UDP+TCP port for ICE (8443)
	STUNPort int      // embedded STUN (3478); 0 = no STUN server advertised
	NAT1To1  []string // public IPs to advertise instead of host IPs (optional)
	Loopback bool     // include loopback candidates (dev mode)
	Server   string   // "tangod/<version>"
	NASID    string
}

// Server owns the WebRTC API and the live sessions.
type Server struct {
	cfg    Config
	api    *webrtc.API
	router *rtcrpc.Router
	hub    *rtcrpc.Hub
	log    *slog.Logger
	ctx    context.Context

	udp net.PacketConn
	tcp net.Listener

	mu    sync.Mutex
	peers map[string]*peer
	files FileHandler // frames de los canales files.N (files.go)

	// TangoConnect (remote.go): PCs que llegan por signalhub, sin el mux de ICE local.
	remoteAPI  *webrtc.API
	remoteCert *webrtc.Certificate
}

// New opens the ICE port and builds the WebRTC API.
func New(ctx context.Context, cfg Config, router *rtcrpc.Router, hub *rtcrpc.Hub, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	se := webrtc.SettingEngine{}
	udp, err := net.ListenPacket("udp", fmt.Sprintf(":%d", cfg.ICEPort))
	if err != nil {
		return nil, fmt.Errorf("rtc: ICE UDP :%d: %w", cfg.ICEPort, err)
	}
	tcp, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.ICEPort))
	if err != nil {
		udp.Close()
		return nil, fmt.Errorf("rtc: ICE TCP :%d: %w", cfg.ICEPort, err)
	}
	se.SetICEUDPMux(webrtc.NewICEUDPMux(nil, udp))
	se.SetICETCPMux(webrtc.NewICETCPMux(nil, tcp, 8))
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6, webrtc.NetworkTypeTCP4, webrtc.NetworkTypeTCP6})
	// QueryOnly: resolve the browser's "xxxx.local" candidates but publish the
	// NAS's real IPs (its address is no secret on the LAN, and clients need
	// not resolve mDNS to reach it).
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeQueryOnly)
	se.SetInterfaceFilter(keepInterface)
	se.SetIncludeLoopbackCandidate(cfg.Loopback)
	if len(cfg.NAT1To1) > 0 {
		se.SetNAT1To1IPs(cfg.NAT1To1, webrtc.ICECandidateTypeHost)
	}
	return &Server{
		cfg: cfg, router: router, hub: hub, log: log, ctx: ctx,
		api: webrtc.NewAPI(webrtc.WithSettingEngine(se)),
		udp: udp, tcp: tcp, peers: map[string]*peer{},
	}, nil
}

// keepInterface skips container and virtual bridges: candidates there only
// slow ICE down.
func keepInterface(name string) bool {
	for _, p := range []string{"docker", "veth", "br-", "virbr", "cni", "flannel", "podman", "lxc", "tailscale", "zt"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// Close ends every session and frees the ICE port.
func (s *Server) Close() {
	s.mu.Lock()
	peers := make([]*peer, 0, len(s.peers))
	for _, p := range s.peers {
		peers = append(peers, p)
	}
	s.mu.Unlock()
	for _, p := range peers {
		p.close("server shutdown")
	}
	s.udp.Close()
	s.tcp.Close()
}

// ---- signaling ------------------------------------------------------------------

type wire struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// SignalPayload es lo que viaja dentro de los mensajes "signal" (WebSocket local o
// payload opaco de signalhub, protocol/README.md §15).
type SignalPayload = signalPayload

type signalPayload struct {
	Kind      string                   `json:"kind"`
	Client    string                   `json:"client,omitempty"`
	Resume    string                   `json:"resume,omitempty"`
	SDP       string                   `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit `json:"candidate"`
}

type iceServer struct {
	URLs []string `json:"urls"`
}

type hello struct {
	Type       string      `json:"type"`
	V          int         `json:"v"`
	Server     string      `json:"server"`
	NASID      string      `json:"nas_id"`
	ICEServers []iceServer `json:"ice_servers"`
	Methods    []string    `json:"methods"`
	Topics     []string    `json:"topics"`
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  16 << 10,
	WriteBufferSize: 16 << 10,
	// Same origin only: the desktop is served by tangod itself.
	CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		if o == "" {
			return true // native clients (tangoctl, tango-desk)
		}
		host := strings.TrimPrefix(strings.TrimPrefix(o, "https://"), "http://")
		return strings.EqualFold(host, r.Host)
	},
}

const maxSignalMsg = 64 << 10

// ServeHTTP is the /signal WebSocket.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("v") != "1" {
		http.Error(w, "unsupported protocol version", http.StatusBadRequest)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(maxSignalMsg)
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	sc := &signalConn{ws: ws}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	h := hello{Type: "hello", V: 1, Server: s.cfg.Server, NASID: s.cfg.NASID, ICEServers: []iceServer{}, Methods: s.router.Methods(), Topics: s.hub.Topics()}
	if s.cfg.STUNPort > 0 && host != "" {
		hp := host
		if strings.Contains(hp, ":") {
			hp = "[" + hp + "]"
		}
		h.ICEServers = append(h.ICEServers, iceServer{URLs: []string{fmt.Sprintf("stun:%s:%d", hp, s.cfg.STUNPort)}})
	}
	if err := sc.send(h); err != nil {
		ws.Close()
		return
	}

	lim := rtcrpc.NewLimiter(16, 32)
	var p *peer
	defer func() {
		ws.Close()
		if p != nil {
			p.detachSignal(sc)
		}
	}()
	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if !lim.Allow() {
			_ = sc.send(map[string]string{"type": "error", "code": rtcrpc.CodeRateLimited, "message": "demasiados mensajes de señalización"})
			continue
		}
		var m wire
		if json.Unmarshal(msg, &m) != nil || m.Type != "signal" {
			continue
		}
		var sp signalPayload
		if json.Unmarshal(m.Payload, &sp) != nil {
			continue
		}
		switch sp.Kind {
		case "hello_client":
			if p != nil {
				continue
			}
			p, err = s.newPeer(ip, sp.Client, sc)
			if err == nil {
				p.sess.Set("host", host) // para armar URLs del mismo host (proxies de apps)
			}
			if err != nil {
				s.log.Error("rtc: new peer", "err", err)
				_ = sc.send(map[string]string{"type": "error", "code": rtcrpc.CodeInternal, "message": "no se pudo crear la conexión"})
				return
			}
		case "answer":
			if p != nil {
				p.setAnswer(sp.SDP)
			}
		case "candidate":
			if p != nil && sp.Candidate != nil {
				p.addCandidate(*sp.Candidate)
			}
		}
	}
}

type signalConn struct {
	mu sync.Mutex
	ws *websocket.Conn
}

func (c *signalConn) send(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.ws.WriteJSON(v)
}

// signaler lleva offer/candidates al cliente: el WebSocket local o signalhub.
type signaler interface {
	signal(p signalPayload) error
}

func (c *signalConn) signal(p signalPayload) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return c.send(wire{Type: "signal", Payload: b})
}

// ---- peers ----------------------------------------------------------------------

const (
	restartAfter = 3 * time.Second
	maxRestarts  = 3
	// Frames that would push a channel's buffer past these are dropped:
	// metrics are perishable, events/rpc are not but a stuck client must
	// not make tangod hold unbounded memory.
	metricsBufferCap = 64 << 10
	reliableBufCap   = 8 << 20
)

type peer struct {
	srv  *Server
	id   string
	pc   *webrtc.PeerConnection
	sess *rtcrpc.Session
	log  *slog.Logger

	mu        sync.Mutex
	sig       signaler
	remoteSet bool
	pending   []webrtc.ICECandidateInit
	restarts  int
	timer     *time.Timer
	closed    bool
	channels  map[string]*webrtc.DataChannel
	statsBase map[string]chanBase
}

type chanBase struct {
	bytes uint64
	at    time.Time
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) newPeer(ip, client string, sc signaler) (*peer, error) {
	switch client {
	case "web", "desk-win", "desk-linux":
	default:
		client = "web"
	}
	return s.newPeerWith(s.api, webrtc.Configuration{}, ip, client, sc, nil)
}

// newPeerWith crea la PC con sus canales. extra son canales adicionales (bundle, en
// las remotas); se crean antes de la offer.
func (s *Server) newPeerWith(api *webrtc.API, conf webrtc.Configuration, ip, client string, sc signaler, extra []string) (*peer, error) {
	pc, err := api.NewPeerConnection(conf)
	if err != nil {
		return nil, err
	}
	p := &peer{srv: s, id: newID(), pc: pc, sig: sc, channels: map[string]*webrtc.DataChannel{}, statsBase: map[string]chanBase{}}
	p.log = s.log.With("peer", p.id, "ip", ip)

	ordered := true
	unordered := false
	zero := uint16(0)
	specs := []struct {
		label string
		init  *webrtc.DataChannelInit
	}{
		{"rpc", &webrtc.DataChannelInit{Ordered: &ordered}},
		{"events", &webrtc.DataChannelInit{Ordered: &ordered}},
		{"metrics", &webrtc.DataChannelInit{Ordered: &unordered, MaxRetransmits: &zero}},
	}
	for _, l := range extra {
		specs = append(specs, struct {
			label string
			init  *webrtc.DataChannelInit
		}{l, &webrtc.DataChannelInit{Ordered: &ordered}})
	}
	for _, sp := range specs {
		dc, err := pc.CreateDataChannel(sp.label, sp.init)
		if err != nil {
			pc.Close()
			return nil, err
		}
		p.channels[sp.label] = dc
	}
	sender := func(label string, limit uint64) rtcrpc.Sender {
		dc := p.channels[label]
		return func(text string) error {
			if dc.ReadyState() != webrtc.DataChannelStateOpen {
				return errors.New("channel not open")
			}
			if dc.BufferedAmount()+uint64(len(text)) > limit {
				return errors.New("channel buffer full")
			}
			return dc.SendText(text)
		}
	}
	p.sess = rtcrpc.NewSession(p.id, ip, client, sender("rpc", reliableBufCap), sender("events", reliableBufCap), sender("metrics", metricsBufferCap))
	p.sess.Set("rtc.stats", p.stats)
	if err := p.addFileChannels(); err != nil { // files.0…3 (protocol/README.md §10)
		pc.Close()
		return nil, err
	}
	if err := p.addTermChannel(); err != nil { // term (protocol/README.md §12.1)
		pc.Close()
		return nil, err
	}
	p.channels["rpc"].OnMessage(func(m webrtc.DataChannelMessage) {
		if m.IsString {
			s.router.Dispatch(s.ctx, p.sess, m.Data)
		}
	})

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		var init *webrtc.ICECandidateInit
		if c != nil {
			ci := c.ToJSON()
			init = &ci
		}
		p.mu.Lock()
		sig := p.sig
		p.mu.Unlock()
		if sig != nil {
			_ = sig.signal(signalPayload{Kind: "candidate", Candidate: init})
		}
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		p.log.Debug("rtc: state", "state", st.String())
		switch st {
		case webrtc.PeerConnectionStateConnected:
			if ra := p.remoteAddr(); ra != "" && p.sess.RemoteIP == "remote" {
				p.sess.RemoteIP = ra // remotas: la IP se conoce recién con el par elegido
			}
			p.mu.Lock()
			p.restarts = 0
			if p.timer != nil {
				p.timer.Stop()
				p.timer = nil
			}
			p.mu.Unlock()
			s.hub.Add(p.sess)
			p.log.Info("rtc: connected", "client", client)
		case webrtc.PeerConnectionStateDisconnected:
			p.mu.Lock()
			if p.timer == nil {
				p.timer = time.AfterFunc(restartAfter, p.restart)
			}
			p.mu.Unlock()
		case webrtc.PeerConnectionStateFailed:
			p.restart()
		case webrtc.PeerConnectionStateClosed:
			p.close("closed")
		}
	})

	s.mu.Lock()
	s.peers[p.id] = p
	s.mu.Unlock()

	if err := p.offer(false); err != nil {
		p.close("offer failed")
		return nil, err
	}
	return p, nil
}

func (p *peer) offer(restart bool) error {
	o, err := p.pc.CreateOffer(&webrtc.OfferOptions{ICERestart: restart})
	if err != nil {
		return err
	}
	if err := p.pc.SetLocalDescription(o); err != nil {
		return err
	}
	p.mu.Lock()
	sig := p.sig
	p.remoteSet = false
	p.mu.Unlock()
	if sig == nil {
		return errors.New("no signaling connection")
	}
	return sig.signal(signalPayload{Kind: "offer", SDP: o.SDP})
}

func (p *peer) setAnswer(sdp string) {
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		p.log.Warn("rtc: bad answer", "err", err)
		return
	}
	p.mu.Lock()
	p.remoteSet = true
	pend := p.pending
	p.pending = nil
	p.mu.Unlock()
	for _, c := range pend {
		_ = p.pc.AddICECandidate(c)
	}
}

func (p *peer) addCandidate(c webrtc.ICECandidateInit) {
	if c.Candidate == "" {
		return // end of candidates
	}
	p.mu.Lock()
	if !p.remoteSet {
		if len(p.pending) < 64 {
			p.pending = append(p.pending, c)
		}
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	if err := p.pc.AddICECandidate(c); err != nil {
		p.log.Debug("rtc: candidate", "err", err)
	}
}

func (p *peer) restart() {
	p.mu.Lock()
	p.timer = nil
	if p.closed {
		p.mu.Unlock()
		return
	}
	if p.sig == nil || p.restarts >= maxRestarts {
		p.mu.Unlock()
		p.close("ice failed")
		return
	}
	p.restarts++
	n := p.restarts
	p.mu.Unlock()
	p.log.Info("rtc: ICE restart", "attempt", n)
	if err := p.offer(true); err != nil {
		p.close("ice restart failed")
	}
}

func (p *peer) detachSignal(sc signaler) {
	p.mu.Lock()
	if p.sig == sc {
		p.sig = nil
	}
	never := p.pc.ConnectionState() != webrtc.PeerConnectionStateConnected
	p.mu.Unlock()
	if never {
		p.close("signaling closed before connecting")
	}
}

func (p *peer) close(reason string) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
	}
	p.mu.Unlock()
	p.srv.hub.Remove(p.id)
	p.srv.mu.Lock()
	delete(p.srv.peers, p.id)
	p.srv.mu.Unlock()
	_ = p.pc.Close()
	p.log.Info("rtc: session closed", "reason", reason, "user", p.sess.User())
}

// ---- rtc.stats ---------------------------------------------------------------------

// Stats is exactly the TS RtcStats.
type Stats struct {
	ICE      string         `json:"ice"`
	Pair     Pair           `json:"pair"`
	RTTMs    float64        `json:"rttMs"`
	DTLS     string         `json:"dtls"`
	Channels []ChannelStats `json:"channels"`
}

type Pair struct {
	Local    string `json:"local"`
	Remote   string `json:"remote"`
	Type     string `json:"type"`
	Protocol string `json:"protocol"`
}

type ChannelStats struct {
	Label    string  `json:"label"`
	Ordered  bool    `json:"ordered"`
	Buffered uint64  `json:"buffered"`
	Rate     float64 `json:"rate"`
	Messages uint32  `json:"messages"`
}

func (p *peer) stats() any {
	st := Stats{ICE: p.pc.ICEConnectionState().String(), DTLS: "DTLS 1.2 · ECDSA P-256", Channels: []ChannelStats{}}
	if sctp := p.pc.SCTP(); sctp != nil && sctp.Transport() != nil {
		it := sctp.Transport().ICETransport()
		if pair, err := it.GetSelectedCandidatePair(); err == nil && pair != nil {
			st.Pair = Pair{
				Local:    net.JoinHostPort(pair.Local.Address, fmt.Sprint(pair.Local.Port)),
				Remote:   net.JoinHostPort(pair.Remote.Address, fmt.Sprint(pair.Remote.Port)),
				Type:     pairType(pair.Local.Typ, pair.Remote.Typ),
				Protocol: pair.Local.Protocol.String(),
			}
		}
		if ps, ok := it.GetSelectedCandidatePairStats(); ok {
			st.RTTMs = ps.CurrentRoundTripTime * 1000
		}
	}
	report := p.pc.GetStats()
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, label := range statLabels {
		dc := p.channels[label]
		cs := ChannelStats{Label: label, Ordered: dc.Ordered(), Buffered: dc.BufferedAmount()}
		for _, v := range report {
			if d, ok := v.(webrtc.DataChannelStats); ok && d.Label == label {
				cs.Messages = d.MessagesSent + d.MessagesReceived
				total := d.BytesSent + d.BytesReceived
				if b, ok := p.statsBase[label]; ok && now.After(b.at) && total >= b.bytes {
					cs.Rate = float64(total-b.bytes) / now.Sub(b.at).Seconds()
				}
				p.statsBase[label] = chanBase{bytes: total, at: now}
			}
		}
		st.Channels = append(st.Channels, cs)
	}
	return st
}

func pairType(l, r webrtc.ICECandidateType) string {
	switch {
	case l == webrtc.ICECandidateTypeRelay || r == webrtc.ICECandidateTypeRelay:
		return "relay"
	case l == webrtc.ICECandidateTypeSrflx || r == webrtc.ICECandidateTypeSrflx || l == webrtc.ICECandidateTypePrflx || r == webrtc.ICECandidateTypePrflx:
		return "srflx"
	}
	return "host"
}
