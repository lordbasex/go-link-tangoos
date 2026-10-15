package diskops

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// ---- Usuarios y grupos del sistema (protocol/README.md §8) ------------------------
//
// Cada usuario de TangoOS es un usuario Unix sin login (UID ≥ 2000, grupo
// tango-users, shell nologin, sin home) con una cuenta Samba. Los permisos de una
// carpeta se expresan con dos grupos por carpeta: tg-<carpeta>-rw y tg-<carpeta>-ro.

// UsersGroup es el grupo primario de todos los usuarios de TangoOS.
const UsersGroup = "tango-users"

// MinUID es el primer UID/GID que usa TangoOS (lejos de los de Debian).
const MinUID = 2000

var userNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Nombres que nunca pueden ser usuarios de TangoOS aunque no existan todavía.
var reservedUsers = map[string]bool{
	"root": true, "daemon": true, "bin": true, "sys": true, "sync": true, "games": true, "man": true, "lp": true,
	"mail": true, "news": true, "uucp": true, "proxy": true, "www-data": true, "backup": true, "list": true, "irc": true,
	"nobody": true, "systemd-network": true, "systemd-resolve": true, "messagebus": true, "sshd": true, "tango": true,
	"tango-agent": true, "tango-users": true, "avahi": true, "_apt": true, "polkitd": true, "docker": true,
}

// ValidUserName valida un nombre de usuario de TangoOS (también nombre Unix y Samba).
func ValidUserName(name string) error {
	if !userNameRe.MatchString(name) {
		return fmt.Errorf("el usuario debe empezar con una letra y tener solo minúsculas, números, - o _ (máx. 32)")
	}
	if reservedUsers[name] || strings.HasPrefix(name, "tg-") || strings.HasPrefix(name, "systemd-") {
		return fmt.Errorf("«%s» es un nombre reservado del sistema", name)
	}
	return nil
}

// ShareGroups devuelve los grupos (rw, ro) de una carpeta compartida. Los nombres de
// grupo Unix tienen que ser cortos y en minúsculas: si la carpeta tiene un nombre
// largo o con caracteres raros se usa un resumen estable.
func ShareGroups(share string) (rw, ro string) {
	base := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, share)
	if len(base) > 20 {
		h := sha256.Sum256([]byte(strings.ToLower(share)))
		base = base[:12] + "_" + hex.EncodeToString(h[:])[:7]
	}
	return "tg-" + base + "-rw", "tg-" + base + "-ro"
}

// SharePerms son los usuarios con acceso a una carpeta (ya expandidos los grupos).
type SharePerms struct {
	RW []string
	RO []string
}

// ---- POSIX ACL (xattr system.posix_acl_access / _default) -------------------------
//
// Formato del kernel (include/uapi/linux/posix_acl_xattr.h): versión 2 (u32 LE) y
// entradas {tag u16, perm u16, id u32}, ordenadas por tag. Así se setean las ACL sin
// setfacl (ADR 0009).

const (
	aclUserObj  = 0x01
	aclUser     = 0x02
	aclGroupObj = 0x04
	aclGroup    = 0x08
	aclMask     = 0x10
	aclOther    = 0x20
	aclNoID     = 0xFFFFFFFF
)

// ACLEntry es un grupo extra con sus permisos (bits rwx: 4 2 1).
type ACLEntry struct {
	GID  uint32
	Perm uint16
}

// PosixACL arma el xattr de una ACL: dueño rwx, grupo dueño (rw de la carpeta)
// groupPerm, grupos extra, máscara y otros sin acceso.
func PosixACL(groupPerm uint16, extra []ACLEntry) []byte {
	b := make([]byte, 4, 4+8*(4+len(extra)))
	binary.LittleEndian.PutUint32(b, 2)
	add := func(tag, perm uint16, id uint32) {
		var e [8]byte
		binary.LittleEndian.PutUint16(e[0:], tag)
		binary.LittleEndian.PutUint16(e[2:], perm)
		binary.LittleEndian.PutUint32(e[4:], id)
		b = append(b, e[:]...)
	}
	add(aclUserObj, 7, aclNoID)
	add(aclGroupObj, groupPerm, aclNoID)
	mask := groupPerm
	for _, e := range extra {
		add(aclGroup, e.Perm, e.GID)
		mask |= e.Perm
	}
	add(aclMask, mask, aclNoID)
	add(aclOther, 0, aclNoID)
	return b
}

// ---- Samba ---------------------------------------------------------------------------

// SambaShareWithPerms genera la sección de una carpeta con acceso por grupos: solo
// entran los miembros de tg-<carpeta>-rw (lectura y escritura) y -ro (solo lectura).
func SambaShareWithPerms(name, path, comment string, opts ...ShareOpts) string {
	rw, ro := ShareGroups(name)
	comment = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '[' || r == ']' {
			return ' '
		}
		return r
	}, comment)
	return fmt.Sprintf(`# Generada por TangoOS (tango-agent). No editar: se regenera.
[%s]
   comment = %s
   path = %s
   browseable = yes
   read only = yes
   valid users = @%[4]s @%[5]s
   write list = @%[4]s
   read list = @%[5]s
   force group = %[4]s
   create mask = 0660
   directory mask = 0770
   inherit acls = yes
   vfs objects = %[6]s
   fruit:metadata = stream
`, name, strings.TrimSpace(comment), path, rw, ro, vfsLine(path)) + tmLines(firstOpts(opts)) + shadowLines(path)
}

// RemoveMDConf saca la línea ARRAY de un array del contenido de mdadm.conf.
func RemoveMDConf(conf, name string) string {
	marker := "ARRAY /dev/md/" + name + " "
	var out []string
	for _, l := range strings.Split(strings.TrimRight(conf, "\n"), "\n") {
		if strings.HasPrefix(l, marker) {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n") + "\n"
}

// SambaPathUnder indica si una sección de Samba publica una ruta dentro de mount.
func SambaPathUnder(section, mount string) bool {
	for _, l := range strings.Split(section, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if ok && strings.TrimSpace(k) == "path" {
			p := strings.TrimSpace(v)
			if p == mount || strings.HasPrefix(p, mount+"/") {
				return true
			}
		}
	}
	return false
}
