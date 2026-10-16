package agentproto

// ---- ZFS (protocol/README.md §18, ADR 0013) ----------------------------------------
//
// Pools ZFS como alternativa a md + LVM + Btrfs. El agente arma el argv de zpool/zfs
// con internal/zfs (allow-list de propiedades) y parsea la salida JSON (-j). Lo que
// monta o desmonta corre en una unidad transitoria (el namespace del sistema); las
// frases de cifrado viajan solo en los argumentos y se pasan por stdin, nunca al
// journal ni al log.

const (
	OpZfsReport  = "zfs.report"
	OpZfsEnable  = "zfs.enable"
	OpZfsCreate  = "zfs.pool.create"
	OpZfsAdd     = "zfs.pool.add"
	OpZfsAux     = "zfs.pool.aux"
	OpZfsReplace = "zfs.pool.replace"
	OpZfsScrub   = "zfs.pool.scrub"
	OpZfsImport  = "zfs.pool.import"
	OpZfsExport  = "zfs.pool.export"
	OpZfsDestroy = "zfs.pool.destroy"

	OpZfsVolCreate  = "zfs.volume.create"
	OpZfsVolSet     = "zfs.volume.set"
	OpZfsVolDelete  = "zfs.volume.delete"
	OpZfsUnlock     = "zfs.volume.unlock"
	OpZfsLock       = "zfs.volume.lock"
	OpZfsPassphrase = "zfs.volume.passphrase"
	OpZfsKeyDevice  = "zfs.volume.keyDevice"

	OpZfsZvolCreate = "zfs.zvol.create"
	OpZfsZvolDelete = "zfs.zvol.delete"
)

// CodeZfsUnavailable: ZFS no está habilitado (falta el módulo o zfsutils).
const CodeZfsUnavailable = "ZFS_UNAVAILABLE"

// ZfsReport responde a zfs.report.
type ZfsReport struct {
	Available  bool            `json:"available"` // módulo cargado y zfsutils instalado
	Installed  bool            `json:"installed"` // zfsutils-linux instalado (puede faltar el módulo)
	Version    string          `json:"version,omitempty"`
	Pools      []ZfsPoolInfo   `json:"pools"`
	Importable []ZfsImportable `json:"importable"`
}

// ZfsPoolInfo es un pool importado.
type ZfsPoolInfo struct {
	Name     string       `json:"name"`
	GUID     string       `json:"guid"`
	Health   string       `json:"health"` // ONLINE | DEGRADED | FAULTED | OFFLINE | UNAVAIL | SUSPENDED
	ReadOnly bool         `json:"readonly"`
	Size     uint64       `json:"size"`
	Alloc    uint64       `json:"alloc"`
	Free     uint64       `json:"free"`
	Frag     *int         `json:"frag,omitempty"`
	Scan     *ZfsScan     `json:"scan,omitempty"`
	Vdevs    []ZfsVdev    `json:"vdevs"`
	Cache    []ZfsLeaf    `json:"cache"`
	Logs     []ZfsVdev    `json:"logs"`
	Spares   []ZfsLeaf    `json:"spares"`
	Errors   string       `json:"errors,omitempty"` // "No known data errors" u otro texto de zpool
	Datasets []ZfsDataset `json:"datasets"`
}

// ZfsScan es el último scrub o resilver.
type ZfsScan struct {
	Function string  `json:"function"` // scrub | resilver
	State    string  `json:"state"`    // scanning | finished | canceled
	Start    int64   `json:"start,omitempty"`
	End      int64   `json:"end,omitempty"`
	Examined uint64  `json:"examined"`
	Total    uint64  `json:"total"`
	Errors   uint64  `json:"errors"`
	Progress float64 `json:"progress"` // 0..1
}

// ZfsVdev es un vdev de datos (o de log): disco suelto, mirror o raidzN.
type ZfsVdev struct {
	Name  string    `json:"name"`
	Type  string    `json:"type"` // disk | mirror | raidz1 | raidz2 | raidz3
	State string    `json:"state"`
	Read  uint64    `json:"read"`
	Write uint64    `json:"write"`
	Cksum uint64    `json:"cksum"`
	Size  uint64    `json:"size"`
	Disks []ZfsLeaf `json:"disks"`
}

// ZfsLeaf es un disco del pool.
type ZfsLeaf struct {
	Name        string `json:"name"` // como lo muestra zpool status (by-id o GUID si no está)
	GUID        string `json:"guid"`
	Disk        string `json:"disk"` // disco entero del kernel (sdb), vacío si no está
	State       string `json:"state"`
	Read        uint64 `json:"read"`
	Write       uint64 `json:"write"`
	Cksum       uint64 `json:"cksum"`
	Resilvering bool   `json:"resilvering,omitempty"`
}

// ZfsDataset es un filesystem o zvol.
type ZfsDataset struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // filesystem | volume
	GUID        string `json:"guid"`
	Mountpoint  string `json:"mountpoint,omitempty"`
	Mounted     bool   `json:"mounted"`
	Used        uint64 `json:"used"`
	Avail       uint64 `json:"avail"`
	Refer       uint64 `json:"refer"`
	Quota       uint64 `json:"quota"`
	Reservation uint64 `json:"reservation"`
	Volsize     uint64 `json:"volsize,omitempty"`
	Encrypted   bool   `json:"encrypted"`
	KeyLoaded   bool   `json:"keyLoaded"`
	Origin      string `json:"origin,omitempty"`
	KeyDevice   string `json:"keyDevice,omitempty"` // tangoos:keydev (llave USB, ADR 0016)
}

// ZfsImportable es un pool que se ve en los discos pero no está importado.
type ZfsImportable struct {
	Name   string   `json:"name"`
	GUID   string   `json:"guid"`
	State  string   `json:"state"`
	Status string   `json:"status,omitempty"`
	Disks  []string `json:"disks"` // discos enteros del kernel
}

// ZfsEnableArgs instala zfsutils + zfs-dkms (compila el módulo) y lo carga.
type ZfsEnableArgs struct {
	OpID string `json:"op_id"`
}

// ZfsPoolCreateArgs: pool nuevo con un vdev (stripe | mirror | raidz1..3).
type ZfsPoolCreateArgs struct {
	OpID   string       `json:"op_id"`
	Name   string       `json:"name"`
	Layout string       `json:"layout"`
	Disks  []VolumeDisk `json:"disks"`
}

// ZfsPoolAddArgs suma un vdev de datos con la misma forma.
type ZfsPoolAddArgs struct {
	OpID   string       `json:"op_id"`
	Name   string       `json:"name"`
	Layout string       `json:"layout"`
	Disks  []VolumeDisk `json:"disks"`
}

// ZfsAuxArgs agrega (Disks) o quita (Remove, nombres de vdev) caché L2ARC o SLOG.
type ZfsAuxArgs struct {
	OpID   string       `json:"op_id"`
	Name   string       `json:"name"`
	Kind   string       `json:"kind"` // cache | log
	Disks  []VolumeDisk `json:"disks,omitempty"`
	Remove []string     `json:"remove,omitempty"`
}

// ZfsReplaceArgs reemplaza un disco (Old: nombre de vdev o GUID) por uno libre.
type ZfsReplaceArgs struct {
	OpID string     `json:"op_id"`
	Name string     `json:"name"`
	Old  string     `json:"old"`
	New  VolumeDisk `json:"new"`
}

// ZfsScrubArgs arranca o frena un scrub.
type ZfsScrubArgs struct {
	Name string `json:"name"`
	Stop bool   `json:"stop,omitempty"`
}

// ZfsImportArgs importa por nombre o GUID; ReadOnly es el primer paso con pools ajenos.
type ZfsImportArgs struct {
	OpID     string `json:"op_id"`
	Pool     string `json:"pool"`
	ReadOnly bool   `json:"readonly"`
}

// ZfsPoolArgs nombra un pool (export, destroy).
type ZfsPoolArgs struct {
	OpID string `json:"op_id"`
	Name string `json:"name"`
}

// ZfsVolCreateArgs crea el dataset <pool>/<name> montado en /<name>, con @shares y
// @apps (padres sin montar) y @snapshots. Passphrase no vacía = cifrado nativo.
type ZfsVolCreateArgs struct {
	OpID        string `json:"op_id"`
	Pool        string `json:"pool"`
	Name        string `json:"name"`
	Quota       uint64 `json:"quota,omitempty"`
	Reservation uint64 `json:"reservation,omitempty"`
	Passphrase  string `json:"passphrase,omitempty"`
}

// ZfsVolSetArgs cambia cuota y reserva (0 = sin límite).
type ZfsVolSetArgs struct {
	Pool        string `json:"pool"`
	Name        string `json:"name"`
	Quota       uint64 `json:"quota"`
	Reservation uint64 `json:"reservation"`
}

// ZfsVolArgs nombra un volumen ZFS (delete, lock).
type ZfsVolArgs struct {
	OpID string `json:"op_id,omitempty"`
	Pool string `json:"pool"`
	Name string `json:"name"`
}

// ZfsUnlockArgs carga la clave y monta el volumen y sus carpetas.
// Con UseKeyDevice la frase sale del sello del volumen, abierto con el archivo del
// pendrive (ADR 0016).
type ZfsUnlockArgs struct {
	Pool         string `json:"pool"`
	Name         string `json:"name"`
	Passphrase   string `json:"passphrase,omitempty"`
	UseKeyDevice bool   `json:"useKeyDevice,omitempty"`
}

// ZfsKeyDeviceArgs guarda una llave nueva en el pendrive Device (UUID del filesystem) y
// sella la frase para ella; Device vacío quita la llave (borra el sello).
type ZfsKeyDeviceArgs struct {
	Pool       string `json:"pool"`
	Name       string `json:"name"`
	Passphrase string `json:"passphrase"`
	Device     string `json:"device,omitempty"`
}

// ZfsPassphraseArgs cambia la frase (la vieja se verifica primero).
type ZfsPassphraseArgs struct {
	Pool string `json:"pool"`
	Name string `json:"name"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// ZfsZvolArgs crea o borra un volumen de bloque <pool>/zvol/<name> (LUN de iSCSI).
type ZfsZvolArgs struct {
	Pool   string `json:"pool"`
	Name   string `json:"name"`
	Size   uint64 `json:"size,omitempty"`
	Sparse bool   `json:"sparse,omitempty"`
}
