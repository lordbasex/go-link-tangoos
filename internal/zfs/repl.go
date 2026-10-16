package zfs

// Replicación de volúmenes con zfs send / receive (ADR 0016). Se manda cada dataset del
// volumen por separado (el volumen, shares/, apps/ y cada carpeta o app), no un stream
// -R: así un envío cortado se retoma dataset por dataset con receive_resume_token.
// Siempre raw (-w): un volumen cifrado viaja cifrado y el destino nunca ve la frase.
// En el destino la réplica vive en <pool>/tgrepl/<origen>/<volumen>, sin montar y de
// solo lectura (heredado de <pool>/tgrepl).

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// ReplRoot es el dataset (bajo el pool) que guarda las réplicas recibidas.
const ReplRoot = "tgrepl"

// ReplLocal es el origen de las réplicas hechas desde este mismo NAS.
const ReplLocal = "local"

var (
	originRe   = regexp.MustCompile(`^(local|[a-f0-9]{12})$`)
	taskRe     = regexp.MustCompile(`^[a-z0-9]{6,16}$`)
	replSnapRe = regexp.MustCompile(`^tgrepl-([a-z0-9]{6,16})-([0-9]{8}T[0-9]{6}Z)$`)
	tokenRe    = regexp.MustCompile(`^[0-9a-f]{1,8}-[0-9a-f]{1,32}-[0-9a-f]{1,32}-[0-9a-f]{16,}$`)
)

// ValidOrigin: "local" o los 12 primeros hex de la huella del NAS de origen.
func ValidOrigin(o string) error {
	if !originRe.MatchString(o) {
		return fmt.Errorf("origen de réplica inválido")
	}
	return nil
}

// ValidTaskID es el id de una tarea de replicación.
func ValidTaskID(id string) error {
	if !taskRe.MatchString(id) {
		return fmt.Errorf("tarea de replicación inválida")
	}
	return nil
}

// ReplSnapName es la instantánea que crea una tarea (en todo el volumen, -r).
func ReplSnapName(task string, t time.Time) string {
	return "tgrepl-" + task + "-" + t.UTC().Format("20060102T150405Z")
}

// ReplSnapTask dice de qué tarea es una instantánea de replicación ("" si no es una).
func ReplSnapTask(name string) string {
	m := replSnapRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return m[1]
}

// ValidReplSnap acepta una instantánea de replicación.
func ValidReplSnap(name string) error {
	if !replSnapRe.MatchString(name) {
		return fmt.Errorf("instantánea de replicación inválida")
	}
	return nil
}

// ValidAnySnap acepta las instantáneas que maneja TangoOS (programadas o de replicación):
// son las que pueden ser base de un envío incremental.
func ValidAnySnap(name string) error {
	if ValidSnapName(name) == nil || ValidReplSnap(name) == nil {
		return nil
	}
	return fmt.Errorf("instantánea inválida")
}

// ValidToken acepta un receive_resume_token (hex con guiones, lo arma ZFS).
func ValidToken(t string) error {
	if len(t) > 70000 || !tokenRe.MatchString(t) {
		return fmt.Errorf("token de reanudación inválido")
	}
	return nil
}

// ReplParent es <pool>/tgrepl/<origen>.
func ReplParent(pool, origin string) string { return pool + "/" + ReplRoot + "/" + origin }

// ReplDataset es la réplica de un volumen en el destino.
func ReplDataset(pool, origin, vol string) string { return ReplParent(pool, origin) + "/" + vol }

// ValidRel acepta la ruta de un dataset dentro de un volumen: "", "shares", "apps",
// "shares/<Carpeta>" o "apps/<app>".
func ValidRel(rel string) error {
	if rel == "" || rel == "shares" || rel == "apps" {
		return nil
	}
	top, child, ok := strings.Cut(rel, "/")
	if !ok || (top != "shares" && top != "apps") || childNameRe.MatchString(child) == false {
		return fmt.Errorf("dataset inválido: %q", rel)
	}
	return nil
}

// Join arma <raíz>/<rel>.
func Join(root, rel string) string {
	if rel == "" {
		return root
	}
	return root + "/" + rel
}

// RelOf es la ruta de ds dentro de root (ok=false si no está debajo).
func RelOf(root, ds string) (string, bool) {
	if ds == root {
		return "", true
	}
	rel, ok := strings.CutPrefix(ds, root+"/")
	return rel, ok && ValidRel(rel) == nil
}

// ArgsSend arma el argv de zfs send: con token retoma; con from es incremental con
// las instantáneas intermedias (-I); dry agrega -nP para estimar el tamaño.
func ArgsSend(ds, snap, from, token string, dry bool) ([]string, error) {
	a := []string{"send"}
	if dry {
		a = append(a, "-n", "-P")
	}
	if token != "" {
		if err := ValidToken(token); err != nil {
			return nil, err
		}
		return append(a, "-t", token), nil
	}
	if err := ValidAnySnap(snap); err != nil {
		return nil, err
	}
	a = append(a, "-w")
	if from != "" {
		if err := ValidAnySnap(from); err != nil {
			return nil, err
		}
		a = append(a, "-I", "@"+from)
	}
	return append(a, ds+"@"+snap), nil
}

// ArgsRecv arma el argv de zfs receive: -s guarda el estado si se corta, -u no monta;
// -F en un incremental vuelve a la última instantánea (la réplica es de solo lectura,
// pero atime o un montaje manual la pueden haber tocado).
func ArgsRecv(target string, incremental bool) []string {
	a := []string{"receive", "-s", "-u"}
	if incremental {
		a = append(a, "-F")
	}
	return append(a, target)
}

// ParseSendSize lee el "size <bytes>" de zfs send -nP.
func ParseSendSize(out string) uint64 {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && f[0] == "size" {
			n, _ := strconv.ParseUint(f[1], 10, 64)
			return n
		}
	}
	return 0
}

// ReplListProps son las columnas de los filesystems que pide el estado de réplica.
const ReplListProps = "name,guid,receive_resume_token,encryption,encryptionroot,keystatus"

// ParseReplState lee `zfs list -j -p -t filesystem -r -o ReplListProps <raíz>` y
// `zfs list -j -p -t snapshot -r -o name,guid,createtxg <raíz>`: los datasets del
// volumen (padres primero) con sus instantáneas en orden de creación.
func ParseReplState(root string, fsJSON, snapJSON []byte) ([]agentproto.ReplDataset, error) {
	type doc struct {
		Datasets map[string]struct {
			Name       string              `json:"name"`
			Properties map[string]jsonProp `json:"properties"`
		} `json:"datasets"`
	}
	var fs, sn doc
	if err := json.Unmarshal(fsJSON, &fs); err != nil {
		return nil, fmt.Errorf("zfs list: %w", err)
	}
	if len(snapJSON) > 0 {
		if err := json.Unmarshal(snapJSON, &sn); err != nil {
			return nil, fmt.Errorf("zfs list -t snapshot: %w", err)
		}
	}
	by := map[string]*agentproto.ReplDataset{}
	var out []*agentproto.ReplDataset
	for _, name := range SortedKeys(fs.Datasets) {
		d := fs.Datasets[name]
		rel, ok := RelOf(root, d.Name)
		if !ok {
			continue
		}
		p := d.Properties
		rd := &agentproto.ReplDataset{Rel: rel, GUID: p["guid"].Value, Snaps: []agentproto.ReplSnap{}}
		if t := p["receive_resume_token"].Value; t != "" && t != "-" {
			rd.Token = t
		}
		if e := p["encryption"].Value; e != "" && e != "off" && e != "-" {
			rd.Encrypted = true
			rd.EncRoot = p["encryptionroot"].Value
			rd.KeyLoaded = p["keystatus"].Value == "available"
		}
		by[d.Name] = rd
		out = append(out, rd)
	}
	for _, name := range SortedKeys(sn.Datasets) {
		d := sn.Datasets[name]
		dsName, snap, ok := strings.Cut(d.Name, "@")
		rd := by[dsName]
		if !ok || rd == nil {
			continue
		}
		txg, _ := strconv.ParseUint(d.Properties["createtxg"].Value, 10, 64)
		rd.Snaps = append(rd.Snaps, agentproto.ReplSnap{Name: snap, GUID: d.Properties["guid"].Value, Txg: txg})
	}
	res := make([]agentproto.ReplDataset, 0, len(out))
	for _, rd := range out {
		sort.Slice(rd.Snaps, func(i, j int) bool { return rd.Snaps[i].Txg < rd.Snaps[j].Txg })
		res = append(res, *rd)
	}
	// Padres primero: "" < "apps" < "apps/x" < "shares" < "shares/x" ya cumple con el
	// orden por nombre, pero se ordena por profundidad para no depender de eso.
	sort.SliceStable(res, func(i, j int) bool {
		return strings.Count(res[i].Rel, "/")+boolInt(res[i].Rel != "") < strings.Count(res[j].Rel, "/")+boolInt(res[j].Rel != "")
	})
	return res, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ReplStep es lo que hay que mandar de un dataset para que el destino llegue a snap.
type ReplStep struct {
	Rel   string
	Mode  string // skip | resume | full | incr
	From  string // base del incremental (o la primera instantánea en un full con historia)
	Snap  string
	Token string
	First string // full: la instantánea más vieja que se manda entera antes del incremental
}

// PlanDataset decide qué mandar de un dataset de origen src para que el destino dst
// (nil si no existe) tenga snap. Un full manda primero la instantánea más vieja y
// después un incremental hasta snap, así la réplica guarda también la historia.
func PlanDataset(src agentproto.ReplDataset, dst *agentproto.ReplDataset, snap string) (ReplStep, error) {
	st := ReplStep{Rel: src.Rel, Snap: snap}
	var target *agentproto.ReplSnap
	for i := range src.Snaps {
		if src.Snaps[i].Name == snap {
			target = &src.Snaps[i]
		}
	}
	if target == nil {
		return st, fmt.Errorf("%s no tiene la instantánea %s", src.Rel, snap)
	}
	if dst != nil && dst.Token != "" {
		st.Mode, st.Token = "resume", dst.Token
		return st, nil
	}
	if dst == nil {
		st.Mode = "full"
		st.First = src.Snaps[0].Name
		if st.First == snap {
			st.First = ""
		}
		return st, nil
	}
	have := map[string]bool{}
	for _, s := range dst.Snaps {
		have[s.GUID] = true
	}
	if have[target.GUID] {
		st.Mode = "skip"
		return st, nil
	}
	// La base es la instantánea común más nueva (anterior a snap).
	for i := len(src.Snaps) - 1; i >= 0; i-- {
		s := src.Snaps[i]
		if s.Txg < target.Txg && have[s.GUID] {
			st.Mode, st.From = "incr", s.Name
			return st, nil
		}
	}
	return st, fmt.Errorf("la réplica de %s no tiene ninguna instantánea en común con el origen: borrala en el destino para empezar de nuevo", relLabel(src.Rel))
}

func relLabel(rel string) string {
	if rel == "" {
		return "el volumen"
	}
	return rel
}

// SnapsAfter son las instantáneas del destino posteriores a base (hay que borrarlas
// antes de recibir un incremental desde base).
func SnapsAfter(dst agentproto.ReplDataset, baseGUID string) []string {
	var baseTxg uint64
	found := false
	for _, s := range dst.Snaps {
		if s.GUID == baseGUID {
			baseTxg, found = s.Txg, true
		}
	}
	if !found {
		return nil
	}
	var out []string
	for _, s := range dst.Snaps {
		if s.Txg > baseTxg {
			out = append(out, s.Name)
		}
	}
	return out
}
