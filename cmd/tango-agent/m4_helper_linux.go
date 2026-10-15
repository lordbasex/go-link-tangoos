//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/services"
	"github.com/lordbasex/go-link-tangoos/internal/updates"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// runM4Helper corre como unidad transitoria (root, sin el sandbox del agente). Sus
// líneas "STAGE <progreso> <texto>" las convierte el agente en etapas del job.
func runM4Helper(task string, args []string) error {
	if err := requireRoot(); err != nil {
		return err
	}
	ctx := context.Background()
	switch task {
	case "updates-simulate":
		_ = runCmd(ctx, "/usr/bin/apt-get", "-q", "update")
		return runCmd(ctx, "/usr/bin/apt-get", "-s", "-q", "dist-upgrade")
	case "updates-apply":
		if len(args) != 2 {
			return errors.New("uso: updates-apply <all|security> <instantánea|\"\">")
		}
		return helperUpdatesApply(ctx, args[0], args[1])
	case "rollback":
		if len(args) != 1 || !updates.ValidSnapID(args[0]) {
			return errors.New("punto inválido")
		}
		return helperRollback(args[0])
	case "nfs-apply":
		var a agentproto.NFSApplyArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperNFS(ctx, &a)
	case "term-shell":
		if len(args) != 4 || !strings.HasPrefix(args[0], "/run/tango/term-") {
			return errors.New("argumentos inválidos")
		}
		return helperTermShell(args[0], args[1], args[2], args[3])
	case "ups-apply":
		var a agentproto.UPSApplyArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperUPS(ctx, &a)
	case "iscsi-apply":
		var a agentproto.ISCSIApplyArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperISCSI(ctx, &a)
	}
	return fmt.Errorf("tarea desconocida %q", task)
}

func stage(p float64, s string) { fmt.Printf("STAGE %.2f %s\n", p, s) }

func readArgs(args []string, v any) error {
	if len(args) != 1 || !strings.HasPrefix(args[0], m4StateDir+"/") {
		return errors.New("argumentos inválidos")
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	return agentproto.DecodeStrict(b, v)
}

func runCmd(ctx context.Context, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stdout
	cmd.Env = append(os.Environ(), "LANG=C.UTF-8", "LC_ALL=C.UTF-8")
	return cmd.Run()
}

// ---- Actualizaciones -----------------------------------------------------------------

func helperUpdatesApply(ctx context.Context, scope, snap string) error {
	if scope != "all" && scope != "security" {
		return errors.New("alcance inválido")
	}
	if snap != "" {
		if !updates.ValidSnapID(snap) {
			return errors.New("nombre de instantánea inválido")
		}
		stage(0.06, "Creando la instantánea del sistema")
		if err := diskops.CreateSnapshot("/", snapDir, snap); err != nil {
			return fmt.Errorf("instantánea del sistema: %w", err)
		}
		// /boot es ext4 aparte: se guarda una copia para volver con el kernel que corresponde.
		if err := copyTree("/boot", filepath.Join(snapDir, snap+".boot")); err != nil {
			fmt.Println("aviso: no se pudo copiar /boot:", err)
		}
		writeLabel(snap, "Antes de actualizar ("+map[string]string{"all": "todo", "security": "solo seguridad"}[scope]+")")
	}
	stage(0.12, "Actualizando la lista de paquetes")
	if err := runCmd(ctx, "/usr/bin/apt-get", "-q", "update"); err != nil {
		return fmt.Errorf("apt-get update: %w", err)
	}
	opts := []string{"-q", "-y", "-o", "Dpkg::Options::=--force-confold", "-o", "Dpkg::Options::=--force-confdef"}
	if scope == "security" {
		out, err := exec.CommandContext(ctx, "/usr/bin/apt-get", "-s", "-q", "dist-upgrade").Output()
		if err != nil {
			return fmt.Errorf("apt-get -s: %w", err)
		}
		names := []string{}
		for _, n := range updates.SecurityNames(updates.ParseSimulate(string(out))) {
			if updates.ValidPackageName(n) {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			stage(0.99, "No hay parches de seguridad pendientes")
			return nil
		}
		stage(0.2, fmt.Sprintf("Instalando %d parches de seguridad", len(names)))
		return runCmd(ctx, "/usr/bin/apt-get", append(append(opts, "--only-upgrade", "install"), names...)...)
	}
	stage(0.2, "Instalando las actualizaciones")
	return runCmd(ctx, "/usr/bin/apt-get", append(opts, "dist-upgrade")...)
}

func writeLabel(id, label string) {
	labelsMu.Lock()
	defer labelsMu.Unlock()
	m := map[string]string{}
	if b, err := os.ReadFile(labelsFile); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	m[id] = label
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(labelsFile, b, 0o644)
}

// helperRollback arma un @rootfs nuevo desde el punto elegido (instantánea escribible),
// guarda el actual como @rootfs-reemplazado-<fecha> y restaura /boot. Al reiniciar,
// fstab/GRUB (subvol=@rootfs) arrancan el sistema anterior.
func helperRollback(id string) error {
	dev, err := rootDevice()
	if err != nil {
		return err
	}
	top := "/run/tango-rollback"
	if err := os.MkdirAll(top, 0o700); err != nil {
		return err
	}
	if err := unix.Mount(dev, top, "btrfs", 0, "subvolid=5"); err != nil {
		return fmt.Errorf("montar el nivel superior: %w", err)
	}
	defer func() { _ = unix.Unmount(top, 0) }()
	src := filepath.Join(top, "@snapshots", id)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("no existe %s", id)
	}
	old := "@rootfs-reemplazado-" + time.Now().UTC().Format("20060102-150405")
	stage(0.4, "Guardando el sistema actual como "+old)
	if err := os.Rename(filepath.Join(top, "@rootfs"), filepath.Join(top, old)); err != nil {
		return fmt.Errorf("renombrar @rootfs: %w", err)
	}
	stage(0.6, "Creando @rootfs desde "+id)
	if err := diskops.CreateWritableSnapshot(src, top, "@rootfs"); err != nil {
		_ = os.Rename(filepath.Join(top, old), filepath.Join(top, "@rootfs")) // deshacer
		return fmt.Errorf("instantánea escribible: %w", err)
	}
	if b := filepath.Join(snapDir, id+".boot"); dirExists(b) {
		stage(0.8, "Restaurando /boot")
		if err := copyTree(b, "/boot"); err != nil {
			return fmt.Errorf("restaurar /boot: %w", err)
		}
	}
	writeLabel(id, "Restaurado el "+time.Now().Format("02/01/2006 15:04"))
	stage(0.9, "Listo: se reinicia con el sistema anterior")
	return nil
}

// rootDevice es el dispositivo que respalda / (de /proc/self/mountinfo).
func rootDevice() (string, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 || fields[4] != "/" {
			continue
		}
		for i, x := range fields {
			if x == "-" && i+2 < len(fields) {
				if fields[i+1] != "btrfs" {
					return "", errors.New("/ no es Btrfs")
				}
				return fields[i+2], nil
			}
		}
	}
	return "", errors.New("no se encontró el dispositivo de /")
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// copyTree copia archivos, directorios y symlinks de src sobre dst (sin borrar extras).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode()&fs.ModeSymlink != 0:
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(l, target)
		case info.Mode().IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			tmp := target + ".tango-tmp"
			out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			if err := out.Sync(); err != nil {
				out.Close()
				return err
			}
			out.Close()
			return os.Rename(tmp, target)
		}
		return nil
	})
}

// ---- NFS ---------------------------------------------------------------------------

func helperNFS(ctx context.Context, a *agentproto.NFSApplyArgs) error {
	text, err := services.RenderExports(a.Exports)
	if err != nil {
		return err
	}
	installed := fileExists("/usr/sbin/exportfs")
	if a.Enabled && !installed {
		stage(0.2, "Instalando el servidor NFS (nfs-kernel-server)")
		if err := runCmd(ctx, "/usr/bin/apt-get", "-q", "update"); err != nil {
			return err
		}
		if err := runCmd(ctx, "/usr/bin/apt-get", "-q", "-y", "--no-install-recommends", "install", "nfs-kernel-server"); err != nil {
			return err
		}
		installed = true
	}
	stage(0.7, "Escribiendo "+services.ExportsFile)
	if err := os.MkdirAll(filepath.Dir(services.ExportsFile), 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(services.ExportsFile, []byte(text), 0o644); err != nil {
		return err
	}
	if !installed {
		return nil
	}
	if a.Enabled {
		stage(0.85, "Activando nfs-server")
		if err := setUnit(ctx, "nfs-server.service", true); err != nil {
			return err
		}
		return runCmd(ctx, "/usr/sbin/exportfs", "-ra")
	}
	stage(0.85, "Deteniendo nfs-server")
	return setUnit(ctx, "nfs-server.service", false)
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// ---- iSCSI (LIO por configfs) ------------------------------------------------------

// FS_NOCOW_FL: sin copy-on-write para el archivo del LUN (rendimiento y fragmentación).
const fsNocowFl = 0x00800000

func helperISCSI(ctx context.Context, a *agentproto.ISCSIApplyArgs) error {
	want := map[string]agentproto.ISCSITarget{}
	if a.Enabled {
		for _, t := range a.Targets {
			if err := services.ValidateTarget(t); err != nil {
				return err
			}
			want[t.IQN] = t
		}
	}
	if a.Enabled || dirExists(services.ConfigFS) {
		stage(0.1, "Cargando los módulos de LIO")
		for _, m := range []string{"target_core_mod", "target_core_file", "iscsi_target_mod"} {
			if err := runCmd(ctx, "/usr/sbin/modprobe", m); err != nil && a.Enabled {
				return fmt.Errorf("modprobe %s: %w", m, err)
			}
		}
		if !dirExists(services.ConfigFS) {
			_ = unix.Mount("configfs", "/sys/kernel/config", "configfs", 0, "")
		}
	}
	// Quitar destinos de TangoOS que ya no van.
	if ents, err := os.ReadDir(filepath.Join(services.ConfigFS, "iscsi")); err == nil {
		for _, e := range ents {
			iqn := e.Name()
			if !strings.HasPrefix(iqn, services.IQNPrefix+":") {
				continue
			}
			if _, keep := want[iqn]; keep {
				continue
			}
			name := strings.TrimPrefix(iqn, services.IQNPrefix+":")
			stage(0.2, "Quitando el destino "+name)
			for _, p := range services.TeardownPaths(agentproto.ISCSITarget{Name: name, IQN: iqn}) {
				if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
					fmt.Println("aviso:", err)
				}
			}
		}
	}
	for _, f := range a.Delete {
		if err := validLUNFile(f); err != nil {
			return err
		}
		stage(0.3, "Borrando "+f)
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	i := 0
	for _, t := range want {
		i++
		stage(0.4+0.5*float64(i)/float64(len(want)+1), "Configurando "+t.Name)
		if err := ensureLUNFile(t); err != nil {
			return err
		}
		if err := applyPlan(services.Plan(t)); err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
	}
	stage(0.98, "Listo")
	return nil
}

func ensureLUNFile(t agentproto.ISCSITarget) error {
	dir := filepath.Dir(t.File)
	mount := filepath.Dir(dir)
	var st unix.Statfs_t
	if err := unix.Statfs(mount, &st); err != nil || st.Type != unix.BTRFS_SUPER_MAGIC {
		return fmt.Errorf("%s no es un volumen de TangoOS montado", mount)
	}
	if !dirExists(dir) {
		if err := diskops.CreateSubvolume(mount, "@iscsi"); err != nil && !errors.Is(err, fs.ErrExist) {
			if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		}
		setNoCOW(dir)
	}
	if fileExists(t.File) {
		return nil
	}
	f, err := os.OpenFile(t.File, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	setNoCOWFd(int(f.Fd()))
	if t.SizeBytes > int64(st.Bavail)*int64(st.Bsize) {
		_ = os.Remove(t.File)
		return errors.New("NO_SPACE: no hay espacio en el volumen")
	}
	return f.Truncate(t.SizeBytes) // provisión fina: ocupa a medida que se escribe
}

func setNoCOW(p string) {
	f, err := os.Open(p)
	if err != nil {
		return
	}
	defer f.Close()
	setNoCOWFd(int(f.Fd()))
}

func setNoCOWFd(fd int) {
	flags, err := unix.IoctlGetInt(fd, unix.FS_IOC_GETFLAGS)
	if err != nil {
		return
	}
	_ = unix.IoctlSetPointerInt(fd, unix.FS_IOC_SETFLAGS, flags|fsNocowFl)
}

// applyPlan ejecuta los pasos de forma idempotente: si el backstore ya está habilitado
// no se reescribe su control (LIO lo rechaza).
func applyPlan(steps []services.Step) error {
	for _, s := range steps {
		switch {
		case s.Mkdir:
			if err := os.MkdirAll(s.Path, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		case s.Symlink != "":
			if _, err := os.Lstat(s.Path); err == nil {
				continue
			}
			if err := os.Symlink(s.Symlink, s.Path); err != nil {
				return err
			}
		default:
			if cur, err := os.ReadFile(s.Path); err == nil && strings.TrimSpace(string(cur)) == s.Value {
				continue
			}
			if strings.HasSuffix(s.Path, "/control") || strings.HasSuffix(s.Path, "/vpd_unit_serial") {
				if b, err := os.ReadFile(filepath.Join(filepath.Dir(strings.TrimSuffix(s.Path, "/wwn/vpd_unit_serial")), "enable")); err == nil && strings.TrimSpace(string(b)) == "1" {
					continue
				}
			}
			if err := os.WriteFile(s.Path, []byte(s.Value), 0o600); err != nil {
				return fmt.Errorf("%s: %w", s.Path, err)
			}
		}
	}
	return nil
}

// helperTermShell crea el PTY en el host, arranca bash en el esclavo (sesión y terminal
// de control propias), le pasa el master al agente y espera a que la shell termine.
func helperTermShell(sock, colsS, rowsS, user string) error {
	cols, _ := strconv.Atoi(colsS)
	rows, _ := strconv.Atoi(rowsS)
	mfd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	if err := unix.IoctlSetPointerInt(mfd, unix.TIOCSPTLCK, 0); err != nil {
		return err
	}
	n, err := unix.IoctlGetInt(mfd, unix.TIOCGPTN)
	if err != nil {
		return err
	}
	_ = unix.IoctlSetWinsize(mfd, unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return err
	}
	cmd := exec.Command("/bin/bash", "-l")
	cmd.Dir = "/root"
	cmd.Env = []string{"TERM=xterm-256color", "HOME=/root", "USER=root", "LOGNAME=root", "SHELL=/bin/bash", "LANG=C.UTF-8",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TANGO_USER=" + user}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return err
	}
	slave.Close()
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	_, _, err = c.WriteMsgUnix([]byte("pty"), unix.UnixRights(mfd), nil)
	c.Close()
	unix.Close(mfd)
	if err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	_ = cmd.Wait()
	return nil
}
