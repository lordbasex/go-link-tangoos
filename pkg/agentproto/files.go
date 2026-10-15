package agentproto

// ---- Fase M3: file-worker (protocol/README.md §10, docs/07 §5) ------------------

// OpFileWorker lanza un file-worker con el UID/GIDs de un usuario de TangoOS y le
// devuelve a tangod, por SCM_RIGHTS en la misma respuesta, su extremo del socketpair
// SOCK_SEQPACKET. Es la única op que responde con un descriptor.
const OpFileWorker = "files.worker"

// FileWorkerArgs elige el usuario (nombre Unix = nombre de TangoOS, UID ≥ 2000).
type FileWorkerArgs struct {
	User string `json:"user"`
}

// FileWorkerResult describe el worker lanzado.
type FileWorkerResult struct {
	PID    int      `json:"pid"`
	UID    uint32   `json:"uid"`
	Groups []uint32 `json:"groups"`
}
