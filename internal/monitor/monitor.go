// Package monitor convierte el estado de los volúmenes y discos en alertas
// (protocol/README.md §11): sin redundancia, solo lectura, errores de Btrfs, poco
// espacio y SMART. Evaluate es puro (se prueba con observaciones armadas a mano);
// tangod lo llama cada 10 s con lo que lee de sysfs.
package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Alert es exactamente el Alert de TS.
type Alert struct {
	ID         string `json:"id"`
	Level      string `json:"level"` // info | warn | crit
	Title      string `json:"title"`
	Body       string `json:"body"`
	At         string `json:"at"`
	Source     string `json:"source"` // md | btrfs | smart | space
	Resolved   bool   `json:"resolved"`
	ResolvedAt string `json:"resolvedAt,omitempty"`
	// Target es el volumen o disco de la alerta (para la UI: abrir el detalle).
	Target string `json:"target,omitempty"`
}

// VolumeObs es lo observado de un volumen.
type VolumeObs struct {
	ID, Name, Profile, State string
	Progress                 *float64
	Size, Used               uint64
	BtrfsErrors              uint64
}

// DiskObs es lo observado de un disco.
type DiskObs struct {
	ID, Label, Smart string
}

// Observation es una pasada del monitor.
type Observation struct {
	Volumes []VolumeObs
	Disks   []DiskObs
}

// Event es un cambio que merece un aviso al usuario.
type Event struct {
	Kind  string // raised | resolved | changed
	Alert Alert
}

// Umbrales de espacio libre (con histéresis para no oscilar).
const (
	spaceWarn    = 0.10
	spaceCrit    = 0.05
	spaceResolve = 0.12
)

type want struct {
	id, level, title, body, source, target string
}

// wanted son las alertas que corresponden a una observación.
func wanted(obs Observation, active map[string]Alert) []want {
	var out []want
	for _, v := range obs.Volumes {
		switch v.State {
		case "degraded":
			out = append(out, want{"md:" + v.ID + ":degraded", "crit", v.Name + " sin redundancia",
				"Un disco del RAID falló o falta. Tus datos siguen disponibles, pero si falla otro disco se pierden. Reemplazalo desde Almacenamiento.", "md", v.ID})
		case "building":
			// Mientras se reconstruye después de un reemplazo, sigue sin redundancia: la
			// alerta queda abierta con el progreso. Un RAID recién creado no tiene alerta.
			if a, ok := active["md:"+v.ID+":degraded"]; ok && !a.Resolved {
				body := "Se está reconstruyendo el RAID sobre el disco nuevo."
				if v.Progress != nil {
					body = fmt.Sprintf("Se está reconstruyendo el RAID sobre el disco nuevo (%d %%).", int(*v.Progress*100))
				}
				out = append(out, want{a.ID, "warn", v.Name + " reconstruyéndose", body, "md", v.ID})
			}
		case "readonly":
			out = append(out, want{"md:" + v.ID + ":readonly", "crit", v.Name + " quedó en solo lectura",
				"El kernel puso el volumen en solo lectura para proteger los datos. Revisá los discos antes de seguir escribiendo.", "md", v.ID})
		}
		if v.BtrfsErrors > 0 {
			out = append(out, want{"btrfs:" + v.ID + ":errors", "warn", "Errores de lectura o checksum en " + v.Name,
				fmt.Sprintf("Btrfs registró %d errores en %s. Ejecutá una verificación (scrub) y revisá el estado de los discos.", v.BtrfsErrors, v.Name), "btrfs", v.ID})
		}
		if v.Size > 0 {
			free := float64(v.Size-min(v.Used, v.Size)) / float64(v.Size)
			id := "space:" + v.ID + ":low"
			_, wasLow := active[id]
			wasLow = wasLow && !active[id].Resolved
			switch {
			case free < spaceCrit:
				out = append(out, want{id, "crit", v.Name + " está casi lleno", fmt.Sprintf("Queda %d %% libre. Liberá espacio o ampliá el volumen.", int(free*100)), "space", v.ID})
			case free < spaceWarn || (wasLow && free < spaceResolve):
				out = append(out, want{id, "warn", "Poco espacio en " + v.Name, fmt.Sprintf("Queda %d %% libre.", int(free*100)), "space", v.ID})
			}
		}
	}
	for _, d := range obs.Disks {
		switch d.Smart {
		case "crit":
			out = append(out, want{"smart:" + d.ID, "crit", d.Label + " está fallando", "SMART reporta una falla. Copiá lo importante y reemplazá el disco.", "smart", d.ID})
		case "warn":
			out = append(out, want{"smart:" + d.ID, "warn", d.Label + " tiene advertencias SMART", "El disco tiene sectores reasignados o pendientes. Vigilalo y tené un reemplazo a mano.", "smart", d.ID})
		}
	}
	return out
}

// Evaluate compara las alertas activas con una observación: abre las nuevas,
// actualiza las que cambiaron y resuelve las que ya no corresponden.
func Evaluate(active map[string]Alert, obs Observation, now time.Time) (map[string]Alert, []Event) {
	ts := now.UTC().Format(time.RFC3339)
	next := map[string]Alert{}
	var events []Event
	seen := map[string]bool{}
	for _, w := range wanted(obs, active) {
		seen[w.id] = true
		a, ok := active[w.id]
		switch {
		case !ok || a.Resolved:
			a = Alert{ID: w.id, Level: w.level, Title: w.title, Body: w.body, At: ts, Source: w.source, Target: w.target}
			events = append(events, Event{Kind: "raised", Alert: a})
		case a.Level != w.level || a.Title != w.title || a.Body != w.body:
			a.Level, a.Title, a.Body = w.level, w.title, w.body
			events = append(events, Event{Kind: "changed", Alert: a})
		}
		next[w.id] = a
	}
	for id, a := range active {
		if seen[id] {
			continue
		}
		if !a.Resolved {
			a.Resolved, a.ResolvedAt = true, ts
			events = append(events, Event{Kind: "resolved", Alert: a})
		}
		next[id] = a
	}
	return next, events
}

// ResolvedNotice es el título del aviso cuando se resuelve una alerta.
func ResolvedNotice(a Alert, volName func(string) string) (title, body string) {
	name := volName(a.Target)
	switch {
	case strings.HasSuffix(a.ID, ":degraded"):
		return name + " volvió a tener redundancia", "La reconstrucción terminó: el RAID está sano."
	case strings.HasPrefix(a.ID, "space:"):
		return "Hay espacio de nuevo en " + name, ""
	case strings.HasSuffix(a.ID, ":readonly"):
		return name + " volvió a escritura", ""
	}
	return "Se resolvió: " + a.Title, ""
}

// ---- Persistencia --------------------------------------------------------------------

// Store guarda las alertas (activas y las últimas resueltas) en alerts.json.
type Store struct {
	path string
	mu   sync.Mutex
	m    map[string]Alert
}

// keepResolved es cuántas resueltas se conservan.
const keepResolved = 50

// OpenStore carga el archivo (o empieza vacío).
func OpenStore(path string) *Store {
	s := &Store{path: path, m: map[string]Alert{}}
	if b, err := os.ReadFile(path); err == nil {
		var list []Alert
		if json.Unmarshal(b, &list) == nil {
			for _, a := range list {
				s.m[a.ID] = a
			}
		}
	}
	return s
}

// Active devuelve una copia del mapa de alertas.
func (s *Store) Active() map[string]Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Alert, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

// Replace guarda el resultado de Evaluate, podando resueltas viejas.
func (s *Store) Replace(m map[string]Alert) {
	list := make([]Alert, 0, len(m))
	for _, a := range m {
		list = append(list, a)
	}
	sortAlerts(list)
	var kept []Alert
	resolved := 0
	for _, a := range list {
		if a.Resolved {
			if resolved >= keepResolved {
				continue
			}
			resolved++
		}
		kept = append(kept, a)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m = map[string]Alert{}
	for _, a := range kept {
		s.m[a.ID] = a
	}
	if b, err := json.MarshalIndent(kept, "", "  "); err == nil && s.path != "" {
		tmp := s.path + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			_ = os.Rename(tmp, s.path)
		}
	}
}

// List devuelve las alertas: activas primero, después las resueltas, más nuevas primero.
func (s *Store) List() []Alert {
	s.mu.Lock()
	list := make([]Alert, 0, len(s.m))
	for _, a := range s.m {
		list = append(list, a)
	}
	s.mu.Unlock()
	sortAlerts(list)
	return list
}

func sortAlerts(list []Alert) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Resolved != list[j].Resolved {
			return !list[i].Resolved
		}
		ti, tj := list[i].At, list[j].At
		if list[i].Resolved {
			ti, tj = list[i].ResolvedAt, list[j].ResolvedAt
		}
		if ti != tj {
			return ti > tj
		}
		return list[i].ID < list[j].ID
	})
}

// ---- Lectura de sysfs --------------------------------------------------------------------

// BtrfsErrors suma los contadores de error de todos los dispositivos de un Btrfs
// (/sys/fs/btrfs/<uuid>/devinfo/<devid>/error_stats, kernel ≥ 5.14).
func BtrfsErrors(sysRoot, uuid string) uint64 {
	if !isUUID(uuid) {
		return 0
	}
	files, _ := filepath.Glob(filepath.Join(sysRoot, "fs", "btrfs", uuid, "devinfo", "*", "error_stats"))
	var total uint64
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 {
				n, _ := strconv.ParseUint(fields[1], 10, 64)
				total += n
			}
		}
	}
	return total
}

func isUUID(s string) bool {
	return len(s) == 36 && strings.Count(s, "-") == 4 && !strings.ContainsAny(s, "/.")
}
