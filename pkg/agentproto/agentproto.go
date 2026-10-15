// Package agentproto define el protocolo entre tangod y tango-agent (docs/02, ADR 0007).
//
// Transporte: socket Unix, una línea JSON por mensaje.
//
//	→ {"v":1,"id":7,"op":"disk.smart","args":{"dev_id":"wwn-0x5000c500a1b2c3d4"}}
//	← {"v":1,"id":7,"ok":true,"result":{…}}
//	← {"v":1,"id":7,"ok":false,"error":{"code":"DEVICE_NOT_FOUND","message":"…"}}
//
// Las operaciones forman una allow-list cerrada: un op desconocido, un campo de más o
// una versión distinta se rechazan antes de ejecutar nada.
package agentproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Version del protocolo. Un cambio incompatible la incrementa.
const Version = 1

// MaxLine es el tamaño máximo de un mensaje (request o response) en bytes.
const MaxLine = 64 << 10

// Request es un pedido de tangod al agente.
type Request struct {
	V    int             `json:"v"`
	ID   uint64          `json:"id"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
	// OpID identifica una operación de forma idempotente (p. ej. el job_id de tangod).
	// Reservado: las operaciones destructivas lo exigirán para no repetirse en un reintento.
	OpID string `json:"op_id,omitempty"`
}

// Response es la respuesta del agente.
type Response struct {
	V      int             `json:"v"`
	ID     uint64          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error es un error estable, nunca salida cruda de un comando.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Códigos de error.
const (
	CodeBadRequest     = "BAD_REQUEST"
	CodeVersion        = "UNSUPPORTED_VERSION"
	CodeUnknownOp      = "UNKNOWN_OP"
	CodeInvalidArgs    = "INVALID_ARGS"
	CodeRateLimited    = "RATE_LIMITED"
	CodeDeviceNotFound = "DEVICE_NOT_FOUND"
	CodeDeviceInvalid  = "DEVICE_INVALID"
	CodeInternal       = "INTERNAL"
)

// Errf construye un *Error.
func Errf(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// DecodeStrict decodifica JSON rechazando campos desconocidos y basura al final.
func DecodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("datos sobrantes después del objeto JSON")
	}
	return nil
}

// ParseRequest valida y decodifica una línea de request.
func ParseRequest(line []byte) (*Request, *Error) {
	var r Request
	if err := DecodeStrict(line, &r); err != nil {
		return nil, Errf(CodeBadRequest, "request inválido: %v", err)
	}
	if r.V != Version {
		return &r, Errf(CodeVersion, "versión %d no soportada (esperada %d)", r.V, Version)
	}
	if r.Op == "" {
		return &r, Errf(CodeBadRequest, "falta op")
	}
	return &r, nil
}

// ---- Operaciones de esta fase (solo lectura) ----------------------------------

// Nombres de operaciones.
const (
	OpVersion   = "agent.version"
	OpDiskSmart = "disk.smart"
)

// NoArgs se usa en ops sin argumentos: solo acepta args ausentes, null o {}.
type NoArgs struct{}

// VersionResult responde a agent.version.
type VersionResult struct {
	Agent    string   `json:"agent"`
	Protocol int      `json:"protocol"`
	Ops      []string `json:"ops"`
}

// DiskSmartArgs pide el SMART de un disco identificado por su nombre en /dev/disk/by-id
// (p. ej. "wwn-0x5000c500a1b2c3d4", "nvme-eui.0025…", "scsi-0QEMU_QEMU_HARDDISK_TANGO-D1").
type DiskSmartArgs struct {
	DevID string `json:"dev_id"`
}

// SmartAttr es un atributo SMART ATA relevante.
type SmartAttr struct {
	ID        uint8  `json:"id"`
	Name      string `json:"name"`
	Value     uint8  `json:"value"`
	Worst     uint8  `json:"worst"`
	Threshold uint8  `json:"threshold"`
	Raw       uint64 `json:"raw"`
	Failing   bool   `json:"failing"`
}

// DiskSmartResult resume el SMART. Si Supported es false, Reason explica por qué y el
// resto de los campos va vacío: la UI muestra "Sin datos SMART", nunca valores inventados.
type DiskSmartResult struct {
	DevID     string  `json:"dev_id"`
	Dev       string  `json:"dev"`
	Supported bool    `json:"supported"`
	Reason    string  `json:"reason,omitempty"`
	Transport string  `json:"transport,omitempty"` // "ata" | "nvme"
	Health    string  `json:"health,omitempty"`    // "ok" | "warn" | "crit"
	TempC     *int    `json:"temp_c,omitempty"`
	PowerOnH  *uint64 `json:"power_on_hours,omitempty"`
	// ATA
	Reallocated *uint64     `json:"reallocated,omitempty"`
	Pending     *uint64     `json:"pending,omitempty"`
	Uncorrect   *uint64     `json:"offline_uncorrectable,omitempty"`
	Attributes  []SmartAttr `json:"attributes,omitempty"`
	// NVMe
	CriticalWarning *uint8  `json:"critical_warning,omitempty"`
	PercentageUsed  *uint8  `json:"percentage_used,omitempty"`
	MediaErrors     *uint64 `json:"media_errors,omitempty"`
}
