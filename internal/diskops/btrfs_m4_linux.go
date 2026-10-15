//go:build linux

package diskops

import (
	"errors"
	"os"
	"unsafe"
)

// CreateWritableSnapshot crea una instantánea escribible de src en dstParent/name (la
// vuelta atrás del sistema arma así el nuevo @rootfs desde un punto read-only).
func CreateWritableSnapshot(src, dstParent, name string) error {
	if len(name) > subvolNameMax || name == "" {
		return errors.New("nombre de instantánea inválido")
	}
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	d, err := os.Open(dstParent)
	if err != nil {
		return err
	}
	defer d.Close()
	var a volArgsV2
	a.fd = int64(s.Fd())
	copy(a.name[:], name)
	return ioctl(d.Fd(), ioctlSnapCreateV2, unsafe.Pointer(&a))
}
