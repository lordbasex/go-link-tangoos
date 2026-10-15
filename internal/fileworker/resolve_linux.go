//go:build linux

package fileworker

import (
	"errors"
	"strings"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// noOpenat2 se activa la primera vez que openat2(2) devuelve ENOSYS: systemd lo
// bloquea así cuando la unidad tiene RestrictSUIDSGID=yes (no puede inspeccionar el
// modo dentro de struct open_how), y el worker hereda ese filtro seccomp del agente.
var noOpenat2 atomic.Bool

// openat2Beneath abre rel (ya limpia, sin "..") debajo de root. Usa openat2 con
// RESOLVE_BENEATH|RESOLVE_NO_MAGICLINKS|RESOLVE_NO_XDEV si está disponible; si no,
// resuelve componente por componente con openat(O_NOFOLLOW), siguiendo a mano solo
// los symlinks relativos que no salen de la raíz.
func openat2Beneath(root int, rel string, flags, mode uint64) (int, error) {
	if !noOpenat2.Load() {
		for {
			fd, err := unix.Openat2(root, rel, &unix.OpenHow{Flags: flags, Mode: mode, Resolve: resolveFlags})
			if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
				continue
			}
			if !errors.Is(err, unix.ENOSYS) {
				return fd, err
			}
			noOpenat2.Store(true)
			break
		}
	}
	return resolveBeneath(root, rel, flags, mode)
}

// maxSymlinks como el kernel (MAXSYMLINKS).
const maxSymlinks = 40

// resolveBeneath es la resolución segura sin openat2. Mantiene la pila de carpetas
// abiertas desde la raíz: ".." (que solo puede venir de un symlink) desapila, y nunca
// por debajo de la raíz. Cada paso es openat(O_PATH|O_NOFOLLOW): si alguien cambia un
// componente por un symlink en medio, el paso siguiente falla en vez de seguirlo.
func resolveBeneath(root int, rel string, flags, mode uint64) (int, error) {
	base, err := unix.Dup(root)
	if err != nil {
		return -1, err
	}
	stack := []int{base}
	defer func() {
		for _, fd := range stack {
			unix.Close(fd)
		}
	}()
	queue := splitComps(rel)
	links := 0
	for len(queue) > 0 {
		comp := queue[0]
		queue = queue[1:]
		switch comp {
		case "", ".":
			if len(queue) == 0 {
				return reopen(stack[len(stack)-1], flags)
			}
			continue
		case "..":
			if len(stack) == 1 {
				return -1, unix.EXDEV // saldría de la raíz
			}
			unix.Close(stack[len(stack)-1])
			stack = stack[:len(stack)-1]
			if len(queue) == 0 {
				return reopen(stack[len(stack)-1], flags)
			}
			continue
		}
		cur := stack[len(stack)-1]
		fd, err := unix.Openat(cur, comp, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return -1, err
		}
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			unix.Close(fd)
			return -1, err
		}
		if st.Mode&unix.S_IFMT == unix.S_IFLNK {
			links++
			if links > maxSymlinks {
				unix.Close(fd)
				return -1, unix.ELOOP
			}
			target, err := readlinkAt(cur, comp)
			unix.Close(fd)
			if err != nil {
				return -1, err
			}
			if strings.HasPrefix(target, "/") {
				return -1, unix.EXDEV // symlink absoluto: siempre afuera
			}
			queue = append(splitComps(target), queue...)
			if len(queue) == 0 {
				return reopen(stack[len(stack)-1], flags)
			}
			continue
		}
		if len(queue) == 0 {
			// Último componente: se abre de verdad, sin seguir symlinks (ya no es uno).
			unix.Close(fd)
			return unix.Openat(cur, comp, int(flags)|unix.O_NOFOLLOW, uint32(mode))
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			unix.Close(fd)
			return -1, unix.ENOTDIR
		}
		stack = append(stack, fd)
	}
	return reopen(stack[len(stack)-1], flags)
}

func splitComps(p string) []string {
	out := []string{}
	for _, c := range strings.Split(p, "/") {
		if c != "" && c != "." {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = append(out, ".")
	}
	return out
}

// reopen abre de nuevo una carpeta de la pila con los flags pedidos.
func reopen(dirfd int, flags uint64) (int, error) {
	return unix.Openat(dirfd, ".", int(flags), 0)
}

func readlinkAt(dirfd int, name string) (string, error) {
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(dirfd, name, buf)
	if err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}

// openTrash abre la carpeta .tango-trash de la raíz sin seguir symlinks.
func openTrash(root int, flags int) (int, error) {
	return unix.Openat(root, TrashDir, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
}
