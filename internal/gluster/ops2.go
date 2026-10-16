package gluster

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---- Formas de brick ------------------------------------------------------------------
//
// Un brick de TangoOS puede ser:
//   - "dir": un directorio dentro de un volumen Btrfs de TangoOS (/volumeN/@gluster/<vol>);
//   - "lv":  un volumen lógico dedicado con XFS en un grupo de almacenamiento, montado en
//     /srv/gluster/<vol> (el brick es el subdirectorio brick/, como en un clúster de
//     producción: si el LV no está montado, glusterd no escribe en el disco del sistema).
//
// Para sumar otra forma (por ejemplo un dataset ZFS) alcanza con agregar una entrada a
// brickForms con su expresión regular: ValidBrick, BrickKind y el resto del flujo la toman.

// LVRoot es donde se montan los LV dedicados a bricks.
const LVRoot = "/srv/gluster"

// ThinPoolName es el pool thin de cada grupo donde van los bricks thin (snapshots).
// Con guion bajo a propósito: los informes de grupos solo muestran LV con nombres de
// volumen de TangoOS, así que ni el pool ni los LV de bricks aparecen como volúmenes.
const ThinPoolName = "gb_thin"

type brickForm struct {
	kind string
	re   *regexp.Regexp // la primera captura es el nombre del volumen del clúster
}

var brickForms = []brickForm{
	{"dir", btrfsBrickRe},
	{"lv", regexp.MustCompile(`^/srv/gluster/([a-z][a-z0-9_]{0,31})/brick$`)},
	// "snap": después de restaurar una instantánea, GlusterFS deja el volumen corriendo
	// sobre los LV de la instantánea, que monta él mismo acá (la ruta no lleva el nombre).
	{"snap", snapBrickRe},
}

var snapBrickRe = regexp.MustCompile(`^/run/gluster/snaps/[0-9a-f]{32}/brick[0-9]{1,3}/brick$`)

// LVName es el nombre del LV de un brick dedicado (gb_<vol>).
func LVName(vol string) string { return "gb_" + vol }

// LVMountPath es donde se monta el LV de un brick dedicado.
func LVMountPath(vol string) string { return LVRoot + "/" + vol }

// LVBrickPath es la ruta del brick dentro del LV dedicado.
func LVBrickPath(vol string) string { return LVMountPath(vol) + "/brick" }

// LVMountUnitName es la unidad .mount de /srv/gluster/<vol> (sin guiones que escapar).
func LVMountUnitName(vol string) string { return "srv-gluster-" + vol + ".mount" }

// BrickKind dice qué forma tiene una ruta de brick ("" si no es de TangoOS).
func BrickKind(path string) string {
	for _, f := range brickForms {
		if f.re.MatchString(path) {
			return f.kind
		}
	}
	return ""
}

// brickVolume devuelve el volumen del clúster de una ruta de brick ("" si no es válida;
// "*" para un brick restaurado de una instantánea, que vale para cualquier volumen).
func brickVolume(path string) string {
	for _, f := range brickForms {
		if m := f.re.FindStringSubmatch(path); m != nil {
			if len(m) < 2 {
				return "*"
			}
			return m[1]
		}
	}
	return ""
}

// ---- TLS ------------------------------------------------------------------------------
//
// Con TLS, glusterd (management) y los bricks (E/S) usan /etc/ssl/glusterfs.{pem,key,ca}:
// el certificado de cada nodo, firmado por la CA del clúster. auth.ssl-allow limita la E/S
// a los CN de los nodos del clúster (al sacar un nodo, su CN sale de la lista).

// Archivos que lee GlusterFS.
const (
	TLSCertFile  = "/etc/ssl/glusterfs.pem"
	TLSKeyFile   = "/etc/ssl/glusterfs.key"
	TLSCAFile    = "/etc/ssl/glusterfs.ca"
	SecureAccess = "/var/lib/glusterd/secure-access"
)

var cnRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$`)

// ValidCN valida el nombre (CN) de un nodo en su certificado.
func ValidCN(cn string) error {
	if !cnRe.MatchString(cn) {
		return fmt.Errorf("nombre de nodo inválido para el certificado: %q", cn)
	}
	return nil
}

// TLSOptions son las opciones de un volumen con TLS (o sin él, con on=false).
func TLSOptions(on bool, cns []string) map[string]string {
	if !on {
		return map[string]string{"client.ssl": "off", "server.ssl": "off"}
	}
	return map[string]string{"client.ssl": "on", "server.ssl": "on", "auth.ssl-allow": strings.Join(cns, ",")}
}

// ArgsSetManaged aplica una opción que maneja TangoOS (TLS): no pasa por la allow-list
// de los usuarios, así que solo acepta estas claves.
func ArgsSetManaged(vol, key, val string) ([]string, error) {
	switch key {
	case "client.ssl", "server.ssl":
		if val != "on" && val != "off" {
			return nil, fmt.Errorf("%s: on u off", key)
		}
	case "auth.ssl-allow":
		for _, cn := range strings.Split(val, ",") {
			if err := ValidCN(cn); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("opción no administrada: %q", key)
	}
	return []string{"volume", "set", vol, key, val}, nil
}

// ---- Cuotas ---------------------------------------------------------------------------
//
// GlusterFS 11 trae dos cuotas. La clásica (`volume quota enable`: marker, quotad y un
// montaje auxiliar de glusterd) cuenta el uso pero no frena las escrituras en estos
// volúmenes, y su montaje auxiliar traba `remove-brick commit`. TangoOS usa la nueva,
// simple-quota (ADR 0015): cada carpeta con límite es un "namespace" del volumen y cada
// brick cuenta lo que se escribe en ella y frena al pasarse. Se maneja con xattrs desde
// un montaje con el pid de cliente del ayudante de cuotas (QuotaHelperPID).

// Atributos de simple-quota y del namespace.
const (
	NamespaceXattr  = "trusted.glusterfs.namespace"
	QuotaLimitXattr = "trusted.gfs.squota.limit"
	QuotaSizeXattr  = "trusted.gfs.squota.size"
	// QuotaResetXattr hace que cada brick vuelva a leer el uso de su disco.
	QuotaResetXattr = "glusterfs.quota.reset"
	// QuotaTotalXattr le da a cada brick el uso total de un volumen distribuido.
	QuotaTotalXattr = "glusterfs.quota.total-usage"
	// QuotaHelperPID (GF_CLIENT_PID_QUOTA_HELPER): con este pid el montaje puede marcar
	// namespaces y su statfs guarda en disco el uso que cada brick tiene en memoria.
	QuotaHelperPID = -14
	// QuotaSoftPercent: desde dónde la app avisa (simple-quota no tiene límite blando).
	QuotaSoftPercent = 80
	// QuotaNoLimit es lo que se escribe en memoria al quitar un límite: simple-quota
	// ignora un límite 0 hasta que el brick reinicia.
	QuotaNoLimit = 1 << 62
)

// QuotaOption guarda los límites en el volumen ("/Docs=1073741824/Fotos=…"): es lo que
// vale. Los xattrs de cada brick se vuelven a poner en cada listado, así los bricks que se
// agregan después también los tienen.
const QuotaOption = "user.tango.quotas"

// ParseQuotaOption lee QuotaOption (carpeta → límite); ignora lo que no entiende.
func ParseQuotaOption(v string) map[string]uint64 {
	out := map[string]uint64{}
	for _, it := range strings.Split(v, "/") {
		name, n, ok := strings.Cut(it, "=")
		l, err := strconv.ParseUint(n, 10, 64)
		if ok && err == nil && ValidQuotaPath("/"+name) == nil && ValidQuotaLimit(l) == nil {
			out["/"+name] = l
		}
	}
	return out
}

// FormatQuotaOption es el valor de QuotaOption, ordenado ("" sin límites).
func FormatQuotaOption(m map[string]uint64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%d", k, m[k])
	}
	return b.String()
}

var quotaPathRe = regexp.MustCompile(`^/[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

// ValidQuotaPath valida una carpeta de primer nivel del volumen (/Documentos). Solo
// primer nivel: un namespace adentro de otro saca su uso del de arriba.
func ValidQuotaPath(p string) error {
	if !quotaPathRe.MatchString(p) || strings.Contains(p, "..") {
		return fmt.Errorf("carpeta inválida: %q (una carpeta de primer nivel, por ejemplo /Documentos)", p)
	}
	return nil
}

// ValidQuotaLimit: entre 1 MB y 1 PB.
func ValidQuotaLimit(limit uint64) error {
	if limit < 1<<20 || limit > 1<<50 {
		return fmt.Errorf("el límite tiene que estar entre 1 MB y 1 PB")
	}
	return nil
}

// ArgsQuotaOn prende o apaga la aplicación de los límites (los límites quedan guardados).
func ArgsQuotaOn(vol string, on bool) [][]string {
	if !on {
		return [][]string{{"volume", "set", vol, "feature.simple-quota-pass-through", "true"}}
	}
	return [][]string{
		{"volume", "set", vol, "features.tag-namespaces", "on"},
		{"volume", "set", vol, "feature.simple-quota-pass-through", "false"},
	}
}

// QuotaOn dice si el volumen aplica cuotas (simple-quota sin pass-through).
func QuotaOn(opts map[string]string) bool {
	return opts["feature.simple-quota-pass-through"] == "false" && opts["features.tag-namespaces"] == "on"
}

// QuotaHelperMount es el comando que monta el volumen como ayudante de cuotas.
func QuotaHelperMount(vol, dir string) []string {
	return []string{"/usr/sbin/glusterfs", "--process-name", "fuse", "-s", "localhost", "--volfile-id", vol,
		"--client-pid=" + strconv.Itoa(QuotaHelperPID), "-l", "/var/log/glusterfs/tango-squota-" + vol + ".log", dir}
}

// ParseXattrInt lee un número de un xattr de simple-quota (texto, a veces con NUL).
func ParseXattrInt(b []byte) (int64, error) {
	return strconv.ParseInt(strings.TrimRight(strings.TrimSpace(string(b)), "\x00"), 10, 64)
}

// Quota es el límite de una carpeta.
type Quota struct {
	Path        string `json:"path"`
	Limit       uint64 `json:"limit"`
	SoftPercent int    `json:"softPercent"`
	Used        uint64 `json:"used"`
	Avail       uint64 `json:"avail"`
	SoftOver    bool   `json:"softOver"`
	HardOver    bool   `json:"hardOver"`
}

// NewQuota arma la fila de una carpeta con su límite y su uso.
func NewQuota(path string, limit, used uint64) Quota {
	q := Quota{Path: path, Limit: limit, SoftPercent: QuotaSoftPercent, Used: used}
	if used < limit {
		q.Avail = limit - used
	}
	q.SoftOver = used*100 >= limit*QuotaSoftPercent
	// simple-quota frena la escritura que se pasaría: el uso queda apenas debajo del límite.
	q.HardOver = used*100 >= limit*99
	return q
}

// QuotaResult es lo que devuelve el agente: los límites y, al poner uno, si la carpeta
// ya tenía contenido (hay que contarlo y reiniciar los bricks).
type QuotaResult struct {
	Quotas   []Quota `json:"quotas"`
	NonEmpty bool    `json:"nonEmpty,omitempty"`
}

// NodeUUID lee el UUID de este nodo de /var/lib/glusterd/glusterd.info.
func NodeUUID(info []byte) string {
	for _, l := range strings.Split(string(info), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "UUID="); ok {
			return v
		}
	}
	return ""
}

// ---- Rebalance y remove-brick ---------------------------------------------------------

// ArgsRebalance arranca, consulta o detiene la redistribución de archivos.
func ArgsRebalance(vol, action string) ([]string, error) {
	switch action {
	case "start", "status", "stop":
		return []string{"volume", "rebalance", vol, action}, nil
	}
	return nil, fmt.Errorf("acción inválida: %q", action)
}

// TaskNode es el avance de un nodo en un rebalance o un remove-brick.
type TaskNode struct {
	Node     string  `json:"node"`
	Files    uint64  `json:"files"`
	Size     uint64  `json:"size"`
	Lookups  uint64  `json:"lookups"`
	Failures uint64  `json:"failures"`
	Skipped  uint64  `json:"skipped"`
	Status   string  `json:"status"` // not started | in progress | completed | stopped | failed
	Runtime  float64 `json:"runtime"`
}

// Task es el estado de un rebalance o de un remove-brick (start).
type Task struct {
	ID     string     `json:"id"`
	Status string     `json:"status"` // el agregado
	Done   bool       `json:"done"`   // completed (o stopped/failed: no sigue)
	Failed bool       `json:"failed"`
	Total  TaskNode   `json:"total"`
	Nodes  []TaskNode `json:"nodes"`
}

type taskNodeXML struct {
	Name     string  `xml:"nodeName"`
	Files    uint64  `xml:"files"`
	Size     uint64  `xml:"size"`
	Lookups  uint64  `xml:"lookups"`
	Failures uint64  `xml:"failures"`
	Skipped  uint64  `xml:"skipped"`
	Status   string  `xml:"statusStr"`
	Runtime  float64 `xml:"runtime"`
}

func (n taskNodeXML) node() TaskNode {
	return TaskNode{Node: n.Name, Files: n.Files, Size: n.Size, Lookups: n.Lookups, Failures: n.Failures, Skipped: n.Skipped, Status: n.Status, Runtime: n.Runtime}
}

// ParseTask lee `volume rebalance <vol> status` o `volume remove-brick … status`.
// self reemplaza "localhost" (como en ParsePool).
func ParseTask(b []byte, self string) (*Task, error) {
	type body struct {
		ID    string        `xml:"task-id"`
		Nodes []taskNodeXML `xml:"node"`
		Agg   taskNodeXML   `xml:"aggregate"`
	}
	var o struct {
		Reb *body `xml:"volRebalance"`
		Rem *body `xml:"volRemoveBrick"`
	}
	if err := decode(b, &o); err != nil {
		return nil, err
	}
	bd := o.Reb
	if bd == nil {
		bd = o.Rem
	}
	if bd == nil {
		return nil, fmt.Errorf("salida sin estado de la tarea")
	}
	t := &Task{ID: bd.ID, Total: bd.Agg.node(), Status: bd.Agg.Status, Nodes: []TaskNode{}}
	for _, n := range bd.Nodes {
		tn := n.node()
		if tn.Node == "localhost" && self != "" {
			tn.Node = self
		}
		t.Nodes = append(t.Nodes, tn)
	}
	switch t.Status {
	case "completed":
		t.Done = true
	case "stopped", "failed":
		t.Done, t.Failed = true, t.Status == "failed"
	}
	return t, nil
}

// ---- Instantáneas ---------------------------------------------------------------------
//
// GlusterFS saca instantáneas con LVM thin: todos los bricks del volumen tienen que estar
// en un LV thin (bricks "lv" creados con thin). Un brick en carpeta no se puede.

var snapNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidSnapName valida el nombre de una instantánea.
func ValidSnapName(s string) error {
	if !snapNameRe.MatchString(s) || s == "all" {
		return fmt.Errorf("nombre inválido: usá letras, números, - o _ (máx. 64)")
	}
	return nil
}

var snapDescRe = regexp.MustCompile(`^[\p{L}\p{N} .,:;()_/-]{0,120}$`)

// ArgsSnapshot: create (con descripción opcional), delete, restore, activate, deactivate, info.
func ArgsSnapshot(op, snap, vol, desc string) ([]string, error) {
	if op != "info" {
		if err := ValidSnapName(snap); err != nil {
			return nil, err
		}
	}
	switch op {
	case "create":
		if !snapDescRe.MatchString(desc) {
			return nil, fmt.Errorf("descripción inválida (máx. 120 caracteres, sin comillas)")
		}
		a := []string{"snapshot", "create", snap, vol, "no-timestamp"}
		if desc != "" {
			a = append(a, "description", desc)
		}
		return a, nil
	case "delete", "restore", "activate", "deactivate":
		return []string{"snapshot", op, snap}, nil
	case "info":
		return []string{"snapshot", "info", "volume", vol}, nil
	}
	return nil, fmt.Errorf("operación inválida: %q", op)
}

// Snapshot es una instantánea de un volumen del clúster.
type Snapshot struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid"`
	Desc    string `json:"desc,omitempty"`
	Created string `json:"created"`
	Status  string `json:"status"` // Started (activada) | Stopped
}

// ParseSnapshots lee `snapshot info volume <vol>`.
func ParseSnapshots(b []byte) ([]Snapshot, error) {
	var o struct {
		Snaps []struct {
			Name    string `xml:"name"`
			UUID    string `xml:"uuid"`
			Desc    string `xml:"description"`
			Created string `xml:"createTime"`
			Status  string `xml:"snapVolume>status"`
		} `xml:"snapInfo>snapshots>snapshot"`
	}
	if err := decode(b, &o); err != nil {
		return nil, err
	}
	out := []Snapshot{}
	for _, s := range o.Snaps {
		out = append(out, Snapshot{Name: s.Name, UUID: s.UUID, Desc: s.Desc, Created: s.Created, Status: s.Status})
	}
	return out, nil
}

// decode valida opRet (como parse) y decodifica el cuerpo en v.
func decode(b []byte, v any) error {
	if _, err := parse(b); err != nil {
		return err
	}
	if err := xml.Unmarshal(b, v); err != nil {
		return fmt.Errorf("salida XML de GlusterFS inválida: %w", err)
	}
	return nil
}
