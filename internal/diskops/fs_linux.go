//go:build linux

package diskops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// Filesystems de los volúmenes de un grupo (ADR 0011): Btrfs por defecto, XFS o ext4 para
// usos especiales. El UUID se lee del superbloque en Go.

// ReadFSInfo identifica lo que hay al comienzo de un dispositivo (filesystem o LUKS).
func ReadFSInfo(dev string) (fstype, uuid string) {
	f, err := os.Open(dev)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	head := make([]byte, HeadSize)
	n, _ := f.ReadAt(head, 0)
	return FSInfo(head[:n])
}

// Mkfs crea el filesystem fs en dev y devuelve su UUID.
func Mkfs(ctx context.Context, fs, label, dev string) (string, error) {
	if err := validDevPath(dev); err != nil {
		return "", err
	}
	if err := ValidVolumeName(label); err != nil {
		return "", err
	}
	switch fs {
	case "btrfs":
		return MkfsBtrfs(ctx, label, dev)
	case "xfs":
		l := label
		if len(l) > 12 { // XFS admite etiquetas de hasta 12 caracteres
			l = l[:12]
		}
		if _, err := run(ctx, "mkfs.xfs", "-f", "-L", l, dev); err != nil {
			return "", err
		}
	case "ext4":
		l := label
		if len(l) > 16 {
			l = l[:16]
		}
		if _, err := run(ctx, "mkfs.ext4", "-F", "-q", "-L", l, "-E", "lazy_itable_init=1,lazy_journal_init=1", dev); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("filesystem no soportado: %s", fs)
	}
	got, uuid := ReadFSInfo(dev)
	if got != fs || uuid == "" {
		return "", errors.New("mkfs terminó pero no se ve el filesystem")
	}
	return uuid, nil
}

// GrowFS agranda el filesystem montado en mount (dev es su dispositivo) hasta llenarlo.
func GrowFS(ctx context.Context, fs, mount, dev string) error {
	switch fs {
	case "btrfs":
		return BtrfsResizeMax(mount)
	case "xfs":
		_, err := run(ctx, "xfs_growfs", mount)
		return err
	case "ext4":
		_, err := run(ctx, "resize2fs", dev)
		return err
	}
	return fmt.Errorf("filesystem no soportado: %s", fs)
}

// WaitDev espera a que aparezca un nodo de /dev (udev después de lvcreate o de abrir LUKS).
func WaitDev(ctx context.Context, dev string) error {
	for range 100 {
		if _, err := os.Stat(dev); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("no apareció " + dev)
}
