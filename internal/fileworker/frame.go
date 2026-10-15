package fileworker

import (
	"encoding/binary"
	"errors"
)

// Formato de los frames de transferencia en los canales files.N (protocol/README.md §10):
//
//	[u32 xfer BE][u64 offset BE][datos]
const FrameHeader = 12

// EncodeFrame arma un frame en dst (que debe tener FrameHeader+len(data) de capacidad o se
// agranda) y lo devuelve.
func EncodeFrame(dst []byte, xfer uint32, offset uint64, data []byte) []byte {
	n := FrameHeader + len(data)
	if cap(dst) < n {
		dst = make([]byte, n)
	}
	dst = dst[:n]
	binary.BigEndian.PutUint32(dst[0:4], xfer)
	binary.BigEndian.PutUint64(dst[4:12], offset)
	copy(dst[FrameHeader:], data)
	return dst
}

// ErrShortFrame indica un frame sin cabecera completa.
var ErrShortFrame = errors.New("frame demasiado corto")

// DecodeFrame separa la cabecera de los datos (sin copiar).
func DecodeFrame(b []byte) (xfer uint32, offset uint64, data []byte, err error) {
	if len(b) < FrameHeader {
		return 0, 0, nil, ErrShortFrame
	}
	return binary.BigEndian.Uint32(b[0:4]), binary.BigEndian.Uint64(b[4:12]), b[FrameHeader:], nil
}
