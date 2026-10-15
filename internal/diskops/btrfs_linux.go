//go:build linux

package diskops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ioctls de Btrfs (include/uapi/linux/btrfs.h), sin btrfs-progs (ADR 0009).
const (
	btrfsIoctlMagic = 0x94
	volNameMax      = 4087
	subvolNameMax   = 4039
	subvolRdonly    = 1 << 1
)

func iow(nr, size uintptr) uintptr  { return 1<<30 | size<<16 | btrfsIoctlMagic<<8 | nr }
func iowr(nr, size uintptr) uintptr { return 3<<30 | size<<16 | btrfsIoctlMagic<<8 | nr }

type volArgs struct {
	fd   int64
	name [volNameMax + 1]byte
}

type volArgsV2 struct {
	fd      int64
	transid uint64
	flags   uint64
	unused  [4]uint64
	name    [subvolNameMax + 1]byte
}

// ScrubProgress es struct btrfs_scrub_progress.
type ScrubProgress struct {
	DataExtentsScrubbed uint64
	TreeExtentsScrubbed uint64
	DataBytesScrubbed   uint64
	TreeBytesScrubbed   uint64
	ReadErrors          uint64
	CsumErrors          uint64
	VerifyErrors        uint64
	NoCsum              uint64
	CsumDiscards        uint64
	SuperErrors         uint64
	MallocErrors        uint64
	UncorrectableErrors uint64
	CorrectedErrors     uint64
	LastPhysical        uint64
	UnverifiedErrors    uint64
}

type scrubArgs struct {
	devid    uint64
	start    uint64
	end      uint64
	flags    uint64
	progress ScrubProgress
	unused   [(1024 - 32 - unsafe.Sizeof(ScrubProgress{})) / 8]uint64
}

var (
	ioctlSubvolCreate  = iow(14, unsafe.Sizeof(volArgs{}))
	ioctlSnapDestroy   = iow(15, unsafe.Sizeof(volArgs{}))
	ioctlSnapCreateV2  = iow(23, unsafe.Sizeof(volArgsV2{}))
	ioctlScrub         = iowr(27, unsafe.Sizeof(scrubArgs{}))
	ioctlScrubProgress = iowr(29, unsafe.Sizeof(scrubArgs{}))
)

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// CreateSubvolume crea parent/name como subvolumen Btrfs.
func CreateSubvolume(parent, name string) error {
	if len(name) > volNameMax || name == "" {
		return errors.New("nombre de subvolumen inválido")
	}
	if _, err := os.Lstat(filepath.Join(parent, name)); err == nil {
		return os.ErrExist
	}
	d, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer d.Close()
	var a volArgs
	copy(a.name[:], name)
	return ioctl(d.Fd(), ioctlSubvolCreate, unsafe.Pointer(&a))
}

// CreateSnapshot crea una instantánea read-only de src en dstParent/name.
func CreateSnapshot(src, dstParent, name string) error {
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
	a.flags = subvolRdonly
	copy(a.name[:], name)
	return ioctl(d.Fd(), ioctlSnapCreateV2, unsafe.Pointer(&a))
}

// Scrub verifica checksums de todo el filesystem montado en mount (dispositivo 1;
// los volúmenes TangoOS son Btrfs de un solo dispositivo sobre md, ADR 0003).
// Bloquea hasta terminar; progress se llama periódicamente desde otra goroutine.
func Scrub(mount string, progress func(ScrubProgress)) (ScrubProgress, error) {
	f, err := os.Open(mount)
	if err != nil {
		return ScrubProgress{}, err
	}
	defer f.Close()
	done := make(chan struct{})
	if progress != nil {
		go func() {
			for {
				select {
				case <-done:
					return
				case <-sleepTick():
				}
				var p scrubArgs
				p.devid = 1
				if ioctl(f.Fd(), ioctlScrubProgress, unsafe.Pointer(&p)) == nil {
					progress(p.progress)
				}
			}
		}()
	}
	var a scrubArgs
	a.devid, a.end = 1, ^uint64(0)
	err = ioctl(f.Fd(), ioctlScrub, unsafe.Pointer(&a))
	close(done)
	if err != nil {
		return a.progress, fmt.Errorf("scrub: %w", err)
	}
	return a.progress, nil
}

// DestroySubvolume borra el subvolumen parent/name (con todo su contenido). Es la
// operación de BTRFS_IOC_SNAP_DESTROY: sirve para instantáneas y subvolúmenes.
func DestroySubvolume(parent, name string) error {
	if name == "" || len(name) > volNameMax || name == "." || name == ".." || filepath.Base(name) != name {
		return errors.New("nombre de subvolumen inválido")
	}
	if _, err := os.Lstat(filepath.Join(parent, name)); err != nil {
		return err
	}
	d, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer d.Close()
	var a volArgs
	copy(a.name[:], name)
	return ioctl(d.Fd(), ioctlSnapDestroy, unsafe.Pointer(&a))
}
