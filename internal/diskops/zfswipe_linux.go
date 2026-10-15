package diskops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// WipeZFSRegions borra lo que puede reaparecer cuando ZFS particiona el disco: la
// partición 1 de zpool empieza en 1 MiB y la 9 (reservada, 8 MiB) va al final. Un
// superbloque md o Btrfs viejo de una partición anterior que empezaba en el mismo
// lugar volvería a aparecer en sdX1 y udev armaría el md solo ("Device or resource
// busy" al crear el pool; visto en la VM de laboratorio). Se ponen en cero los
// primeros y últimos 17 MiB. Requiere el disco libre (O_EXCL).
func WipeZFSRegions(dev string) error {
	f, err := openExcl(dev, true)
	if err != nil {
		return err
	}
	defer f.Close()
	size, err := f.Seek(0, 2)
	if err != nil {
		return err
	}
	const span = 17 << 20
	zero := make([]byte, 1<<20)
	write := func(from int64) error {
		for off := from; off < from+span && off < size; off += int64(len(zero)) {
			if _, err := f.WriteAt(zero, off); err != nil {
				return err
			}
		}
		return nil
	}
	if err := write(0); err != nil {
		return err
	}
	if size > 2*span {
		if err := write(size - span); err != nil {
			return err
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	rereadPartitions(f)
	return nil
}

// busy indica si el disco o alguna de sus particiones está abierto en exclusiva por
// el kernel (un pool ZFS importado, un md, un dm). Un nodo que no existe no cuenta.
func (e Env) busy(disk string) bool {
	names := []string{disk}
	ents, _ := os.ReadDir(filepath.Join(e.Sys, "block", disk))
	for _, p := range ents {
		if strings.HasPrefix(p.Name(), disk) {
			names = append(names, p.Name())
		}
	}
	for _, n := range names {
		f, err := openExcl(filepath.Join(e.Dev, n), false)
		if errors.Is(err, ErrInUse) {
			return true
		}
		if err == nil {
			f.Close()
		}
	}
	return false
}
