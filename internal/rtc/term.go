package rtc

import (
	"errors"

	"github.com/pion/webrtc/v4"

	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Canal "term" (protocol/README.md §12.1): binario y ordenado, frames [u32 id BE][bytes]
// en los dos sentidos. Lo crea el NAS antes de la offer, como files.N.

// TermHandler recibe cada frame que llega por el canal term.
type TermHandler func(s *rtcrpc.Session, data []byte)

var termHandler TermHandler

// SetTermHandler registra quién atiende los frames de terminal (internal/api).
func (s *Server) SetTermHandler(h TermHandler) {
	s.mu.Lock()
	termHandler = h
	s.mu.Unlock()
}

func (s *Server) termHandler() TermHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return termHandler
}

// TermSender es lo que la sesión guarda con la clave "term".
type TermSender func(frame []byte) error

const termBufCap = 4 << 20

func (p *peer) addTermChannel() error {
	ordered := true
	dc, err := p.pc.CreateDataChannel("term", &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		return err
	}
	p.channels["term"] = dc
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if m.IsString {
			return
		}
		if h := p.srv.termHandler(); h != nil {
			h(p.sess, m.Data)
		}
	})
	p.sess.Set("term", TermSender(func(b []byte) error {
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			return errors.New("canal term cerrado")
		}
		if dc.BufferedAmount()+uint64(len(b)) > termBufCap {
			return errors.New("canal term lleno")
		}
		return dc.Send(b)
	}))
	return nil
}
