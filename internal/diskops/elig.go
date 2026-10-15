package diskops

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Env son las raíces del sistema; los tests las apuntan a fixtures.
type Env struct {
	Sys  string // /sys
	Proc string // /proc
	Dev  string // /dev
}

// DefaultEnv es el sistema real. Se lee /proc/1/mountinfo (el de PID 1) y no el
// propio: el agente corre en su propio namespace de montaje (ProtectSystem) y los
// montajes del host son los que importan.
var DefaultEnv = Env{Sys: "/sys", Proc: "/proc", Dev: "/dev"}

// systemMounts son los puntos de montaje cuyo disco es "del sistema".
var systemMounts = map[string]bool{"/": true, "/boot": true, "/boot/efi": true, "/usr": true, "/var": true}

type mount struct{ majMin, point, fstype, source string }

func (e Env) mounts() []mount {
	f, err := os.Open(filepath.Join(e.Proc, "1", "mountinfo"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []mount
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		pre, post, ok := strings.Cut(sc.Text(), " - ")
		if !ok {
			continue
		}
		fs, gs := strings.Fields(pre), strings.Fields(post)
		if len(fs) >= 5 && len(gs) >= 2 {
			out = append(out, mount{majMin: fs[2], point: fs[4], fstype: gs[0], source: gs[1]})
		}
	}
	return out
}

// disksUnder baja de un dispositivo (partición, md, dm) a los discos enteros que lo forman.
func (e Env) disksUnder(sysPath string, depth int) []string {
	if depth > 8 {
		return nil
	}
	if _, err := os.Stat(filepath.Join(sysPath, "partition")); err == nil {
		return []string{filepath.Base(filepath.Dir(sysPath))}
	}
	slaves, _ := os.ReadDir(filepath.Join(sysPath, "slaves"))
	if len(slaves) > 0 {
		var out []string
		for _, s := range slaves {
			if p, err := filepath.EvalSymlinks(filepath.Join(sysPath, "slaves", s.Name())); err == nil {
				out = append(out, e.disksUnder(p, depth+1)...)
			}
		}
		return out
	}
	return []string{filepath.Base(sysPath)}
}

func (e Env) diskOf(majMin string) []string {
	p, err := filepath.EvalSymlinks(filepath.Join(e.Sys, "dev", "block", majMin))
	if err != nil {
		return nil
	}
	return e.disksUnder(p, 0)
}

// disksOfMount resuelve los discos de un montaje. Btrfs informa un major:minor
// anónimo (0:N) en mountinfo: en ese caso se usa la fuente (/dev/md127, /dev/sdb1…).
func (e Env) disksOfMount(m mount) []string {
	if !strings.HasPrefix(m.majMin, "0:") {
		return e.diskOf(m.majMin)
	}
	if !strings.HasPrefix(m.source, "/dev/") {
		return nil
	}
	p, err := filepath.EvalSymlinks(filepath.Join(e.Dev, strings.TrimPrefix(m.source, "/dev/")))
	if err != nil {
		return nil
	}
	sp, err := filepath.EvalSymlinks(filepath.Join(e.Sys, "class", "block", filepath.Base(p)))
	if err != nil {
		return nil
	}
	return e.disksUnder(sp, 0)
}

// SystemDisks devuelve los discos enteros que respaldan el sistema operativo.
func (e Env) SystemDisks() map[string]bool {
	out := map[string]bool{}
	for _, m := range e.mounts() {
		if systemMounts[m.point] {
			for _, d := range e.disksOfMount(m) {
				out[d] = true
			}
		}
	}
	return out
}

// MountedDisks devuelve los discos enteros con algo montado (directo o vía partición/md/dm).
func (e Env) MountedDisks() map[string]bool {
	out := map[string]bool{}
	for _, m := range e.mounts() {
		for _, d := range e.disksOfMount(m) { // los virtuales (proc, tmpfs…) no resuelven a nada
			out[d] = true
		}
	}
	return out
}

// HasHolders indica si el disco o alguna de sus particiones está tomado por md/dm.
func (e Env) HasHolders(disk string) bool {
	base := filepath.Join(e.Sys, "block", disk)
	if hs, _ := os.ReadDir(filepath.Join(base, "holders")); len(hs) > 0 {
		return true
	}
	ents, _ := os.ReadDir(base)
	for _, p := range ents {
		if strings.HasPrefix(p.Name(), disk) {
			if hs, _ := os.ReadDir(filepath.Join(base, p.Name(), "holders")); len(hs) > 0 {
				return true
			}
		}
	}
	return false
}

// Eligible decide si un disco entero puede formar parte de un volumen nuevo.
// Es la misma regla en el plan (tangod vía disk.inspect) y en la ejecución
// (el agente revalida justo antes de escribir): docs/04 "Ciclo de vida".
func (e Env) Eligible(disk string) (bool, string) {
	if disk == "" || strings.ContainsAny(disk, "/.") {
		return false, "nombre de dispositivo inválido"
	}
	base := filepath.Join(e.Sys, "block", disk)
	if _, err := os.Stat(base); err != nil {
		return false, "el disco ya no está"
	}
	if _, err := os.Stat(filepath.Join(base, "partition")); err == nil {
		return false, "es una partición: se usa el disco entero"
	}
	if e.SystemDisks()[disk] {
		return false, "es el disco del sistema"
	}
	if e.MountedDisks()[disk] {
		return false, "tiene particiones montadas"
	}
	if e.HasHolders(disk) {
		return false, "ya es miembro de un RAID o de otro dispositivo"
	}
	if ro := strings.TrimSpace(readFile(filepath.Join(base, "ro"))); ro == "1" {
		return false, "es de solo lectura"
	}
	if rm := strings.TrimSpace(readFile(filepath.Join(base, "removable"))); rm == "1" {
		return false, "es removible (USB): solo sirve como destino de respaldo"
	}
	return true, ""
}

func readFile(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

// MountSource devuelve el dispositivo y el tipo de filesystem montado en point
// (p. ej. "/dev/md127", "btrfs"). En Btrfs el major:minor de mountinfo es anónimo,
// por eso se usa la fuente.
func (e Env) MountSource(point string) (source, fstype string, ok bool) {
	for _, m := range e.mounts() {
		if m.point == point {
			return m.source, m.fstype, true
		}
	}
	return "", "", false
}
