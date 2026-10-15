//go:build linux

package diskops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Particiones de los grupos híbridos (ADR 0011): la GPT se escribe en Go sobre un disco
// tomado con O_EXCL y el kernel la relee con BLKRRPART.

// SectorSize devuelve el tamaño de sector lógico del dispositivo.
func SectorSize(f *os.File) (uint64, error) {
	var n int32
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, f.Fd(), unix.BLKSSZGET, uintptr(unsafe.Pointer(&n))); e != 0 {
		return 0, e
	}
	return uint64(n), nil
}

// PartDev devuelve el dispositivo de la partición n de un disco ("/dev/sdb" → "/dev/sdb2",
// "/dev/nvme0n1" → "/dev/nvme0n1p2").
func PartDev(disk string, n int) string {
	if last := disk[len(disk)-1]; last >= '0' && last <= '9' {
		return fmt.Sprintf("%sp%d", disk, n)
	}
	return fmt.Sprintf("%s%d", disk, n)
}

// WritePartitions escribe una GPT nueva con estas particiones. El disco tiene que estar
// libre (O_EXCL) y ya sin firmas (Wipe). Espera a que el kernel publique las particiones.
func WritePartitions(ctx context.Context, dev string, parts []GPTPart) error {
	f, err := openExcl(dev, true)
	if err != nil {
		return err
	}
	size, err := blkSize(f)
	if err != nil {
		f.Close()
		return err
	}
	sector, err := SectorSize(f)
	if err != nil {
		f.Close()
		return err
	}
	img, err := BuildGPT(size, sector, "", parts)
	if err != nil {
		f.Close()
		return err
	}
	if _, err := f.WriteAt(img.Head, 0); err != nil {
		f.Close()
		return fmt.Errorf("escribir la GPT: %w", err)
	}
	if _, err := f.WriteAt(img.Tail, int64(img.TailOff)); err != nil {
		f.Close()
		return fmt.Errorf("escribir la copia de la GPT: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	var rrErr error
	for range 20 { // udev puede tener el disco abierto un instante después de escribir
		if _, _, e := unix.Syscall(unix.SYS_IOCTL, f.Fd(), unix.BLKRRPART, 0); e == 0 {
			rrErr = nil
			break
		} else {
			rrErr = e
		}
		time.Sleep(250 * time.Millisecond)
	}
	f.Close()
	if rrErr != nil {
		return fmt.Errorf("el kernel no releyó la tabla de particiones: %w", rrErr)
	}
	for i := range parts {
		p := PartDev(dev, i+1)
		ok := false
		for range 100 {
			if _, err := os.Stat(p); err == nil {
				ok = true
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		if !ok {
			return errors.New("udev no publicó " + p)
		}
	}
	return nil
}

// ReadPartitions lee la GPT de un disco (aunque esté en uso: solo lectura, sin O_EXCL).
func ReadPartitions(dev string) ([]GPTEntry, error) {
	f, err := os.Open(dev)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sector, err := SectorSize(f)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 2*sector+gptEntries*gptEntrySize)
	if _, err := f.ReadAt(head, 0); err != nil {
		return nil, err
	}
	return ParseGPT(head, sector)
}

// PartName lee el nombre GPT de una partición (sdb2) desde su uevent (PARTNAME=).
func PartName(part string) string {
	b, _ := os.ReadFile(filepath.Join("/sys/class/block", part, "uevent"))
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "PARTNAME="); ok {
			return v
		}
	}
	return ""
}

// Partitions lista las particiones (sdb1, sdb2…) de un disco en sysfs.
func Partitions(disk string) []string {
	ents, _ := os.ReadDir(filepath.Join("/sys/block", disk))
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), disk) {
			if _, err := os.Stat(filepath.Join("/sys/block", disk, e.Name(), "partition")); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	return out
}
