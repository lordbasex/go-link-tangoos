package wsd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/ipv4"
)

// Server responde WS-Discovery en todas las interfaces físicas con IPv4.
type Server struct {
	Device   func() Device // se evalúa en cada respuesta (el hostname puede cambiar)
	Log      *slog.Logger
	HTTPAddr string // por defecto ":5357"

	seq  atomic.Int64
	mu   sync.Mutex
	seen map[string]time.Time // MessageID → cuándo (Windows repite cada mensaje)
}

func skipIface(name string) bool {
	for _, p := range []string{"docker", "veth", "br-", "virbr", "lo", "tailscale", "wg"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ifaceIPv4 devuelve las interfaces candidatas con su primera IPv4.
func ifaceIPv4() map[int]struct {
	ifi net.Interface
	ip  net.IP
} {
	out := map[int]struct {
		ifi net.Interface
		ip  net.IP
	}{}
	ifs, _ := net.Interfaces()
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 || skipIface(ifi.Name) {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out[ifi.Index] = struct {
					ifi net.Interface
					ip  net.IP
				}{ifi, n.IP.To4()}
				break
			}
		}
	}
	return out
}

func (s *Server) dup(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]time.Time{}
	}
	now := time.Now()
	for k, t := range s.seen {
		if now.Sub(t) > time.Minute {
			delete(s.seen, k)
		}
	}
	if _, ok := s.seen[id]; ok {
		return true
	}
	s.seen[id] = now
	return false
}

// Run atiende hasta que ctx termina; al salir manda Bye.
func (s *Server) Run(ctx context.Context) error {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	group := net.ParseIP(MulticastAddr)
	c, err := net.ListenPacket("udp4", ":"+strconv.Itoa(Port))
	if err != nil {
		return err
	}
	defer c.Close()
	p := ipv4.NewPacketConn(c)
	_ = p.SetControlMessage(ipv4.FlagInterface|ipv4.FlagDst, true)
	_ = p.SetMulticastTTL(1)
	_ = p.SetMulticastLoopback(true)
	ifaces := ifaceIPv4()
	joined := 0
	for _, x := range ifaces {
		ifi := x.ifi
		if err := p.JoinGroup(&ifi, &net.UDPAddr{IP: group}); err == nil {
			joined++
		} else {
			s.Log.Warn("wsd: join group", "iface", ifi.Name, "err", err)
		}
	}
	if joined == 0 {
		return errors.New("wsd: no hay interfaces con multicast")
	}

	srv := &http.Server{Addr: s.httpAddr(), Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.Log.Warn("wsd: http", "err", err)
		}
	}()

	dst := &net.UDPAddr{IP: group, Port: Port}
	sendAll := func(build func(ip string) []byte) {
		for _, x := range ifaceIPv4() {
			ifi := x.ifi
			_ = p.SetMulticastInterface(&ifi)
			msg := build(x.ip.String())
			for i := 0; i < 2; i++ { // UDP: se repite (como wsdd)
				_, _ = p.WriteTo(msg, nil, dst)
			}
		}
	}
	sendAll(func(ip string) []byte { return s.Device().Hello(ip, s.seq.Add(1)) })
	s.Log.Info("wsd: listening", "interfaces", joined, "udp", Port, "http", s.httpAddr())

	go func() {
		<-ctx.Done()
		d := s.Device()
		sendAll(func(string) []byte { return d.Bye(s.seq.Add(1)) })
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(sctx)
		cancel()
		_ = c.Close()
	}()

	buf := make([]byte, 64<<10)
	for {
		n, cm, src, err := p.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		m, err := Parse(buf[:n])
		if err != nil || s.dup(m.MessageID) {
			continue
		}
		ip := ""
		if cm != nil {
			if x, ok := ifaceIPv4()[cm.IfIndex]; ok {
				ip = x.ip.String()
			}
		}
		if ip == "" {
			continue
		}
		reply := s.Device().Reply(m, ip, s.seq.Add(1))
		if reply == nil {
			continue
		}
		// Las respuestas van por unicast al que preguntó.
		_, _ = p.WriteTo(reply, nil, src)
	}
}

func (s *Server) httpAddr() string {
	if s.HTTPAddr != "" {
		return s.HTTPAddr
	}
	return ":" + strconv.Itoa(HTTPPort)
}

// ServeHTTP atiende WS-Transfer Get en /<uuid>.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d := s.Device()
	if r.Method != http.MethodPost || strings.Trim(r.URL.Path, "/") != d.UUID {
		http.NotFound(w, r)
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	m, err := Parse(b)
	if err != nil || m.Action != ActGet {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/soap+xml")
	_, _ = w.Write(d.GetResponse(m.MessageID))
}

// Probe manda un Probe multicast desde laddr ("" = cualquiera) y junta las respuestas
// durante wait. Lo usan tangoctl y los tests e2e.
func Probe(ctx context.Context, wait time.Duration) ([]Message, error) {
	c, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, err
	}
	defer c.Close()
	msg, id := ProbeMessage()
	dst := &net.UDPAddr{IP: net.ParseIP(MulticastAddr), Port: Port}
	p := ipv4.NewPacketConn(c)
	_ = p.SetMulticastTTL(1)
	sent := false
	for _, x := range ifaceIPv4() {
		ifi := x.ifi
		if p.SetMulticastInterface(&ifi) == nil {
			if _, err := p.WriteTo(msg, nil, dst); err == nil {
				sent = true
			}
		}
	}
	if !sent {
		if _, err := c.WriteTo(msg, dst); err != nil {
			return nil, err
		}
	}
	_ = c.SetReadDeadline(time.Now().Add(wait))
	var out []Message
	buf := make([]byte, 64<<10)
	seen := map[string]bool{}
	for {
		n, _, err := c.ReadFrom(buf)
		if err != nil {
			break
		}
		m, err := Parse(buf[:n])
		if err != nil || m.Action != ActProbeMatches || m.RelatesTo != id || seen[m.MessageID] {
			continue
		}
		seen[m.MessageID] = true
		out = append(out, m)
		if ctx.Err() != nil {
			break
		}
	}
	return out, nil
}
