package ups

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Policy dice qué hacer con un corte de luz.
type Policy struct {
	Action       string `json:"action"`       // "shutdown" | "notify"
	AfterMinutes int    `json:"afterMinutes"` // apagar después de N minutos en batería (0 = no por tiempo)
	BelowPercent int    `json:"belowPercent"` // apagar si la batería baja de X % (0 = no por carga)
}

// Config es la configuración de UPS que guarda tangod (ups.json, 0600).
type Config struct {
	Mode      string `json:"mode"` // "none" | "local" | "remote"
	Host      string `json:"host"` // remote: el NAS o equipo con upsd
	Port      int    `json:"port"`
	UPSName   string `json:"upsName"`
	User      string `json:"user,omitempty"`
	Password  string `json:"password,omitempty"`
	NetServer bool   `json:"netServer"` // local: compartir el UPS con otros equipos de la red
	Policy    Policy `json:"policy"`
}

// DefaultConfig: sin UPS; política conservadora para cuando se configure.
func DefaultConfig() Config {
	return Config{Mode: "none", Port: DefaultPort, UPSName: "ups", Policy: Policy{Action: "shutdown", AfterMinutes: 5, BelowPercent: 20}}
}

// Status es exactamente el UpsStatus del cliente.
type Status struct {
	Mode           string     `json:"mode"`
	Configured     bool       `json:"configured"`
	Online         bool       `json:"online"` // upsd responde y el UPS existe
	Name           string     `json:"name"`
	Model          string     `json:"model"`
	Status         string     `json:"status"` // ups.status crudo: "OL", "OB DISCHRG", "OL CHRG LB"…
	StatusText     string     `json:"statusText"`
	BatteryPct     *float64   `json:"batteryPct"`
	RuntimeSec     *int       `json:"runtimeSec"`
	LoadPct        *float64   `json:"loadPct"`
	InputVoltage   *float64   `json:"inputVoltage"`
	LastUpdate     string     `json:"lastUpdate"`
	OnBatterySince *string    `json:"onBatterySince"`
	Detected       []Detected `json:"detected"`
	Error          string     `json:"error,omitempty"`
}

// Flags del status de NUT.
func hasFlag(status, f string) bool {
	for _, x := range strings.Fields(status) {
		if x == f {
			return true
		}
	}
	return false
}

// OnBattery dice si el UPS está en batería.
func OnBattery(status string) bool { return hasFlag(status, "OB") }

// LowBattery dice si el UPS avisó batería baja.
func LowBattery(status string) bool { return hasFlag(status, "LB") }

// StatusText traduce el status de NUT a algo legible.
func StatusText(status string) string {
	switch {
	case status == "":
		return "Sin datos"
	case hasFlag(status, "OB") && hasFlag(status, "LB"):
		return "En batería, carga baja"
	case hasFlag(status, "OB"):
		return "En batería (sin luz)"
	case hasFlag(status, "RB"):
		return "Hay que reemplazar la batería"
	case hasFlag(status, "OVER"):
		return "Sobrecarga"
	case hasFlag(status, "CHRG"):
		return "Con luz, cargando"
	case hasFlag(status, "OL"):
		return "Con luz"
	case hasFlag(status, "OFF"):
		return "Apagado"
	}
	return status
}

func fnum(v map[string]string, k string) *float64 {
	s, ok := v[k]
	if !ok {
		return nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return nil
	}
	return &f
}

// Event es lo que el monitor avisa hacia afuera.
type Event struct {
	Level string // "info" | "ok" | "warn" | "crit"
	Title string
	Body  string
}

// Monitor consulta el UPS periódicamente y aplica la política.
type Monitor struct {
	Interval time.Duration
	Now      func() time.Time
	// Hooks (opcionales).
	OnChange   func(Status)
	OnEvent    func(Event)
	OnShutdown func(reason string)
	// Detect lista UPS USB conectados (DetectUSB en producción).
	Detect func() []Detected

	mu         sync.Mutex
	cfg        Config
	st         Status
	obSince    time.Time
	shutdownAt bool // ya se pidió apagar (una sola vez)
	wake       chan struct{}
}

// NewMonitor crea el monitor con la configuración inicial.
func NewMonitor(cfg Config) *Monitor {
	return &Monitor{Interval: 5 * time.Second, Now: time.Now, cfg: cfg, wake: make(chan struct{}, 1)}
}

// SetConfig cambia la configuración y fuerza una consulta.
func (m *Monitor) SetConfig(cfg Config) {
	m.mu.Lock()
	m.cfg = cfg
	m.obSince, m.shutdownAt = time.Time{}, false
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Config devuelve la configuración actual.
func (m *Monitor) Config() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// Status devuelve el último estado.
func (m *Monitor) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.st
	if m.Detect != nil {
		st.Detected = m.Detect()
	}
	if st.Detected == nil {
		st.Detected = []Detected{}
	}
	return st
}

// Run consulta hasta que ctx termine.
func (m *Monitor) Run(ctx context.Context) {
	for {
		m.Poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		case <-time.After(m.Interval):
		}
	}
}

// target es host/puerto de upsd según el modo.
func (c Config) target() (string, int) {
	if c.Mode == "local" {
		return "127.0.0.1", DefaultPort
	}
	return c.Host, c.Port
}

// Poll hace una consulta y evalúa la política (exportado para tests).
func (m *Monitor) Poll(ctx context.Context) {
	m.mu.Lock()
	cfg := m.cfg
	m.mu.Unlock()
	now := m.Now()
	st := Status{Mode: cfg.Mode, Configured: cfg.Mode == "local" || cfg.Mode == "remote", Name: cfg.UPSName, LastUpdate: now.UTC().Format(time.RFC3339)}
	if st.Configured {
		host, port := cfg.target()
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		vars, err := fetch(cctx, host, port, cfg.UPSName)
		cancel()
		if err != nil {
			st.Error = "No se pudo leer el UPS: " + err.Error()
		} else {
			st.Online = true
			st.Status = vars["ups.status"]
			st.StatusText = StatusText(st.Status)
			st.Model = strings.TrimSpace(vars["device.mfr"] + " " + firstNonEmpty(vars["device.model"], vars["ups.model"]))
			st.BatteryPct = fnum(vars, "battery.charge")
			if r := fnum(vars, "battery.runtime"); r != nil {
				v := int(*r)
				st.RuntimeSec = &v
			}
			st.LoadPct = fnum(vars, "ups.load")
			st.InputVoltage = fnum(vars, "input.voltage")
		}
	}
	m.apply(now, cfg, st)
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func fetch(ctx context.Context, host string, port int, name string) (map[string]string, error) {
	c, err := Dial(ctx, host, port)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.Vars(ctx, name)
}

// apply actualiza el estado, avisa cambios y decide si apagar.
func (m *Monitor) apply(now time.Time, cfg Config, st Status) {
	m.mu.Lock()
	prev := m.st
	wasOB := OnBattery(prev.Status) && prev.Online
	isOB := OnBattery(st.Status) && st.Online
	var events []Event
	switch {
	case isOB && !wasOB:
		m.obSince = now
		events = append(events, Event{Level: "crit", Title: "Corte de luz", Body: "El NAS está funcionando con la batería del UPS."})
	case !isOB && wasOB && st.Online:
		m.obSince, m.shutdownAt = time.Time{}, false
		events = append(events, Event{Level: "ok", Title: "Volvió la luz", Body: "El UPS volvió a la red eléctrica."})
	}
	if prev.Online && !st.Online && st.Configured {
		events = append(events, Event{Level: "warn", Title: "Se perdió el contacto con el UPS", Body: st.Error})
	}
	if isOB {
		s := m.obSince.UTC().Format(time.RFC3339)
		st.OnBatterySince = &s
	}
	reason := ""
	if isOB && !m.shutdownAt {
		switch {
		case LowBattery(st.Status):
			reason = "el UPS avisó batería baja"
		case cfg.Policy.BelowPercent > 0 && st.BatteryPct != nil && *st.BatteryPct < float64(cfg.Policy.BelowPercent):
			reason = "la batería bajó de " + strconv.Itoa(cfg.Policy.BelowPercent) + " %"
		case cfg.Policy.AfterMinutes > 0 && now.Sub(m.obSince) >= time.Duration(cfg.Policy.AfterMinutes)*time.Minute:
			reason = strconv.Itoa(cfg.Policy.AfterMinutes) + " minutos sin luz"
		}
	}
	shutdown := reason != "" && cfg.Policy.Action == "shutdown"
	if reason != "" {
		m.shutdownAt = true
		if shutdown {
			events = append(events, Event{Level: "crit", Title: "Batería baja: apagando", Body: "Se apaga el NAS de forma ordenada porque " + reason + "."})
		} else {
			events = append(events, Event{Level: "crit", Title: "Batería baja", Body: "Se alcanzó el límite (" + reason + "). La política es solo avisar: el NAS no se apaga solo."})
		}
	}
	changed := prev.Status != st.Status || prev.Online != st.Online || prev.Mode != st.Mode || !eqf(prev.BatteryPct, st.BatteryPct) || prev.Error != st.Error
	m.st = st
	m.mu.Unlock()

	for _, e := range events {
		if m.OnEvent != nil {
			m.OnEvent(e)
		}
	}
	if changed && m.OnChange != nil {
		m.OnChange(m.Status())
	}
	if shutdown && m.OnShutdown != nil {
		m.OnShutdown(reason)
	}
}

func eqf(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
