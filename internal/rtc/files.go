package rtc

import (
	"context"
	"errors"
	"fmt"

	"github.com/pion/webrtc/v4"

	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Canales de transferencia de archivos (protocol/README.md §10): files.0 … files.3,
// ordenados y binarios. Los crea el NAS antes de la offer.
const NumFileChannels = 4

// statLabels son los canales que informa rtc.stats.
var statLabels = []string{"rpc", "events", "metrics", "files.0", "files.1", "files.2", "files.3"}

// FileHandler recibe cada frame binario que llega por un canal files.N.
type FileHandler func(s *rtcrpc.Session, ch int, data []byte)

// SetFileHandler registra quién atiende los frames de archivos (internal/api).
func (s *Server) SetFileHandler(h FileHandler) {
	s.mu.Lock()
	s.files = h
	s.mu.Unlock()
}

func (s *Server) fileHandler() FileHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.files
}

// Umbrales de backpressure por canal: se espera a que el buffer baje de lowWater
// cuando supera el máximo que pide el emisor.
const lowWater = 1 << 20

// FileChannels es lo que la sesión ofrece para enviar por files.N. Se guarda en la
// sesión con la clave "files" (internal/api la usa vía una interfaz propia).
type FileChannels struct {
	dcs  [NumFileChannels]*webrtc.DataChannel
	low  [NumFileChannels]chan struct{}
	open [NumFileChannels]chan struct{}
}

func (p *peer) addFileChannels() error {
	ordered := true
	fc := &FileChannels{}
	for i := 0; i < NumFileChannels; i++ {
		label := fmt.Sprintf("files.%d", i)
		dc, err := p.pc.CreateDataChannel(label, &webrtc.DataChannelInit{Ordered: &ordered})
		if err != nil {
			return err
		}
		p.channels[label] = dc
		fc.dcs[i] = dc
		fc.low[i] = make(chan struct{}, 1)
		fc.open[i] = make(chan struct{})
		idx := i
		dc.SetBufferedAmountLowThreshold(lowWater)
		dc.OnBufferedAmountLow(func() {
			select {
			case fc.low[idx] <- struct{}{}:
			default:
			}
		})
		openCh := fc.open[i]
		dc.OnOpen(func() { close(openCh) })
		dc.OnMessage(func(m webrtc.DataChannelMessage) {
			if m.IsString {
				return
			}
			if h := p.srv.fileHandler(); h != nil {
				h(p.sess, idx, m.Data)
			}
		})
	}
	p.sess.Set("files", fc)
	return nil
}

// N es la cantidad de canales de archivos.
func (fc *FileChannels) N() int { return NumFileChannels }

// SendFile envía un frame binario por files.<ch>.
func (fc *FileChannels) SendFile(ch int, b []byte) error {
	if ch < 0 || ch >= NumFileChannels {
		return errors.New("canal inválido")
	}
	dc := fc.dcs[ch]
	if dc.ReadyState() != webrtc.DataChannelStateOpen {
		return errors.New("canal de archivos cerrado")
	}
	return dc.Send(b)
}

// WaitFile bloquea hasta que el buffer de files.<ch> tenga lugar (≤ max bytes) o se
// cancele ctx.
func (fc *FileChannels) WaitFile(ctx context.Context, ch int, max uint64) error {
	if ch < 0 || ch >= NumFileChannels {
		return errors.New("canal inválido")
	}
	dc := fc.dcs[ch]
	select {
	case <-fc.open[ch]:
	case <-ctx.Done():
		return ctx.Err()
	}
	for dc.BufferedAmount() > max {
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			return errors.New("canal de archivos cerrado")
		}
		select {
		case <-fc.low[ch]:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
