package agentproto

// Cierre v1.0 (protocol/README.md §14): borrar el usuario del sistema de un usuario
// de TangoOS.

// OpUserDelete borra la cuenta Samba y el usuario Unix (UID ≥ 2000) de un usuario de
// TangoOS. Opcionalmente pasa sus archivos de las carpetas compartidas a otro usuario.
const OpUserDelete = "user.delete"

// UserDeleteArgs: ReassignTo vacío = los archivos quedan con su UID numérico. Mounts son
// los puntos de montaje de los volúmenes ("/volume1"); se recorre <mount>/@shares.
type UserDeleteArgs struct {
	Name       string   `json:"name"`
	ReassignTo string   `json:"reassign_to,omitempty"`
	Mounts     []string `json:"mounts,omitempty"`
}

// UserDeleteResult dice si existía el usuario Unix y cuántos archivos cambiaron de dueño.
type UserDeleteResult struct {
	Existed    bool `json:"existed"`
	Reassigned int  `json:"reassigned"`
}
