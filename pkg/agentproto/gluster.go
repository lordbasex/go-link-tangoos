package agentproto

import "github.com/lordbasex/go-link-tangoos/internal/gluster"

// ---- Clúster GlusterFS (protocol/README.md §17, ADR 0012) ----------------------------
//
// Operaciones tipadas: el agente arma el argv de `gluster --mode=script --xml` con
// internal/gluster (nunca recibe un comando ni una opción fuera de la allow-list).

const (
	OpGlusterInstall      = "gluster.install"        // asíncrona: nftables + glusterfs-server
	OpGlusterFirewall     = "gluster.firewall"       // allow-list de nodos (nftables)
	OpGlusterStatus       = "gluster.status"         // nodos, volúmenes, curación, bricks huérfanos
	OpGlusterPeer         = "gluster.peer"           // probe / detach
	OpGlusterVolumeCreate = "gluster.volume.create"  // create (+ opciones y start)
	OpGlusterVolumeAction = "gluster.volume.action"  // start / stop / delete
	OpGlusterVolumeSet    = "gluster.volume.options" // set / reset con allow-list
	OpGlusterHeal         = "gluster.volume.heal"    // curación incremental o completa
	OpGlusterBricks       = "gluster.volume.bricks"  // add / remove / replace
	OpGlusterMount        = "gluster.mount"          // montaje local y carpeta SMB
	OpGlusterBrickClean   = "gluster.brick.cleanup"  // borra un brick huérfano de este nodo
	OpGlusterHealInfo     = "gluster.heal.info"      // pendientes y split-brain por brick
)

// GlusterHealInfoArgs pide `volume heal <Name> info summary`. Corre fuera del sandbox
// del agente: glfsheal se conecta a glusterd y a los bricks por TCP y el agente no
// tiene red.
type GlusterHealInfoArgs struct {
	Name string `json:"name"`
}

// GlusterInstallArgs instala GlusterFS con el firewall ya cargado (Allowed: los
// nodos permitidos desde el primer momento; vacío = solo este equipo).
type GlusterInstallArgs struct {
	OpID    string   `json:"op_id"`
	Allowed []string `json:"allowed"`
}

// GlusterFirewallArgs reemplaza la allow-list (solo IPs: tangod resuelve los nombres,
// el agente no tiene red).
type GlusterFirewallArgs struct {
	Allowed []string `json:"allowed"`
}

// GlusterStatusArgs: Self es el nombre de este nodo para reemplazar "localhost".
type GlusterStatusArgs struct {
	Self string `json:"self"`
}

// GlusterOrphan es un brick de este nodo cuyo volumen ya no existe. Kind "lv": un LV
// dedicado (TangoVolume es el grupo); si no, una carpeta en un volumen de TangoOS.
type GlusterOrphan struct {
	Kind        string `json:"kind,omitempty"`
	TangoVolume string `json:"tango_volume"`
	Name        string `json:"name"`
	Path        string `json:"path"`
}

// GlusterMountInfo es un volumen montado en /cluster.
type GlusterMountInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Mounted bool   `json:"mounted"`
	Share   string `json:"share,omitempty"`
}

// GlusterStatusResult: Installed=false si no está glusterfs-server; Running=false si
// glusterd no responde (Error lo explica).
type GlusterStatusResult struct {
	Installed bool               `json:"installed"`
	Running   bool               `json:"running"`
	Version   string             `json:"version,omitempty"`
	Error     string             `json:"error,omitempty"`
	Peers     []gluster.Peer     `json:"peers"`
	Volumes   []gluster.Volume   `json:"volumes"`
	Orphans   []GlusterOrphan    `json:"orphans"`
	Mounts    []GlusterMountInfo `json:"mounts"`
	Allowed   []string           `json:"allowed"`
	TLS       GlusterTLSState    `json:"tls"`
	LVBricks  []GlusterLVBrick   `json:"lv_bricks"`
}

// GlusterPeerArgs une (probe) o saca (detach) un nodo.
type GlusterPeerArgs struct {
	Action string `json:"action"`
	Host   string `json:"host"`
}

// GlusterVolumeCreateArgs crea un volumen; Options se aplican antes de arrancarlo.
type GlusterVolumeCreateArgs struct {
	Name    string            `json:"name"`
	Layout  gluster.Layout    `json:"layout"`
	Bricks  []gluster.Brick   `json:"bricks"`
	Options map[string]string `json:"options"`
	Start   bool              `json:"start"`
}

// GlusterVolumeActionArgs arranca, detiene o borra.
type GlusterVolumeActionArgs struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

// GlusterVolumeOptionsArgs cambia opciones (Set) y vuelve otras a su valor por
// defecto (Reset).
type GlusterVolumeOptionsArgs struct {
	Name  string            `json:"name"`
	Set   map[string]string `json:"set"`
	Reset []string          `json:"reset"`
}

// GlusterHealArgs lanza una curación.
type GlusterHealArgs struct {
	Name string `json:"name"`
	Full bool   `json:"full"`
}

// GlusterBricksArgs: Op add | remove | replace. Replica > 0 cambia la cantidad de
// copias (add/remove). Phase (remove): start | status | commit. From/To (replace).
type GlusterBricksArgs struct {
	Name    string          `json:"name"`
	Op      string          `json:"op"`
	Replica int             `json:"replica"`
	Bricks  []gluster.Brick `json:"bricks"`
	Phase   string          `json:"phase"`
	From    gluster.Brick   `json:"from"`
	To      gluster.Brick   `json:"to"`
}

// GlusterMountArgs monta (o desmonta) un volumen en /cluster/<Name> y, con Share,
// lo publica por SMB con los permisos de TangoOS (grupos tg-<carpeta>-rw/-ro).
type GlusterMountArgs struct {
	// Action "stop" o "start" solo detiene o arranca el montaje ya configurado (sin tocar
	// la carpeta SMB): lo usa la parada controlada al prender o apagar TLS.
	Action  string   `json:"action,omitempty"`
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Backups []string `json:"backups"`
	Share   string   `json:"share"`
	Comment string   `json:"comment"`
	RW      []string `json:"rw"`
	RO      []string `json:"ro"`
}

// GlusterBrickCleanArgs borra /<TangoVolume>/@gluster/<Name> si ese volumen ya no
// existe en el clúster.
type GlusterBrickCleanArgs struct {
	TangoVolume string `json:"tango_volume"`
	Name        string `json:"name"`
}
