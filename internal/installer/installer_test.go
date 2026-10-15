package installer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const disksTSV = "/dev/sda\t17179869184\tQEMU HARDDISK\tTANGO-SYS\n/dev/sdb\t4294967296\tQEMU HARDDISK\tLAB-D1\n"

// fakeEngine escribe un tango-install de mentira: lista discos y "instala" con progreso.
func fakeEngine(t *testing.T, fail bool) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tango-install")
	body := "#!/bin/sh\nif [ \"$1\" = --list-disks ]; then printf '" + strings.ReplaceAll(disksTSV, "\t", "\\t") + "'; exit 0; fi\n" +
		"echo \"$*\" > \"$(dirname \"$0\")/args\"\n" +
		"echo '@@ 2 Disco de sistema'\necho '[12:00:00] particiones'\necho '@@ 45 Copiando el sistema…'\n"
	if fail {
		body += "echo '@@ERR grub-install falló'\nexit 1\n"
	} else {
		body += "echo '@@ 100 TangoOS instalado en 42 s'\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func newServer(t *testing.T, fail bool) (*Server, string, *int) {
	t.Helper()
	eng := fakeEngine(t, fail)
	reboots := new(int)
	s, err := New(Config{Engine: eng, Version: "test", Lang: "es", Command: exec.CommandContext,
		Reboot: func() error { *reboots++; return nil }, Addrs: func() []string { return []string{"192.168.1.77"} }})
	if err != nil {
		t.Fatal(err)
	}
	return s, eng, reboots
}

func do(t *testing.T, h http.Handler, method, path, remote, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = remote
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Tango-Installer", "1")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const local, lan = "127.0.0.1:40000", "192.168.1.20:50000"

func TestPINShownOnlyLocally(t *testing.T) {
	s, _, _ := newServer(t, false)
	h := s.Handler()
	var st State
	_ = json.Unmarshal(do(t, h, "GET", "/api/state", local, "", nil).Body.Bytes(), &st)
	if st.PIN != s.PIN() || len(st.PIN) != 6 || !st.Unlocked || st.URLs[0] != "http://192.168.1.77/" || st.Lang != "es" {
		t.Fatalf("estado local: %+v", st)
	}
	st = State{}
	_ = json.Unmarshal(do(t, h, "GET", "/api/state", lan, "", nil).Body.Bytes(), &st)
	if st.PIN != "" || st.Unlocked || st.Log != nil || st.Lang != "" {
		t.Fatalf("la LAN no debe ver el código ni el log: %+v", st)
	}
}

func TestLANNeedsPIN(t *testing.T) {
	s, _, _ := newServer(t, false)
	h := s.Handler()
	if w := do(t, h, "GET", "/api/disks", lan, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("discos sin código: %d", w.Code)
	}
	if w := do(t, h, "GET", "/api/disks", local, "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "TANGO-SYS") {
		t.Fatalf("discos locales: %d %s", w.Code, w.Body)
	}
	bad := "000000"
	if s.PIN() == bad {
		bad = "111111"
	}
	if w := do(t, h, "POST", "/api/unlock", lan, `{"pin":"`+bad+`"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("código malo: %d", w.Code)
	}
	w := do(t, h, "POST", "/api/unlock", lan, `{"pin":"`+s.PIN()+`"}`, nil)
	if w.Code != 200 {
		t.Fatalf("código bueno: %d %s", w.Code, w.Body)
	}
	c := w.Result().Cookies()
	if len(c) != 1 || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie: %+v", c)
	}
	if w := do(t, h, "GET", "/api/disks", lan, "", map[string]string{"Cookie": c[0].Name + "=" + c[0].Value}); w.Code != 200 {
		t.Fatalf("discos con cookie: %d", w.Code)
	}
	if w := do(t, h, "GET", "/api/disks", lan, "", map[string]string{"Cookie": c[0].Name + "=otra"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie falsa: %d", w.Code)
	}
}

func TestPINRateLimit(t *testing.T) {
	s, _, _ := newServer(t, false)
	h := s.Handler()
	bad := "000000"
	if s.PIN() == bad {
		bad = "111111"
	}
	for i := 0; i < 5; i++ {
		do(t, h, "POST", "/api/unlock", lan, `{"pin":"`+bad+`"}`, nil)
	}
	// Bloqueado: ni el código correcto pasa hasta que vence la espera.
	if w := do(t, h, "POST", "/api/unlock", lan, `{"pin":"`+s.PIN()+`"}`, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("después de 5 errores: %d", w.Code)
	}
}

func TestPostNeedsHeader(t *testing.T) {
	s, _, _ := newServer(t, false)
	h := s.Handler()
	// Un formulario de otro sitio no puede poner X-Tango-Installer.
	r := httptest.NewRequest("POST", "/api/install", strings.NewReader(`{}`))
	r.RemoteAddr = local
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("sin cabecera: %d", w.Code)
	}
}

func TestValidate(t *testing.T) {
	disks := ParseDisks(disksTSV)
	if len(disks) != 2 || disks[0].Bytes != 17179869184 || disks[1].Serial != "LAB-D1" {
		t.Fatalf("ParseDisks: %+v", disks)
	}
	ok := Request{Disk: "/dev/sda", Confirm: "/dev/sda", Hostname: "Nas-Casa", Keyboard: "latam", Net: Network{Mode: "dhcp"}}
	args, err := Validate(ok, disks)
	if err != nil || strings.Join(args, " ") != "--disk /dev/sda --yes --hostname nas-casa --keyboard latam --no-reboot --progress" {
		t.Fatalf("válido: %v %v", args, err)
	}
	st := ok
	st.Net = Network{Mode: "static", Address: "192.168.1.50/24", Gateway: "192.168.1.1"}
	args, err = Validate(st, disks)
	if err != nil || args[len(args)-1] != "192.168.1.50/24,192.168.1.1,192.168.1.1" {
		t.Fatalf("IP fija: %v %v", args, err)
	}
	bad := map[string]func(r *Request){
		"disco ajeno":        func(r *Request) { r.Disk, r.Confirm = "/dev/sr0", "/dev/sr0" },
		"sin confirmar":      func(r *Request) { r.Confirm = "" },
		"confirma otro":      func(r *Request) { r.Confirm = "/dev/sdb" },
		"nombre con espacio": func(r *Request) { r.Hostname = "mi nas" },
		"nombre inyección":   func(r *Request) { r.Hostname = "a;reboot" },
		"guion inicial":      func(r *Request) { r.Hostname = "-x" },
		"teclado raro":       func(r *Request) { r.Keyboard = "../../x" },
		"ip sin máscara":     func(r *Request) { r.Net = Network{Mode: "static", Address: "192.168.1.50", Gateway: "192.168.1.1"} },
		"gw fuera de red":    func(r *Request) { r.Net = Network{Mode: "static", Address: "192.168.1.50/24", Gateway: "10.0.0.1"} },
		"gw igual a ip":      func(r *Request) { r.Net = Network{Mode: "static", Address: "192.168.1.50/24", Gateway: "192.168.1.50"} },
		"dns inválido": func(r *Request) {
			r.Net = Network{Mode: "static", Address: "192.168.1.50/24", Gateway: "192.168.1.1", DNS: "x,y"}
		},
		"ipv6":      func(r *Request) { r.Net = Network{Mode: "static", Address: "fd00::5/64", Gateway: "fd00::1"} },
		"modo raro": func(r *Request) { r.Net = Network{Mode: "pppoe"} },
	}
	for name, mut := range bad {
		r := ok
		mut(&r)
		if _, err := Validate(r, disks); err == nil {
			t.Errorf("%s: se aceptó", name)
		}
	}
}

func waitPhase(t *testing.T, s *Server, want string) State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.Snapshot(true, true); st.Phase == want {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no llegó a %s: %+v", want, s.Snapshot(true, true))
	return State{}
}

func TestInstallFlow(t *testing.T) {
	s, eng, reboots := newServer(t, false)
	h := s.Handler()
	body := `{"disk":"/dev/sda","confirm":"/dev/sda","hostname":"tango","keyboard":"us","net":{"mode":"dhcp"}}`
	if w := do(t, h, "POST", "/api/install", local, body, nil); w.Code != 200 {
		t.Fatalf("install: %d %s", w.Code, w.Body)
	}
	st := waitPhase(t, s, PhaseDone)
	if st.Percent != 100 || st.Message != "TangoOS instalado en 42 s" || len(st.Log) != 1 {
		t.Fatalf("estado final: %+v", st)
	}
	args, _ := os.ReadFile(filepath.Join(filepath.Dir(eng), "args"))
	if strings.TrimSpace(string(args)) != "--disk /dev/sda --yes --hostname tango --keyboard us --no-reboot --progress" {
		t.Fatalf("argumentos del motor: %q", args)
	}
	// Una sola instalación.
	if w := do(t, h, "POST", "/api/install", local, body, nil); w.Code != http.StatusConflict {
		t.Fatalf("segunda instalación: %d", w.Code)
	}
	if w := do(t, h, "POST", "/api/reboot", local, `{}`, nil); w.Code != 200 {
		t.Fatalf("reboot: %d", w.Code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for *reboots == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if *reboots != 1 {
		t.Fatal("no reinició")
	}
}

func TestInstallFailureAllowsRetry(t *testing.T) {
	s, _, _ := newServer(t, true)
	if err := s.Start([]string{"--disk", "/dev/sda"}, "tango"); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, s, PhaseFailed)
	if st.Error != "grub-install falló" {
		t.Fatalf("error: %+v", st)
	}
	if err := s.Start([]string{"--disk", "/dev/sda"}, "tango"); err != nil {
		t.Fatalf("reintento: %v", err)
	}
	waitPhase(t, s, PhaseFailed)
}

func TestInstallRejectsUnknownFields(t *testing.T) {
	s, _, _ := newServer(t, false)
	body := `{"disk":"/dev/sda","confirm":"/dev/sda","hostname":"tango","keyboard":"us","net":{"mode":"dhcp"},"extra":"--rm"}`
	if w := do(t, s.Handler(), "POST", "/api/install", local, body, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("campo extra: %d", w.Code)
	}
}

func TestStaticUIAndHeaders(t *testing.T) {
	s, _, _ := newServer(t, false)
	for _, p := range []string{"/", "/app.js", "/style.css", "/wallpaper.svg", "/inter.woff2", "/logo.svg"} {
		w := do(t, s.Handler(), "GET", p, lan, "", nil)
		if w.Code != 200 {
			t.Fatalf("%s: %d", p, w.Code)
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("%s sin CSP", p)
		}
	}
}
