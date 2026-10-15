//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	sd "github.com/coreos/go-systemd/v22/dbus"
	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"

	"github.com/lordbasex/go-link-tangoos/internal/appsys"
	"github.com/lordbasex/go-link-tangoos/internal/services"
	"github.com/lordbasex/go-link-tangoos/internal/updates"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Fase M4 (protocol/README.md §12). Lo que necesita red (apt), montar el nivel superior
// del Btrfs del sistema, cargar módulos del kernel (LIO) o un shell completo corre como
// unidad transitoria de systemd (`tango-agent m4-helper …`), fuera del sandbox del
// agente (PrivateNetwork, ProtectKernelModules, SystemCallFilter sin @mount). El agente
// valida todo antes; el helper solo recibe argumentos ya validados o un JSON que vuelve
// a validar.

const (
	m4HelperArg = "m4-helper"
	m4LogDir    = "/var/lib/tango-agent/m4-logs"
	m4StateDir  = "/var/lib/tango-agent/m4"
	snapDir     = "/.snapshots"
	journalctl  = "/usr/bin/journalctl"
)

var (
	hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	tzRe       = regexp.MustCompile(`^[A-Za-z]+(/[A-Za-z0-9_+-]+){0,2}$|^UTC$`)
)

// managedUnits son las únicas unidades cuyo estado consulta o cambia el agente.
var managedUnits = map[string]bool{"smbd.service": true, "nmbd.service": true, "nfs-server.service": true, "docker.service": true, "tangod.service": true}

func init() {
	if len(os.Args) < 3 || os.Args[1] != m4HelperArg {
		return
	}
	if err := runM4Helper(os.Args[2], os.Args[3:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func registerM4(r *agentproto.Registry, jobs *asyncJobs, log *slog.Logger) {
	agentproto.Register(r, agentproto.OpM4Job, func(a *agentproto.M4JobArgs) *agentproto.Error {
		return validOpID(a.OpID)
	}, func(_ context.Context, a *agentproto.M4JobArgs) (any, *agentproto.Error) {
		return jobs.status(a.OpID)
	})

	agentproto.Register(r, agentproto.OpPower, func(a *agentproto.PowerArgs) *agentproto.Error {
		if a.Action != "reboot" && a.Action != "poweroff" {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "acción inválida")
		}
		return nil
	}, func(_ context.Context, a *agentproto.PowerArgs) (any, *agentproto.Error) {
		go func() {
			time.Sleep(time.Second) // responder primero (§12.1)
			if err := logind(a.Action); err != nil {
				log.Error("no se pudo "+a.Action, "err", err)
			}
		}()
		log.Warn("energía", "action", a.Action)
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpIdentity, func(a *agentproto.IdentityArgs) *agentproto.Error {
		if a.Hostname != "" && !hostnameRe.MatchString(a.Hostname) {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "nombre de equipo inválido")
		}
		if a.Timezone != "" {
			if !tzRe.MatchString(a.Timezone) || strings.Contains(a.Timezone, "..") {
				return agentproto.Errf(agentproto.CodeInvalidArgs, "zona horaria inválida")
			}
			if _, err := os.Stat(filepath.Join("/usr/share/zoneinfo", a.Timezone)); err != nil {
				return agentproto.Errf(agentproto.CodeInvalidArgs, "zona horaria desconocida: %s", a.Timezone)
			}
		}
		return nil
	}, func(ctx context.Context, a *agentproto.IdentityArgs) (any, *agentproto.Error) {
		conn, err := dbus.ConnectSystemBus()
		if err != nil {
			return nil, agentproto.Errf(agentproto.CodeInternal, "D-Bus: %v", err)
		}
		defer conn.Close()
		if a.Hostname != "" {
			o := conn.Object("org.freedesktop.hostname1", "/org/freedesktop/hostname1")
			for _, m := range []string{"SetStaticHostname", "SetHostname"} {
				if c := o.CallWithContext(ctx, "org.freedesktop.hostname1."+m, 0, a.Hostname, false); c.Err != nil {
					return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo cambiar el nombre: %v", c.Err)
				}
			}
		}
		if a.Timezone != "" {
			o := conn.Object("org.freedesktop.timedate1", "/org/freedesktop/timedate1")
			if c := o.CallWithContext(ctx, "org.freedesktop.timedate1.SetTimezone", 0, a.Timezone, false); c.Err != nil {
				return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo cambiar la zona horaria: %v", c.Err)
			}
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpLogsQuery, func(a *agentproto.LogsQueryArgs) *agentproto.Error {
		if _, ok := updates.Units[a.Unit]; !ok {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "servicio desconocido")
		}
		if a.Since != "" {
			if _, err := time.Parse(time.RFC3339, a.Since); err != nil {
				return agentproto.Errf(agentproto.CodeInvalidArgs, "fecha inválida")
			}
		}
		if len(a.Q) > 200 {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "búsqueda demasiado larga")
		}
		if a.Limit <= 0 || a.Limit > 2000 {
			a.Limit = 200
		}
		return nil
	}, func(ctx context.Context, a *agentproto.LogsQueryArgs) (any, *agentproto.Error) {
		n := a.Limit
		if a.Q != "" {
			n = 20000 // se filtra acá: journalctl -g necesita PCRE2 y el texto puede tener regex
		}
		args := []string{"-o", "json", "-r", "--no-pager", "-n", strconv.Itoa(n)}
		args = append(args, updates.Units[a.Unit]...)
		if a.Since != "" {
			t, _ := time.Parse(time.RFC3339, a.Since)
			args = append(args, "--since", t.Local().Format("2006-01-02 15:04:05"))
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, journalctl, args...)
		cmd.Env = []string{"LANG=C.UTF-8", "SYSTEMD_COLORS=0"}
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil && out.Len() == 0 {
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo leer el journal")
		}
		return agentproto.LogsQueryResult{Lines: updates.ParseJournal(out.Bytes(), a.Q, a.Limit)}, nil
	})

	agentproto.Register(r, agentproto.OpServicesState, func(a *agentproto.ServicesStateArgs) *agentproto.Error {
		for _, u := range a.Units {
			if !managedUnits[u] {
				return agentproto.Errf(agentproto.CodeInvalidArgs, "unidad no permitida: %s", u)
			}
		}
		return nil
	}, func(ctx context.Context, a *agentproto.ServicesStateArgs) (any, *agentproto.Error) {
		res := agentproto.ServicesStateResult{Units: []agentproto.ServiceState{}}
		conn, err := sd.NewSystemConnectionContext(ctx)
		if err != nil {
			return nil, agentproto.Errf(agentproto.CodeInternal, "systemd: %v", err)
		}
		defer conn.Close()
		for _, u := range a.Units {
			st := agentproto.ServiceState{Unit: u}
			if p, err := conn.GetUnitPropertyContext(ctx, u, "LoadState"); err == nil {
				s, _ := p.Value.Value().(string)
				st.Installed = s == "loaded"
			}
			if p, err := conn.GetUnitPropertyContext(ctx, u, "ActiveState"); err == nil {
				s, _ := p.Value.Value().(string)
				st.Active = s == "active"
			}
			if p, err := conn.GetUnitPropertyContext(ctx, u, "UnitFileState"); err == nil {
				s, _ := p.Value.Value().(string)
				st.Enabled = s == "enabled" || s == "enabled-runtime"
			}
			res.Units = append(res.Units, st)
		}
		return res, nil
	})

	agentproto.Register(r, agentproto.OpServiceSet, func(a *agentproto.ServiceSetArgs) *agentproto.Error {
		if a.Unit != "smbd.service" && a.Unit != "nmbd.service" {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "unidad no permitida: %s", a.Unit)
		}
		return nil
	}, func(ctx context.Context, a *agentproto.ServiceSetArgs) (any, *agentproto.Error) {
		if err := setUnit(ctx, a.Unit, a.Enabled); err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpUpdatesCheck, nil, func(ctx context.Context, _ *agentproto.NoArgs) (any, *agentproto.Error) {
		res := agentproto.UpdatesCheckResult{Debian: []agentproto.PackageUpdate{}, RootBtrfs: rootIsBtrfs()}
		res.TangoOSCurrent = dpkgVersion(ctx, "tangoos")
		self, _ := os.Executable()
		logp := filepath.Join(m4LogDir, "updates-check.log")
		// apt-get update necesita red: corre fuera del sandbox con un límite corto.
		uctx, cancel := context.WithTimeout(ctx, 22*time.Second)
		err := appsys.RunUnit(uctx, "tango-apt-check", []string{self, m4HelperArg, "updates-simulate"}, []string{"DEBIAN_FRONTEND=noninteractive"}, logp, nil)
		cancel()
		out, _ := os.ReadFile(logp)
		res.Refreshed = err == nil
		res.Debian = updates.ParseSimulate(string(out))
		for _, p := range res.Debian {
			if p.Name == "tangoos" {
				res.TangoOSAvailable = p.To
			}
		}
		if err != nil && len(res.Debian) == 0 && !bytes.Contains(out, []byte("Calculating upgrade")) {
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo consultar las actualizaciones (¿hay internet?)")
		}
		return res, nil
	})

	agentproto.Register(r, agentproto.OpUpdatesApply, func(a *agentproto.UpdatesApplyArgs) *agentproto.Error {
		if e := validOpID(a.OpID); e != nil {
			return e
		}
		if a.Scope != "all" && a.Scope != "security" {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "alcance inválido")
		}
		return nil
	}, func(_ context.Context, a *agentproto.UpdatesApplyArgs) (any, *agentproto.Error) {
		if e := jobs.start(a.OpID, agentproto.OpUpdatesApply, a, func(ctx context.Context, step stepFn) (any, error) {
			return applyUpdates(ctx, step, a)
		}); e != nil {
			return nil, e
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpRollbackList, nil, func(context.Context, *agentproto.NoArgs) (any, *agentproto.Error) {
		res := agentproto.RollbackListResult{Points: []agentproto.RollbackPoint{}}
		if !rootIsBtrfs() {
			res.Reason = "El sistema no está instalado sobre Btrfs (instalación desde la imagen cloud): la vuelta atrás necesita la ISO de TangoOS."
			return res, nil
		}
		if _, err := os.Stat(snapDir); err != nil {
			res.Reason = "No está montado /.snapshots."
			return res, nil
		}
		res.Supported = true
		ents, _ := os.ReadDir(snapDir)
		names := []string{}
		for _, e := range ents {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
		res.Points = updates.Points(names, readLabels())
		return res, nil
	})

	agentproto.Register(r, agentproto.OpRollbackApply, func(a *agentproto.RollbackApplyArgs) *agentproto.Error {
		if e := validOpID(a.OpID); e != nil {
			return e
		}
		if !updates.ValidSnapID(a.ID) {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "punto de vuelta atrás inválido")
		}
		if !rootIsBtrfs() {
			return agentproto.Errf(agentproto.CodeNotSupported, "el sistema no está sobre Btrfs")
		}
		if _, err := os.Stat(filepath.Join(snapDir, a.ID)); err != nil {
			return agentproto.Errf(agentproto.CodeNotFound, "no existe el punto %s", a.ID)
		}
		return nil
	}, func(_ context.Context, a *agentproto.RollbackApplyArgs) (any, *agentproto.Error) {
		if e := jobs.start(a.OpID, agentproto.OpRollbackApply, a, func(ctx context.Context, step stepFn) (any, error) {
			self, _ := os.Executable()
			step(0.2, "Preparando el sistema anterior")
			logp := filepath.Join(m4LogDir, a.OpID+".log")
			if err := appsys.RunUnit(ctx, "tango-rollback", []string{self, m4HelperArg, "rollback", a.ID}, nil, logp, nil); err != nil {
				return nil, err
			}
			step(0.95, "Reiniciando con la versión anterior")
			go func() {
				time.Sleep(2 * time.Second)
				_ = logind("reboot")
			}()
			return map[string]string{"id": a.ID}, nil
		}); e != nil {
			return nil, e
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpNFSApply, func(a *agentproto.NFSApplyArgs) *agentproto.Error {
		if e := validOpID(a.OpID); e != nil {
			return e
		}
		if _, err := services.RenderExports(a.Exports); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		return nil
	}, func(_ context.Context, a *agentproto.NFSApplyArgs) (any, *agentproto.Error) {
		if e := jobs.start(a.OpID, agentproto.OpNFSApply, map[string]any{"enabled": a.Enabled, "exports": len(a.Exports)}, func(ctx context.Context, step stepFn) (any, error) {
			return nil, runHelperJSON(ctx, step, "nfs-apply", a.OpID, a)
		}); e != nil {
			return nil, e
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpISCSIApply, func(a *agentproto.ISCSIApplyArgs) *agentproto.Error {
		if e := validOpID(a.OpID); e != nil {
			return e
		}
		for _, t := range a.Targets {
			if err := services.ValidateTarget(t); err != nil {
				return agentproto.Errf(agentproto.CodeInvalidArgs, "%s: %v", t.Name, err)
			}
		}
		for _, f := range a.Delete {
			if err := validLUNFile(f); err != nil {
				return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
			}
		}
		return nil
	}, func(_ context.Context, a *agentproto.ISCSIApplyArgs) (any, *agentproto.Error) {
		redacted := map[string]any{"enabled": a.Enabled, "targets": len(a.Targets), "delete": len(a.Delete)}
		if e := jobs.start(a.OpID, agentproto.OpISCSIApply, redacted, func(ctx context.Context, step stepFn) (any, error) {
			return nil, runHelperJSON(ctx, step, "iscsi-apply", a.OpID, a)
		}); e != nil {
			return nil, e
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpISCSIStatus, nil, func(context.Context, *agentproto.NoArgs) (any, *agentproto.Error) {
		return iscsiStatus(), nil
	})
}

// validLUNFile: solo archivos de LUN de TangoOS.
func validLUNFile(f string) error {
	clean := filepath.Clean(f)
	parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	if clean != f || len(parts) != 3 || parts[1] != "@iscsi" || !strings.HasSuffix(parts[2], ".img") {
		return fmt.Errorf("archivo de LUN inválido %q", f)
	}
	return services.ValidTargetName(strings.TrimSuffix(parts[2], ".img"))
}

// runHelperJSON escribe args en un archivo 0600 y corre el helper con su ruta.
func runHelperJSON(ctx context.Context, step stepFn, task, opID string, args any) error {
	if err := os.MkdirAll(m4StateDir, 0o700); err != nil {
		return err
	}
	p := filepath.Join(m4StateDir, opID+".json")
	b, _ := json.Marshal(args)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	defer os.Remove(p)
	self, _ := os.Executable()
	logp := filepath.Join(m4LogDir, opID+".log")
	step(0.1, "Aplicando la configuración")
	onLine := func(l string) {
		if strings.HasPrefix(l, "STAGE ") {
			parts := strings.SplitN(strings.TrimPrefix(l, "STAGE "), " ", 2)
			if len(parts) == 2 {
				if p, err := strconv.ParseFloat(parts[0], 64); err == nil {
					step(p, parts[1])
				}
			}
		}
	}
	return appsys.RunUnit(ctx, "tango-"+task, []string{self, m4HelperArg, task, p}, []string{"DEBIAN_FRONTEND=noninteractive"}, logp, onLine)
}

func applyUpdates(ctx context.Context, step stepFn, a *agentproto.UpdatesApplyArgs) (any, error) {
	self, _ := os.Executable()
	logp := filepath.Join(m4LogDir, a.OpID+".log")
	snap := ""
	if rootIsBtrfs() {
		snap = updates.SnapName(time.Now())
		step(0.05, "Creando la instantánea del sistema ("+snap+")")
	} else {
		step(0.05, "Sin instantánea: el sistema no está sobre Btrfs")
	}
	prog := 0.1
	onLine := func(l string) {
		switch {
		case strings.HasPrefix(l, "STAGE "):
			parts := strings.SplitN(strings.TrimPrefix(l, "STAGE "), " ", 2)
			if len(parts) == 2 {
				if p, err := strconv.ParseFloat(parts[0], 64); err == nil {
					prog = p
					step(p, parts[1])
				}
			}
		case strings.HasPrefix(l, "Get:"):
			prog = min(0.5, prog+0.01)
			step(prog, "Descargando "+l)
		case strings.HasPrefix(l, "Unpacking"), strings.HasPrefix(l, "Setting up"):
			prog = min(0.95, max(prog, 0.5)+0.01)
			step(prog, l)
		}
	}
	argv := []string{self, m4HelperArg, "updates-apply", a.Scope, snap}
	if err := appsys.RunUnit(ctx, "tango-apt-upgrade", argv, []string{"DEBIAN_FRONTEND=noninteractive", "APT_LISTCHANGES_FRONTEND=none", "NEEDRESTART_MODE=a"}, logp, onLine); err != nil {
		return nil, fmt.Errorf("la actualización falló: %w", err)
	}
	return map[string]string{"snapshot": snap}, nil
}

// ---- Terminal ------------------------------------------------------------------------

// termHook atiende term.spawn: abre un PTY, lanza la shell como unidad transitoria con
// TTYPath (fuera del sandbox: es una consola de administración real, solo para admins
// de TangoOS y auditada por tangod) y entrega el master por SCM_RIGHTS.
func termHook(ctx context.Context, c *net.UnixConn, req *agentproto.Request, log *slog.Logger) bool {
	if req.Op != agentproto.OpTermSpawn {
		return false
	}
	resp := agentproto.Response{V: agentproto.Version, ID: req.ID}
	fail := func(e *agentproto.Error) bool {
		resp.Error = e
		b, _ := json.Marshal(resp)
		_, _ = c.Write(append(b, '\n'))
		return true
	}
	var a agentproto.TermSpawnArgs
	if err := agentproto.DecodeStrict(req.Args, &a); err != nil {
		return fail(agentproto.Errf(agentproto.CodeInvalidArgs, "argumentos inválidos: %v", err))
	}
	if a.Cols < 10 || a.Cols > 500 || a.Rows < 3 || a.Rows > 300 {
		return fail(agentproto.Errf(agentproto.CodeInvalidArgs, "tamaño de terminal inválido"))
	}
	master, unit, err := spawnShell(ctx, a.Cols, a.Rows, a.User)
	if err != nil {
		return fail(agentproto.Errf(agentproto.CodeFailed, "no se pudo abrir la consola: %v", err))
	}
	defer master.Close()
	res, _ := json.Marshal(agentproto.TermSpawnResult{})
	resp.OK, resp.Result = true, res
	out, _ := json.Marshal(resp)
	if _, _, err := c.WriteMsgUnix(append(out, '\n'), unix.UnixRights(int(master.Fd())), nil); err != nil {
		log.Warn("term.spawn: no se pudo entregar el fd", "err", err)
	}
	log.Warn("consola de administración abierta", "user", a.User, "unit", unit)
	return true
}

// spawnShell lanza `tango-agent m4-helper term-shell` como unidad transitoria: el helper
// crea el PTY en el devpts del host (el agente tiene el suyo propio por el sandbox),
// arranca la shell y devuelve el master por un socket Unix en /run/tango.
func spawnShell(ctx context.Context, cols, rows int, user string) (*os.File, string, error) {
	var rb [6]byte
	_, _ = rand.Read(rb[:])
	id := hex.EncodeToString(rb[:])
	sock := "/run/tango/term-" + id + ".sock"
	_ = os.MkdirAll("/run/tango", 0o755)
	old := syscall.Umask(0o077)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	syscall.Umask(old)
	if err != nil {
		return nil, "", err
	}
	defer os.Remove(sock)
	defer ln.Close()
	self, _ := os.Executable()
	conn, err := sd.NewSystemConnectionContext(ctx)
	if err != nil {
		return nil, "", err
	}
	defer conn.Close()
	unit := "tango-term-" + id + ".service"
	props := []sd.Property{
		sd.PropDescription("TangoOS: consola de administración (" + user + ")"),
		sd.PropExecStart([]string{self, m4HelperArg, "term-shell", sock, strconv.Itoa(cols), strconv.Itoa(rows), user}, false),
		sd.PropType("simple"),
		{Name: "CollectMode", Value: dbus.MakeVariant("inactive-or-failed")},
		{Name: "KillMode", Value: dbus.MakeVariant("control-group")},
	}
	if _, err := conn.StartTransientUnitContext(ctx, unit, "fail", props, nil); err != nil {
		return nil, "", err
	}
	_ = ln.SetDeadline(time.Now().Add(10 * time.Second))
	c, err := ln.AcceptUnix()
	if err != nil {
		_, _ = conn.StopUnitContext(context.Background(), unit, "replace", nil)
		return nil, "", fmt.Errorf("la shell no respondió: %w", err)
	}
	defer c.Close()
	buf := make([]byte, 16)
	oob := make([]byte, unix.CmsgSpace(4))
	_, oobn, _, _, err := c.ReadMsgUnix(buf, oob)
	if err != nil || oobn == 0 {
		return nil, "", errors.New("la shell no entregó la terminal")
	}
	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(msgs) == 0 {
		return nil, "", errors.New("mensaje de control inválido")
	}
	fds, err := unix.ParseUnixRights(&msgs[0])
	if err != nil || len(fds) == 0 {
		return nil, "", errors.New("sin descriptor")
	}
	for _, extra := range fds[1:] {
		unix.Close(extra)
	}
	return os.NewFile(uintptr(fds[0]), "ptmx"), unit, nil
}

// ---- systemd / logind -----------------------------------------------------------------

func logind(action string) error {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	m := map[string]string{"reboot": "Reboot", "poweroff": "PowerOff"}[action]
	return conn.Object("org.freedesktop.login1", "/org/freedesktop/login1").Call("org.freedesktop.login1.Manager."+m, 0, false).Err
}

func setUnit(ctx context.Context, unit string, enabled bool) error {
	conn, err := sd.NewSystemConnectionContext(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	ch := make(chan string, 1)
	if enabled {
		if _, _, err := conn.EnableUnitFilesContext(ctx, []string{unit}, false, true); err != nil {
			return err
		}
		if _, err := conn.StartUnitContext(ctx, unit, "replace", ch); err != nil {
			return err
		}
	} else {
		if _, err := conn.DisableUnitFilesContext(ctx, []string{unit}, false); err != nil {
			return err
		}
		if _, err := conn.StopUnitContext(ctx, unit, "replace", ch); err != nil {
			return err
		}
	}
	select {
	case r := <-ch:
		if r != "done" {
			return fmt.Errorf("%s: %s", unit, r)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// rootIsBtrfs dice si / está sobre Btrfs (instalación desde la ISO de TangoOS).
func rootIsBtrfs() bool {
	var st unix.Statfs_t
	if err := unix.Statfs("/", &st); err != nil {
		return false
	}
	return st.Type == unix.BTRFS_SUPER_MAGIC
}

func dpkgVersion(ctx context.Context, pkg string) string {
	b, err := os.ReadFile("/var/lib/dpkg/status")
	if err != nil {
		return ""
	}
	_ = ctx
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	in := false
	for sc.Scan() {
		l := sc.Text()
		switch {
		case l == "Package: "+pkg:
			in = true
		case strings.HasPrefix(l, "Package: "):
			in = false
		case in && strings.HasPrefix(l, "Version: "):
			return strings.TrimPrefix(l, "Version: ")
		}
	}
	return ""
}

const labelsFile = "/.snapshots/.tango-labels.json"

var labelsMu sync.Mutex

func readLabels() map[string]string {
	labelsMu.Lock()
	defer labelsMu.Unlock()
	m := map[string]string{}
	if b, err := os.ReadFile(labelsFile); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// iscsiStatus cuenta sesiones por IQN leyendo configfs (sin cargar módulos).
func iscsiStatus() agentproto.ISCSIStatusResult {
	res := agentproto.ISCSIStatusResult{Sessions: map[string]int{}}
	base := filepath.Join(services.ConfigFS, "iscsi")
	ents, err := os.ReadDir(base)
	if err != nil {
		return res
	}
	res.Loaded = true
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), services.IQNPrefix+":") {
			continue
		}
		n := 0
		// Sesiones dinámicas (generate_node_acls): dynamic_sessions lista un iniciador por línea.
		if b, err := os.ReadFile(filepath.Join(base, e.Name(), "tpgt_1", "dynamic_sessions")); err == nil {
			for _, l := range strings.Split(strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", "")), "\n") {
				if strings.TrimSpace(l) != "" {
					n++
				}
			}
		}
		res.Sessions[e.Name()] = n
	}
	return res
}

// errNotRoot por si el helper se lanza sin privilegios.
var errNotRoot = errors.New("el helper necesita root")

func requireRoot() error {
	if syscall.Geteuid() != 0 {
		return errNotRoot
	}
	return nil
}
