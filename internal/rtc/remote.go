package rtc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"

	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// TangoConnect (protocol/README.md §15): navegadores que llegan por signalhub desde
// internet. Usan el mismo stack que los locales (mismos canales, router y hub) pero:
//   - otra API de WebRTC sin el mux de ICE: puertos efímeros, así pion junta
//     candidatos srflx (STUN) y relay (TURN) con los servidores que da signalhub;
//   - un certificado DTLS persistente, para que el portal pueda fijar su fingerprint;
//   - un canal más, `bundle`, por el que el NAS sirve el escritorio firmado.

// RemoteSignaler lleva offer/candidates por signalhub al navegador remoto.
type RemoteSignaler interface {
	Signal(p SignalPayload) error
}

type remoteSig struct{ s RemoteSignaler }

func (r remoteSig) signal(p signalPayload) error { return r.s.Signal(p) }

// RemotePeer es una PC creada para un navegador de signalhub.
type RemotePeer struct {
	p   *peer
	sig remoteSig
}

// Session es la sesión rtcrpc de la PC.
func (r *RemotePeer) Session() *rtcrpc.Session { return r.p.sess }

// ID es el id interno de la PC.
func (r *RemotePeer) ID() string { return r.p.id }

// HandleSignal procesa un payload que llegó del navegador (answer o candidate).
func (r *RemotePeer) HandleSignal(sp SignalPayload) {
	switch sp.Kind {
	case "answer":
		r.p.setAnswer(sp.SDP)
	case "candidate":
		if sp.Candidate != nil {
			r.p.addCandidate(*sp.Candidate)
		}
	}
}

// Detach avisa que el navegador ya no está en signalhub (peer_left): si la PC nunca
// llegó a conectar, se cierra; si ya conectó, sigue (solo pierde el ICE restart).
func (r *RemotePeer) Detach() { r.p.detachSignal(r.sig) }

// Close cierra la PC.
func (r *RemotePeer) Close(reason string) { r.p.close(reason) }

// Closed indica si la PC ya se cerró.
func (r *RemotePeer) Closed() bool {
	r.p.mu.Lock()
	defer r.p.mu.Unlock()
	return r.p.closed
}

// RemoteOptions configura una PC remota.
type RemoteOptions struct {
	ICEServers []webrtc.ICEServer
	Client     string // "remote" o "guest": queda en Session.Client (auditoría, sesiones)
	Bundle     bool   // abre el canal bundle (navegadores vinculados; no invitados)
}

// EnableRemote prepara la API remota con el certificado DTLS persistente de dir
// (connect-dtls.pem, 0600). Hace falta antes de NewRemotePeer.
func (s *Server) EnableRemote(dir string) (fingerprint string, err error) {
	cert, err := loadOrCreateCert(filepath.Join(dir, "connect-dtls.pem"))
	if err != nil {
		return "", err
	}
	se := webrtc.SettingEngine{}
	se.SetInterfaceFilter(keepInterface)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6})
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeQueryOnly)
	s.mu.Lock()
	s.remoteAPI = webrtc.NewAPI(webrtc.WithSettingEngine(se))
	s.remoteCert = cert
	s.mu.Unlock()
	fps, err := cert.GetFingerprints()
	if err != nil || len(fps) == 0 {
		return "", errors.New("rtc: certificado DTLS sin fingerprint")
	}
	return strings.ToUpper(fps[0].Algorithm) + " " + strings.ToUpper(fps[0].Value), nil
}

// NewRemotePeer crea la PC para un navegador remoto y le manda la offer por sig.
func (s *Server) NewRemotePeer(sig RemoteSignaler, o RemoteOptions) (*RemotePeer, error) {
	s.mu.Lock()
	api, cert := s.remoteAPI, s.remoteCert
	s.mu.Unlock()
	if api == nil || cert == nil {
		return nil, errors.New("rtc: TangoConnect no está habilitado")
	}
	client := o.Client
	if client == "" {
		client = "remote"
	}
	var extra []string
	if o.Bundle {
		extra = append(extra, "bundle")
	}
	rs := remoteSig{s: sig}
	conf := webrtc.Configuration{ICEServers: o.ICEServers, Certificates: []webrtc.Certificate{*cert}}
	p, err := s.newPeerWith(api, conf, "remote", client, rs, extra)
	if err != nil {
		return nil, err
	}
	p.sess.Set("origin", "remote")
	if dc := p.channels["bundle"]; dc != nil {
		bc := &BinChan{dc: dc, low: make(chan struct{}, 1)}
		dc.SetBufferedAmountLowThreshold(lowWater)
		dc.OnBufferedAmountLow(func() {
			select {
			case bc.low <- struct{}{}:
			default:
			}
		})
		p.sess.Set("bundle", bc)
	}
	return &RemotePeer{p: p, sig: rs}, nil
}

// remoteAddr es la IP del navegador según el par de candidatos elegido.
func (p *peer) remoteAddr() string {
	sctp := p.pc.SCTP()
	if sctp == nil || sctp.Transport() == nil {
		return ""
	}
	pair, err := sctp.Transport().ICETransport().GetSelectedCandidatePair()
	if err != nil || pair == nil || pair.Remote == nil {
		return ""
	}
	if net.ParseIP(pair.Remote.Address) == nil {
		return "" // mDNS u otra cosa que no es una IP
	}
	return pair.Remote.Address
}

// BinChan es un canal binario con backpressure (bundle).
type BinChan struct {
	dc  *webrtc.DataChannel
	low chan struct{}
	mu  sync.Mutex
}

// Send manda un frame.
func (b *BinChan) Send(frame []byte) error {
	if b.dc.ReadyState() != webrtc.DataChannelStateOpen {
		return errors.New("canal bundle cerrado")
	}
	return b.dc.Send(frame)
}

// Wait bloquea hasta que el buffer tenga menos de max bytes.
func (b *BinChan) Wait(ctx context.Context, max uint64) error {
	for b.dc.BufferedAmount() > max {
		if b.dc.ReadyState() != webrtc.DataChannelStateOpen {
			return errors.New("canal bundle cerrado")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.low:
		}
	}
	return nil
}

func loadOrCreateCert(path string) (*webrtc.Certificate, error) {
	if b, err := os.ReadFile(path); err == nil {
		// Se renueva 30 días antes de vencer (el portal tendrá que volver a vincular:
		// cambia el fingerprint fijado).
		if c, err := webrtc.CertificateFromPEM(string(b)); err == nil && time.Until(c.Expires()) > 30*24*time.Hour {
			return c, nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	// webrtc.GenerateCertificate vence en un mes y pion rechaza los vencidos: este dura
	// 10 años para que el fingerprint fijado en el portal no cambie.
	c, err := webrtc.NewCertificate(key, x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "tangoos-connect"},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
	})
	if err != nil {
		return nil, err
	}
	pem, err := c.PEM()
	if err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(pem), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return c, nil
}
