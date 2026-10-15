//go:build linux

package diskops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Los únicos binarios que el agente ejecuta (ADR 0009: exec como último recurso,
// argumentos armados por código, nunca un string de shell).
var allowedBins = map[string]string{
	"mdadm":      "/usr/sbin/mdadm",
	"mkfs.btrfs": "/usr/sbin/mkfs.btrfs",
	"testparm":   "/usr/bin/testparm",
	// Usuarios y grupos (ADR 0009: useradd en el MVP; editar /etc/passwd y /etc/shadow
	// a mano en Go es más riesgo que beneficio) y la base de cuentas de Samba (TDB).
	"useradd":   "/usr/sbin/useradd",
	"groupadd":  "/usr/sbin/groupadd",
	"gpasswd":   "/usr/bin/gpasswd",
	"smbpasswd": "/usr/bin/smbpasswd",
	"userdel":   "/usr/sbin/userdel",
	// Grupos de almacenamiento (ADR 0011): LVM (un solo binario con subcomandos),
	// LUKS2 y los filesystems de los volúmenes que no son Btrfs.
	"lvm":        "/usr/sbin/lvm",
	"cryptsetup": "/usr/sbin/cryptsetup",
	"mkfs.xfs":   "/usr/sbin/mkfs.xfs",
	"mkfs.ext4":  "/usr/sbin/mkfs.ext4",
	"xfs_growfs": "/usr/sbin/xfs_growfs",
	"resize2fs":  "/usr/sbin/resize2fs",
}

func run(ctx context.Context, bin string, args ...string) (string, error) {
	return runStdin(ctx, "", bin, args...)
}

// runStdin es run con stdin (contraseñas para smbpasswd): nunca aparece en el log.
func runStdin(ctx context.Context, stdin, bin string, args ...string) (string, error) {
	return runSecret(ctx, stdin, nil, bin, args...)
}

// runSecret es runStdin con un segundo secreto en un pipe heredado como fd 3
// (/dev/fd/3): cryptsetup lo lee como "archivo de clave" sin que toque el disco.
func runSecret(ctx context.Context, stdin string, fd3 []byte, bin string, args ...string) (string, error) {
	path, ok := allowedBins[bin]
	if !ok {
		return "", fmt.Errorf("binario no permitido: %s", bin)
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("%s no está instalado", bin)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C", "LVM_SUPPRESS_FD_WARNINGS=1"}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if fd3 != nil {
		r, w, err := os.Pipe()
		if err != nil {
			return "", err
		}
		defer r.Close()
		cmd.ExtraFiles = []*os.File{r}
		go func() {
			_, _ = w.Write(fd3)
			w.Close()
		}()
	}
	if err := cmd.Run(); err != nil {
		return out.String(), &ExecError{Bin: bin, Err: err, Output: limit(out.String(), 4096)}
	}
	return out.String(), nil
}

func limit(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// CreateMD arma un array md (RAID 1, 5, 6 o 10) con discos enteros y devuelve el
// nombre del kernel (p. ej. "md127"). Nunca usa --assume-clean: el array se
// sincroniza de verdad (resync en segundo plano, se ve en /proc/mdstat).
func CreateMD(ctx context.Context, name, level string, devs []string) (string, error) {
	profile := map[string]string{"1": "raid1", "5": "raid5", "6": "raid6", "10": "raid10"}[level]
	if profile == "" {
		return "", fmt.Errorf("nivel de RAID no soportado: %q", level)
	}
	if err := CheckProfileDisks(profile, len(devs)); err != nil {
		return "", err
	}
	if err := ValidMDName(name); err != nil {
		return "", err
	}
	// --homehost=any: el nombre queda "any:<name>" y el array se ensambla como
	// /dev/md/<name> en cualquier host, incluido el initramfs (que no conoce el
	// hostname) y otro NAS al que se muden los discos.
	args := []string{"--create", "/dev/md/" + name, "--level=" + level, "--raid-devices=" + strconv.Itoa(len(devs)),
		"--metadata=1.2", "--homehost=any", "--name=" + name, "--run", "--bitmap=internal"}
	args = append(args, devs...)
	if _, err := run(ctx, "mdadm", args...); err != nil {
		return "", err
	}
	// udev crea /dev/md/<name> → ../mdNNN; esperarlo.
	link := "/dev/md/" + name
	for range 100 {
		if t, err := filepath.EvalSymlinks(link); err == nil {
			return filepath.Base(t), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return "", errors.New("el array se creó pero udev no publicó /dev/md/" + name)
}

// StopMD detiene un array (solo para deshacer una creación fallida antes de tener datos).
func StopMD(ctx context.Context, md string) error {
	_, err := run(ctx, "mdadm", "--stop", "/dev/"+md)
	return err
}

// ZeroSuperblock borra el superbloque md de un ex miembro (el array ya detenido).
func ZeroSuperblock(ctx context.Context, dev string) error {
	_, err := run(ctx, "mdadm", "--zero-superblock", dev)
	return err
}

// MDMembers devuelve los discos (sdX) de un array, de /sys/block/mdX/slaves.
func MDMembers(md string) []string {
	ents, _ := os.ReadDir(filepath.Join("/sys/block", md, "slaves"))
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// RemoveMDConfLine saca el array de /etc/mdadm/mdadm.conf.
func RemoveMDConfLine(name string) error {
	const path = "/etc/mdadm/mdadm.conf"
	cur, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return writeAtomic(path, []byte(RemoveMDConf(string(cur), name)), 0o644)
}

// MkfsBtrfs crea Btrfs (metadata DUP, datos single) en dev y devuelve el UUID.
func MkfsBtrfs(ctx context.Context, label, dev string) (string, error) {
	if err := ValidVolumeName(label); err != nil {
		return "", err
	}
	if _, err := run(ctx, "mkfs.btrfs", "-f", "-L", label, "-m", "dup", "-d", "single", dev); err != nil {
		return "", err
	}
	f, err := os.Open(dev)
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, HeadSize)
	if _, err := f.ReadAt(head, 0); err != nil {
		return "", err
	}
	uuid, ok := BtrfsFSID(head)
	if !ok {
		return "", errors.New("mkfs.btrfs terminó pero no hay superbloque Btrfs")
	}
	return uuid, nil
}

// MDSuperOf lee el superbloque md 1.2 de un miembro (para mdadm.conf).
func MDSuperOf(member string) (MDSuper, error) {
	f, err := os.Open(member)
	if err != nil {
		return MDSuper{}, err
	}
	defer f.Close()
	head := make([]byte, 8192)
	if _, err := f.ReadAt(head, 0); err != nil {
		return MDSuper{}, err
	}
	s, ok := mdSuperAt(head)
	if !ok {
		return MDSuper{}, errors.New("sin superbloque md")
	}
	return s, nil
}

// WriteMDConf agrega el array a /etc/mdadm/mdadm.conf (escritura atómica).
func WriteMDConf(name string, s MDSuper) error {
	const path = "/etc/mdadm/mdadm.conf"
	cur, _ := os.ReadFile(path)
	return writeAtomic(path, []byte(MergeMDConf(string(cur), name, MDConfLine(name, s))), 0o644)
}

// MDSync devuelve (acción, progreso 0..1) de un array.
func MDSync(md string) (string, float64) {
	dir := filepath.Join("/sys/block", md, "md")
	action := strings.TrimSpace(readFile(filepath.Join(dir, "sync_action")))
	p := 0.0
	if a, b, ok := strings.Cut(strings.TrimSpace(readFile(filepath.Join(dir, "sync_completed"))), "/"); ok {
		x, _ := strconv.ParseFloat(strings.TrimSpace(a), 64)
		y, _ := strconv.ParseFloat(strings.TrimSpace(b), 64)
		if y > 0 {
			p = x / y
		}
	}
	return action, p
}

// MDCheck lanza "check" en el array y espera que termine; devuelve mismatch_cnt.
func MDCheck(ctx context.Context, md string, progress func(float64)) (uint64, error) {
	dir := filepath.Join("/sys/block", md, "md")
	if a, _ := MDSync(md); a != "idle" {
		return 0, fmt.Errorf("el array está ocupado (%s)", a)
	}
	if err := os.WriteFile(filepath.Join(dir, "sync_action"), []byte("check"), 0); err != nil {
		return 0, err
	}
	time.Sleep(500 * time.Millisecond)
	for {
		a, p := MDSync(md)
		if a == "idle" {
			break
		}
		if progress != nil {
			progress(p)
		}
		select {
		case <-ctx.Done():
			_ = os.WriteFile(filepath.Join(dir, "sync_action"), []byte("idle"), 0)
			return 0, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	n, _ := strconv.ParseUint(strings.TrimSpace(readFile(filepath.Join(dir, "mismatch_cnt"))), 10, 64)
	return n, nil
}

// ValidateSambaConf valida la configuración completa de Samba.
func ValidateSambaConf(ctx context.Context) error {
	_, err := run(ctx, "testparm", "-s", "--suppress-prompt", "/etc/samba/smb.conf")
	return err
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tango-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func sleepTick() <-chan time.Time { return time.After(time.Second) }
