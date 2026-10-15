// Package updates interpreta apt y nombra los puntos de vuelta atrás del root
// (protocol/README.md §12, docs/08). Portable y con tests; lo usa el agente.
package updates

import (
	"bufio"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// instRe: "Inst libssl3t64 [3.5.1-1] (3.5.1-1+deb13u1 Debian-Security:13/stable-security [amd64])"
// o sin versión previa (paquete nuevo): "Inst foo (1.0 Debian:13/stable [amd64])".
var instRe = regexp.MustCompile(`^Inst (\S+) (?:\[([^\]]+)\] )?\((\S+) ([^\[]*)\[[^\]]*\]\)`)

// ParseSimulate lee la salida de `apt-get -s dist-upgrade` (en C.UTF-8).
func ParseSimulate(out string) []agentproto.PackageUpdate {
	res := []agentproto.PackageUpdate{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		m := instRe.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if m == nil {
			continue
		}
		origin := m[4]
		res = append(res, agentproto.PackageUpdate{
			Name: m[1], From: m[2], To: m[3],
			Security: strings.Contains(origin, "-Security") || strings.Contains(origin, "-security"),
		})
	}
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].Security != res[j].Security {
			return res[i].Security
		}
		return res[i].Name < res[j].Name
	})
	return res
}

// SecurityNames devuelve los paquetes de seguridad (para `--only-upgrade install`).
func SecurityNames(p []agentproto.PackageUpdate) []string {
	out := []string{}
	for _, u := range p {
		if u.Security {
			out = append(out, u.Name)
		}
	}
	return out
}

var pkgNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{1,127}$`)

// ValidPackageName: lo que apt acepta como nombre (nunca opciones ni rutas).
func ValidPackageName(n string) bool { return pkgNameRe.MatchString(n) }

// ---- Puntos de vuelta atrás ---------------------------------------------------------

// RootSnapPrefix: las instantáneas del root viven en @snapshots/rootfs-<fecha>.
const RootSnapPrefix = "rootfs-"

// SnapName arma el nombre de la instantánea previa a una actualización.
func SnapName(t time.Time) string {
	return RootSnapPrefix + t.UTC().Format("20060102-150405")
}

var snapRe = regexp.MustCompile(`^rootfs-(\d{8}-\d{6})$`)

// ValidSnapID: solo nombres que genera SnapName.
func ValidSnapID(id string) bool { return snapRe.MatchString(id) }

// SnapTime devuelve la fecha de una instantánea (cero si el nombre no es válido).
func SnapTime(id string) time.Time {
	m := snapRe.FindStringSubmatch(id)
	if m == nil {
		return time.Time{}
	}
	t, _ := time.Parse("20060102-150405", m[1])
	return t
}

// Points ordena los puntos (más nuevo primero) con etiqueta legible. labels trae la
// etiqueta guardada al crearlos ("Antes de actualizar 14 paquetes").
func Points(names []string, labels map[string]string) []agentproto.RollbackPoint {
	out := []agentproto.RollbackPoint{}
	for _, n := range names {
		if !ValidSnapID(n) {
			continue
		}
		l := labels[n]
		if l == "" {
			l = "Instantánea del sistema"
		}
		out = append(out, agentproto.RollbackPoint{ID: n, At: SnapTime(n).Format(time.RFC3339), Label: l})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// ---- Journal -----------------------------------------------------------------------

// Units mapea los alias del cliente a filtros de journalctl.
var Units = map[string][]string{
	"tangod":      {"-u", "tangod.service"},
	"tango-agent": {"-u", "tango-agent.service"},
	"smbd":        {"-u", "smbd.service", "-u", "nmbd.service"},
	"docker":      {"-u", "docker.service", "-u", "containerd.service"},
	"kernel":      {"-k"},
	"":            {"-u", "tangod.service", "-u", "tango-agent.service", "-u", "smbd.service", "-u", "nfs-server.service"},
}

// unitAlias convierte el _SYSTEMD_UNIT del journal en el alias corto.
func unitAlias(u string, transport string) string {
	switch {
	case transport == "kernel":
		return "kernel"
	case strings.HasPrefix(u, "tangod"):
		return "tangod"
	case strings.HasPrefix(u, "tango-agent"):
		return "tango-agent"
	case strings.HasPrefix(u, "smbd"), strings.HasPrefix(u, "nmbd"):
		return "smbd"
	case strings.HasPrefix(u, "docker"), strings.HasPrefix(u, "containerd"):
		return "docker"
	}
	return strings.TrimSuffix(u, ".service")
}

// ParseJournal lee `journalctl -o json` (una línea por registro). MESSAGE puede venir
// como array de bytes cuando no es UTF-8 válido.
func ParseJournal(out []byte, q string, limit int) []agentproto.LogLine {
	needle := strings.ToLower(q)
	lines := []agentproto.LogLine{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var r map[string]any
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		msg := ""
		switch m := r["MESSAGE"].(type) {
		case string:
			msg = m
		case []any:
			b := make([]byte, 0, len(m))
			for _, x := range m {
				if f, ok := x.(float64); ok {
					b = append(b, byte(f))
				}
			}
			msg = strings.ToValidUTF8(string(b), "�")
		}
		if needle != "" && !strings.Contains(strings.ToLower(msg), needle) {
			continue
		}
		at := ""
		if us, ok := r["__REALTIME_TIMESTAMP"].(string); ok {
			var n int64
			for _, c := range us {
				if c < '0' || c > '9' {
					n = 0
					break
				}
				n = n*10 + int64(c-'0')
			}
			if n > 0 {
				at = time.UnixMicro(n).UTC().Format(time.RFC3339Nano)
			}
		}
		prio := 6
		if p, ok := r["PRIORITY"].(string); ok && len(p) == 1 && p[0] >= '0' && p[0] <= '7' {
			prio = int(p[0] - '0')
		}
		unit, _ := r["_SYSTEMD_UNIT"].(string)
		tr, _ := r["_TRANSPORT"].(string)
		lines = append(lines, agentproto.LogLine{At: at, Unit: unitAlias(unit, tr), Prio: prio, Msg: Redact(msg)})
	}
	// journalctl -r ya los da del más nuevo al más viejo; se recorta al límite.
	if limit > 0 && len(lines) > limit {
		lines = lines[:limit]
	}
	return lines
}

var secretRe = regexp.MustCompile(`(?i)(password|passwd|secret|token|apikey|api_key)(["']?\s*[=:]\s*["']?)[^\s"',}]+`)

// Redact tacha valores que parecen secretos en una línea de log.
func Redact(s string) string { return secretRe.ReplaceAllString(s, "$1$2***") }
