package gluster

import (
	"encoding/xml"
	"fmt"
	"regexp"
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

var quotaPathRe = regexp.MustCompile(`^/([A-Za-z0-9][A-Za-z0-9 ._-]{0,63})(/[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}){0,7}$`)

// ValidQuotaPath valida un directorio del volumen (/carpeta[/sub…]); nunca la raíz con "..".
func ValidQuotaPath(p string) error {
	if p == "/" {
		return nil
	}
	if !quotaPathRe.MatchString(p) || strings.Contains(p, "..") {
		return fmt.Errorf("directorio inválido: %q (por ejemplo /Documentos)", p)
	}
	return nil
}

// ArgsQuota arma las operaciones de cuota: enable, disable, limit (bytes), remove, list.
func ArgsQuota(vol, op, path string, limit uint64) ([]string, error) {
	switch op {
	case "enable", "disable":
		return []string{"volume", "quota", vol, op}, nil
	case "list":
		return []string{"volume", "quota", vol, "list"}, nil
	case "limit":
		if err := ValidQuotaPath(path); err != nil {
			return nil, err
		}
		if limit < 1<<20 || limit > 1<<50 {
			return nil, fmt.Errorf("el límite tiene que estar entre 1 MB y 1 PB")
		}
		return []string{"volume", "quota", vol, "limit-usage", path, strconv.FormatUint(limit, 10) + "B"}, nil
	case "remove":
		if err := ValidQuotaPath(path); err != nil {
			return nil, err
		}
		return []string{"volume", "quota", vol, "remove", path}, nil
	}
	return nil, fmt.Errorf("operación de cuota inválida: %q", op)
}

// Quota es el límite de un directorio.
type Quota struct {
	Path        string `json:"path"`
	Limit       uint64 `json:"limit"`
	SoftPercent int    `json:"softPercent"`
	Used        uint64 `json:"used"`
	Avail       uint64 `json:"avail"`
	SoftOver    bool   `json:"softOver"`
	HardOver    bool   `json:"hardOver"`
}

// ParseQuota lee `volume quota <vol> list`.
func ParseQuota(b []byte) ([]Quota, error) {
	var o struct {
		Limits []struct {
			Path   string `xml:"path"`
			Hard   uint64 `xml:"hard_limit"`
			SoftP  string `xml:"soft_limit_percent"`
			Used   uint64 `xml:"used_space"`
			Avail  uint64 `xml:"avail_space"`
			SoftEx string `xml:"sl_exceeded"`
			HardEx string `xml:"hl_exceeded"`
		} `xml:"volQuota>limit"`
	}
	if err := decode(b, &o); err != nil {
		return nil, err
	}
	out := []Quota{}
	for _, l := range o.Limits {
		sp, _ := strconv.Atoi(strings.TrimSuffix(l.SoftP, "%"))
		out = append(out, Quota{Path: l.Path, Limit: l.Hard, SoftPercent: sp, Used: l.Used, Avail: l.Avail,
			SoftOver: strings.EqualFold(l.SoftEx, "yes"), HardOver: strings.EqualFold(l.HardEx, "yes")})
	}
	return out, nil
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
