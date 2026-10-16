//go:build linux

package diskops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// ErrSystemUser indica que ya existe un usuario o grupo del sistema con ese nombre.
var ErrSystemUser = errors.New("ya existe un usuario o grupo del sistema con ese nombre")

// EnsureGroup crea un grupo de TangoOS (GID ≥ 2000) si no existe y devuelve su GID.
func EnsureGroup(ctx context.Context, name string) (uint32, error) {
	if g, err := osuser.LookupGroup(name); err == nil {
		gid, _ := strconv.Atoi(g.Gid)
		if gid < MinUID {
			return 0, fmt.Errorf("%w: %s", ErrSystemUser, name)
		}
		return uint32(gid), nil
	}
	if _, err := run(ctx, "groupadd", "-K", "GID_MIN=2000", "-K", "GID_MAX=59999", name); err != nil {
		return 0, err
	}
	g, err := osuser.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	gid, _ := strconv.Atoi(g.Gid)
	return uint32(gid), nil
}

// Grupos de las carpetas compartidas de un volumen del clúster: GID fijo y calculado del
// nombre (ClusterGID), para que las ACL guardadas en GlusterFS valgan igual en cada nodo.
const (
	ClusterGIDMin  = 60000
	ClusterGIDSpan = 5000
)

// ClusterGID es el GID de un grupo de carpeta del clúster: FNV-1a del nombre en
// 60000–64999. Si dos nombres chocan, EnsureClusterGroup prueba el siguiente libre (el
// mismo en todos los nodos mientras se creen en el mismo orden).
func ClusterGID(name string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(name); i++ {
		h ^= uint32(name[i])
		h *= 16777619
	}
	return ClusterGIDMin + h%ClusterGIDSpan
}

// EnsureClusterGroup crea (o corrige) un grupo con el GID de ClusterGID. Si ya existía con
// otro GID, lo cambia (las ACL del volumen del clúster se vuelven a aplicar después).
func EnsureClusterGroup(ctx context.Context, name string) (uint32, error) {
	want := ClusterGID(name)
	if g, err := osuser.LookupGroup(name); err == nil {
		gid, _ := strconv.Atoi(g.Gid)
		if gid < MinUID {
			return 0, fmt.Errorf("%w: %s", ErrSystemUser, name)
		}
		if gid >= ClusterGIDMin && gid < ClusterGIDMin+ClusterGIDSpan {
			return uint32(gid), nil
		}
		for i := uint32(0); i < 32; i++ {
			try := ClusterGIDMin + (want-ClusterGIDMin+i)%ClusterGIDSpan
			if _, err := osuser.LookupGroupId(strconv.Itoa(int(try))); err == nil {
				continue
			}
			if _, err := run(ctx, "groupmod", "-g", strconv.Itoa(int(try)), name); err != nil {
				return 0, err
			}
			return try, nil
		}
		return 0, fmt.Errorf("no hay GID libre para %s", name)
	}
	for i := uint32(0); i < 32; i++ {
		try := ClusterGIDMin + (want-ClusterGIDMin+i)%ClusterGIDSpan
		if _, err := osuser.LookupGroupId(strconv.Itoa(int(try))); err == nil {
			continue
		}
		if _, err := run(ctx, "groupadd", "-g", strconv.Itoa(int(try)), name); err != nil {
			return 0, err
		}
		return try, nil
	}
	return 0, fmt.Errorf("no hay GID libre para %s", name)
}

// EnsureUser crea el usuario Unix de un usuario de TangoOS: sin home, sin login,
// grupo primario tango-users. Si ya existe tiene que ser uno de TangoOS (UID ≥ 2000).
func EnsureUser(ctx context.Context, name, full string) (uint32, error) {
	if err := ValidUserName(name); err != nil {
		return 0, err
	}
	if _, err := EnsureGroup(ctx, UsersGroup); err != nil {
		return 0, err
	}
	if u, err := osuser.Lookup(name); err == nil {
		uid, _ := strconv.Atoi(u.Uid)
		if uid < MinUID {
			return 0, fmt.Errorf("%w: %s", ErrSystemUser, name)
		}
		return uint32(uid), nil
	}
	comment := strings.Map(func(r rune) rune {
		if r == ':' || r == ',' || r == '\n' || r == '\r' || r < 0x20 {
			return -1
		}
		return r
	}, full)
	args := []string{"-M", "-N", "-g", UsersGroup, "-s", "/usr/sbin/nologin", "-d", "/nonexistent",
		"-K", "UID_MIN=2000", "-K", "UID_MAX=59999"}
	if comment != "" {
		args = append(args, "-c", comment)
	}
	if _, err := run(ctx, "useradd", append(args, name)...); err != nil {
		return 0, err
	}
	u, err := osuser.Lookup(name)
	if err != nil {
		return 0, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	return uint32(uid), nil
}

// SetGroupMembers deja en el grupo exactamente estos usuarios.
func SetGroupMembers(ctx context.Context, group string, members []string) error {
	_, err := run(ctx, "gpasswd", "-M", strings.Join(members, ","), group)
	return err
}

// SetSambaPassword crea o actualiza la cuenta Samba del usuario. La contraseña va
// por stdin (nunca en argumentos ni en el log).
func SetSambaPassword(ctx context.Context, name, password string) error {
	if strings.ContainsAny(password, "\n\r\x00") {
		return errors.New("la contraseña no puede tener saltos de línea")
	}
	if _, err := os.Stat(allowedBins["smbpasswd"]); err != nil {
		return ErrSambaMissing
	}
	_, err := runStdin(ctx, password+"\n"+password+"\n", "smbpasswd", "-s", "-a", name)
	return err
}

// ApplyShareACL aplica los permisos de una carpeta a todo su contenido: grupo dueño
// tg-<carpeta>-rw, y ACL con el grupo rw (lectura y escritura) y el ro (solo
// lectura) como entradas con nombre; nadie más. Los directorios llevan además la ACL
// por defecto, así lo nuevo hereda los permisos aunque lo cree un usuario con otro
// grupo primario. Sin setgid: la unidad del agente tiene RestrictSUIDSGID=yes (a
// propósito) y la ACL nombrada lo hace innecesario.
// ApplyShareRootACL vuelve a poner grupo, modo y ACL solo en la raíz (GlusterFS los pierde
// al agregar bricks o restaurar una instantánea; lo de adentro conserva los suyos).
func ApplyShareRootACL(root string, rwGID, roGID uint32) error {
	dirACL := PosixACL(7, []ACLEntry{{GID: rwGID, Perm: 7}, {GID: roGID, Perm: 5}})
	if err := os.Lchown(root, -1, int(rwGID)); err != nil {
		return err
	}
	if err := os.Chmod(root, 0o770); err != nil {
		return err
	}
	if err := unix.Setxattr(root, "system.posix_acl_access", dirACL, 0); err != nil {
		return fmt.Errorf("ACL de %s: %w", root, err)
	}
	if err := unix.Setxattr(root, "system.posix_acl_default", dirACL, 0); err != nil {
		return fmt.Errorf("ACL por defecto de %s: %w", root, err)
	}
	return nil
}

func ApplyShareACL(root string, rwGID, roGID uint32) error {
	dirACL := PosixACL(7, []ACLEntry{{GID: rwGID, Perm: 7}, {GID: roGID, Perm: 5}})
	fileACL := PosixACL(6, []ACLEntry{{GID: rwGID, Perm: 6}, {GID: roGID, Perm: 4}})
	rootDev := devOfPath(root)
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return os.Lchown(p, -1, int(rwGID))
		}
		// No cruzar a otros subvolúmenes o filesystems montados adentro.
		if d.IsDir() && p != root && devOfPath(p) != rootDev {
			return filepath.SkipDir
		}
		if err := os.Lchown(p, -1, int(rwGID)); err != nil {
			return err
		}
		if d.IsDir() {
			if err := os.Chmod(p, 0o770); err != nil {
				return err
			}
			if err := unix.Setxattr(p, "system.posix_acl_access", dirACL, 0); err != nil {
				return fmt.Errorf("ACL de %s: %w", p, err)
			}
			if err := unix.Setxattr(p, "system.posix_acl_default", dirACL, 0); err != nil {
				return fmt.Errorf("ACL por defecto de %s: %w", p, err)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		mode := fi.Mode().Perm()&0o110 | 0o660
		if err := os.Chmod(p, mode); err != nil {
			return err
		}
		return unix.Setxattr(p, "system.posix_acl_access", fileACL, 0)
	})
}

func devOfPath(p string) uint64 {
	var st unix.Stat_t
	if unix.Lstat(p, &st) != nil {
		return 0
	}
	return st.Dev
}

// DeleteUser borra la cuenta Samba (si hay) y el usuario Unix de un usuario de TangoOS.
// Solo usuarios de TangoOS (UID ≥ 2000); userdel también lo saca de sus grupos
// suplementarios (tg-*). Sin -r: no tienen home y sus archivos se conservan.
func DeleteUser(ctx context.Context, name string) (existed bool, err error) {
	if err := ValidUserName(name); err != nil {
		return false, err
	}
	u, lookErr := osuser.Lookup(name)
	if lookErr == nil {
		uid, _ := strconv.Atoi(u.Uid)
		if uid < MinUID {
			return false, fmt.Errorf("%w: %s", ErrSystemUser, name)
		}
	}
	if _, statErr := os.Stat(allowedBins["smbpasswd"]); statErr == nil {
		// Si no tenía cuenta Samba smbpasswd falla: no importa.
		_, _ = run(ctx, "smbpasswd", "-x", name)
	}
	if lookErr != nil {
		return false, nil
	}
	if _, err := run(ctx, "userdel", name); err != nil {
		return true, err
	}
	return true, nil
}

// LookupTangoUID devuelve el UID de un usuario de TangoOS (≥ 2000).
func LookupTangoUID(name string) (uint32, error) {
	u, err := osuser.Lookup(name)
	if err != nil {
		return 0, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	if uid < MinUID {
		return 0, fmt.Errorf("%w: %s", ErrSystemUser, name)
	}
	return uint32(uid), nil
}

// ReassignFiles cambia el dueño de los archivos de fromUID a toUID dentro de root, sin
// seguir symlinks ni cruzar a otros subvolúmenes o filesystems.
func ReassignFiles(root string, fromUID, toUID uint32) (int, error) {
	rootDev := devOfPath(root)
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil
		}
		if d.IsDir() && p != root && devOfPath(p) != rootDev {
			return filepath.SkipDir
		}
		var st unix.Stat_t
		if unix.Lstat(p, &st) != nil || st.Uid != fromUID {
			return nil
		}
		if err := os.Lchown(p, int(toUID), -1); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}
