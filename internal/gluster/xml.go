package gluster

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
)

// Salida XML de `gluster --mode=script --xml` (GlusterFS 11). Los fixtures de
// testdata/ son salidas reales capturadas en un clúster de 3 nodos.

// Error es un error que devolvió GlusterFS (opRet != 0), con su texto.
type Error struct {
	Ret   int
	Errno int
	Msg   string
}

func (e *Error) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("GlusterFS falló (código %d)", e.Ret)
	}
	return e.Msg
}

// IsBusy dice si otro nodo tenía tomado el lock del clúster (se puede reintentar).
func IsBusy(err error) bool {
	var e *Error
	return errors.As(err, &e) && (strings.Contains(e.Msg, "Another transaction is in progress") || strings.Contains(e.Msg, "Locking failed"))
}

// IsNotFound dice si GlusterFS respondió que el volumen no existe.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && strings.Contains(e.Msg, "does not exist")
}

// statusNode es un brick en `volume status detail`.
type statusNode struct {
	Hostname  string `xml:"hostname"`
	Path      string `xml:"path"`
	PeerID    string `xml:"peerid"`
	Status    int    `xml:"status"`
	Port      string `xml:"port"`
	PID       int    `xml:"pid"`
	SizeTotal uint64 `xml:"sizeTotal"`
	SizeFree  uint64 `xml:"sizeFree"`
	Device    string `xml:"device"`
	FsName    string `xml:"fsName"`
}

type cliOutput struct {
	OpRet    int    `xml:"opRet"`
	OpErrno  int    `xml:"opErrno"`
	OpErrstr string `xml:"opErrstr"`
	Peers    []struct {
		UUID      string   `xml:"uuid"`
		Hostname  string   `xml:"hostname"`
		Hostnames []string `xml:"hostnames>hostname"`
		Connected int      `xml:"connected"`
		State     int      `xml:"state"`
		StateStr  string   `xml:"stateStr"`
	} `xml:"peerStatus>peer"`
	Volumes []struct {
		Name         string `xml:"name"`
		ID           string `xml:"id"`
		Status       int    `xml:"status"`
		StatusStr    string `xml:"statusStr"`
		BrickCount   int    `xml:"brickCount"`
		DistCount    int    `xml:"distCount"`
		ReplicaCount int    `xml:"replicaCount"`
		ArbiterCount int    `xml:"arbiterCount"`
		TypeStr      string `xml:"typeStr"`
		Transport    int    `xml:"transport"`
		Bricks       []struct {
			Name     string `xml:"name"`
			HostUUID string `xml:"hostUuid"`
			Arbiter  int    `xml:"isArbiter"`
		} `xml:"bricks>brick"`
		Options []struct {
			Name  string `xml:"name"`
			Value string `xml:"value"`
		} `xml:"options>option"`
	} `xml:"volInfo>volumes>volume"`
	Status []struct {
		Name  string       `xml:"volName"`
		Nodes []statusNode `xml:"node"`
	} `xml:"volStatus>volumes>volume"`
	Heal []struct {
		HostUUID     string `xml:"hostUuid,attr"`
		Name         string `xml:"name"`
		Status       string `xml:"status"`
		Total        int    `xml:"totalNumberOfEntries"`
		HealPending  int    `xml:"numberOfEntriesInHealPending"`
		SplitBrain   int    `xml:"numberOfEntriesInSplitBrain"`
		PossiblyHeal int    `xml:"numberOfEntriesPossiblyHealing"`
	} `xml:"healInfo>bricks>brick"`
}

// parse decodifica y convierte opRet != 0 en *Error. Algunas salidas (heal info)
// traen opRet al final: se decodifica todo antes de mirar el código.
func parse(b []byte) (*cliOutput, error) {
	var o cliOutput
	if err := xml.Unmarshal(b, &o); err != nil {
		s := strings.TrimSpace(string(b))
		if s != "" && !strings.HasPrefix(s, "<") { // el CLI a veces responde texto (sin glusterd)
			return nil, &Error{Ret: -1, Msg: firstLine(s)}
		}
		return nil, fmt.Errorf("salida XML de GlusterFS inválida: %w", err)
	}
	if o.OpRet != 0 {
		return &o, &Error{Ret: o.OpRet, Errno: o.OpErrno, Msg: strings.TrimSpace(o.OpErrstr)}
	}
	return &o, nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(l)
}

// ParseResult valida la salida de una operación sin datos (create, start, set…).
// Algunas (heal) responden texto aunque se pida XML: ahí manda el código de salida
// del CLI (exitCode 0 = bien).
func ParseResult(b []byte, exitCode int) error {
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, "<") {
		if exitCode == 0 {
			return nil
		}
		if s == "" {
			return &Error{Ret: exitCode, Msg: fmt.Sprintf("GlusterFS falló (código %d)", exitCode)}
		}
		return &Error{Ret: exitCode, Msg: firstLine(s)}
	}
	_, err := parse(b)
	return err
}

// Peer es un nodo del clúster.
type Peer struct {
	UUID      string   `json:"uuid"`
	Host      string   `json:"host"`
	Hostnames []string `json:"hostnames,omitempty"`
	Connected bool     `json:"connected"`
	State     string   `json:"state"`
	Self      bool     `json:"self"`
}

// ParsePool lee `pool list` (incluye este nodo como "localhost"; se reemplaza por
// self, el nombre con el que lo conocen los demás).
func ParsePool(b []byte, self string) ([]Peer, error) {
	o, err := parse(b)
	if err != nil {
		return nil, err
	}
	out := make([]Peer, 0, len(o.Peers))
	for _, p := range o.Peers {
		pe := Peer{UUID: p.UUID, Host: p.Hostname, Hostnames: p.Hostnames, Connected: p.Connected == 1, State: p.StateStr}
		if p.Hostname == "localhost" {
			pe.Self, pe.Host, pe.State = true, self, "Este nodo"
		}
		out = append(out, pe)
	}
	return out, nil
}

// BrickStatus es un brick con su estado en vivo.
type BrickStatus struct {
	Brick
	HostUUID  string `json:"hostUuid"`
	Arbiter   bool   `json:"arbiter,omitempty"`
	Online    bool   `json:"online"`
	Port      int    `json:"port,omitempty"`
	PID       int    `json:"pid,omitempty"`
	SizeTotal uint64 `json:"sizeTotal,omitempty"`
	SizeFree  uint64 `json:"sizeFree,omitempty"`
	FsName    string `json:"fsName,omitempty"`
	// Kind es la forma del brick: dir (carpeta en un volumen) o lv (LV dedicado con XFS).
	Kind string `json:"kind,omitempty"`
	// Curación (heal info summary): -1 si el brick no respondió.
	HealPending int    `json:"healPending"`
	SplitBrain  int    `json:"splitBrain"`
	HealStatus  string `json:"healStatus,omitempty"`
}

// Volume es un volumen del clúster como lo muestra la app.
type Volume struct {
	Name     string            `json:"name"`
	ID       string            `json:"id"`
	Status   string            `json:"status"` // Created, Started, Stopped
	Type     string            `json:"type"`   // Replicate, Distributed-Replicate…
	Replica  int               `json:"replica"`
	Arbiter  int               `json:"arbiter"`
	Sets     int               `json:"sets"` // conjuntos de réplica (distCount)
	Bricks   []BrickStatus     `json:"bricks"`
	Options  map[string]string `json:"options"`
	OptOrder []string          `json:"optOrder"` // orden de `gluster volume info`
	// Totales del volumen visto desde los bricks en línea.
	SizeTotal   uint64 `json:"sizeTotal"`
	SizeFree    uint64 `json:"sizeFree"`
	HealPending int    `json:"healPending"`
	SplitBrain  int    `json:"splitBrain"`
	Online      int    `json:"online"` // bricks en línea
	// TLS: client.ssl y server.ssl on (E/S cifrada y solo nodos con certificado del clúster).
	TLS bool `json:"tls"`
	// Quota: cuotas por carpeta aplicadas (simple-quota, QuotaOn).
	Quota bool `json:"quota"`
}

func splitBrick(name string) Brick {
	h, p, _ := strings.Cut(name, ":")
	return Brick{Host: h, Path: p}
}

// ParseVolumes combina `volume info all` con `volume status all detail` (puede ser
// nil: volúmenes detenidos o glusterd sin responder). Un brick que no aparece en el
// status está fuera de línea (GlusterFS omite los nodos caídos).
func ParseVolumes(info, status []byte) ([]Volume, error) {
	o, err := parse(info)
	if err != nil {
		return nil, err
	}
	type key struct{ host, path string }
	live := map[key]int{}
	var st *cliOutput
	if status != nil {
		if s, err := parse(status); err == nil {
			st = s
		}
	}
	out := make([]Volume, 0, len(o.Volumes))
	for _, v := range o.Volumes {
		vol := Volume{Name: v.Name, ID: v.ID, Status: v.StatusStr, Type: v.TypeStr, Replica: v.ReplicaCount,
			Arbiter: v.ArbiterCount, Options: map[string]string{}}
		if vol.Sets = v.BrickCount; v.ReplicaCount > 0 {
			vol.Sets = v.BrickCount / v.ReplicaCount
		}
		for _, op := range v.Options {
			vol.Options[op.Name] = op.Value
			vol.OptOrder = append(vol.OptOrder, op.Name)
		}
		clear(live)
		var nodes []statusNode
		if st != nil {
			for _, s := range st.Status {
				if s.Name == v.Name {
					nodes = s.Nodes
				}
			}
		}
		for i, n := range nodes {
			live[key{n.Hostname, n.Path}] = i
		}
		for _, b := range v.Bricks {
			bs := BrickStatus{Brick: splitBrick(b.Name), HostUUID: b.HostUUID, Arbiter: b.Arbiter == 1}
			bs.Kind = BrickKind(bs.Path)
			if i, ok := live[key{bs.Host, bs.Path}]; ok {
				n := nodes[i]
				bs.Online = n.Status == 1
				fmt.Sscan(n.Port, &bs.Port)
				bs.PID, bs.SizeTotal, bs.SizeFree, bs.FsName = n.PID, n.SizeTotal, n.SizeFree, n.FsName
			}
			if bs.Online {
				vol.Online++
				if !bs.Arbiter && (vol.SizeTotal == 0 || bs.SizeTotal < vol.SizeTotal) {
					vol.SizeTotal, vol.SizeFree = bs.SizeTotal, bs.SizeFree // réplica: lo que entra en la copia más chica
				}
			}
			vol.Bricks = append(vol.Bricks, bs)
		}
		vol.TLS = vol.Options["client.ssl"] == "on" && vol.Options["server.ssl"] == "on"
		vol.Quota = QuotaOn(vol.Options)
		if vol.Sets > 1 { // distribuido: cada conjunto suma su capacidad
			vol.SizeTotal *= uint64(vol.Sets)
			vol.SizeFree *= uint64(vol.Sets)
		}
		out = append(out, vol)
	}
	return out, nil
}

// HealBrick es una fila de `heal info summary`.
type HealBrick struct {
	Brick
	Connected  bool   `json:"connected"`
	Status     string `json:"status"`
	Pending    int    `json:"pending"`
	SplitBrain int    `json:"splitBrain"`
	Healing    int    `json:"healing"`
}

// ParseHeal lee `volume heal <vol> info summary`. Un brick caído trae el error en
// <status> y ningún contador.
func ParseHeal(b []byte) ([]HealBrick, error) {
	o, err := parse(b)
	if err != nil {
		return nil, err
	}
	out := make([]HealBrick, 0, len(o.Heal))
	for _, h := range o.Heal {
		hb := HealBrick{Brick: splitBrick(h.Name), Status: h.Status, Connected: h.Status == "Connected",
			Pending: h.HealPending, SplitBrain: h.SplitBrain, Healing: h.PossiblyHeal}
		if !hb.Connected {
			hb.Pending, hb.Status = -1, cleanHealStatus(h.Status)
		}
		out = append(out, hb)
	}
	return out, nil
}

// cleanHealStatus saca el contador pegado que GlusterFS 11 deja en el texto de error
// ("...not connected)totalNumberOfEntries0").
func cleanHealStatus(s string) string {
	if i := strings.Index(s, "totalNumberOfEntries"); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// MergeHeal suma la curación a los bricks de un volumen.
func MergeHeal(v *Volume, heal []HealBrick) {
	byName := map[string]HealBrick{}
	for _, h := range heal {
		byName[h.Brick.String()] = h
	}
	v.HealPending, v.SplitBrain = 0, 0
	for i := range v.Bricks {
		h, ok := byName[v.Bricks[i].Brick.String()]
		if !ok {
			continue
		}
		v.Bricks[i].HealPending, v.Bricks[i].SplitBrain, v.Bricks[i].HealStatus = h.Pending, h.SplitBrain, h.Status
		if h.Pending > 0 {
			v.HealPending += h.Pending
		}
		v.SplitBrain += h.SplitBrain
	}
}

// InfoText reproduce `gluster volume info <vol>` (la vista de texto de la app).
func InfoText(v Volume) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Volume Name: %s\nType: %s\nVolume ID: %s\nStatus: %s\nSnapshot Count: 0\n", v.Name, v.Type, v.ID, v.Status)
	switch {
	case v.Arbiter > 0:
		fmt.Fprintf(&b, "Number of Bricks: %d x (%d + %d) = %d\n", v.Sets, v.Replica-v.Arbiter, v.Arbiter, len(v.Bricks))
	case v.Replica > 1:
		fmt.Fprintf(&b, "Number of Bricks: %d x %d = %d\n", v.Sets, v.Replica, len(v.Bricks))
	default:
		fmt.Fprintf(&b, "Number of Bricks: %d\n", len(v.Bricks))
	}
	b.WriteString("Transport-type: tcp\nBricks:\n")
	for i, br := range v.Bricks {
		arb := ""
		if br.Arbiter {
			arb = " (arbiter)"
		}
		fmt.Fprintf(&b, "Brick%d: %s%s\n", i+1, br.Brick, arb)
	}
	if len(v.OptOrder) > 0 {
		b.WriteString("Options Reconfigured:\n")
		for _, k := range v.OptOrder {
			fmt.Fprintf(&b, "%s: %s\n", k, v.Options[k])
		}
	}
	return b.String()
}
