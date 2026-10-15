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

// ErrInUse indica que el kernel tiene el disco tomado (montado, en un md, abierto en exclusivo).
var ErrInUse = errors.New("el disco está en uso")

// openExcl abre un dispositivo de bloque con O_EXCL: el kernel lo rechaza (EBUSY) si
// está montado o tomado por md/dm. Es la última barrera antes de escribir.
func openExcl(dev string, write bool) (*os.File, error) {
	flags := unix.O_RDONLY
	if write {
		flags = unix.O_RDWR
	}
	fd, err := unix.Open(dev, flags|unix.O_EXCL|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.EBUSY) {
		return nil, ErrInUse
	}
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), dev), nil
}

func blkSize(f *os.File) (uint64, error) {
	var size uint64
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, f.Fd(), unix.BLKGETSIZE64, uintptr(unsafe.Pointer(&size))); e != 0 {
		return 0, e
	}
	return size, nil
}

// Snapshot es lo que se lee de un disco para planear y revalidar.
type Snapshot struct {
	Dev         string
	Size        uint64
	Fingerprint string
	Content     string
}

// ReadDisk lee tamaño, cabecera y cola del disco (con O_EXCL: falla si está en uso).
func ReadDisk(dev string) (Snapshot, error) {
	f, err := openExcl(dev, false)
	if err != nil {
		return Snapshot{}, err
	}
	defer f.Close()
	size, err := blkSize(f)
	if err != nil {
		return Snapshot{}, err
	}
	head := make([]byte, min(uint64(HeadSize), size))
	if _, err := f.ReadAt(head, 0); err != nil {
		return Snapshot{}, fmt.Errorf("leer cabecera: %w", err)
	}
	tl := min(uint64(TailSize), size)
	tail := make([]byte, tl)
	if _, err := f.ReadAt(tail, int64(size-tl)); err != nil {
		return Snapshot{}, fmt.Errorf("leer cola: %w", err)
	}
	return Snapshot{Dev: dev, Size: size, Fingerprint: Fingerprint(size, head, tail), Content: DetectContent(head, tail)}, nil
}

// wipeRanges son las zonas que se ponen en cero para borrar firmas (equivalente a
// wipefs, en Go): primer y último MiB (MBR, GPT y su copia, md 1.x, ext4, XFS, ZFS),
// y las copias del superbloque de Btrfs a 64 MiB y 256 GiB.
func wipeRanges(size uint64) [][2]uint64 {
	const mib = 1 << 20
	r := [][2]uint64{{0, min(mib, size)}}
	if size > mib {
		r = append(r, [2]uint64{size - mib, mib})
	}
	for _, off := range []uint64{64 << 20, 256 << 30} {
		if off+64<<10 < size-mib {
			r = append(r, [2]uint64{off, 64 << 10})
		}
	}
	return r
}

// Wipe borra las firmas del disco. Exige que la huella siga siendo la del plan.
func Wipe(dev, wantFingerprint string) error {
	snap, err := ReadDisk(dev)
	if err != nil {
		return err
	}
	if snap.Fingerprint != wantFingerprint {
		return ErrChanged
	}
	f, err := openExcl(dev, true)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, r := range wipeRanges(snap.Size) {
		if _, err := f.WriteAt(make([]byte, r[1]), int64(r[0])); err != nil {
			return fmt.Errorf("borrar firmas: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	// Que el kernel relea la tabla de particiones (ya no hay ninguna).
	_, _, _ = unix.Syscall(unix.SYS_IOCTL, f.Fd(), unix.BLKRRPART, 0)
	return nil
}

// WipeMember borra las firmas de un ex miembro de un volumen que se está eliminando.
// No compara huella (el disco tiene datos que cambian): la identidad se validó antes
// contra los miembros reales del array. O_EXCL garantiza que ya no está en uso.
func WipeMember(dev string) error {
	f, err := openExcl(dev, true)
	if err != nil {
		return err
	}
	defer f.Close()
	size, err := blkSize(f)
	if err != nil {
		return err
	}
	for _, r := range wipeRanges(size) {
		if _, err := f.WriteAt(make([]byte, r[1]), int64(r[0])); err != nil {
			return fmt.Errorf("borrar firmas: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	_, _, _ = unix.Syscall(unix.SYS_IOCTL, f.Fd(), unix.BLKRRPART, 0)
	return nil
}

// ErrChanged indica que el disco cambió desde el plan.
var ErrChanged = errors.New("el disco cambió desde que se armó el plan")

// DevName devuelve "sdb" para "/dev/sdb".
func DevName(dev string) string { return filepath.Base(dev) }
