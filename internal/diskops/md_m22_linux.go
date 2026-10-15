//go:build linux

package diskops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"
)

// Fase M2.2 (protocol/README.md §11): reemplazar un disco de un array y ampliarlo.
// Solo mdadm por exec (allow-list de md_linux.go); el resto es sysfs e ioctl.

// MDOf devuelve el nombre del kernel ("md127") del array /dev/md/<name>.
func MDOf(name string) (string, error) {
	if err := ValidVolumeName(name); err != nil {
		return "", err
	}
	t, err := filepath.EvalSymlinks("/dev/md/" + name)
	if err != nil {
		return "", fmt.Errorf("no existe el RAID %s", name)
	}
	return filepath.Base(t), nil
}

// MDInfo es el estado de un array leído de sysfs.
type MDInfo struct {
	Level      string // raid1 | raid5 | raid6 | raid10
	RaidDisks  int
	Degraded   int
	Action     string // idle | resync | recover | reshape | check | repair | frozen
	Progress   float64
	SizeBytes  uint64
	Members    map[string]string // disco entero (sdX) → estado de md ("in_sync", "faulty", "spare"…)
	ArrayState string
}

// ReadMD lee el estado de un array.
func ReadMD(md string) MDInfo {
	dir := filepath.Join("/sys/block", md, "md")
	in := MDInfo{Members: map[string]string{}}
	in.Level = strings.TrimSpace(readFile(filepath.Join(dir, "level")))
	in.RaidDisks, _ = strconv.Atoi(strings.TrimSpace(readFile(filepath.Join(dir, "raid_disks"))))
	in.Degraded, _ = strconv.Atoi(strings.TrimSpace(readFile(filepath.Join(dir, "degraded"))))
	in.Action, in.Progress = MDSync(md)
	in.ArrayState = strings.TrimSpace(readFile(filepath.Join(dir, "array_state")))
	sectors, _ := strconv.ParseUint(strings.TrimSpace(readFile(filepath.Join("/sys/block", md, "size"))), 10, 64)
	in.SizeBytes = sectors * 512
	devs, _ := filepath.Glob(filepath.Join(dir, "dev-*"))
	for _, d := range devs {
		p, err := filepath.EvalSymlinks(filepath.Join(d, "block"))
		if err != nil {
			continue
		}
		disk := filepath.Base(p)
		// Miembro por partición: el disco es el directorio padre en sysfs.
		if _, err := os.Stat(filepath.Join(p, "partition")); err == nil {
			disk = filepath.Base(filepath.Dir(p))
		}
		in.Members[disk] = strings.TrimSpace(readFile(filepath.Join(d, "state")))
	}
	return in
}

// MDManage ejecuta `mdadm --manage /dev/md/<name> --<action> <dev>` (fail, remove o add).
func MDManage(ctx context.Context, name, action, dev string) error {
	switch action {
	case "fail", "remove", "add":
	default:
		return fmt.Errorf("acción de mdadm no permitida: %s", action)
	}
	if err := ValidVolumeName(name); err != nil {
		return err
	}
	if !strings.HasPrefix(dev, "/dev/") || strings.Contains(dev, "..") {
		return errors.New("dispositivo inválido")
	}
	_, err := run(ctx, "mdadm", "--manage", "/dev/md/"+name, "--"+action, dev)
	return err
}

// MDGrow cambia la cantidad de discos activos de un array (reshape en segundo plano).
// backupFile se usa solo si mdadm lo exige (arrays sin espacio para mover el data offset).
func MDGrow(ctx context.Context, name string, raidDevices int, backupFile string) error {
	if err := ValidVolumeName(name); err != nil {
		return err
	}
	if raidDevices < 2 || raidDevices > 16 {
		return errors.New("cantidad de discos inválida")
	}
	args := []string{"--grow", "/dev/md/" + name, "--raid-devices=" + strconv.Itoa(raidDevices)}
	if backupFile != "" {
		args = append(args, "--backup-file="+backupFile)
	}
	_, err := run(ctx, "mdadm", args...)
	return err
}

// ioctlResize es BTRFS_IOC_RESIZE (_IOW(0x94, 3, struct btrfs_ioctl_vol_args)).
var ioctlResize = iow(3, unsafe.Sizeof(volArgs{}))

// BtrfsResizeMax agranda el filesystem montado en mount hasta ocupar todo su
// dispositivo (devid 1: el md o el disco del volumen), como `btrfs filesystem resize max`.
func BtrfsResizeMax(mount string) error {
	d, err := os.Open(mount)
	if err != nil {
		return err
	}
	defer d.Close()
	var a volArgs
	copy(a.name[:], "1:max")
	return ioctl(d.Fd(), ioctlResize, unsafe.Pointer(&a))
}
