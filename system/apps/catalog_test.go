package appcatalog

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogLoads(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"homeassistant", "immich", "jellyfin", "nextcloud", "pihole", "syncthing", "uptime-kuma", "vaultwarden"}
	if len(all) != len(want) {
		t.Fatalf("catálogo con %d apps, se esperaban %d", len(all), len(want))
	}
	for _, id := range want {
		a, ok := Get(id)
		if !ok {
			t.Fatalf("falta %s", id)
		}
		if !strings.HasPrefix(a.Icon, "data:image/svg+xml;base64,") {
			t.Errorf("%s: icon %q", id, a.Icon)
		}
		c, err := Compose(id)
		if err != nil || !strings.Contains(string(c), "${DATA}") {
			t.Errorf("%s: compose sin ${DATA}", id)
		}
		if strings.Contains(string(c), "privileged") || strings.Contains(string(c), "docker.sock") {
			t.Errorf("%s: el compose no puede ser privilegiado ni montar el socket de Docker", id)
		}
		if strings.Contains(string(c), ":latest") {
			t.Errorf("%s: imagen sin versión fija", id)
		}
		if _, ok := Icon(id); !ok {
			t.Errorf("%s: sin icono", id)
		}
	}
}

func TestRenderEnvGolden(t *testing.T) {
	a, _ := Get("nextcloud")
	got, err := RenderEnv(a, Context{Data: "/volume1/@apps/nextcloud/data", Port: 8082, TZ: "America/Argentina/Buenos_Aires", Host: "172.17.0.44"},
		map[string]string{"ADMIN_PASSWORD": "s3cretPass99"})
	if err != nil {
		t.Fatal(err)
	}
	want := `# Generado por TangoOS: no editar a mano (se reescribe al actualizar).
DATA='/volume1/@apps/nextcloud/data'
PORT='8082'
TZ='America/Argentina/Buenos_Aires'
HOST='172.17.0.44'
ADMIN_PASSWORD='s3cretPass99'
ADMIN_USER='admin'
`
	if got != want {
		t.Fatalf("env:\n%s\nquería:\n%s", got, want)
	}
}

func TestResolveRejects(t *testing.T) {
	nc, _ := Get("nextcloud")
	vw, _ := Get("vaultwarden")
	cases := []struct {
		name string
		app  App
		opts map[string]string
	}{
		{"opción desconocida", nc, map[string]string{"ADMIN_PASSWORD": "abcdefgh1", "EVIL": "x"}},
		{"falta requerida", nc, map[string]string{}},
		{"comilla", nc, map[string]string{"ADMIN_PASSWORD": "abc'defgh"}},
		{"salto de línea", nc, map[string]string{"ADMIN_PASSWORD": "abcdefgh\nX=1"}},
		{"contraseña corta", nc, map[string]string{"ADMIN_PASSWORD": "abc"}},
		{"bool inválido", vw, map[string]string{"SIGNUPS_ALLOWED": "quizás"}},
	}
	for _, c := range cases {
		if _, err := Resolve(c.app, c.opts); err == nil {
			t.Errorf("%s: se esperaba error", c.name)
		}
	}
	if _, err := RenderEnv(nc, Context{Data: "/etc", Port: 8082}, map[string]string{"ADMIN_PASSWORD": "abcdefgh1"}); err == nil {
		t.Error("datos fuera de @apps/<id>: se esperaba error")
	}
	if _, err := RenderEnv(nc, Context{Data: "/volume1/@apps/nextcloud/data", Port: 80}, map[string]string{"ADMIN_PASSWORD": "abcdefgh1"}); err == nil {
		t.Error("puerto privilegiado: se esperaba error")
	}
}

func TestIconHandler(t *testing.T) {
	h := IconHandler()
	for path, code := range map[string]int{"/app-icons/jellyfin.svg": 200, "/app-icons/../catalog.go": 404, "/app-icons/nope.svg": 404, "/app-icons/jellyfin": 404} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != code {
			t.Errorf("%s: %d, quería %d", path, w.Code, code)
		}
	}
}
