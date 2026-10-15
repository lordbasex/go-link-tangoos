// Package snapsched decide cuándo corresponde una instantánea programada y cuáles
// sobran según la retención (protocol/README.md §11). No toca discos: tangod
// (internal/api) lo consulta y le pide al agente crear o borrar.
package snapsched

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Hourly, Daily y Weekly son los niveles de una política.
type Hourly struct {
	Every int `json:"every"` // 1 | 2 | 4 | 6 | 12 (horas)
	Keep  int `json:"keep"`
}

type Daily struct {
	At   string `json:"at"` // "HH:MM" hora local
	Keep int    `json:"keep"`
}

type Weekly struct {
	Day  int    `json:"day"` // 0 = domingo … 6 = sábado
	At   string `json:"at"`
	Keep int    `json:"keep"`
}

// Schedule es la política de una carpeta compartida. Keep 0 desactiva el nivel.
type Schedule struct {
	Enabled bool   `json:"enabled"`
	Hourly  Hourly `json:"hourly"`
	Daily   Daily  `json:"daily"`
	Weekly  Weekly `json:"weekly"`
}

// Default es la política de las carpetas nuevas (y de las que nunca se configuraron).
func Default() Schedule {
	return Schedule{
		Enabled: true,
		Hourly:  Hourly{Every: 1, Keep: 24},
		Daily:   Daily{At: "03:00", Keep: 14},
		Weekly:  Weekly{Day: 0, At: "03:30", Keep: 8},
	}
}

// Levels en el orden en que se evalúan.
var Levels = []string{"hourly", "daily", "weekly"}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// Validate revisa una política que llega de la UI.
func (s Schedule) Validate() error {
	switch s.Hourly.Every {
	case 1, 2, 4, 6, 12:
	default:
		return fmt.Errorf("cada cuántas horas: 1, 2, 4, 6 o 12")
	}
	if !hhmm.MatchString(s.Daily.At) || !hhmm.MatchString(s.Weekly.At) {
		return fmt.Errorf("las horas van como HH:MM")
	}
	if s.Weekly.Day < 0 || s.Weekly.Day > 6 {
		return fmt.Errorf("día de la semana inválido")
	}
	for _, k := range []int{s.Hourly.Keep, s.Daily.Keep, s.Weekly.Keep} {
		if k < 0 || k > 1000 {
			return fmt.Errorf("se conservan entre 0 y 1000 instantáneas por nivel")
		}
	}
	return nil
}

// Keep devuelve cuántas conserva un nivel (0 = nivel apagado).
func (s Schedule) Keep(level string) int {
	switch level {
	case "hourly":
		return s.Hourly.Keep
	case "daily":
		return s.Daily.Keep
	case "weekly":
		return s.Weekly.Keep
	}
	return 0
}

func clock(at string) (int, int) {
	var h, m int
	_, _ = fmt.Sscanf(at, "%d:%d", &h, &m)
	return h, m
}

// PrevSlot es el último momento programado de un nivel que ya pasó (≤ now), en la
// zona horaria de now. unit es la duración de una "hora" (time.Hour; en pruebas, un
// minuto acelera el nivel horario).
func PrevSlot(s Schedule, level string, now time.Time, unit time.Duration) time.Time {
	loc := now.Location()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	switch level {
	case "hourly":
		step := time.Duration(max(s.Hourly.Every, 1)) * unit
		if unit < time.Hour { // modo prueba: alinear a la hora en curso
			base := now.Truncate(time.Hour)
			return base.Add(now.Sub(base) / step * step)
		}
		// Alineado a la medianoche local: cada 6 h = 00, 06, 12, 18.
		return day.Add(now.Sub(day) / step * step)
	case "daily":
		h, m := clock(s.Daily.At)
		t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, loc)
		if t.After(now) {
			t = t.AddDate(0, 0, -1)
		}
		return t
	case "weekly":
		h, m := clock(s.Weekly.At)
		t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, loc)
		back := (int(now.Weekday()) - s.Weekly.Day + 7) % 7
		t = t.AddDate(0, 0, -back)
		if t.After(now) {
			t = t.AddDate(0, 0, -7)
		}
		return t
	}
	return time.Time{}
}

// Due dice si a un nivel le corresponde una instantánea ahora: hay un momento
// programado posterior a la última que se hizo. Si tangod estuvo apagado y se
// perdieron varios, se hace una sola (catch-up de a lo sumo una por nivel).
func Due(s Schedule, level string, last, now time.Time, unit time.Duration) bool {
	if !s.Enabled || s.Keep(level) <= 0 {
		return false
	}
	return last.Before(PrevSlot(s, level, now, unit))
}

// Prune devuelve las instantáneas que sobran de un nivel: names son los nombres de
// ese nivel (ordenan cronológicamente); se conservan las keep más nuevas.
func Prune(names []string, keep int) []string {
	if keep < 0 || len(names) <= keep {
		return nil
	}
	s := append([]string{}, names...)
	sort.Strings(s)
	return s[:len(s)-keep]
}

// ---- Persistencia --------------------------------------------------------------------

// Store guarda las políticas y la última ejecución de cada nivel por carpeta.
type Store struct {
	path string

	mu        sync.Mutex
	Schedules map[string]Schedule             `json:"schedules"`
	LastRun   map[string]map[string]time.Time `json:"last_run"`
}

// Open carga el archivo (o empieza vacío).
func Open(path string) *Store {
	s := &Store{path: path, Schedules: map[string]Schedule{}, LastRun: map[string]map[string]time.Time{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, s)
	}
	if s.Schedules == nil {
		s.Schedules = map[string]Schedule{}
	}
	if s.LastRun == nil {
		s.LastRun = map[string]map[string]time.Time{}
	}
	return s
}

// Get devuelve la política de una carpeta (Default si nunca se configuró).
func (s *Store) Get(share string) Schedule {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sc, ok := s.Schedules[share]; ok {
		return sc
	}
	return Default()
}

// Set guarda la política de una carpeta.
func (s *Store) Set(share string, sc Schedule) error {
	if err := sc.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Schedules[share] = sc
	return s.saveLocked()
}

// Last es la última ejecución registrada de un nivel.
func (s *Store) Last(share, level string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.LastRun[share][level]
}

// MarkRun registra una ejecución.
func (s *Store) MarkRun(share, level string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.LastRun[share] == nil {
		s.LastRun[share] = map[string]time.Time{}
	}
	s.LastRun[share][level] = t
	_ = s.saveLocked()
}

// Forget borra todo lo de una carpeta (cuando se elimina).
func (s *Store) Forget(share string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Schedules, share)
	delete(s.LastRun, share)
	_ = s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
