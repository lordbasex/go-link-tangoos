package agentproto

// ---- Fase M3.5: Centro de apps (protocol/README.md §9, ADR 0010) ------------------
//
// El agente solo acepta ids del catálogo embebido (system/apps) y opciones que valida
// contra él: nunca un compose ni una imagen arbitraria. Las operaciones largas (instalar
// Docker, bajar imágenes, levantar una app) son asíncronas como las de almacenamiento:
// responden con el op_id y tangod las sigue con app.job.

// Nombres de operaciones del Centro de apps.
const (
	OpDockerStatus  = "docker.status"
	OpDockerInstall = "docker.install"
	OpAppUp         = "app.up"
	OpAppAction     = "app.action"
	OpAppUninstall  = "app.uninstall"
	OpAppLogs       = "app.logs"
	OpAppStats      = "app.stats"
	OpAppJob        = "app.job"
)

// CodeDockerMissing: Docker no está instalado o no corre.
const CodeDockerMissing = "DOCKER_NOT_INSTALLED"

// DockerStatus responde a docker.status.
type DockerStatus struct {
	Installed bool    `json:"installed"`
	Running   bool    `json:"running"`
	Version   *string `json:"version"`
	Compose   bool    `json:"compose"`
}

// DockerInstallArgs instala docker.io + docker-compose desde los repos de Debian.
type DockerInstallArgs struct {
	OpID string `json:"op_id"`
}

// AppUpArgs instala o actualiza una app del catálogo y la deja corriendo.
//
// Mount es el punto de montaje del volumen ("/volume1"); los datos van a
// <Mount>/@apps/<ID>/. Options son las del catálogo (ya generadas las secretas).
type AppUpArgs struct {
	OpID    string            `json:"op_id"`
	ID      string            `json:"id"`
	Mount   string            `json:"mount"`
	Port    int               `json:"port"`
	TZ      string            `json:"tz"`
	Host    string            `json:"host"`
	Options map[string]string `json:"options"`
	// Pull: bajar imágenes antes de levantar (instalar y actualizar: true).
	Pull bool `json:"pull"`
}

// AppActionArgs: start | stop | restart.
type AppActionArgs struct {
	OpID   string `json:"op_id"`
	ID     string `json:"id"`
	Mount  string `json:"mount"`
	Action string `json:"action"`
}

// AppUninstallArgs baja la app; con KeepData=false borra también sus datos.
type AppUninstallArgs struct {
	OpID     string `json:"op_id"`
	ID       string `json:"id"`
	Mount    string `json:"mount"`
	KeepData bool   `json:"keep_data"`
}

// AppLogsArgs pide las últimas líneas de log de la app (todos sus contenedores).
type AppLogsArgs struct {
	ID   string `json:"id"`
	Tail int    `json:"tail"`
}

// AppLogsResult responde a app.logs.
type AppLogsResult struct {
	Lines []string `json:"lines"`
}

// AppStatsArgs pide estado y consumo de las apps indicadas (vacío = todas las de TangoOS).
type AppStatsArgs struct {
	IDs []string `json:"ids,omitempty"`
}

// AppStat es el estado real de una app según Docker.
type AppStat struct {
	ID         string   `json:"id"`
	State      string   `json:"state"` // running | stopped | partial | error | missing
	Containers int      `json:"containers"`
	Running    int      `json:"running"`
	Unhealthy  int      `json:"unhealthy"`
	CPU        *float64 `json:"cpu"` // 0..1 del total de la máquina
	Mem        *uint64  `json:"mem"` // bytes
	Error      string   `json:"error,omitempty"`
}

// AppStatsResult responde a app.stats.
type AppStatsResult struct {
	Apps []AppStat `json:"apps"`
}

// AppJobArgs consulta una operación asíncrona del Centro de apps.
type AppJobArgs struct {
	OpID string `json:"op_id"`
}
