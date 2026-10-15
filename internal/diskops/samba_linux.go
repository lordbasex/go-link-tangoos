//go:build linux

package diskops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrSambaMissing indica que Samba no está instalado.
var ErrSambaMissing = errors.New("samba no está instalado")

const (
	smbConf    = "/etc/samba/smb.conf"
	smbIndex   = "/etc/samba/tango.conf"
	smbShareDr = "/etc/samba/tango.d"
)

// ApplySambaShare publica una carpeta: escribe su sección, regenera el índice,
// asegura la línea include en smb.conf, valida con testparm (revirtiendo si falla)
// y recarga smbd por D-Bus si está activo.
func ApplySambaShare(ctx context.Context, name, path, comment string, opts ...ShareOpts) (bool, error) {
	return applySamba(ctx, name, SambaShare(name, path, comment, opts...))
}

// ApplySambaShareWithPerms publica una carpeta con acceso por grupos (ver
// SambaShareWithPerms).
func ApplySambaShareWithPerms(ctx context.Context, name, path, comment string, opts ...ShareOpts) (bool, error) {
	return applySamba(ctx, name, SambaShareWithPerms(name, path, comment, opts...))
}

// RemoveSambaShare despublica una carpeta (si estaba publicada) y recarga smbd.
func RemoveSambaShare(ctx context.Context, name string) (bool, error) {
	file := filepath.Join(smbShareDr, strings.ToLower(name)+".conf")
	if _, err := os.Stat(file); err != nil {
		return false, nil
	}
	if err := os.Remove(file); err != nil {
		return false, err
	}
	if err := writeIndex(); err != nil {
		return false, err
	}
	return ReloadIfActive(ctx, "smbd.service")
}

// SambaShareConf devuelve la sección actual de una carpeta ("" si no está publicada).
func SambaShareConf(name string) string {
	b, _ := os.ReadFile(filepath.Join(smbShareDr, strings.ToLower(name)+".conf"))
	return string(b)
}

func writeIndex() error {
	ents, _ := os.ReadDir(smbShareDr)
	var files []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".conf") && !strings.HasPrefix(e.Name(), ".") {
			files = append(files, e.Name())
		}
	}
	if err := writeAtomic(smbIndex, []byte(SambaIndex(files)), 0o644); err != nil {
		return err
	}
	writeAvahi(files)
	return nil
}

// writeAvahi deja el anuncio Bonjour al día con las carpetas de Time Machine. Avahi
// vigila /etc/avahi/services y lo recarga solo. Sin Avahi instalado no hace nada.
func writeAvahi(files []string) {
	dir := filepath.Dir(AvahiServiceFile)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return
	}
	var secs []string
	for _, f := range files {
		if b, err := os.ReadFile(filepath.Join(smbShareDr, f)); err == nil {
			secs = append(secs, string(b))
		}
	}
	body := AvahiSMBService(TimeMachineShares(secs))
	if cur, err := os.ReadFile(AvahiServiceFile); err == nil && string(cur) == body {
		return
	}
	_ = writeAtomic(AvahiServiceFile, []byte(body), 0o644)
}

func applySamba(ctx context.Context, name, section string) (bool, error) {
	cur, err := os.ReadFile(smbConf)
	if err != nil {
		return false, ErrSambaMissing
	}
	file := filepath.Join(smbShareDr, strings.ToLower(name)+".conf")
	prevShare, hadShare := readMaybe(file)
	prevIndex, hadIndex := readMaybe(smbIndex)
	// Primera vez con los globales para macOS: smbd tiene que reiniciarse (no alcanza un
	// reload) para dejar de anunciarse por su cuenta y no chocar con el de TangoOS.
	needRestart := !strings.Contains(string(prevIndex), "multicast dns register")
	if err := os.MkdirAll(smbShareDr, 0o755); err != nil {
		return false, err
	}
	if err := writeAtomic(file, []byte(section), 0o644); err != nil {
		return false, err
	}
	if err := writeIndex(); err != nil {
		return false, err
	}
	newConf, changed := EnsureSambaInclude(string(cur))
	if changed {
		if err := writeAtomic(smbConf, []byte(newConf), 0o644); err != nil {
			return false, err
		}
	}
	if err := ValidateSambaConf(ctx); err != nil {
		// Revertir: nunca dejar una configuración que smbd no acepta.
		restore(file, prevShare, hadShare)
		restore(smbIndex, prevIndex, hadIndex)
		if changed {
			_ = writeAtomic(smbConf, cur, 0o644)
		}
		return false, err
	}
	if needRestart {
		ok, err := RestartIfActive(ctx, "smbd.service")
		refreshAvahi() // Avahi descartó el grupo por la colisión: que lo vuelva a leer
		return ok, err
	}
	return ReloadIfActive(ctx, "smbd.service")
}

// refreshAvahi reescribe el archivo de servicios para que Avahi lo vuelva a cargar
// (lo vigila con inotify; si un grupo falló por colisión no reintenta solo).
func refreshAvahi() {
	b, err := os.ReadFile(AvahiServiceFile)
	if err != nil {
		return
	}
	_ = os.Remove(AvahiServiceFile)
	time.Sleep(300 * time.Millisecond)
	_ = writeAtomic(AvahiServiceFile, b, 0o644)
}

func readMaybe(p string) ([]byte, bool) {
	b, err := os.ReadFile(p)
	return b, err == nil
}

func restore(p string, b []byte, had bool) {
	if had {
		_ = writeAtomic(p, b, 0o644)
	} else {
		_ = os.Remove(p)
	}
}

// RemoveSambaSharesUnder despublica todas las carpetas cuyo path está dentro de mount
// (al borrar un volumen) y recarga smbd. Devuelve cuántas quitó.
func RemoveSambaSharesUnder(ctx context.Context, mount string) (int, error) {
	ents, _ := os.ReadDir(smbShareDr)
	n := 0
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		p := filepath.Join(smbShareDr, e.Name())
		b, err := os.ReadFile(p)
		if err != nil || !SambaPathUnder(string(b), mount) {
			continue
		}
		if err := os.Remove(p); err != nil {
			return n, err
		}
		n++
	}
	if n == 0 {
		return 0, nil
	}
	if err := writeIndex(); err != nil {
		return n, err
	}
	_, err := ReloadIfActive(ctx, "smbd.service")
	return n, err
}
