// Package audit guarda el registro de auditoría de TangoOS (protocol/README.md §12,
// docs/07 §6): un JSONL append-only en /var/lib/tango/audit.jsonl, rotado a los
// 10 MB con 5 archivos viejos. Toda llamada que modifica algo y todo intento de
// login quedan registrados. Nunca se guardan contraseñas, tokens ni secretos: los
// parámetros se reducen a un "objetivo" legible y los campos sensibles se tachan.
package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Event es exactamente el AuditEvent del cliente.
type Event struct {
	At     string `json:"at"`
	Actor  string `json:"actor"`
	IP     string `json:"ip"`
	Client string `json:"client"`
	Method string `json:"method"`
	Target string `json:"target"`
	Result string `json:"result"` // ok | error | denied
	Job    string `json:"job,omitempty"`
}

// Límites de rotación.
const (
	MaxBytes = 10 << 20
	Keep     = 5
)

// Log es el registro. Seguro para uso concurrente.
type Log struct {
	path    string
	mu      sync.Mutex
	f       *os.File
	size    int64
	OnEvent func(Event) // publica audit.event (solo a administradores)
	now     func() time.Time
}

// Open abre (o crea) <dir>/audit.jsonl con permisos 0600.
func Open(dir string) (*Log, error) {
	l := &Log{path: filepath.Join(dir, "audit.jsonl"), now: time.Now}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Log) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, st.Size()
	return nil
}

// Close cierra el archivo.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Record agrega un evento (completa At si falta) y lo publica.
func (l *Log) Record(e Event) {
	if e.At == "" {
		e.At = l.now().UTC().Format(time.RFC3339Nano)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	b = append(b, '\n')
	l.mu.Lock()
	if l.f != nil {
		if l.size+int64(len(b)) > MaxBytes {
			l.rotateLocked()
		}
		if n, err := l.f.Write(b); err == nil {
			l.size += int64(n)
		}
	}
	cb := l.OnEvent
	l.mu.Unlock()
	if cb != nil {
		cb(e)
	}
}

func (l *Log) rotateLocked() {
	_ = l.f.Close()
	for i := Keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1))
	}
	_ = os.Rename(l.path, l.path+".1")
	_ = os.Remove(fmt.Sprintf("%s.%d", l.path, Keep+1))
	if err := l.open(); err != nil {
		l.f = nil
	}
}

// Query filtra: q busca en actor/método/objetivo/IP (sin distinguir mayúsculas),
// since (RFC 3339) corta por fecha, limit (1–1000) toma los más recientes.
type Query struct {
	Q     string `json:"q,omitempty"`
	Since string `json:"since,omitempty"`
	Limit int    `json:"limit"`
}

// List devuelve los eventos más recientes primero, del archivo actual y los rotados.
func (l *Log) List(q Query) ([]Event, error) {
	if q.Limit <= 0 || q.Limit > 1000 {
		q.Limit = 200
	}
	var since time.Time
	if q.Since != "" {
		if t, err := time.Parse(time.RFC3339, q.Since); err == nil {
			since = t
		}
	}
	needle := strings.ToLower(strings.TrimSpace(q.Q))
	files := []string{l.path}
	for i := 1; i <= Keep; i++ {
		files = append(files, fmt.Sprintf("%s.%d", l.path, i))
	}
	out := []Event{}
	for _, name := range files { // del más nuevo al más viejo
		evs, err := readFile(name)
		if err != nil {
			continue
		}
		for i := len(evs) - 1; i >= 0; i-- {
			e := evs[i]
			if !since.IsZero() {
				if t, err := time.Parse(time.RFC3339Nano, e.At); err == nil && t.Before(since) {
					continue
				}
			}
			if needle != "" && !strings.Contains(strings.ToLower(e.Actor+" "+e.Method+" "+e.Target+" "+e.IP+" "+e.Result), needle) {
				continue
			}
			out = append(out, e)
			if len(out) >= q.Limit {
				return out, nil
			}
		}
	}
	// Ya está del más nuevo al más viejo (orden del archivo, que solo se agrega). No se
	// ordena por At: RFC3339Nano recorta ceros finales y la comparación de texto falla.
	return out, nil
}

func readFile(name string) ([]Event, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// ---- Qué se audita y cómo se resume ---------------------------------------------------

// readOnly son los métodos de solo lectura: no se auditan (salvo si fallan por permisos).
var readOnly = map[string]bool{
	"auth.users": true, "auth.challenge": true, "system.info": true, "setup.state": true,
	"storage.disks.list": true, "storage.volumes.list": true, "sessions.list": true, "users.list": true,
	"groups.list": true, "jobs.list": true, "notices.list": true, "rtc.stats": true, "shares.list": true,
	"snapshots.list": true, "files.list": true, "files.search": true, "files.trash.list": true, "files.versions": true,
	"files.thumb": true, "apps.catalog": true, "apps.list": true, "apps.logs": true, "apps.docker.status": true,
	"alerts.list": true, "snapshots.schedule.get": true, "shares.permissions.get": true, "system.updates.check": true,
	"system.rollback.list": true, "cert.info": true, "cert.caDownload": true, "services.list": true,
	"nfs.exports.get": true, "iscsi.targets.list": true, "notify.channels.get": true, "audit.list": true,
	"logs.query": true, "term.resize": true, "files.download.begin": true, "storage.volumes.plan": true,
	"storage.volumes.deletePlan": true, "storage.disks.replacePlan": true, "storage.volumes.expandPlan": true,
	// Grupos de almacenamiento (protocol/README.md §16).
	"storage.pools.list": true, "storage.pools.plan": true, "storage.pools.expandPlan": true, "storage.pools.replacePlan": true,
	"storage.pools.deletePlan": true, "storage.keyDevices.list": true, "storage.cache.plan": true,
}

// ShouldRecord dice si una llamada va al registro.
func ShouldRecord(method string, denied bool) bool {
	if method == "auth.login" || method == "auth.logout" || method == "auth.resume" {
		return method != "auth.resume" || denied
	}
	return denied || !readOnly[method]
}

// sensitive son claves que jamás se guardan.
func sensitive(k string) bool {
	k = strings.ToLower(k)
	for _, s := range []string{"password", "passphrase", "secret", "token", "confirm_token", "pem", "key", "data", "code"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// targetKeys son las claves que describen el objetivo, en orden de preferencia.
var targetKeys = []string{"path", "paths", "share", "name", "volume", "pool", "id", "user", "disk", "channel", "unit", "scope", "dir", "plan_id", "typed_name", "hostname"}

// Target resume los parámetros en un texto corto sin secretos.
func Target(params json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(params, &m) != nil || len(m) == 0 {
		return ""
	}
	parts := []string{}
	for _, k := range targetKeys {
		v, ok := m[k]
		if !ok || sensitive(k) {
			continue
		}
		parts = append(parts, k+"="+short(v))
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, " ")
}

func short(v any) string {
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case []any:
		items := []string{}
		for i, x := range t {
			if i == 3 {
				items = append(items, fmt.Sprintf("+%d", len(t)-3))
				break
			}
			items = append(items, short(x))
		}
		s = strings.Join(items, ",")
	case map[string]any:
		s = "{…}"
	default:
		b, _ := json.Marshal(t)
		s = string(b)
	}
	if len([]rune(s)) > 80 {
		s = string([]rune(s)[:79]) + "…"
	}
	return s
}

// LoginActor saca el usuario de un auth.login (que todavía no tiene sesión).
func LoginActor(params json.RawMessage) string {
	var p struct {
		User string `json:"user"`
	}
	_ = json.Unmarshal(params, &p)
	return p.User
}
