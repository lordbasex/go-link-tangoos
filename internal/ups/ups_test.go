package ups

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/ups/upstest"
)

func TestSplitFields(t *testing.T) {
	f, err := splitFields(`VAR ups device.model "Back-UPS \"ES\" 700" `)
	if err != nil || len(f) != 4 || f[3] != `Back-UPS "ES" 700` {
		t.Fatalf("%q %v", f, err)
	}
	if _, err := splitFields(`VAR ups x "abierta`); err == nil {
		t.Fatal("comillas sin cerrar aceptadas")
	}
}

func TestClientAgainstFake(t *testing.T) {
	srv, err := upstest.Start("127.0.0.1:0", "ups", map[string]string{"ups.status": "OL CHRG", "battery.charge": "87"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ctx := context.Background()
	c, err := Dial(ctx, "127.0.0.1", srv.Addr().Port)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	list, err := c.ListUPS(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "ups" || list[0].Desc != "UPS de prueba" {
		t.Fatalf("%v %v", list, err)
	}
	v, err := c.Vars(ctx, "ups")
	if err != nil || v["battery.charge"] != "87" || v["ups.status"] != "OL CHRG" {
		t.Fatalf("%v %v", v, err)
	}
	if _, err := c.Vars(ctx, "otro"); err == nil || !strings.Contains(err.Error(), "UNKNOWN-UPS") {
		t.Fatalf("UPS desconocido: %v", err)
	}
}

type events struct {
	mu   sync.Mutex
	evs  []Event
	down []string
}

func (e *events) titles() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var s []string
	for _, x := range e.evs {
		s = append(s, x.Title)
	}
	return strings.Join(s, "|")
}

func TestMonitorPolicy(t *testing.T) {
	srv, err := upstest.Start("127.0.0.1:0", "ups", map[string]string{"ups.status": "OL", "battery.charge": "100"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	cfg := DefaultConfig()
	cfg.Mode, cfg.Host, cfg.Port = "remote", "127.0.0.1", srv.Addr().Port
	cfg.Policy = Policy{Action: "shutdown", AfterMinutes: 5, BelowPercent: 20}
	m := NewMonitor(cfg)
	now := time.Unix(1_900_000_000, 0)
	m.Now = func() time.Time { return now }
	var ev events
	m.OnEvent = func(e Event) { ev.mu.Lock(); ev.evs = append(ev.evs, e); ev.mu.Unlock() }
	m.OnShutdown = func(r string) { ev.mu.Lock(); ev.down = append(ev.down, r); ev.mu.Unlock() }
	ctx := context.Background()

	m.Poll(ctx)
	if st := m.Status(); !st.Online || st.StatusText != "Con luz" || *st.BatteryPct != 100 {
		t.Fatalf("estado inicial %+v", st)
	}
	srv.Set("ups.status", "OB DISCHRG")
	srv.Set("battery.charge", "80")
	m.Poll(ctx)
	if ev.titles() != "Corte de luz" || len(ev.down) != 0 {
		t.Fatalf("corte: %s %v", ev.titles(), ev.down)
	}
	if st := m.Status(); st.OnBatterySince == nil {
		t.Fatal("sin onBatterySince")
	}
	// 4 minutos en batería: todavía no.
	now = now.Add(4 * time.Minute)
	m.Poll(ctx)
	if len(ev.down) != 0 {
		t.Fatal("apagó antes de tiempo")
	}
	// 5 minutos: apaga, una sola vez.
	now = now.Add(time.Minute)
	m.Poll(ctx)
	m.Poll(ctx)
	if len(ev.down) != 1 || !strings.Contains(ev.down[0], "5 minutos") {
		t.Fatalf("apagado por tiempo: %v", ev.down)
	}
	// Vuelve la luz: avisa y rearma.
	srv.Set("ups.status", "OL CHRG")
	m.Poll(ctx)
	if !strings.HasSuffix(ev.titles(), "Volvió la luz") {
		t.Fatalf("vuelta: %s", ev.titles())
	}
	// Batería baja (LB) con política "notify": avisa, no apaga.
	cfg.Policy.Action = "notify"
	m.SetConfig(cfg)
	srv.Set("ups.status", "OB LB")
	m.Poll(ctx)
	if len(ev.down) != 1 || !strings.Contains(ev.titles(), "Batería baja") {
		t.Fatalf("notify: %s %v", ev.titles(), ev.down)
	}
	// Sin contacto con upsd.
	srv.Close()
	m.Poll(ctx)
	if st := m.Status(); st.Online || st.Error == "" {
		t.Fatalf("sin contacto: %+v", st)
	}
}

func TestBelowPercent(t *testing.T) {
	srv, _ := upstest.Start("127.0.0.1:0", "ups", map[string]string{"ups.status": "OB DISCHRG", "battery.charge": "19"})
	defer srv.Close()
	cfg := DefaultConfig()
	cfg.Mode, cfg.Host, cfg.Port = "remote", "127.0.0.1", srv.Addr().Port
	cfg.Policy = Policy{Action: "shutdown", BelowPercent: 20}
	m := NewMonitor(cfg)
	var reason string
	m.OnShutdown = func(r string) { reason = r }
	m.Poll(context.Background())
	if !strings.Contains(reason, "20 %") {
		t.Fatalf("por carga: %q", reason)
	}
}

func TestValidateAndFiles(t *testing.T) {
	bad := []Config{
		{Mode: "x"},
		{Mode: "remote", UPSName: "ups", Host: "", Port: 3493, Policy: Policy{Action: "shutdown"}},
		{Mode: "remote", UPSName: "u ps", Host: "1.2.3.4", Port: 3493, Policy: Policy{Action: "shutdown"}},
		{Mode: "remote", UPSName: "ups", Host: "1.2.3.4", Port: 3493, Policy: Policy{Action: "reboot"}},
		{Mode: "remote", UPSName: "ups", Host: "a b", Port: 3493, Policy: Policy{Action: "notify"}},
	}
	for _, c := range bad {
		if c.Validate() == nil {
			t.Errorf("aceptó %+v", c)
		}
	}
	c := DefaultConfig()
	c.Mode, c.NetServer = "local", true
	files, err := NUTFiles(c, "abcdefghijkl1234")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files["nut.conf"], "MODE=netserver") || !strings.Contains(files["ups.conf"], "driver = usbhid-ups") ||
		!strings.Contains(files["upsd.conf"], "LISTEN 0.0.0.0 3493") || !strings.Contains(files["upsd.users"], "password = abcdefghijkl1234") {
		t.Fatalf("%v", files)
	}
	c.NetServer = false
	files, _ = NUTFiles(c, "abcdefghijkl1234")
	if strings.Contains(files["upsd.conf"], "0.0.0.0") {
		t.Fatal("standalone escucha en la red")
	}
	if _, err := NUTFiles(c, "corta"); err == nil {
		t.Fatal("contraseña corta aceptada")
	}
}

func TestDetectUSB(t *testing.T) {
	root := t.TempDir()
	mk := func(dev string, kv map[string]string) {
		d := filepath.Join(root, "bus/usb/devices", dev)
		os.MkdirAll(d, 0o755)
		for k, v := range kv {
			os.WriteFile(filepath.Join(d, k), []byte(v+"\n"), 0o644)
		}
	}
	mk("1-1", map[string]string{"idVendor": "051d", "idProduct": "0002", "product": "Back-UPS ES 700", "manufacturer": "American Power Conversion"})
	mk("1-2", map[string]string{"idVendor": "046d", "idProduct": "c52b"}) // un mouse
	mk("1-3", map[string]string{"idVendor": "0665", "idProduct": "5161"}) // genérico sin interfaz HID
	got := DetectUSB(root)
	if len(got) != 1 || got[0].ID != "051d:0002" || got[0].Product != "Back-UPS ES 700" {
		t.Fatalf("%+v", got)
	}
}
