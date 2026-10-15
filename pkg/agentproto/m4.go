package agentproto

// ---- Fase M4: producto (protocol/README.md §12) ------------------------------------
//
// Operaciones del agente para el sistema: energía, identidad (hostname y zona horaria),
// journal, actualizaciones con instantánea del root y vuelta atrás, NFS, iSCSI (LIO por
// configfs), estado de servicios y terminal de administración (PTY entregado por
// SCM_RIGHTS, como el file-worker).

const (
	OpPower         = "system.power"
	OpIdentity      = "system.identity"
	OpLogsQuery     = "logs.query"
	OpUpdatesCheck  = "updates.check"
	OpUpdatesApply  = "updates.apply"
	OpRollbackList  = "rollback.list"
	OpRollbackApply = "rollback.apply"
	OpServicesState = "services.state"
	OpServiceSet    = "service.set"
	OpNFSApply      = "nfs.apply"
	OpISCSIApply    = "iscsi.apply"
	OpISCSIStatus   = "iscsi.status"
	OpTermSpawn     = "term.spawn"
	OpM4Job         = "m4.job"
)

// PowerArgs: reboot | poweroff. Se ejecuta 1 s después de responder.
type PowerArgs struct {
	Action string `json:"action"`
}

// IdentityArgs cambia el hostname y/o la zona horaria (vacío = no tocar).
type IdentityArgs struct {
	Hostname string `json:"hostname,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// LogsQueryArgs filtra el journal. Unit es un alias del allow-list (tangod, tango-agent,
// smbd, docker, kernel) o vacío (todo lo de TangoOS).
type LogsQueryArgs struct {
	Unit  string `json:"unit,omitempty"`
	Q     string `json:"q,omitempty"`
	Since string `json:"since,omitempty"`
	Limit int    `json:"limit"`
}

// LogLine es exactamente el LogLine del cliente.
type LogLine struct {
	At   string `json:"at"`
	Unit string `json:"unit"`
	Prio int    `json:"prio"`
	Msg  string `json:"msg"`
}

// LogsQueryResult responde a logs.query.
type LogsQueryResult struct {
	Lines []LogLine `json:"lines"`
}

// PackageUpdate es un paquete con versión nueva.
type PackageUpdate struct {
	Name     string `json:"name"`
	From     string `json:"from"`
	To       string `json:"to"`
	Security bool   `json:"security"`
}

// UpdatesCheckResult responde a updates.check.
type UpdatesCheckResult struct {
	TangoOSCurrent   string          `json:"tangoos_current"`
	TangoOSAvailable string          `json:"tangoos_available,omitempty"`
	Debian           []PackageUpdate `json:"debian"`
	RootBtrfs        bool            `json:"root_btrfs"`
	Refreshed        bool            `json:"refreshed"`
}

// UpdatesApplyArgs: scope all | security (asíncrona).
type UpdatesApplyArgs struct {
	OpID  string `json:"op_id"`
	Scope string `json:"scope"`
}

// RollbackPoint es un punto de vuelta atrás (instantánea del root).
type RollbackPoint struct {
	ID      string `json:"id"`
	At      string `json:"at"`
	Label   string `json:"label"`
	Current bool   `json:"current,omitempty"`
}

// RollbackListResult responde a rollback.list.
type RollbackListResult struct {
	Supported bool            `json:"supported"`
	Reason    string          `json:"reason,omitempty"`
	Points    []RollbackPoint `json:"points"`
}

// RollbackApplyArgs vuelve a un punto y reinicia.
type RollbackApplyArgs struct {
	OpID string `json:"op_id"`
	ID   string `json:"id"`
}

// ServiceState es el estado de una unidad de systemd.
type ServiceState struct {
	Unit      string `json:"unit"`
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	Enabled   bool   `json:"enabled"`
}

// ServicesStateArgs pide el estado de unidades del allow-list.
type ServicesStateArgs struct {
	Units []string `json:"units"`
}

// ServicesStateResult responde a services.state.
type ServicesStateResult struct {
	Units []ServiceState `json:"units"`
}

// ServiceSetArgs habilita o deshabilita una unidad del allow-list (smbd, nfs-server…).
type ServiceSetArgs struct {
	Unit    string `json:"unit"`
	Enabled bool   `json:"enabled"`
}

// NFSExport es una carpeta exportada.
type NFSExport struct {
	Path    string      `json:"path"`
	Clients []NFSClient `json:"clients"`
}

// NFSClient es un host o red con su acceso.
type NFSClient struct {
	Host   string `json:"host"`
	Access string `json:"access"` // rw | ro
}

// NFSApplyArgs escribe /etc/exports.d/tango.exports y deja nfs-server como corresponde
// (lo instala si hace falta: asíncrona).
type NFSApplyArgs struct {
	OpID    string      `json:"op_id"`
	Enabled bool        `json:"enabled"`
	Exports []NFSExport `json:"exports"`
}

// ISCSITarget es un destino LIO con un LUN de archivo.
type ISCSITarget struct {
	Name       string `json:"name"`
	IQN        string `json:"iqn"`
	File       string `json:"file"`           // /<vol>/@iscsi/<name>.img
	Zvol       string `json:"zvol,omitempty"` // o /dev/zvol/<pool>/zvol/<name> (ZFS, ADR 0013)
	SizeBytes  int64  `json:"size_bytes"`
	CHAPUser   string `json:"chap_user,omitempty"`
	CHAPSecret string `json:"chap_secret,omitempty"`
}

// ISCSIApplyArgs deja configfs exactamente con estos destinos (asíncrona: puede crear
// archivos grandes). Delete lista archivos de LUN a borrar (destinos eliminados).
type ISCSIApplyArgs struct {
	OpID    string        `json:"op_id"`
	Enabled bool          `json:"enabled"`
	Targets []ISCSITarget `json:"targets"`
	Delete  []string      `json:"delete,omitempty"`
}

// ISCSIStatusResult: sesiones por IQN y si el módulo está cargado.
type ISCSIStatusResult struct {
	Loaded   bool           `json:"loaded"`
	Sessions map[string]int `json:"sessions"`
}

// TermSpawnArgs abre una shell de administración en un PTY.
type TermSpawnArgs struct {
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
	User string `json:"user"` // usuario de TangoOS que la pidió (auditoría)
}

// TermSpawnResult acompaña al fd del master del PTY.
type TermSpawnResult struct {
	PID int `json:"pid"`
}

// M4JobArgs consulta una operación asíncrona de M4.
type M4JobArgs struct {
	OpID string `json:"op_id"`
}
