package agentproto

// ---- UPS (protocol/README.md §13) -----------------------------------------------------

// OpUPSApply deja NUT para un UPS USB local (asíncrona: puede instalar paquetes). Con
// Enabled=false detiene nut-server (los paquetes quedan).
const OpUPSApply = "ups.apply"

// UPSApplyArgs: el agente arma /etc/nut con ups.NUTFiles (no recibe texto libre).
type UPSApplyArgs struct {
	OpID        string `json:"op_id"`
	Enabled     bool   `json:"enabled"`
	UPSName     string `json:"ups_name"`
	NetServer   bool   `json:"net_server"`
	MonPassword string `json:"mon_password"`
}
