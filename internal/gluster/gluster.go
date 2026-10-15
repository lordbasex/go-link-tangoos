// Package gluster arma y valida todo lo que TangoOS le pide a GlusterFS (ADR 0011,
// protocol/README.md §16): nombres, bricks, opciones permitidas, presets, los
// argumentos de `gluster --mode=script --xml` y el parseo de su salida XML.
//
// Nada de este paquete ejecuta comandos: el agente (root) arma el argv con estas
// funciones y lo corre; así lo que llega desde la red nunca se concatena en un shell
// ni pasa una opción que no esté en la allow-list.
package gluster

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Binary es el CLI de GlusterFS en Debian.
const Binary = "/usr/sbin/gluster"

// MountRoot es donde TangoOS monta los volúmenes del clúster (/cluster/<volumen>).
const MountRoot = "/cluster"

// Puertos de GlusterFS: glusterd (24007), RDMA de glusterd (24008, se cierra igual)
// y el rango de los procesos de cada brick (glusterd.vol: base-port..max-port).
const (
	PortGlusterd  = 24007
	PortRDMA      = 24008
	PortBrickMin  = 49152
	PortBrickMax  = 60999
	MaxBricks     = 48
	MaxReplica    = 4
	MaxNameLength = 32
)

var (
	volNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	// Nombre de host: etiquetas DNS en minúsculas (como en /etc/hosts de un clúster).
	hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	// Brick de TangoOS hoy: un directorio dentro de un volumen Btrfs (/volumeN/@gluster/<vol>/brick).
	btrfsBrickRe = regexp.MustCompile(`^/volume[0-9]{1,3}/@gluster/([a-z][a-z0-9_]{0,31})/brick$`)
)

// ValidVolumeName valida un nombre de volumen del clúster. Sin guiones a propósito:
// así /cluster/<nombre> no necesita escaparse en el nombre de la unidad .mount.
func ValidVolumeName(name string) error {
	if !volNameRe.MatchString(name) {
		return fmt.Errorf("el nombre del volumen debe empezar con una letra y tener solo minúsculas, números o _ (máx. %d)", MaxNameLength)
	}
	if name == "all" || name == "help" {
		return fmt.Errorf("«%s» es una palabra reservada de GlusterFS", name)
	}
	return nil
}

// ValidHost valida un nodo: una IP o un nombre de host. Nunca empieza con "-"
// (no se puede confundir con una opción del CLI).
func ValidHost(h string) error {
	if ip := net.ParseIP(h); ip != nil {
		if ip.IsUnspecified() || ip.IsMulticast() {
			return fmt.Errorf("dirección inválida: %s", h)
		}
		return nil
	}
	if len(h) > 253 || !hostRe.MatchString(h) {
		return fmt.Errorf("nodo inválido: %q (usá una IP o un nombre de host)", h)
	}
	return nil
}

// Brick es un directorio de un nodo que guarda una copia (o una parte) del volumen.
type Brick struct {
	Host string `json:"host"`
	Path string `json:"path"`
}

// String es la forma del CLI: host:/ruta.
func (b Brick) String() string { return b.Host + ":" + b.Path }

// BrickPath es la ruta de un brick de TangoOS dentro de un volumen Btrfs del nodo.
//
// Hook para LVM (docs/17 §4): cuando exista el pool LVM, un brick tendrá su propio LV
// con XFS montado en /srv/gluster/<vol>/<brick>; esa forma se agrega acá y en
// ValidBrick sin cambiar el resto del flujo.
func BrickPath(tangoVolume, vol string) string {
	return "/" + strings.TrimPrefix(tangoVolume, "/") + "/@gluster/" + vol + "/brick"
}

// ValidBrick exige un host válido y la ruta de TangoOS para ese volumen del clúster.
func ValidBrick(b Brick, vol string) error {
	if err := ValidHost(b.Host); err != nil {
		return err
	}
	m := btrfsBrickRe.FindStringSubmatch(b.Path)
	if m == nil || m[1] != vol {
		return fmt.Errorf("brick %s: la ruta tiene que ser /volumeN/@gluster/%s/brick", b, vol)
	}
	return nil
}

// Layout es la forma del volumen: réplica (2–4) y, opcional, un árbitro por
// conjunto (réplica 3 con un brick que solo guarda metadatos).
type Layout struct {
	Replica int  `json:"replica"`
	Arbiter bool `json:"arbiter,omitempty"`
}

// TypeName es el tipo como lo muestra `gluster volume info` (Replicate,
// Distributed-Replicate).
func (l Layout) TypeName(bricks int) string {
	if l.Replica > 1 && bricks > l.Replica {
		return "Distributed-Replicate"
	}
	if l.Replica > 1 {
		return "Replicate"
	}
	if bricks > 1 {
		return "Distribute"
	}
	return "Distribute"
}

// ValidateCreate revisa un volumen nuevo: nombre, forma y bricks (un brick por nodo
// dentro de cada conjunto de réplica, para que dos copias no queden en el mismo equipo).
func ValidateCreate(name string, l Layout, bricks []Brick) error {
	if err := ValidVolumeName(name); err != nil {
		return err
	}
	if l.Replica < 1 || l.Replica > MaxReplica {
		return fmt.Errorf("réplica entre 1 y %d", MaxReplica)
	}
	if l.Arbiter && l.Replica != 3 {
		return fmt.Errorf("el árbitro solo existe con réplica 3 (2 copias + 1 árbitro)")
	}
	if len(bricks) == 0 || len(bricks) > MaxBricks {
		return fmt.Errorf("se esperan entre 1 y %d bricks", MaxBricks)
	}
	if len(bricks)%l.Replica != 0 {
		return fmt.Errorf("con réplica %d la cantidad de bricks tiene que ser múltiplo de %d", l.Replica, l.Replica)
	}
	seen := map[string]bool{}
	for i, b := range bricks {
		if err := ValidBrick(b, name); err != nil {
			return err
		}
		if seen[b.String()] {
			return fmt.Errorf("brick repetido: %s", b)
		}
		seen[b.String()] = true
		// Mismo nodo dos veces en un conjunto de réplica: una caída se lleva dos copias.
		set := i / l.Replica
		for j := set * l.Replica; j < i; j++ {
			if bricks[j].Host == b.Host {
				return fmt.Errorf("el conjunto %d tiene dos bricks en %s: cada copia tiene que ir en un nodo distinto", set+1, b.Host)
			}
		}
	}
	return nil
}

// ---- Opciones -----------------------------------------------------------------------

type optKind int

const (
	kindBool optKind = iota
	kindInt
	kindEnum
	kindPercent
)

type optSpec struct {
	kind     optKind
	min, max int
	enum     []string
	// global: se aplica con `volume set all` (vale para todo el clúster).
	global bool
	desc   string
}

// options es la allow-list: cualquier otra clave se rechaza. Cubre lo que usan los
// presets y los ajustes habituales de réplica, quórum, curación y caché.
var options = map[string]optSpec{
	"cluster.quorum-type":         {kind: kindEnum, enum: []string{"auto", "fixed", "none"}, desc: "Quórum de clientes: con auto, se escribe solo si responde la mayoría de las copias"},
	"cluster.quorum-count":        {kind: kindInt, min: 1, max: MaxReplica, desc: "Copias mínimas para escribir (con quorum-type fixed)"},
	"cluster.server-quorum-type":  {kind: kindEnum, enum: []string{"server", "none"}, desc: "Quórum de servidores: un nodo sin mayoría apaga sus bricks"},
	"cluster.server-quorum-ratio": {kind: kindPercent, min: 1, max: 100, global: true, desc: "Porcentaje de nodos para tener quórum de servidores (todo el clúster)"},
	"cluster.heal-timeout":        {kind: kindInt, min: 5, max: 86400, desc: "Segundos entre rondas del demonio de curación"},
	"cluster.shd-wait-qlength":    {kind: kindInt, min: 1, max: 655536, desc: "Entradas en espera del demonio de curación"},
	"cluster.shd-max-threads":     {kind: kindInt, min: 1, max: 64, desc: "Hilos de curación por brick"},
	"cluster.granular-entry-heal": {kind: kindBool, desc: "Curación por entrada (más rápida en directorios grandes)"},
	"cluster.self-heal-daemon":    {kind: kindBool, desc: "Demonio de curación automática"},
	"cluster.data-self-heal":      {kind: kindBool, desc: "Curación de datos desde los clientes"},
	"cluster.metadata-self-heal":  {kind: kindBool, desc: "Curación de metadatos desde los clientes"},
	"cluster.entry-self-heal":     {kind: kindBool, desc: "Curación de entradas desde los clientes"},
	"cluster.favorite-child-policy": {kind: kindEnum, enum: []string{"none", "size", "ctime", "mtime", "majority"},
		desc: "Cómo resolver un split-brain sin intervención"},
	"network.ping-timeout":          {kind: kindInt, min: 1, max: 1800, desc: "Segundos sin respuesta para dar un nodo por caído"},
	"performance.open-behind":       {kind: kindBool, desc: "Caché de aperturas"},
	"performance.quick-read":        {kind: kindBool, desc: "Lectura rápida de archivos chicos"},
	"performance.read-ahead":        {kind: kindBool, desc: "Lectura anticipada"},
	"performance.write-behind":      {kind: kindBool, desc: "Escritura diferida"},
	"performance.io-cache":          {kind: kindBool, desc: "Caché de lectura del cliente"},
	"performance.stat-prefetch":     {kind: kindBool, desc: "Caché de metadatos"},
	"performance.client-io-threads": {kind: kindBool, desc: "Hilos de E/S en el cliente"},
	"performance.cache-size":        {kind: kindInt, min: 4 << 20, max: 32 << 30, desc: "Tamaño de la caché de lectura (bytes)"},
	"diagnostics.client-log-level":  {kind: kindEnum, enum: []string{"CRITICAL", "ERROR", "WARNING", "INFO", "DEBUG", "TRACE", "NONE"}, desc: "Nivel de log de los clientes"},
	"diagnostics.brick-log-level":   {kind: kindEnum, enum: []string{"CRITICAL", "ERROR", "WARNING", "INFO", "DEBUG", "TRACE", "NONE"}, desc: "Nivel de log de los bricks"},
	"storage.fips-mode-rchecksum":   {kind: kindBool, desc: "Sumas de control compatibles con FIPS al curar"},
	"transport.address-family":      {kind: kindEnum, enum: []string{"inet", "inet6"}, desc: "IPv4 o IPv6 entre nodos"},
	"features.shard":                {kind: kindBool, desc: "Partir archivos grandes (imágenes de VM)"},
	"nfs.disable":                   {kind: kindBool, desc: "Servidor NFS interno de GlusterFS (TangoOS lo deja apagado)"},
	"auth.allow":                    {kind: kindEnum, desc: "Clientes permitidos (TangoOS lo maneja solo)"},
}

// Option describe una clave permitida (para la UI).
type Option struct {
	Key    string   `json:"key"`
	Type   string   `json:"type"`
	Min    int      `json:"min,omitempty"`
	Max    int      `json:"max,omitempty"`
	Enum   []string `json:"enum,omitempty"`
	Global bool     `json:"global,omitempty"`
	Desc   string   `json:"desc"`
}

// Options devuelve la allow-list ordenada (sin las que maneja TangoOS).
func Options() []Option {
	var out []Option
	for k, s := range options {
		if k == "auth.allow" {
			continue
		}
		t := map[optKind]string{kindBool: "bool", kindInt: "int", kindEnum: "enum", kindPercent: "percent"}[s.kind]
		out = append(out, Option{Key: k, Type: t, Min: s.min, Max: s.max, Enum: s.enum, Global: s.global, Desc: s.desc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// IsGlobal dice si una opción se aplica a todo el clúster (`volume set all`).
func IsGlobal(key string) bool { return options[key].global }

// ValidOption valida una clave y su valor contra la allow-list.
func ValidOption(key, val string) error {
	s, ok := options[key]
	if !ok || key == "auth.allow" {
		return fmt.Errorf("opción no permitida: %q", key)
	}
	bad := fmt.Errorf("valor inválido para %s: %q", key, val)
	switch s.kind {
	case kindBool:
		if val != "on" && val != "off" {
			return bad
		}
	case kindInt:
		n, err := strconv.Atoi(val)
		if err != nil || n < s.min || n > s.max {
			return fmt.Errorf("%s: entero entre %d y %d", key, s.min, s.max)
		}
	case kindPercent:
		n, err := strconv.Atoi(strings.TrimSuffix(val, "%"))
		if err != nil || !strings.HasSuffix(val, "%") || n < s.min || n > s.max {
			return fmt.Errorf("%s: porcentaje entre %d%% y %d%%", key, s.min, s.max)
		}
	case kindEnum:
		for _, e := range s.enum {
			if val == e {
				return nil
			}
		}
		return bad
	}
	return nil
}

// ValidOptions valida un mapa de opciones.
func ValidOptions(opts map[string]string) error {
	if len(opts) > len(options) {
		return fmt.Errorf("demasiadas opciones")
	}
	for k, v := range opts {
		if err := ValidOption(k, v); err != nil {
			return err
		}
	}
	return nil
}

// ---- Presets ------------------------------------------------------------------------

// Preset es un conjunto de opciones con nombre.
type Preset struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Desc    string            `json:"desc"`
	Replica int               `json:"replica,omitempty"`
	Options map[string]string `json:"options"`
}

// Presets: "production-replica3" son exactamente las opciones de un clúster de
// producción de réplica 3 (quórum de servidores 51 %, caídas detectadas en 10 s,
// cachés del cliente apagadas para que todos los nodos vean lo mismo al instante).
func Presets() []Preset {
	return []Preset{
		{ID: "default", Name: "Por defecto", Desc: "Los valores de GlusterFS, sin cambios.", Options: map[string]string{}},
		{ID: "production-replica3", Name: "Producción réplica 3", Replica: 3,
			Desc: "Quórum de servidores y de clientes, detección de caídas en 10 s, curación granular y sin cachés del cliente: cada nodo ve los cambios al instante.",
			Options: map[string]string{
				"cluster.heal-timeout":          "10",
				"cluster.shd-wait-qlength":      "512",
				"cluster.shd-max-threads":       "2",
				"performance.open-behind":       "off",
				"performance.quick-read":        "off",
				"performance.read-ahead":        "off",
				"performance.write-behind":      "off",
				"diagnostics.client-log-level":  "WARNING",
				"diagnostics.brick-log-level":   "WARNING",
				"network.ping-timeout":          "10",
				"cluster.server-quorum-type":    "server",
				"cluster.quorum-type":           "auto",
				"cluster.granular-entry-heal":   "on",
				"storage.fips-mode-rchecksum":   "on",
				"transport.address-family":      "inet",
				"performance.client-io-threads": "on",
				"cluster.server-quorum-ratio":   "51%",
			}},
	}
}

// PresetByID busca un preset.
func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets() {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// ---- Argumentos del CLI -------------------------------------------------------------
//
// Todas devuelven solo los argumentos de la operación; el agente antepone
// Binary, "--mode=script" y "--xml". Ninguna agrega "force".

// ArgsPeerProbe une un nodo al clúster.
func ArgsPeerProbe(host string) []string { return []string{"peer", "probe", host} }

// ArgsPeerDetach saca un nodo (falla si todavía tiene bricks).
func ArgsPeerDetach(host string) []string { return []string{"peer", "detach", host} }

// ArgsPoolList lista los nodos del clúster, incluido este.
func ArgsPoolList() []string { return []string{"pool", "list"} }

// ArgsVolumeInfo es `volume info all`.
func ArgsVolumeInfo() []string { return []string{"volume", "info", "all"} }

// ArgsVolumeStatus es `volume status all detail` (estado, puerto y espacio de cada brick).
func ArgsVolumeStatus() []string { return []string{"volume", "status", "all", "detail"} }

// ArgsHealSummary resume lo pendiente de curar y los split-brain por brick.
func ArgsHealSummary(vol string) []string { return []string{"volume", "heal", vol, "info", "summary"} }

// ArgsVolumeCreate crea un volumen (no lo arranca).
func ArgsVolumeCreate(name string, l Layout, bricks []Brick) []string {
	a := []string{"volume", "create", name}
	if l.Replica > 1 {
		a = append(a, "replica", strconv.Itoa(l.Replica))
		if l.Arbiter {
			a = append(a, "arbiter", "1")
		}
	}
	a = append(a, "transport", "tcp")
	for _, b := range bricks {
		a = append(a, b.String())
	}
	return a
}

// ArgsVolumeAction arranca, detiene o borra un volumen.
func ArgsVolumeAction(name, action string) ([]string, error) {
	switch action {
	case "start", "stop", "delete":
		return []string{"volume", action, name}, nil
	}
	return nil, fmt.Errorf("acción inválida: %q", action)
}

// ArgsHeal lanza una curación (incremental o completa).
func ArgsHeal(name string, full bool) []string {
	if full {
		return []string{"volume", "heal", name, "full"}
	}
	return []string{"volume", "heal", name}
}

// ArgsSet aplica una opción; las globales van con `volume set all`.
func ArgsSet(vol, key, val string) []string {
	if IsGlobal(key) {
		return []string{"volume", "set", "all", key, val}
	}
	return []string{"volume", "set", vol, key, val}
}

// ArgsReset vuelve una opción a su valor por defecto.
func ArgsReset(vol, key string) []string { return []string{"volume", "reset", vol, key} }

// ArgsAddBrick suma bricks (con replica > 0 cambia la cantidad de copias).
func ArgsAddBrick(vol string, replica int, bricks []Brick) []string {
	a := []string{"volume", "add-brick", vol}
	if replica > 0 {
		a = append(a, "replica", strconv.Itoa(replica))
	}
	for _, b := range bricks {
		a = append(a, b.String())
	}
	return a
}

// ArgsRemoveBrick saca bricks en dos pasos: start (migra los datos) y commit; con
// replica > 0 baja la cantidad de copias y va directo con force (no hay datos que
// migrar: las otras copias ya los tienen).
func ArgsRemoveBrick(vol string, replica int, bricks []Brick, phase string) ([]string, error) {
	a := []string{"volume", "remove-brick", vol}
	if replica > 0 {
		a = append(a, "replica", strconv.Itoa(replica))
	}
	for _, b := range bricks {
		a = append(a, b.String())
	}
	switch phase {
	case "start", "status", "commit":
		if replica > 0 && phase != "commit" {
			return nil, fmt.Errorf("al bajar la réplica el paso es commit")
		}
		if replica > 0 {
			return append(a, "force"), nil
		}
		return append(a, phase), nil
	}
	return nil, fmt.Errorf("paso inválido: %q", phase)
}

// ArgsReplaceBrick cambia un brick por otro (GlusterFS solo admite "commit force":
// la copia nueva se completa con la curación).
func ArgsReplaceBrick(vol string, from, to Brick) []string {
	return []string{"volume", "replace-brick", vol, from.String(), to.String(), "commit", "force"}
}
