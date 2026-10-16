package agentproto

// ---- Replicación ZFS (protocol/README.md §18.1, ADR 0016) -----------------------------
//
// El agente no tiene red: zfs.repl.send y zfs.repl.recv responden con un extremo de un
// pipe por SCM_RIGHTS (como files.worker) y tangod lleva los bytes, a otro pool de este
// NAS o por TLS a otro TangoOS. zfs.repl.wait devuelve cómo terminó el proceso.

const (
	OpZfsReplState    = "zfs.repl.state"
	OpZfsReplSnapshot = "zfs.repl.snapshot"
	OpZfsReplSend     = "zfs.repl.send" // responde con un fd (lectura)
	OpZfsReplRecv     = "zfs.repl.recv" // responde con un fd (escritura)
	OpZfsReplWait     = "zfs.repl.wait"
	OpZfsReplPrune    = "zfs.repl.prune"   // origen: borra instantáneas viejas de la tarea
	OpZfsReplMirror   = "zfs.repl.mirror"  // destino: borra las que ya no están en el origen
	OpZfsReplAbort    = "zfs.repl.abort"   // destino: descarta un envío cortado (receive -A)
	OpZfsReplDestroy  = "zfs.repl.destroy" // destino: borra una réplica
	OpZfsReplPromote  = "zfs.repl.promote" // destino: convierte la réplica en un volumen
)

// ReplSnap es una instantánea de un dataset.
type ReplSnap struct {
	Name string `json:"name"`
	GUID string `json:"guid"`
	Txg  uint64 `json:"txg"`
}

// ReplDataset es un dataset de un volumen (o de su réplica): Rel es "" (el volumen),
// "shares", "apps", "shares/<Carpeta>" o "apps/<app>".
type ReplDataset struct {
	Rel       string     `json:"rel"`
	GUID      string     `json:"guid"`
	Snaps     []ReplSnap `json:"snaps"`
	Token     string     `json:"token,omitempty"` // receive_resume_token (envío cortado)
	Encrypted bool       `json:"encrypted,omitempty"`
	EncRoot   string     `json:"encRoot,omitempty"`
	KeyLoaded bool       `json:"keyLoaded,omitempty"`
}

// ZfsReplStateArgs: el volumen de origen (Origin vacío) o su réplica en el destino.
type ZfsReplStateArgs struct {
	Pool   string `json:"pool"`
	Origin string `json:"origin,omitempty"`
	Vol    string `json:"vol"`
}

// ZfsReplState responde a zfs.repl.state. Exists=false: no hay nada todavía.
type ZfsReplState struct {
	Exists   bool          `json:"exists"`
	Datasets []ReplDataset `json:"datasets"`
}

// ZfsReplSnapshotArgs crea <pool>/<vol>@<name> recursivo.
type ZfsReplSnapshotArgs struct {
	Pool string `json:"pool"`
	Vol  string `json:"vol"`
	Name string `json:"name"`
}

// ZfsReplSendArgs: Token retoma; si no, Snap (con From, incremental -I). Pool, Vol y Rel
// van siempre: el agente comprueba que el token (que viene del destino) sea de ese
// dataset, de una instantánea de TangoOS y raw.
type ZfsReplSendArgs struct {
	Pool  string `json:"pool"`
	Vol   string `json:"vol"`
	Rel   string `json:"rel"`
	Snap  string `json:"snap,omitempty"`
	From  string `json:"from,omitempty"`
	Token string `json:"token,omitempty"`
}

// ZfsReplRecvArgs recibe en <pool>/tgrepl/<origin>/<vol>/<rel>. Incremental: se borran
// antes las instantáneas posteriores a BaseGUID y se recibe con -F.
type ZfsReplRecvArgs struct {
	Pool     string `json:"pool"`
	Origin   string `json:"origin"`
	Vol      string `json:"vol"`
	Rel      string `json:"rel"`
	BaseGUID string `json:"baseGuid,omitempty"`
}

// ZfsStreamResult acompaña al fd: ID para zfs.repl.wait y el tamaño estimado.
type ZfsStreamResult struct {
	ID   string `json:"id"`
	Size uint64 `json:"size,omitempty"`
}

// ZfsReplWaitArgs espera a que termine el proceso de un stream.
type ZfsReplWaitArgs struct {
	ID string `json:"id"`
}

// ZfsReplPruneArgs borra (-r) las instantáneas de la tarea Task salvo Keep.
type ZfsReplPruneArgs struct {
	Pool string `json:"pool"`
	Vol  string `json:"vol"`
	Task string `json:"task"`
	Keep string `json:"keep"`
}

// ZfsReplMirrorArgs borra en la réplica las instantáneas cuyo GUID no está en Keep[rel].
type ZfsReplMirrorArgs struct {
	Pool   string              `json:"pool"`
	Origin string              `json:"origin"`
	Vol    string              `json:"vol"`
	Keep   map[string][]string `json:"keep"`
}

// ZfsReplTargetArgs señala una réplica (o un dataset de ella con Rel).
type ZfsReplTargetArgs struct {
	Pool   string `json:"pool"`
	Origin string `json:"origin"`
	Vol    string `json:"vol"`
	Rel    string `json:"rel,omitempty"`
}

// ZfsReplPromoteArgs convierte la réplica en el volumen <pool>/<Name> (mismo pool).
// Si está cifrada, Passphrase carga la clave (la de origen); si no se da, queda bloqueado.
type ZfsReplPromoteArgs struct {
	OpID       string `json:"op_id,omitempty"`
	Pool       string `json:"pool"`
	Origin     string `json:"origin"`
	Vol        string `json:"vol"`
	Name       string `json:"name"`
	Passphrase string `json:"passphrase,omitempty"`
}
