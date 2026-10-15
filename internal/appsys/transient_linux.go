//go:build linux

package appsys

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	sd "github.com/coreos/go-systemd/v22/dbus"
	"github.com/godbus/dbus/v5"
)

// RunError es el fallo de un comando: el código de salida y las últimas líneas de su
// salida (para el log del agente y un mensaje legible; nunca secretos: los valores
// sensibles viajan en el .env, no en el argv).
type RunError struct {
	Unit   string
	Status int
	Tail   []string
}

func (e *RunError) Error() string {
	msg := fmt.Sprintf("%s terminó con código %d", e.Unit, e.Status)
	if n := len(e.Tail); n > 0 {
		msg += ": " + e.Tail[n-1]
	}
	return msg
}

// RunUnit ejecuta argv como unidad transitoria de systemd (systemd-run por D-Bus):
// corre en el namespace del host, fuera del sandbox del agente. La salida va a
// logPath; onLine recibe cada línea nueva mientras corre. Bloquea hasta que termina.
func RunUnit(ctx context.Context, prefix string, argv, env []string, logPath string, onLine func(string)) error {
	if len(argv) == 0 || !filepath.IsAbs(argv[0]) {
		return errors.New("argv inválido")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		return err
	}
	conn, err := sd.NewSystemConnectionContext(ctx)
	if err != nil {
		return fmt.Errorf("systemd: %w", err)
	}
	defer conn.Close()

	var rb [4]byte
	_, _ = rand.Read(rb[:])
	unit := fmt.Sprintf("%s-%s.service", prefix, hex.EncodeToString(rb[:]))
	props := []sd.Property{
		sd.PropDescription("TangoOS: " + strings.Join(argv[:min(3, len(argv))], " ")),
		sd.PropExecStart(argv, false),
		sd.PropType("oneshot"),
		sd.PropRemainAfterExit(false),
		{Name: "StandardOutputFileToAppend", Value: dbus.MakeVariant(logPath)},
		{Name: "StandardErrorFileToAppend", Value: dbus.MakeVariant(logPath)},
		{Name: "Environment", Value: dbus.MakeVariant(append([]string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}, env...))},
		{Name: "TimeoutStartUSec", Value: dbus.MakeVariant(uint64(2 * time.Hour / time.Microsecond))},
	}
	ch := make(chan string, 1)
	if _, err := conn.StartTransientUnitContext(ctx, unit, "fail", props, ch); err != nil {
		return fmt.Errorf("systemd: %w", err)
	}

	tailCtx, stopTail := context.WithCancel(context.Background())
	tailDone := make(chan []string, 1)
	go func() { tailDone <- follow(tailCtx, logPath, onLine) }()

	var result string
	select {
	case result = <-ch:
	case <-ctx.Done():
		_, _ = conn.StopUnitContext(context.Background(), unit, "replace", nil)
		result = "canceled"
	}
	time.Sleep(300 * time.Millisecond) // que el tail lea las últimas líneas
	stopTail()
	tail := <-tailDone
	if result == "done" {
		return nil
	}
	status := -1
	if p, err := conn.GetServicePropertyContext(context.Background(), unit, "ExecMainStatus"); err == nil {
		if v, ok := p.Value.Value().(int32); ok {
			status = int(v)
		}
	}
	_ = conn.ResetFailedUnitContext(context.Background(), unit)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return &RunError{Unit: unit, Status: status, Tail: tail}
}

// follow lee el archivo a medida que crece y devuelve las últimas líneas.
func follow(ctx context.Context, path string, onLine func(string)) []string {
	var last []string
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	rd := bufio.NewReader(f)
	var partial string
	emit := func(line string) {
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return
		}
		last = append(last, line)
		if len(last) > 20 {
			last = last[1:]
		}
		if onLine != nil {
			onLine(line)
		}
	}
	for {
		s, err := rd.ReadString('\n')
		if err == nil {
			emit(partial + s)
			partial = ""
			continue
		}
		partial += s
		if !errors.Is(err, io.EOF) {
			return last
		}
		select {
		case <-ctx.Done():
			// Vaciar lo que haya quedado.
			for {
				s, err := rd.ReadString('\n')
				partial += s
				if err != nil {
					break
				}
				emit(partial)
				partial = ""
			}
			emit(partial)
			return last
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// EnableAndStart habilita y arranca una unidad del sistema (docker.service).
func EnableAndStart(ctx context.Context, unit string) error {
	conn, err := sd.NewSystemConnectionContext(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, _, err := conn.EnableUnitFilesContext(ctx, []string{unit}, false, true); err != nil {
		return err
	}
	ch := make(chan string, 1)
	if _, err := conn.StartUnitContext(ctx, unit, "replace", ch); err != nil {
		return err
	}
	select {
	case r := <-ch:
		if r != "done" {
			return fmt.Errorf("%s: %s", unit, r)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// UnitActive dice si una unidad está activa.
func UnitActive(ctx context.Context, unit string) bool {
	conn, err := sd.NewSystemConnectionContext(ctx)
	if err != nil {
		return false
	}
	defer conn.Close()
	p, err := conn.GetUnitPropertyContext(ctx, unit, "ActiveState")
	if err != nil {
		return false
	}
	s, _ := p.Value.Value().(string)
	return s == "active"
}
