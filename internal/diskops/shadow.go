package diskops

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
)

// ShadowLinkDir es el directorio, dentro de /<vol>/@snapshots/<Carpeta>, con un symlink
// por instantánea nombrado solo con la hora UTC ("2026-10-15T02-00-04Z" →
// "../2026-10-15T02-00-04Z.hourly"). vfs_shadow_copy2 necesita nombres que coincidan
// exactamente con shadow:format: con el sufijo de nivel los lista pero no los abre.
const ShadowLinkDir = ".gmt"

// SyncShadowLinks deja .gmt al día con las instantáneas de snapDir: crea los enlaces que
// faltan y borra los que sobran. Si dos instantáneas tienen la misma hora (programadas de
// distinto nivel en el mismo momento) el enlace apunta a cualquiera: son idénticas.
func SyncShadowLinks(snapDir string) error {
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		return err
	}
	links := filepath.Join(snapDir, ShadowLinkDir)
	if err := os.MkdirAll(links, 0o755); err != nil {
		return err
	}
	ents, err := os.ReadDir(snapDir)
	if err != nil {
		return err
	}
	want := map[string]string{} // hora → instantánea
	var names []string
	for _, e := range ents {
		if !e.IsDir() || ValidSnapshotName(e.Name()) != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names) // determinístico: a igual hora gana la primera en orden
	for _, n := range names {
		base, _ := SplitSnapshotLevel(n)
		if _, ok := want[base]; !ok {
			want[base] = n
		}
	}
	have, err := os.ReadDir(links)
	if err != nil {
		return err
	}
	var errs []error
	for _, h := range have {
		p := filepath.Join(links, h.Name())
		target, rerr := os.Readlink(p)
		if rerr == nil && want[h.Name()] != "" && target == "../"+want[h.Name()] {
			delete(want, h.Name()) // ya está bien
			continue
		}
		if h.Type()&os.ModeSymlink == 0 {
			continue // nunca borrar algo que no sea un enlace nuestro
		}
		if err := os.Remove(p); err != nil {
			errs = append(errs, err)
		}
	}
	for base, n := range want {
		if err := os.Symlink("../"+n, filepath.Join(links, base)); err != nil && !errors.Is(err, os.ErrExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
