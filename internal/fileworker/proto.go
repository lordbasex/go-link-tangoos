// Package fileworker es el "file-worker" de TangoOS (docs/07 §5, ADR 0007,
// protocol/README.md §10): un proceso por usuario, lanzado por tango-agent con
// el UID/GIDs del usuario, que hace todas las operaciones de archivos. tangod le
// habla por un socketpair SOCK_SEQPACKET (un mensaje JSON por paquete) y recibe
// por SCM_RIGHTS los descriptores de los archivos que sube o baja: el kernel ya
// verificó los permisos al abrirlos, y tangod solo lee o escribe por ese fd.
//
// Toda ruta se resuelve con openat2(RESOLVE_BENEATH|RESOLVE_NO_MAGICLINKS|
// RESOLVE_NO_XDEV) anclada en la raíz de la carpeta compartida: no hay forma de
// salir de ella ni con "..", ni con rutas absolutas, ni con symlinks.
package fileworker

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Ops del worker.
const (
	OpProbe          = "probe"
	OpStat           = "stat"
	OpList           = "list"
	OpMkdir          = "mkdir"
	OpRename         = "rename"
	OpMove           = "move"
	OpTrash          = "trash"
	OpTrashList      = "trashList"
	OpRestore        = "restore"
	OpTrashEmpty     = "trashEmpty"
	OpSearch         = "search"
	OpRestoreVersion = "restoreVersion"
	OpThumb          = "thumb"
	OpOpenRead       = "openRead"
	OpOpenWrite      = "openWrite"
	OpCommit         = "commit"
	OpAbort          = "abort"
	// OpWatch espera (long-poll, hasta Limit segundos) cambios en las carpetas
	// Roots[i]/Rels[i] con inotify y devuelve los índices que cambiaron (protocol §13).
	OpWatch = "watch"
)

// WatchResult es la respuesta de OpWatch: índices de Roots/Rels que cambiaron.
type WatchResult struct {
	Changed []int `json:"changed"`
}

// Límites del watcher.
const (
	MaxWatchDirs    = 64
	MaxWatchSeconds = 30
)

// Request es un pedido de tangod al worker. Root es la raíz de la carpeta
// compartida (o de una instantánea de ella); Rel es relativa a esa raíz.
type Request struct {
	ID        uint64   `json:"id"`
	Op        string   `json:"op"`
	Root      string   `json:"root,omitempty"`
	Rel       string   `json:"rel,omitempty"`
	Name      string   `json:"name,omitempty"`
	DstRoot   string   `json:"dst_root,omitempty"`
	DstRel    string   `json:"dst_rel,omitempty"`
	Roots     []string `json:"roots,omitempty"`
	Rels      []string `json:"rels,omitempty"` // OpWatch: carpeta relativa a Roots[i]
	Query     string   `json:"query,omitempty"`
	Offset    int      `json:"offset,omitempty"`
	Limit     int      `json:"limit,omitempty"`
	Size      int      `json:"size,omitempty"`
	Overwrite bool     `json:"overwrite,omitempty"`
}

// Response es la respuesta. Si lleva un fd, viaja en el mismo paquete (SCM_RIGHTS).
type Response struct {
	ID     uint64          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error es un error estable (los mismos códigos llegan al cliente).
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Códigos de error.
const (
	CodeNotFound     = "PATH_NOT_FOUND"
	CodePermission   = "PERMISSION_DENIED"
	CodeExists       = "PATH_EXISTS"
	CodeInvalid      = "INVALID_PARAMS"
	CodeInvalidName  = "INVALID_NAME"
	CodeEscape       = "PATH_ESCAPE"
	CodeNoSpace      = "NO_SPACE"
	CodeNotSupported = "NOT_SUPPORTED"
	CodeInternal     = "INTERNAL"
	CodeUnavailable  = "WORKER_UNAVAILABLE"
)

func errf(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Entry es un archivo o carpeta.
type Entry struct {
	Name  string `json:"name"`
	Rel   string `json:"rel"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime string `json:"mtime"` // RFC 3339
	Root  string `json:"root,omitempty"`
}

// ListResult es una página de un listado.
type ListResult struct {
	Entries []Entry `json:"entries"`
	More    bool    `json:"more"`
}

// TrashItem es algo en la papelera de una carpeta.
type TrashItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Origin    string `json:"origin"` // ruta relativa original
	DeletedAt string `json:"deleted_at"`
	Size      int64  `json:"size"`
	Dir       bool   `json:"dir"`
}

// ThumbResult es una miniatura JPEG en base64.
type ThumbResult struct {
	MIME string `json:"mime"`
	Data string `json:"data"`
}

// OpenResult acompaña a un fd abierto.
type OpenResult struct {
	Size int64  `json:"size"`
	Part string `json:"part,omitempty"`
}

// ---- Validación (portable, con tests) ----------------------------------------------

// Nombres que el usuario nunca ve ni puede crear: papelera, transferencias a medias y
// la papelera de Samba.
const (
	TrashDir   = ".tango-trash"
	PartSuffix = ".tango-part"
)

// Hidden dice si un nombre es interno de TangoOS (no se lista).
func Hidden(name string) bool {
	return name == TrashDir || name == "#recycle" || strings.HasPrefix(name, ".tango-") || strings.HasSuffix(name, PartSuffix)
}

// ValidName valida un nombre de archivo o carpeta nuevo.
func ValidName(name string) *Error {
	switch {
	case name == "" || name == "." || name == "..":
		return errf(CodeInvalidName, "el nombre no puede estar vacío")
	case len(name) > 255:
		return errf(CodeInvalidName, "el nombre es demasiado largo")
	case strings.ContainsAny(name, "/\x00"):
		return errf(CodeInvalidName, "el nombre no puede tener «/»")
	case Hidden(name):
		return errf(CodeInvalidName, "«%s» es un nombre reservado de TangoOS", name)
	case strings.TrimSpace(name) != name:
		return errf(CodeInvalidName, "el nombre no puede empezar ni terminar con espacios")
	}
	return nil
}

// CleanRel normaliza una ruta relativa a la raíz de la carpeta. "" y "/" son la
// raíz ("."). Rechaza todo lo que intente salir (el kernel también lo impide).
func CleanRel(rel string) (string, *Error) {
	if strings.ContainsRune(rel, 0) {
		return "", errf(CodeInvalid, "ruta inválida")
	}
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return ".", nil
	}
	c := path.Clean(rel)
	if c == ".." || strings.HasPrefix(c, "../") || path.IsAbs(c) {
		return "", errf(CodeEscape, "la ruta sale de la carpeta compartida")
	}
	for _, part := range strings.Split(c, "/") {
		if Hidden(part) {
			return "", errf(CodeNotFound, "no existe %s", rel)
		}
	}
	return c, nil
}

// rootRe acepta solo raíces de carpetas compartidas (/volumen/@shares/Carpeta) y de
// sus instantáneas (/volumen/@snapshots/Carpeta/<instantánea>). tangod es de
// confianza, pero el worker no abre nada fuera de los volúmenes.
var rootRe = regexp.MustCompile(`^/[A-Za-z0-9._-]+/@(shares/[^/\x00]+|snapshots/[^/\x00]+/[^/\x00]+)$`)

// ValidRoot valida la raíz de una operación.
func ValidRoot(root string) *Error {
	if !rootRe.MatchString(root) || strings.Contains(root, "/../") || strings.HasSuffix(root, "/..") || strings.HasSuffix(root, "/.") {
		return errf(CodeInvalid, "raíz inválida: %q", root)
	}
	return nil
}

// TrashMeta es el sidecar de un elemento en la papelera.
type TrashMeta struct {
	Name      string `json:"name"`
	Origin    string `json:"origin"`
	DeletedAt string `json:"deleted_at"`
}

var trashIDRe = regexp.MustCompile(`^[0-9a-f]{20}$`)

// ValidTrashID valida el id de un elemento de la papelera.
func ValidTrashID(id string) bool { return trashIDRe.MatchString(id) }

// ErrClosed indica que el worker ya no está disponible.
var ErrClosed = errors.New("fileworker: conexión cerrada")
