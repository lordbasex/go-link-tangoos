package agentproto

// ---- Grupos de almacenamiento (protocol/README.md §16, ADR 0011) -------------------
//
// LVM sobre md (o sobre tramos de RAID híbrido), volúmenes lógicos con LUKS2 opcional y
// caché SSD con lvmcache. Las operaciones largas son asíncronas (job.status). Las frases
// de cifrado viajan solo en los argumentos: el agente nunca las escribe en el journal ni
// en el log.

const (
	OpPoolReport  = "pool.report"
	OpPoolCreate  = "pool.create"
	OpPoolExpand  = "pool.expand"
	OpPoolReplace = "pool.replace"
	OpPoolDelete  = "pool.delete"

	OpLVCreate = "lv.create"
	OpLVResize = "lv.resize"
	OpLVDelete = "lv.delete"

	OpLUKSUnlock     = "luks.unlock"
	OpLUKSLock       = "luks.lock"
	OpLUKSPassphrase = "luks.passphrase"
	OpLUKSKeyDevice  = "luks.keydevice"
	OpKeyDevList     = "keydev.list"

	OpCacheCreate = "cache.create"
	OpCacheRemove = "cache.remove"
)

// Códigos de error de esta fase.
const (
	CodeWrongPassphrase  = "WRONG_PASSPHRASE"
	CodeKeyDeviceMissing = "KEY_DEVICE_MISSING"
	CodeNoSpace          = "NO_SPACE"
	CodeLocked           = "VOLUME_LOCKED"
	CodePoolNotEmpty     = "POOL_NOT_EMPTY"
)

// PoolReport responde a pool.report: los grupos de TangoOS (volume groups con la
// etiqueta "tangoos") con sus PV, tramos y volúmenes lógicos.
type PoolReport struct {
	Pools []PoolInfo `json:"pools"`
}

// PoolInfo es un grupo de almacenamiento.
type PoolInfo struct {
	Name    string     `json:"name"`
	UUID    string     `json:"uuid"`
	Profile string     `json:"profile"`
	Size    uint64     `json:"size"`
	Free    uint64     `json:"free"`
	PVs     []PoolPV   `json:"pvs"`
	Tiers   []PoolTier `json:"tiers"`
	LVs     []PoolLV   `json:"lvs"`
}

// PoolPV es un volumen físico del grupo.
type PoolPV struct {
	Dev   string   `json:"dev"`   // /dev/md127, /dev/sdb
	MD    string   `json:"md"`    // nombre del md ("pool1_md1"), vacío si es un disco
	Role  string   `json:"role"`  // data | cache
	Size  uint64   `json:"size"`  // bytes
	Free  uint64   `json:"free"`  // bytes
	Disks []string `json:"disks"` // discos enteros (sdX) debajo
}

// PoolTier es un tramo: un md (o, en un grupo híbrido, particiones todavía sin md).
type PoolTier struct {
	Index   int      `json:"index"`
	MD      string   `json:"md,omitempty"`     // kernel ("md125"); vacío si está sin usar
	Name    string   `json:"name,omitempty"`   // "pool1_md2"
	Level   string   `json:"level"`            // basic | raid1 | raid5 | raid6 | raid10 | unused
	Size    uint64   `json:"size"`             // por miembro (bytes)
	Members []string `json:"members"`          // discos enteros (sdX)
	Parts   []string `json:"parts,omitempty"`  // particiones (sdX2) en los híbridos
	Role    string   `json:"role,omitempty"`   // cache para el md de las SSD
	Volume  string   `json:"volume,omitempty"` // volumen al que sirve (caché)
}

// PoolLV es un volumen lógico visible (los internos de la caché no se listan).
type PoolLV struct {
	Name      string     `json:"name"`
	UUID      string     `json:"uuid"`
	Size      uint64     `json:"size"`
	Active    bool       `json:"active"`
	DM        string     `json:"dm"`                   // dm-3 (el LV)
	FS        string     `json:"fs"`                   // btrfs | xfs | ext4 (de la etiqueta)
	FSUUID    string     `json:"fs_uuid,omitempty"`    // visible si está abierto o sin cifrar
	Encrypted bool       `json:"encrypted"`            // LUKS2
	Open      bool       `json:"open"`                 // /dev/mapper/tgc_<vol> existe
	LUKSUUID  string     `json:"luks_uuid,omitempty"`  // del encabezado
	KeyDevice string     `json:"key_device,omitempty"` // UUID del filesystem con el archivo de clave
	Cache     *CacheInfo `json:"cache,omitempty"`
}

// CacheInfo son los datos de la caché SSD de un volumen (lvs cache_*).
type CacheInfo struct {
	Mode        string   `json:"mode"` // read | readwrite
	Disks       []string `json:"disks"`
	Size        uint64   `json:"size"`
	Used        uint64   `json:"used"`
	Dirty       uint64   `json:"dirty"`
	ReadHits    uint64   `json:"read_hits"`
	ReadMisses  uint64   `json:"read_misses"`
	WriteHits   uint64   `json:"write_hits"`
	WriteMisses uint64   `json:"write_misses"`
}

// PoolCreateArgs crea un grupo: discos → md (o tramos híbridos) → PV → VG.
type PoolCreateArgs struct {
	OpID    string       `json:"op_id"`
	Name    string       `json:"name"`
	Profile string       `json:"profile"`
	Disks   []VolumeDisk `json:"disks"`
}

// PoolCreateResult es el resultado final de pool.create.
type PoolCreateResult struct {
	Name  string   `json:"name"`
	UUID  string   `json:"uuid"`
	MDs   []string `json:"mds"`
	Bytes uint64   `json:"bytes"`
}

// PoolExpandArgs suma discos a un grupo RAID 5/6/10 o híbrido.
type PoolExpandArgs struct {
	OpID string       `json:"op_id"`
	Name string       `json:"name"`
	Add  []VolumeDisk `json:"add"`
}

// PoolExpandResult es el resultado final de pool.expand.
type PoolExpandResult struct {
	Before uint64 `json:"before"`
	After  uint64 `json:"after"`
}

// PoolReplaceArgs reemplaza un disco de un grupo: lo saca de cada md donde está (si
// sigue ahí), particiona el nuevo igual (híbrido) y lo agrega a cada md.
type PoolReplaceArgs struct {
	OpID        string     `json:"op_id"`
	Name        string     `json:"name"`
	Failed      string     `json:"failed,omitempty"`
	Replacement VolumeDisk `json:"replacement"`
}

// PoolDeleteArgs elimina un grupo vacío: VG, PV, md y firmas de sus discos. DevIDs son
// todos los discos del grupo (datos y caché) que vio el plan.
type PoolDeleteArgs struct {
	OpID   string   `json:"op_id"`
	Name   string   `json:"name"`
	DevIDs []string `json:"dev_ids"`
}

// LVCreateArgs crea un volumen dentro de un grupo, con LUKS2 si Passphrase no está vacía.
type LVCreateArgs struct {
	OpID       string `json:"op_id"`
	Pool       string `json:"pool"`
	Name       string `json:"name"`
	Size       uint64 `json:"size"` // 0 = todo el espacio libre
	FS         string `json:"fs"`
	Passphrase string `json:"passphrase,omitempty"`
	KeyDevice  string `json:"key_device,omitempty"`
}

// Redacted devuelve una copia sin la frase (para el journal).
func (a LVCreateArgs) Redacted() LVCreateArgs {
	if a.Passphrase != "" {
		a.Passphrase = "[redactada]"
	}
	return a
}

// LVCreateResult es el resultado final de lv.create.
type LVCreateResult struct {
	Name   string `json:"name"`
	Mount  string `json:"mount"`
	UUID   string `json:"uuid"`
	FSUUID string `json:"fs_uuid"`
	Size   uint64 `json:"size"`
}

// LVResizeArgs agranda un volumen (y su filesystem) a Size bytes.
type LVResizeArgs struct {
	OpID string `json:"op_id"`
	Name string `json:"name"`
	Size uint64 `json:"size"`
}

// LVResizeResult es el resultado final de lv.resize.
type LVResizeResult struct {
	Size   uint64 `json:"size"`
	FSSize uint64 `json:"fs_size"`
}

// LVDeleteArgs elimina un volumen de un grupo: despublica sus carpetas, lo desmonta,
// cierra LUKS, quita la caché y hace lvremove.
type LVDeleteArgs struct {
	OpID   string   `json:"op_id"`
	Name   string   `json:"name"`
	UUID   string   `json:"uuid"` // el LV que vio el plan
	Shares []string `json:"shares"`
}

// LUKSUnlockArgs abre un volumen cifrado y lo monta.
type LUKSUnlockArgs struct {
	Name         string `json:"name"`
	Passphrase   string `json:"passphrase,omitempty"`
	UseKeyDevice bool   `json:"use_key_device,omitempty"`
}

// LUKSLockArgs desmonta y cierra un volumen cifrado.
type LUKSLockArgs struct {
	Name string `json:"name"`
}

// LUKSPassphraseArgs cambia la frase de un volumen.
type LUKSPassphraseArgs struct {
	Name string `json:"name"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// LUKSKeyDeviceArgs guarda (o quita, con Device vacío) el archivo de clave de un
// volumen en un dispositivo extraíble.
type LUKSKeyDeviceArgs struct {
	Name       string `json:"name"`
	Passphrase string `json:"passphrase"`
	Device     string `json:"device,omitempty"`
}

// KeyDevice es un filesystem donde se puede guardar un archivo de clave.
type KeyDevice struct {
	UUID  string `json:"uuid"`
	FS    string `json:"fs"`
	Label string `json:"label"`
	Size  uint64 `json:"size"`
	Disk  string `json:"disk"` // sdX
	Dev   string `json:"dev"`  // sdX1
	USB   bool   `json:"usb"`
}

// KeyDevListResult responde a keydev.list.
type KeyDevListResult struct {
	Devices []KeyDevice `json:"devices"`
}

// CacheCreateArgs agrega una caché SSD a un volumen.
type CacheCreateArgs struct {
	OpID  string       `json:"op_id"`
	Name  string       `json:"name"` // volumen
	Mode  string       `json:"mode"` // read | readwrite
	Disks []VolumeDisk `json:"disks"`
}

// CacheRemoveArgs quita la caché de un volumen (vacía primero los bloques sucios).
type CacheRemoveArgs struct {
	OpID string `json:"op_id"`
	Name string `json:"name"`
}
