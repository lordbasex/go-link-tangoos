package diskops

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Nombres de volumen: minúsculas y dígitos, sin guiones (así la unidad .mount no
// necesita escaparse: /volume1 → volume1.mount).
var volumeNameRe = regexp.MustCompile(`^[a-z][a-z0-9]{0,31}$`)

// Directorios del sistema que nunca pueden ser un punto de montaje de volumen.
var reservedNames = map[string]bool{
	"bin": true, "boot": true, "dev": true, "etc": true, "home": true, "lib": true, "lib32": true, "lib64": true,
	"libx32": true, "media": true, "mnt": true, "opt": true, "proc": true, "root": true, "run": true, "sbin": true,
	"srv": true, "sys": true, "tmp": true, "usr": true, "var": true, "import": true, "lost": true, "snap": true,
}

// ValidVolumeName valida un nombre de volumen.
func ValidVolumeName(name string) error {
	if !volumeNameRe.MatchString(name) {
		return fmt.Errorf("el nombre debe empezar con una letra y tener solo minúsculas y números (máx. 32)")
	}
	if reservedNames[name] {
		return fmt.Errorf("«%s» es un directorio del sistema", name)
	}
	return nil
}

// Nombres de carpetas compartidas: también son nombres de recurso SMB y de subvolumen.
var shareNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// SMB reserva estos nombres de sección.
var reservedShares = map[string]bool{"global": true, "homes": false, "printers": true, "print$": true, "ipc$": true}

// ValidShareName valida un nombre de carpeta compartida.
func ValidShareName(name string) error {
	if !shareNameRe.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Errorf("usá letras, números, punto, guion o guion bajo (máx. 64), sin espacios")
	}
	if reservedShares[strings.ToLower(name)] {
		return fmt.Errorf("«%s» es un nombre reservado de SMB", name)
	}
	return nil
}

// ValidMount acepta solo puntos de montaje de volúmenes TangoOS: "/<nombre de volumen>".
func ValidMount(mount string) error {
	if !strings.HasPrefix(mount, "/") || strings.Count(mount, "/") != 1 {
		return fmt.Errorf("punto de montaje inválido")
	}
	return ValidVolumeName(mount[1:])
}

// Las rutas de subvolumen son relativas al volumen: "@shares/Familia", "@apps/jellyfin".
var subvolPathRe = regexp.MustCompile(`^@(shares|snapshots|apps)(/[A-Za-z0-9][A-Za-z0-9_.-]{0,63}){0,2}$`)

// ValidSubvolPath valida una ruta de subvolumen relativa al volumen.
func ValidSubvolPath(p string) error {
	if !subvolPathRe.MatchString(p) || strings.Contains(p, "..") {
		return fmt.Errorf("ruta de subvolumen inválida")
	}
	return nil
}

// SnapshotName nombra una instantánea por su hora UTC; ordena cronológicamente.
func SnapshotName(t time.Time) string { return t.UTC().Format("2006-01-02T15-04-05Z") }

// ParseSnapshotName es la inversa de SnapshotName. Acepta el sufijo de nivel de las
// instantáneas programadas ("…Z.hourly", ver ScheduledSnapshotName).
func ParseSnapshotName(s string) (time.Time, bool) {
	s, _ = SplitSnapshotLevel(s)
	t, err := time.Parse("2006-01-02T15-04-05Z", s)
	return t, err == nil
}

// Niveles de las instantáneas programadas (protocol/README.md §11).
var SnapshotLevels = []string{"hourly", "daily", "weekly"}

// ScheduledSnapshotName nombra una instantánea programada: hora UTC + "." + nivel.
// Sigue ordenando cronológicamente y la retención de cada nivel la reconoce.
func ScheduledSnapshotName(t time.Time, level string) string { return SnapshotName(t) + "." + level }

// SplitSnapshotLevel separa el nivel de un nombre ("" = instantánea manual).
func SplitSnapshotLevel(s string) (base, level string) {
	for _, l := range SnapshotLevels {
		if b, ok := strings.CutSuffix(s, "."+l); ok {
			return b, l
		}
	}
	return s, ""
}

var snapNameRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}Z(\.(hourly|daily|weekly))?$`)

// ValidSnapshotName valida un nombre producido por SnapshotName.
func ValidSnapshotName(s string) error {
	if !snapNameRe.MatchString(s) {
		return fmt.Errorf("nombre de instantánea inválido")
	}
	return nil
}
