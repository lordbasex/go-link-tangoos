package agentproto

// ---- Fase M2.2: mantenimiento de volúmenes (protocol/README.md §11) ---------------

const (
	OpMDReplace      = "md.replace"
	OpMDGrow         = "md.grow"
	OpSnapshotDelete = "snapshot.delete"
	OpDiskLocate     = "disk.locate"
)

// CodeNotSupported indica que el hardware o el perfil no permiten la operación.
const CodeNotSupported = "NOT_SUPPORTED"

// MDReplaceArgs reemplaza un disco de un array: si Failed sigue en el array se marca
// como fallado y se saca; después se agrega Replacement (con la huella que vio el plan)
// y md reconstruye sobre él. Failed vacío = el disco ya no está (array degradado).
type MDReplaceArgs struct {
	OpID        string     `json:"op_id"`
	Name        string     `json:"name"`
	Failed      string     `json:"failed,omitempty"`
	Replacement VolumeDisk `json:"replacement"`
}

// MDReplaceResult es el resultado final de md.replace.
type MDReplaceResult struct {
	Removed     string `json:"removed,omitempty"`
	WipedFailed bool   `json:"wiped_failed"`
	Added       string `json:"added"`
}

// MDGrowArgs agrega discos a un RAID 5/6/10 y lo amplía: --add + --grow
// --raid-devices=N, espera el reshape y agranda el Btrfs montado en Mount.
type MDGrowArgs struct {
	OpID        string       `json:"op_id"`
	Name        string       `json:"name"`
	Mount       string       `json:"mount"`
	Add         []VolumeDisk `json:"add"`
	RaidDevices int          `json:"raid_devices"`
}

// MDGrowResult es el resultado final de md.grow.
type MDGrowResult struct {
	RaidDevices int    `json:"raid_devices"`
	ArrayBytes  uint64 `json:"array_bytes"`
	FSBytes     uint64 `json:"fs_bytes"`
}

// SnapshotDeleteArgs borra una instantánea: solo @snapshots/<share>/<name>.
type SnapshotDeleteArgs struct {
	Mount string `json:"mount"`
	Share string `json:"share"`
	Name  string `json:"name"`
}

// DiskLocateArgs prende el LED de identificación de un disco (si el gabinete lo tiene).
type DiskLocateArgs struct {
	DevID string `json:"dev_id"`
	On    bool   `json:"on"`
}
