// Package appcatalog es el catálogo del Centro de apps (protocol/README.md §9, ADR 0010).
//
// Se embebe tanto en tangod (para mostrarlo) como en tango-agent (que es quien instala):
// el agente solo acepta un id del catálogo y opciones validadas contra él, nunca un
// compose arbitrario. Así un tangod comprometido no puede pedir un contenedor
// privilegiado que monte "/".
package appcatalog

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed catalog
var files embed.FS

// Option es un parámetro que el usuario completa al instalar.
type Option struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"` // string | password | port | bool
	Default  any    `json:"default,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Generate: si queda vacío, tangod genera un valor aleatorio (contraseñas internas).
	Generate bool `json:"generate,omitempty"`
}

// App es exactamente el CatalogApp de TS (más campos internos sin exportar a JSON).
type App struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	Category            string   `json:"category"`
	Icon                string   `json:"icon"`
	Version             string   `json:"version"`
	WebPort             *int     `json:"webPort"`
	Options             []Option `json:"options"`
	Notes               string   `json:"notes,omitempty"`
	RequiresHostNetwork bool     `json:"requiresHostNetwork,omitempty"`
	SizeHint            int64    `json:"sizeHint"`
}

var (
	loadOnce sync.Once
	apps     map[string]App
	order    []string
	loadErr  error
)

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
var keyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)

var categories = map[string]bool{"Multimedia": true, "Fotos": true, "Hogar": true, "Productividad": true, "Red": true, "Seguridad": true, "Utilidades": true}

// reservedKeys los pone TangoOS; una app no puede declarar opciones con esos nombres.
var reservedKeys = map[string]bool{"DATA": true, "PORT": true, "TZ": true, "HOST": true}

func load() {
	apps = map[string]App{}
	entries, err := fs.ReadDir(files, "catalog")
	if err != nil {
		loadErr = err
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		b, err := files.ReadFile(path.Join("catalog", id, "app.json"))
		if err != nil {
			loadErr = fmt.Errorf("%s: %w", id, err)
			return
		}
		var a App
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&a); err != nil {
			loadErr = fmt.Errorf("%s/app.json: %w", id, err)
			return
		}
		if err := validateApp(id, a); err != nil {
			loadErr = err
			return
		}
		if _, err := files.ReadFile(path.Join("catalog", id, "compose.yaml")); err != nil {
			loadErr = fmt.Errorf("%s: falta compose.yaml", id)
			return
		}
		// data-URL y no /app-icons/: el escritorio también corre en el portal TangoConnect
		// (otro origen), donde una ruta relativa apuntaría al portal y no al NAS.
		icon, err := files.ReadFile(path.Join("catalog", id, "icon.svg"))
		if err != nil {
			loadErr = fmt.Errorf("%s: falta icon.svg", id)
			return
		}
		a.Icon = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(icon)
		if a.Options == nil {
			a.Options = []Option{}
		}
		apps[id] = a
		order = append(order, id)
	}
	sort.Slice(order, func(i, j int) bool { return apps[order[i]].Name < apps[order[j]].Name })
}

func validateApp(dir string, a App) error {
	if a.ID != dir || !idRe.MatchString(a.ID) {
		return fmt.Errorf("catálogo: id %q inválido o distinto de su carpeta", a.ID)
	}
	if a.Name == "" || a.Version == "" || !categories[a.Category] {
		return fmt.Errorf("catálogo: %s incompleta (nombre, versión o categoría)", a.ID)
	}
	if a.WebPort != nil && (*a.WebPort < 1024 || *a.WebPort > 65535) {
		return fmt.Errorf("catálogo: %s webPort fuera de rango", a.ID)
	}
	for _, o := range a.Options {
		if !keyRe.MatchString(o.Key) || reservedKeys[o.Key] {
			return fmt.Errorf("catálogo: %s opción %q inválida", a.ID, o.Key)
		}
		switch o.Type {
		case "string", "password", "port", "bool":
		default:
			return fmt.Errorf("catálogo: %s opción %s con tipo %q", a.ID, o.Key, o.Type)
		}
	}
	return nil
}

// All devuelve el catálogo ordenado por nombre.
func All() ([]App, error) {
	loadOnce.Do(load)
	if loadErr != nil {
		return nil, loadErr
	}
	out := make([]App, 0, len(order))
	for _, id := range order {
		out = append(out, apps[id])
	}
	return out, nil
}

// Get devuelve una app del catálogo.
func Get(id string) (App, bool) {
	loadOnce.Do(load)
	a, ok := apps[id]
	return a, ok && loadErr == nil
}

// Compose devuelve el compose.yaml de la app, tal cual (las variables las resuelve
// docker compose desde el .env que arma RenderEnv).
func Compose(id string) ([]byte, error) {
	if _, ok := Get(id); !ok {
		return nil, errors.New("app desconocida")
	}
	return files.ReadFile(path.Join("catalog", id, "compose.yaml"))
}

// Icon devuelve el SVG de la app.
func Icon(id string) ([]byte, bool) {
	if _, ok := Get(id); !ok {
		return nil, false
	}
	b, err := files.ReadFile(path.Join("catalog", id, "icon.svg"))
	return b, err == nil
}

// IconHandler sirve /app-icons/<id>.svg (público, cacheable).
func IconHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/app-icons/")
		id, ok := strings.CutSuffix(name, ".svg")
		if !ok || !idRe.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		b, ok := Icon(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		_, _ = w.Write(b)
	})
}

// Context son los valores que pone TangoOS (no el usuario).
type Context struct {
	Data string // directorio de datos absoluto: /<volumen>/@apps/<id>/data
	Port int    // puerto web en el host
	TZ   string
	Host string // IP o nombre con el que se llega al NAS (p. ej. trusted domains)
}

var safeVal = regexp.MustCompile(`^[\x20-\x7E]*$`)

// Resolve valida las opciones contra el catálogo y completa defaults. Las requeridas
// con Generate deben llegar ya generadas por tangod (el agente no inventa secretos).
func Resolve(a App, opts map[string]string) (map[string]string, error) {
	out := map[string]string{}
	known := map[string]Option{}
	for _, o := range a.Options {
		known[o.Key] = o
	}
	for k := range opts {
		if _, ok := known[k]; !ok {
			return nil, fmt.Errorf("opción desconocida: %s", k)
		}
	}
	for _, o := range a.Options {
		v, ok := opts[o.Key]
		if !ok || v == "" {
			if o.Default != nil {
				v = fmt.Sprint(o.Default)
			}
		}
		if v == "" && o.Required {
			return nil, fmt.Errorf("falta %s", o.Label)
		}
		if len(v) > 256 || !safeVal.MatchString(v) || strings.ContainsAny(v, "'\\") {
			return nil, fmt.Errorf("%s tiene caracteres no permitidos", o.Label)
		}
		switch o.Type {
		case "bool":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, fmt.Errorf("%s debe ser sí o no", o.Label)
			}
			v = strconv.FormatBool(b)
		case "port":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1024 || n > 65535 {
				return nil, fmt.Errorf("%s debe ser un puerto entre 1024 y 65535", o.Label)
			}
		case "password":
			if v != "" && len(v) < 8 {
				return nil, fmt.Errorf("%s debe tener al menos 8 caracteres", o.Label)
			}
		}
		out[o.Key] = v
	}
	return out, nil
}

var tzRe = regexp.MustCompile(`^[A-Za-z0-9_+/-]{1,64}$`)
var hostRe = regexp.MustCompile(`^[A-Za-z0-9.:-]{1,253}$`)

// RenderEnv arma el .env de docker compose. Cada valor va entre comillas simples
// (compose no interpola dentro de ellas) y Resolve ya rechazó comillas y saltos de línea.
func RenderEnv(a App, c Context, opts map[string]string) (string, error) {
	vals, err := Resolve(a, opts)
	if err != nil {
		return "", err
	}
	if !path.IsAbs(c.Data) || path.Clean(c.Data) != c.Data || !strings.Contains(c.Data, "/@apps/"+a.ID+"/") {
		return "", errors.New("directorio de datos inválido")
	}
	if c.Port < 1024 || c.Port > 65535 {
		return "", errors.New("puerto inválido")
	}
	if c.TZ == "" {
		c.TZ = "UTC"
	}
	if !tzRe.MatchString(c.TZ) {
		return "", errors.New("zona horaria inválida")
	}
	if c.Host == "" {
		c.Host = "localhost"
	}
	if !hostRe.MatchString(c.Host) {
		return "", errors.New("host inválido")
	}
	var b strings.Builder
	b.WriteString("# Generado por TangoOS: no editar a mano (se reescribe al actualizar).\n")
	line := func(k, v string) { fmt.Fprintf(&b, "%s='%s'\n", k, v) }
	line("DATA", c.Data)
	line("PORT", strconv.Itoa(c.Port))
	line("TZ", c.TZ)
	line("HOST", c.Host)
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		line(k, vals[k])
	}
	return b.String(), nil
}

// ValidID dice si id es una app del catálogo.
func ValidID(id string) bool {
	_, ok := Get(id)
	return ok
}
