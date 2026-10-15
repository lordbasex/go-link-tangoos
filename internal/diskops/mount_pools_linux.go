//go:build linux

package diskops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	sd "github.com/coreos/go-systemd/v22/dbus"
	godbus "github.com/godbus/dbus/v5"
)

// Montajes de los volúmenes de un grupo (ADR 0011). Igual que los clásicos: unidades
// .mount que arranca systemd por D-Bus. Un volumen cifrado tiene su unidad instalada pero
// no habilitada: se monta al desbloquearlo.

// InstallMountFS escribe la unidad .mount de un volumen con su filesystem; si enable, la
// habilita para el arranque. Siempre la arranca.
func InstallMountFS(ctx context.Context, name, fsUUID, fstype string, enable bool) error {
	unit := MountUnitName(name)
	path := filepath.Join(unitDir, unit)
	if err := writeAtomic(path, []byte(MountUnitFS(name, fsUUID, fstype)), 0o644); err != nil {
		return err
	}
	c, err := sdConn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.ReloadContext(ctx); err != nil {
		return err
	}
	if enable {
		if _, _, err := c.EnableUnitFilesContext(ctx, []string{path}, false, true); err != nil {
			return err
		}
	}
	return startAndWait(ctx, c, unit, "/"+name)
}

func startAndWait(ctx context.Context, c *sd.Conn, unit, where string) error {
	ch := make(chan string, 1)
	if _, err := c.StartUnitContext(ctx, unit, "replace", ch); err != nil {
		return err
	}
	if err := waitJob(ctx, ch); err != nil {
		return err
	}
	for range 50 {
		if isMountPoint(ctx, where) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("systemd montó " + where + " pero no se ve")
}

// StartMount monta un volumen que ya tiene su unidad (al desbloquearlo).
func StartMount(ctx context.Context, name string) error {
	c, err := sdConn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return startAndWait(ctx, c, MountUnitName(name), "/"+name)
}

// StopMount desmonta un volumen y deja su unidad (al bloquearlo). Reintenta como
// UninstallMount si algo lo tiene ocupado un instante.
func StopMount(ctx context.Context, name string) error {
	c, err := sdConn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	var stopErr error
	for range 10 {
		ch := make(chan string, 1)
		if _, err := c.StopUnitContext(ctx, MountUnitName(name), "replace", ch); err != nil {
			return err
		}
		if stopErr = waitJob(ctx, ch); stopErr == nil && !isMountPoint(ctx, "/"+name) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if stopErr == nil {
		stopErr = errors.New("sigue montado")
	}
	return stopErr
}

// IsMounted indica si /<name> es un punto de montaje.
func IsMounted(name string) bool { return isMountPoint(context.Background(), "/"+name) }

// KeyMountDir es donde systemd monta un dispositivo de clave el instante que se usa.
const KeyMountDir = "/run/tangoos-key"

// keyMountUnit es el nombre de la unidad .mount de KeyMountDir (escape de systemd: "-" → "\x2d").
const keyMountUnit = `run-tangoos\x2dkey.mount`

// MountKeyDevice monta el filesystem fsUUID en KeyMountDir con una unidad transitoria de
// systemd (el agente no puede llamar a mount(2): su filtro de seccomp no lo permite).
func MountKeyDevice(ctx context.Context, fsUUID, fstype string, rw bool) error {
	if err := ValidKeyDeviceUUID(fsUUID); err != nil {
		return err
	}
	switch fstype {
	case "vfat", "exfat", "ext4":
	default:
		return errors.New("filesystem de la llave no soportado (vfat, exFAT o ext4)")
	}
	opts := "ro,nosuid,nodev,noexec"
	if rw {
		opts = "rw,nosuid,nodev,noexec"
	}
	if fstype == "vfat" || fstype == "exfat" {
		opts += ",umask=077"
	}
	c, err := sdConn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	props := []sd.Property{
		sd.PropDescription("TangoOS: dispositivo con archivo de clave"),
		{Name: "What", Value: godbus.MakeVariant("/dev/disk/by-uuid/" + fsUUID)},
		{Name: "Where", Value: godbus.MakeVariant(KeyMountDir)},
		{Name: "Type", Value: godbus.MakeVariant(fstype)},
		{Name: "Options", Value: godbus.MakeVariant(opts)},
	}
	_ = os.MkdirAll(KeyMountDir, 0o700)
	ch := make(chan string, 1)
	if _, err := c.StartTransientUnitContext(ctx, keyMountUnit, "replace", props, ch); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			UnmountKeyDevice(context.Background())
			ch = make(chan string, 1)
			if _, err = c.StartTransientUnitContext(ctx, keyMountUnit, "replace", props, ch); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	if err := waitJob(ctx, ch); err != nil {
		return err
	}
	for range 50 {
		if isMountPoint(ctx, KeyMountDir) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("el dispositivo de clave no quedó montado")
}

// UnmountKeyDevice desmonta KeyMountDir.
func UnmountKeyDevice(ctx context.Context) {
	c, err := sdConn(ctx)
	if err != nil {
		return
	}
	defer c.Close()
	ch := make(chan string, 1)
	if _, err := c.StopUnitContext(ctx, keyMountUnit, "replace", ch); err == nil {
		_ = waitJob(ctx, ch)
	}
	_ = c.ResetFailedUnitContext(ctx, keyMountUnit)
}
