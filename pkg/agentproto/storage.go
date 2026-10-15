package agentproto

// ---- Fase M2: almacenamiento (protocol/README.md §7, docs/04) -------------------
//
// Las operaciones largas (volume.create, scrub.start) son asíncronas: el agente
// responde enseguida con el op_id y tangod consulta job.status hasta que terminan.
// Cada una queda en el journal; si el agente muere a mitad, job.status la
// devuelve como "interrupted" y nunca se reintenta sola.

// Nombres de operaciones de almacenamiento.
const (
	OpDiskInspect    = "disk.inspect"
	OpVolumeCreate   = "volume.create"
	OpScrubStart     = "scrub.start"
	OpJobStatus      = "job.status"
	OpSubvolCreate   = "subvol.create"
	OpSnapshotCreate = "snapshot.create"
	OpSambaApply     = "samba.share.apply"

	// Fase M2.1 (protocol/README.md §8).
	OpVolumeDelete = "volume.delete"
	OpShareDelete  = "share.delete"
	OpSharePerms   = "share.permissions"
	OpUserEnsure   = "user.ensure"
	OpUserPassword = "user.password"
)

// Códigos de error de almacenamiento.
const (
	CodeNotEligible   = "DEVICE_NOT_ELIGIBLE"
	CodeDeviceChanged = "DEVICE_CHANGED"
	CodeBusy          = "BUSY"
	CodeExists        = "ALREADY_EXISTS"
	CodeNotFound      = "NOT_FOUND"
	CodeNotInstalled  = "NOT_INSTALLED"
	CodeFailed        = "OPERATION_FAILED"
)

// DiskInspectArgs pide revisar discos antes de planear un volumen.
type DiskInspectArgs struct {
	DevIDs []string `json:"dev_ids"`
}

// DiskInspection describe un disco: su huella (para detectar cambios entre el plan y la
// ejecución), qué contiene y si es elegible para un volumen nuevo.
type DiskInspection struct {
	DevID       string `json:"dev_id"`
	Dev         string `json:"dev"`
	Size        uint64 `json:"size"`
	Fingerprint string `json:"fingerprint"`
	Content     string `json:"content"`
	Eligible    bool   `json:"eligible"`
	Reason      string `json:"reason,omitempty"`
}

// DiskInspectResult responde a disk.inspect.
type DiskInspectResult struct {
	Disks []DiskInspection `json:"disks"`
}

// VolumeDisk es un disco del volumen con la huella que vio el plan.
type VolumeDisk struct {
	DevID       string `json:"dev_id"`
	Fingerprint string `json:"fingerprint"`
}

// VolumeCreateArgs crea un volumen: md (raid1) + Btrfs, montado en /<name>.
type VolumeCreateArgs struct {
	OpID    string       `json:"op_id"`
	Name    string       `json:"name"`
	Profile string       `json:"profile"` // basic | raid1 | raid5 | raid6 | raid10
	Disks   []VolumeDisk `json:"disks"`
}

// VolumeCreateResult es el resultado final de volume.create (en job.status).
type VolumeCreateResult struct {
	Name   string `json:"name"`
	Mount  string `json:"mount"`
	MD     string `json:"md,omitempty"` // "md127" (vacío en basic)
	FSUUID string `json:"fs_uuid"`
}

// ScrubStartArgs verifica un volumen: check de md (si hay) y scrub de Btrfs.
type ScrubStartArgs struct {
	OpID  string `json:"op_id"`
	Mount string `json:"mount"`
}

// ScrubResult es el resultado final del scrub.
type ScrubResult struct {
	MDMismatches  uint64 `json:"md_mismatches"`
	CsumErrors    uint64 `json:"csum_errors"`
	ReadErrors    uint64 `json:"read_errors"`
	Corrected     uint64 `json:"corrected"`
	Uncorrectable uint64 `json:"uncorrectable"`
	BytesScrubbed uint64 `json:"bytes_scrubbed"`
}

// OpRef identifica una operación asíncrona.
type OpRef struct {
	OpID string `json:"op_id"`
}

// JobStatus es el estado de una operación asíncrona.
type JobStatus struct {
	OpID     string  `json:"op_id"`
	Op       string  `json:"op"`
	State    string  `json:"state"` // running | succeeded | failed | interrupted
	Progress float64 `json:"progress"`
	Stage    string  `json:"stage"`
	Error    *Error  `json:"error,omitempty"`
	Result   any     `json:"result,omitempty"`
}

// SubvolCreateArgs crea un subvolumen bajo un volumen montado (p. ej. "@shares/Familia").
type SubvolCreateArgs struct {
	Mount string `json:"mount"`
	Path  string `json:"path"`
}

// SnapshotCreateArgs crea una instantánea read-only de @shares/<share> en
// @snapshots/<share>/<name>.
type SnapshotCreateArgs struct {
	Mount string `json:"mount"`
	Share string `json:"share"`
	Name  string `json:"name"`
}

// SambaShareArgs publica (o actualiza) una carpeta compartida por SMB.
type SambaShareArgs struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Comment string `json:"comment"`
	// Time Machine (protocol/README.md §13).
	TimeMachine bool  `json:"time_machine,omitempty"`
	TMMaxBytes  int64 `json:"tm_max_bytes,omitempty"`
}

// SambaShareResult indica si smbd recargó la configuración.
type SambaShareResult struct {
	Reloaded bool `json:"reloaded"`
}

// ---- Fase M2.1 --------------------------------------------------------------------

// VolumeDeleteArgs elimina un volumen: despublica sus carpetas, lo desmonta, borra la
// unidad .mount, detiene el md y limpia superbloques y firmas de sus discos.
// DevIDs son los miembros que vio el plan: el agente exige que coincidan con los
// miembros reales del array (o con el disco del volumen básico) antes de borrar.
type VolumeDeleteArgs struct {
	OpID   string   `json:"op_id"`
	Name   string   `json:"name"`
	DevIDs []string `json:"dev_ids"`
	Shares []string `json:"shares"`
}

// VolumeDeleteResult es el resultado final de volume.delete.
type VolumeDeleteResult struct {
	Wiped []string `json:"wiped"`
}

// ShareDeleteArgs borra una carpeta compartida: sus instantáneas, el subvolumen y su
// sección de Samba.
type ShareDeleteArgs struct {
	Mount string `json:"mount"`
	Share string `json:"share"`
}

// ShareDeleteResult cuenta lo borrado.
type ShareDeleteResult struct {
	Snapshots int  `json:"snapshots"`
	Reloaded  bool `json:"reloaded"`
}

// SharePermsArgs aplica los permisos de una carpeta: los usuarios de RW y RO quedan
// como únicos miembros de tg-<carpeta>-rw / -ro, el subvolumen recibe dueño y ACL, y
// si SMB está activo la sección de Samba pasa a acceso por grupos.
type SharePermsArgs struct {
	Mount   string   `json:"mount"`
	Share   string   `json:"share"`
	Comment string   `json:"comment"`
	SMB     bool     `json:"smb"`
	RW      []string `json:"rw"`
	RO      []string `json:"ro"`
	// Time Machine (protocol/README.md §13).
	TimeMachine bool  `json:"time_machine,omitempty"`
	TMMaxBytes  int64 `json:"tm_max_bytes,omitempty"`
}

// SharePermsResult indica si smbd recargó la configuración.
type SharePermsResult struct {
	Reloaded bool `json:"reloaded"`
}

// UserEnsureArgs crea (si falta) el usuario Unix sin login de un usuario de TangoOS.
type UserEnsureArgs struct {
	Name string `json:"name"`
	Full string `json:"full"`
}

// UserEnsureResult devuelve el UID.
type UserEnsureResult struct {
	UID uint32 `json:"uid"`
}

// UserPasswordArgs crea o actualiza la cuenta Samba de un usuario. La contraseña
// nunca se loguea ni se guarda en el journal.
type UserPasswordArgs struct {
	Name     string `json:"name"`
	Full     string `json:"full"`
	Password string `json:"password"`
}
