package agentproto

import "github.com/lordbasex/go-link-tangoos/internal/gluster"

// ---- Clúster GlusterFS, segunda etapa (protocol/README.md §17, ADR 0012) --------------
//
// TLS entre nodos, bricks en un LV dedicado con XFS (thin para instantáneas), cuotas,
// rebalance e instantáneas. Mismas reglas: argv armado con internal/gluster, valores
// validados, nada de texto libre hacia el CLI.

const (
	OpGlusterTLS       = "gluster.tls"        // archivos TLS de este nodo, secure-access y reinicio de glusterd
	OpGlusterVolumeTLS = "gluster.volume.tls" // client.ssl / server.ssl / auth.ssl-allow de un volumen
	OpGlusterBrickLV   = "gluster.brick.lv"   // crea o borra el LV (XFS) de un brick dedicado
	OpGlusterQuota     = "gluster.quota"      // cuotas por directorio
	OpGlusterRebalance = "gluster.rebalance"  // start / status / stop
	OpGlusterSnapshot  = "gluster.snapshot"   // instantáneas (bricks en LV thin)
)

// GlusterTLSArgs deja en /etc/ssl/glusterfs.{pem,key,ca} el certificado del nodo
// (vacíos = no tocarlos), crea o borra /var/lib/glusterd/secure-access (TLS de
// management) y, con Restart, reinicia glusterd para que lo tome.
type GlusterTLSArgs struct {
	Cert    string `json:"cert"`
	Key     string `json:"key"`
	CA      string `json:"ca"`
	Secure  bool   `json:"secure"`
	Restart bool   `json:"restart"`
}

// Redacted oculta la clave (journal).
func (a GlusterTLSArgs) Redacted() GlusterTLSArgs {
	if a.Key != "" {
		a.Key = "[redactada]"
	}
	return a
}

// GlusterTLSState es lo que el agente sabe del TLS de este nodo.
type GlusterTLSState struct {
	Files  bool `json:"files"`  // están los tres archivos
	Secure bool `json:"secure"` // secure-access (management con TLS)
}

// GlusterVolumeTLSArgs activa o apaga el TLS de E/S de un volumen. CNs = nodos
// permitidos (auth.ssl-allow); se ignora con On=false.
type GlusterVolumeTLSArgs struct {
	Name string   `json:"name"`
	On   bool     `json:"on"`
	CNs  []string `json:"cns"`
}

// GlusterBrickLVArgs: Op create | delete. Create hace un LV (thin si Thin) en el grupo
// Pool con XFS (-i size=512), lo monta en /srv/gluster/<Vol> y crea brick/.
type GlusterBrickLVArgs struct {
	Op   string `json:"op"`
	Pool string `json:"pool"`
	Vol  string `json:"vol"`
	Size uint64 `json:"size"`
	Thin bool   `json:"thin"`
	// Self (delete): los nombres e IPs de este nodo. Si el volumen todavía existe, solo se
	// borra el LV cuando ningún brick de este nodo lo usa (el agente no ve las IPs: no
	// tiene red).
	Self []string `json:"self,omitempty"`
}

// GlusterBrickLVResult es el brick listo.
type GlusterBrickLVResult struct {
	Path string `json:"path"`
	Thin bool   `json:"thin"`
}

// GlusterLVBrick es un LV de brick de este nodo (para el estado y los huérfanos).
type GlusterLVBrick struct {
	Vol     string `json:"vol"`
	Pool    string `json:"pool"`
	Size    uint64 `json:"size"`
	Thin    bool   `json:"thin"`
	Mounted bool   `json:"mounted"`
	// Snaps: LV de instantáneas de GlusterFS que salieron de este (restauraciones).
	Snaps int `json:"snaps"`
}

// GlusterQuotaArgs: Op enable | disable | limit | remove | list.
type GlusterQuotaArgs struct {
	Name  string `json:"name"`
	Op    string `json:"op"`
	Path  string `json:"path"`
	Limit uint64 `json:"limit"`
}

// GlusterRebalanceArgs: Action start | status | stop.
type GlusterRebalanceArgs struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

// GlusterSnapshotArgs: Op create | delete | restore | activate | deactivate | info.
type GlusterSnapshotArgs struct {
	Name string `json:"name"` // volumen
	Op   string `json:"op"`
	Snap string `json:"snap"`
	Desc string `json:"desc"`
}

// GlusterTaskResult envuelve el estado de un rebalance o de un remove-brick.
type GlusterTaskResult struct {
	Task *gluster.Task `json:"task,omitempty"`
}
