// Package appsys tiene las piezas de sistema del Centro de apps que usa tango-agent:
// un cliente de la Docker Engine API (HTTP sobre el socket Unix, Go nativo) y la
// ejecución de docker compose / apt-get como unidades transitorias de systemd.
package appsys

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// DockerSocket es el socket del daemon de Docker.
const DockerSocket = "/run/docker.sock"

// ProjectPrefix: todos los proyectos compose de TangoOS se llaman tango-<id>.
const ProjectPrefix = "tango-"

// Docker habla con la Engine API.
type Docker struct {
	Socket string
	hc     *http.Client
	once   sync.Once
}

func (d *Docker) client() *http.Client {
	d.once.Do(func() {
		sock := d.Socket
		if sock == "" {
			sock = DockerSocket
		}
		d.hc = &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var dl net.Dialer
					return dl.DialContext(ctx, "unix", sock)
				},
				MaxIdleConns: 4,
			},
		}
	})
	return d.hc
}

// ErrNoDaemon: el socket no existe o el daemon no responde.
var ErrNoDaemon = errors.New("docker no responde")

func (d *Docker) get(ctx context.Context, path string, q url.Values, out any) error {
	u := "http://docker" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := d.client().Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoDaemon, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Version devuelve la versión del daemon.
func (d *Docker) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"Version"`
	}
	if err := d.get(ctx, "/version", nil, &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// Container es lo que usamos de /containers/json.
type Container struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	State   string            `json:"State"`  // running | exited | created | restarting | paused | dead
	Status  string            `json:"Status"` // "Up 3 minutes (healthy)"
	Labels  map[string]string `json:"Labels"`
	Image   string            `json:"Image"`
	Created int64             `json:"Created"`
}

// Project devuelve el id de app (sin el prefijo) o "".
func (c Container) Project() string {
	p := c.Labels["com.docker.compose.project"]
	if !strings.HasPrefix(p, ProjectPrefix) {
		return ""
	}
	return strings.TrimPrefix(p, ProjectPrefix)
}

// Service devuelve el nombre del servicio compose.
func (c Container) Service() string { return c.Labels["com.docker.compose.service"] }

// Containers lista los contenedores de los proyectos de TangoOS (todos o el de un id).
func (d *Docker) Containers(ctx context.Context, appID string) ([]Container, error) {
	label := "com.docker.compose.project"
	if appID != "" {
		label += "=" + ProjectPrefix + appID
	}
	f, _ := json.Marshal(map[string][]string{"label": {label}})
	var cs []Container
	if err := d.get(ctx, "/containers/json", url.Values{"all": {"1"}, "filters": {string(f)}}, &cs); err != nil {
		return nil, err
	}
	out := cs[:0]
	for _, c := range cs {
		if c.Project() != "" {
			out = append(out, c)
		}
	}
	return out, nil
}

type statsJSON struct {
	CPU struct {
		Usage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		System uint64 `json:"system_cpu_usage"`
	} `json:"cpu_stats"`
	PreCPU struct {
		Usage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		System uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	Memory struct {
		Usage uint64            `json:"usage"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

// Usage es el consumo de un contenedor: CPU como fracción de toda la máquina (0..1).
type Usage struct {
	CPU float64
	Mem uint64
}

// Stats pide una muestra (stream=false: Docker espera ~1 s para calcular la CPU).
func (d *Docker) Stats(ctx context.Context, id string) (Usage, error) {
	var s statsJSON
	if err := d.get(ctx, "/containers/"+url.PathEscape(id)+"/stats", url.Values{"stream": {"false"}}, &s); err != nil {
		return Usage{}, err
	}
	return usageOf(s), nil
}

func usageOf(s statsJSON) Usage {
	var u Usage
	dc := float64(s.CPU.Usage.Total) - float64(s.PreCPU.Usage.Total)
	ds := float64(s.CPU.System) - float64(s.PreCPU.System)
	if dc > 0 && ds > 0 {
		u.CPU = min(1, dc/ds)
	}
	u.Mem = s.Memory.Usage
	// Igual que `docker stats`: la caché de archivos inactiva no cuenta como uso.
	if inact, ok := s.Memory.Stats["inactive_file"]; ok && inact < u.Mem {
		u.Mem -= inact
	}
	return u
}

// LogLine es una línea de log con su marca de tiempo (para mezclar contenedores).
type LogLine struct {
	At   string
	Text string
}

// Logs devuelve las últimas tail líneas de un contenedor (stdout y stderr).
func (d *Docker) Logs(ctx context.Context, id string, tail int) ([]LogLine, error) {
	u := "http://docker/containers/" + url.PathEscape(id) + "/logs?" + url.Values{
		"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}, "tail": {fmt.Sprint(tail)},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoDaemon, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker logs: %d", resp.StatusCode)
	}
	var raw []byte
	if resp.Header.Get("Content-Type") == "application/vnd.docker.multiplexed-stream" {
		raw, err = demux(io.LimitReader(resp.Body, 4<<20))
	} else {
		raw, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	}
	if err != nil {
		return nil, err
	}
	return parseLogLines(raw), nil
}

// demux separa el stream multiplexado de Docker (cabecera de 8 bytes por frame).
func demux(r io.Reader) ([]byte, error) {
	var out []byte
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return out, nil
			}
			return out, err
		}
		n := binary.BigEndian.Uint32(hdr[4:])
		if n > 1<<20 {
			return out, errors.New("frame de log demasiado grande")
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return out, nil
		}
		out = append(out, buf...)
	}
}

// ansiRe: secuencias de color/cursor que muchas apps escriben en sus logs.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func parseLogLines(raw []byte) []LogLine {
	var out []LogLine
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := ansiRe.ReplaceAllString(strings.TrimRight(sc.Text(), "\r"), "")
		ts, text, ok := strings.Cut(line, " ")
		if !ok || !strings.Contains(ts, "T") {
			ts, text = "", line
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, LogLine{At: ts, Text: text})
	}
	return out
}

// MergeLogs mezcla las líneas de varios servicios por marca de tiempo y deja las últimas tail.
func MergeLogs(per map[string][]LogLine, tail int, prefix bool) []string {
	type item struct {
		at, svc, text string
		seq           int
	}
	var all []item
	seq := 0
	for svc, lines := range per {
		for _, l := range lines {
			all = append(all, item{l.At, svc, l.Text, seq})
			seq++
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].at == all[j].at {
			return all[i].seq < all[j].seq
		}
		return all[i].at < all[j].at
	})
	if len(all) > tail {
		all = all[len(all)-tail:]
	}
	out := make([]string, 0, len(all))
	for _, it := range all {
		if prefix {
			out = append(out, it.svc+" | "+it.text)
		} else {
			out = append(out, it.text)
		}
	}
	return out
}

// Health resume el estado de los contenedores de una app.
func Health(cs []Container) (state string, running, unhealthy int) {
	for _, c := range cs {
		if c.State == "running" {
			running++
		}
		if strings.Contains(c.Status, "(unhealthy)") {
			unhealthy++
		}
	}
	switch {
	case len(cs) == 0:
		return "missing", 0, 0
	case running == len(cs):
		return "running", running, unhealthy
	case running == 0:
		return "stopped", 0, unhealthy
	default:
		return "partial", running, unhealthy
	}
}

// PullProgress sigue las capas de `docker compose pull`: cuántas aparecieron y cuántas
// terminaron ("Pull complete" / "Already exists"). Devuelve 0..1, nunca retrocede.
type PullProgress struct {
	seen, done map[string]bool
	last       float64
}

// Line procesa una línea de salida y devuelve la fracción completada.
func (p *PullProgress) Line(l string) float64 {
	if p.seen == nil {
		p.seen, p.done = map[string]bool{}, map[string]bool{}
	}
	f := strings.Fields(strings.TrimSpace(l))
	if len(f) >= 2 && layerRe.MatchString(f[0]) {
		id, rest := f[0], strings.Join(f[1:], " ")
		p.seen[id] = true
		if strings.HasPrefix(rest, "Pull complete") || strings.HasPrefix(rest, "Already exists") {
			p.done[id] = true
		}
	}
	if len(p.seen) > 0 {
		p.last = max(p.last, float64(len(p.done))/float64(len(p.seen)))
	}
	return p.last
}

var layerRe = regexp.MustCompile(`^[0-9a-f]{12}$`)
