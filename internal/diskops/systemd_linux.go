//go:build linux

package diskops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sd "github.com/coreos/go-systemd/v22/dbus"
)

// El agente corre en su propio namespace de montaje (ProtectSystem en la unidad):
// un mount(2) hecho por él no llegaría al host. Por eso los volúmenes se montan con
// unidades .mount que arranca systemd (PID 1) por D-Bus (ADR 0009: sin exec).

const unitDir = "/etc/systemd/system"

func sdConn(ctx context.Context) (*sd.Conn, error) {
	return sd.NewSystemConnectionContext(ctx)
}

func waitJob(ctx context.Context, ch <-chan string) error {
	select {
	case r := <-ch:
		if r != "done" {
			return fmt.Errorf("systemd: %s", r)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// InstallMount escribe la unidad .mount del volumen, la habilita y la arranca.
func InstallMount(ctx context.Context, name, fsUUID string) error {
	unit := MountUnitName(name)
	path := filepath.Join(unitDir, unit)
	if err := writeAtomic(path, []byte(MountUnit(name, fsUUID)), 0o644); err != nil {
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
	if _, _, err := c.EnableUnitFilesContext(ctx, []string{path}, false, true); err != nil {
		return err
	}
	ch := make(chan string, 1)
	if _, err := c.StartUnitContext(ctx, unit, "replace", ch); err != nil {
		return err
	}
	if err := waitJob(ctx, ch); err != nil {
		return err
	}
	// Esperar que el montaje se vea también desde el namespace del agente.
	for i := 0; i < 50; i++ {
		if isMountPoint(ctx, "/"+name) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("systemd montó el volumen pero no aparece en /" + name)
}

// RemoveMount deshace InstallMount (para revertir una creación fallida).
func RemoveMount(ctx context.Context, name string) {
	unit := MountUnitName(name)
	if c, err := sdConn(ctx); err == nil {
		ch := make(chan string, 1)
		if _, err := c.StopUnitContext(ctx, unit, "replace", ch); err == nil {
			_ = waitJob(ctx, ch)
		}
		_, _ = c.DisableUnitFilesContext(ctx, []string{unit}, false)
		_ = os.Remove(filepath.Join(unitDir, unit))
		_ = c.ReloadContext(ctx)
		c.Close()
	}
}

// ReloadIfActive recarga una unidad (p. ej. smbd.service) si está activa.
func ReloadIfActive(ctx context.Context, unit string) (bool, error) {
	c, err := sdConn(ctx)
	if err != nil {
		return false, err
	}
	defer c.Close()
	p, err := c.GetUnitPropertyContext(ctx, unit, "ActiveState")
	if err != nil {
		return false, err
	}
	if strings.Trim(p.Value.String(), `"`) != "active" {
		return false, nil
	}
	ch := make(chan string, 1)
	if _, err := c.ReloadUnitContext(ctx, unit, "replace", ch); err != nil {
		return false, err
	}
	return true, waitJob(ctx, ch)
}

// RestartIfActive reinicia una unidad si está activa (hace falta para ajustes que
// smbd lee solo al arrancar, como "multicast dns register").
func RestartIfActive(ctx context.Context, unit string) (bool, error) {
	c, err := sdConn(ctx)
	if err != nil {
		return false, err
	}
	defer c.Close()
	p, err := c.GetUnitPropertyContext(ctx, unit, "ActiveState")
	if err != nil {
		return false, err
	}
	if strings.Trim(p.Value.String(), `"`) != "active" {
		return false, nil
	}
	ch := make(chan string, 1)
	if _, err := c.RestartUnitContext(ctx, unit, "replace", ch); err != nil {
		return false, err
	}
	return true, waitJob(ctx, ch)
}

// isMountPoint compara el dispositivo del directorio con el de su padre.
func isMountPoint(_ context.Context, dir string) bool {
	a, err1 := os.Stat(dir)
	b, err2 := os.Stat(filepath.Dir(dir))
	if err1 != nil || err2 != nil {
		return false
	}
	return devOf(a) != devOf(b)
}

// UninstallMount desmonta un volumen (detiene su unidad .mount), la deshabilita y la
// borra. A diferencia de RemoveMount, informa los errores: se usa al eliminar un
// volumen y no se puede seguir si sigue montado.
func UninstallMount(ctx context.Context, name string) error {
	unit := MountUnitName(name)
	c, err := sdConn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	// Un proceso que acaba de soltar el volumen (file-worker cerrándose, smbd recargando)
	// puede dejarlo ocupado unos instantes: reintentar el desmontaje hasta ~10 s
	// (hallazgo e2e 2026-10-15: "umount: target is busy" al borrar un volumen recién usado).
	var stopErr error
	for attempt := 0; attempt < 10; attempt++ {
		ch := make(chan string, 1)
		if _, err := c.StopUnitContext(ctx, unit, "replace", ch); err != nil {
			return err
		}
		if stopErr = waitJob(ctx, ch); stopErr == nil && !isMountPoint(ctx, "/"+name) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if stopErr != nil {
		return fmt.Errorf("desmontar /%s: %w (¿algún proceso lo está usando?)", name, stopErr)
	}
	if _, err := c.DisableUnitFilesContext(ctx, []string{unit}, false); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(unitDir, unit)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := c.ReloadContext(ctx); err != nil {
		return err
	}
	for range 50 {
		if !isMountPoint(ctx, "/"+name) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("systemd detuvo la unidad pero /" + name + " sigue montado")
}
