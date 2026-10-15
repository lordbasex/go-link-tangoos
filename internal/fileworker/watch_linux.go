package fileworker

import (
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// watch espera cambios en hasta MaxWatchDirs carpetas (inotify) por hasta timeout
// segundos. Cuando llega el primer evento junta los que lleguen en los 250 ms
// siguientes y responde. Los nombres internos (papelera, parciales) se ignoran.
func watch(roots, rels []string, timeout int) (WatchResult, *Error) {
	if len(roots) != len(rels) || len(roots) == 0 || len(roots) > MaxWatchDirs {
		return WatchResult{Changed: []int{}}, errf(CodeInvalid, "entre 1 y %d carpetas", MaxWatchDirs)
	}
	if timeout <= 0 || timeout > MaxWatchSeconds {
		timeout = MaxWatchSeconds
	}
	dirs := map[int]int{} // índice → fd O_PATH de la carpeta (ya resuelta con RESOLVE_BENEATH)
	for i := range roots {
		_, _ = withRoot(roots[i], func(root int) (any, *Error) {
			dfd, e := openBeneath(root, rels[i], unix.O_PATH|unix.O_DIRECTORY, 0)
			if e == nil {
				dirs[i] = dfd // sin permiso o ya no existe: no se vigila
			}
			return nil, nil
		})
	}
	defer func() {
		for _, fd := range dirs {
			unix.Close(fd)
		}
	}()
	return watchFDs(dirs, time.Duration(timeout)*time.Second)
}

// watchFDs vigila las carpetas ya abiertas (índice → fd) hasta timeout.
func watchFDs(dirs map[int]int, timeout time.Duration) (WatchResult, *Error) {
	out := WatchResult{Changed: []int{}}
	ifd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return out, errf(CodeInternal, "inotify: %v", err)
	}
	defer unix.Close(ifd)
	const mask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
		unix.IN_CLOSE_WRITE | unix.IN_ATTRIB | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR
	wds := map[int32]int{}
	for i, dfd := range dirs {
		// Se vigila ese inodo (vía el fd), no una ruta que podría cambiar.
		if wd, err := unix.InotifyAddWatch(ifd, "/proc/self/fd/"+strconv.Itoa(dfd), mask); err == nil {
			wds[int32(wd)] = i
		}
	}
	if len(wds) == 0 {
		return out, errf(CodeNotFound, "ninguna carpeta se puede vigilar")
	}
	seen := map[int]bool{}
	buf := make([]byte, 64<<10)
	deadline := time.Now().Add(timeout)
	var settle time.Time // fin de la ventana de agrupado (después del primer evento)
	for {
		end := deadline
		if !settle.IsZero() {
			end = settle
		}
		wait := time.Until(end)
		if wait <= 0 {
			break
		}
		fds := []unix.PollFd{{Fd: int32(ifd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, int(wait/time.Millisecond)+1)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n == 0 {
			continue
		}
		m, err := unix.Read(ifd, buf)
		if err != nil || m <= 0 {
			continue
		}
		for off := 0; off+unix.SizeofInotifyEvent <= m; {
			ev := (*unix.InotifyEvent)(unsafePointer(&buf[off]))
			name := ""
			if ev.Len > 0 {
				raw := buf[off+unix.SizeofInotifyEvent : off+unix.SizeofInotifyEvent+int(ev.Len)]
				for i, c := range raw {
					if c == 0 {
						raw = raw[:i]
						break
					}
				}
				name = string(raw)
			}
			off += unix.SizeofInotifyEvent + int(ev.Len)
			if name != "" && Hidden(name) {
				continue
			}
			if i, ok := wds[ev.Wd]; ok && !seen[i] {
				seen[i] = true
				out.Changed = append(out.Changed, i)
			}
		}
		if len(seen) > 0 && settle.IsZero() {
			settle = time.Now().Add(250 * time.Millisecond)
		}
	}
	return out, nil
}
