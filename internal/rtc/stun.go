package rtc

import (
	"fmt"
	"io"
	"net"

	"github.com/pion/turn/v5"
)

// StartSTUN runs the embedded STUN server on UDP and TCP (docs/adr/0002).
// No AuthHandler: pion/turn answers Binding requests and rejects every
// allocation, so it is STUN only (TURN with ephemeral HMAC credentials
// arrives with TangoConnect, docs/12 cuña 4).
func StartSTUN(port int) (io.Closer, error) {
	udp, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("stun: UDP :%d: %w", port, err)
	}
	tcp, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		udp.Close()
		return nil, fmt.Errorf("stun: TCP :%d: %w", port, err)
	}
	srv, err := turn.NewServer(turn.ServerConfig{
		Realm:             "tango",
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: udp}},
		ListenerConfigs: []turn.ListenerConfig{{
			Listener:              tcp,
			RelayAddressGenerator: &turn.RelayAddressGeneratorNone{Address: "0.0.0.0"},
		}},
	})
	if err != nil {
		udp.Close()
		tcp.Close()
		return nil, err
	}
	return srv, nil
}
