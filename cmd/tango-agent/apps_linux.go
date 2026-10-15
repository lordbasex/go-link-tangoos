//go:build linux

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/appsys"
	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	appcatalog "github.com/lordbasex/go-link-tangoos/system/apps"
)

// Centro de apps (protocol/README.md §9, ADR 0010). El agente solo instala ids del
// catálogo embebido, con opciones validadas contra él; docker compose y apt-get
// corren como unidades transitorias de systemd (fuera de este sandbox) y el estado
// sale de la Docker Engine API por su socket.

const (
	appLogDir      = "/var/lib/tango-agent/apps-logs"
	daemonJSONPath = "/etc/docker/daemon.json"
	helperArg      = "apps-helper"
)

// daemonJSON: remapeo de usuarios (el root del contenedor no es root del NAS), logs al
// journal y live-restore (actualizar Docker no baja las apps).
const daemonJSON = `{
  "userns-remap": "default",
  "log-driver": "journald",
  "live-restore": true
}
`

// init atiende `tango-agent apps-helper <tarea>`: lo invoca systemd como unidad
// transitoria, fuera del sandbox del agente (que no puede escribir /etc/docker).
func init() {
	if len(os.Args) < 3 || os.Args[1] != helperArg {
		return
	}
	switch os.Args[2] {
	case "write-daemon-json":
		if err := writeDaemonJSON(); err != nil {
			fmt.Fprintln(os.Stderr, "daemon.json:", err)
			os.Exit(1)
		}
		fmt.Println("daemon.json listo")
		os.Exit(0)
	default:
		fmt.Fprintln(os.Stderr, "tarea desconocida")
		os.Exit(2)
	}
}

// writeDaemonJSON escribe /etc/docker/daemon.json si no existe (no pisa uno del admin).
func writeDaemonJSON() error {
	if err := os.MkdirAll(filepath.Dir(daemonJSONPath), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(daemonJSONPath); err == nil {
		return nil
	}
	tmp := daemonJSONPath + ".tango-tmp"
	if err := os.WriteFile(tmp, []byte(daemonJSON), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, daemonJSONPath)
}

func registerApps(r *agentproto.Registry, jobs *asyncJobs, log *slog.Logger) {
	dock := &appsys.Docker{}

	agentproto.Register(r, agentproto.OpDockerStatus, nil, func(ctx context.Context, _ *agentproto.NoArgs) (any, *agentproto.Error) {
		return dockerStatus(ctx, dock), nil
	})

	agentproto.Register(r, agentproto.OpDockerInstall, func(a *agentproto.DockerInstallArgs) *agentproto.Error {
		return validOpID(a.OpID)
	}, func(_ context.Context, a *agentproto.DockerInstallArgs) (any, *agentproto.Error) {
		if e := jobs.start(a.OpID, agentproto.OpDockerInstall, a, func(ctx context.Context, step stepFn) (any, error) {
			return installDocker(ctx, step, dock, a.OpID)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpAppUp, func(a *agentproto.AppUpArgs) *agentproto.Error {
		if e := validOpID(a.OpID); e != nil {
			return e
		}
		return validApp(a.ID, a.Mount)
	}, func(ctx context.Context, a *agentproto.AppUpArgs) (any, *agentproto.Error) {
		app, _ := appcatalog.Get(a.ID)
		dir := appDir(a.Mount, a.ID)
		env, err := appcatalog.RenderEnv(app, appcatalog.Context{Data: filepath.Join(dir, "data"), Port: a.Port, TZ: a.TZ, Host: a.Host}, a.Options)
		if err != nil {
			return nil, agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		if _, err := appsys.ComposeBin(); err != nil {
			return nil, agentproto.Errf(agentproto.CodeDockerMissing, "Docker no está instalado")
		}
		if e := mountedVolume(a.Mount); e != nil {
			return nil, e
		}
		if e := jobs.start(a.OpID, agentproto.OpAppUp, redacted(a), func(ctx context.Context, step stepFn) (any, error) {
			return appUp(ctx, step, a, dir, env)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpAppAction, func(a *agentproto.AppActionArgs) *agentproto.Error {
		if e := validOpID(a.OpID); e != nil {
			return e
		}
		switch a.Action {
		case "start", "stop", "restart":
		default:
			return agentproto.Errf(agentproto.CodeInvalidArgs, "acción inválida")
		}
		return validApp(a.ID, a.Mount)
	}, func(ctx context.Context, a *agentproto.AppActionArgs) (any, *agentproto.Error) {
		dir := appDir(a.Mount, a.ID)
		if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err != nil {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "%s no está instalada en %s", a.ID, a.Mount)
		}
		if e := jobs.start(a.OpID, agentproto.OpAppAction, a, func(ctx context.Context, step stepFn) (any, error) {
			sub := map[string][]string{"start": {"up", "-d", "--remove-orphans"}, "stop": {"stop"}, "restart": {"restart"}}[a.Action]
			step(0.2, map[string]string{"start": "Iniciando", "stop": "Deteniendo", "restart": "Reiniciando"}[a.Action])
			return nil, compose(ctx, a.ID, dir, a.OpID, step, 0.2, 0.95, sub...)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpAppUninstall, func(a *agentproto.AppUninstallArgs) *agentproto.Error {
		if e := validOpID(a.OpID); e != nil {
			return e
		}
		return validApp(a.ID, a.Mount)
	}, func(ctx context.Context, a *agentproto.AppUninstallArgs) (any, *agentproto.Error) {
		if e := jobs.start(a.OpID, agentproto.OpAppUninstall, a, func(ctx context.Context, step stepFn) (any, error) {
			return nil, appUninstall(ctx, step, a)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpAppLogs, func(a *agentproto.AppLogsArgs) *agentproto.Error {
		if !appcatalog.ValidID(a.ID) {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "app desconocida")
		}
		if a.Tail <= 0 || a.Tail > 2000 {
			a.Tail = 200
		}
		return nil
	}, func(ctx context.Context, a *agentproto.AppLogsArgs) (any, *agentproto.Error) {
		cs, err := dock.Containers(ctx, a.ID)
		if err != nil {
			return nil, dockerErr(err)
		}
		per := map[string][]appsys.LogLine{}
		for _, c := range cs {
			lines, err := dock.Logs(ctx, c.ID, a.Tail)
			if err != nil {
				log.Warn("logs de contenedor", "app", a.ID, "err", err)
				continue
			}
			per[c.Service()] = append(per[c.Service()], lines...)
		}
		return agentproto.AppLogsResult{Lines: nonNilStrings(appsys.MergeLogs(per, a.Tail, len(per) > 1))}, nil
	})

	agentproto.Register(r, agentproto.OpAppStats, func(a *agentproto.AppStatsArgs) *agentproto.Error {
		for _, id := range a.IDs {
			if !appcatalog.ValidID(id) {
				return agentproto.Errf(agentproto.CodeInvalidArgs, "app desconocida: %s", id)
			}
		}
		return nil
	}, func(ctx context.Context, a *agentproto.AppStatsArgs) (any, *agentproto.Error) {
		return appStats(ctx, dock, a.IDs)
	})

	agentproto.Register(r, agentproto.OpAppJob, func(a *agentproto.AppJobArgs) *agentproto.Error {
		return validOpID(a.OpID)
	}, func(_ context.Context, a *agentproto.AppJobArgs) (any, *agentproto.Error) {
		return jobs.status(a.OpID)
	})
}

func validOpID(id string) *agentproto.Error {
	if !opIDRe.MatchString(id) {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "op_id inválido")
	}
	return nil
}

func validApp(id, mount string) *agentproto.Error {
	if !appcatalog.ValidID(id) {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "app desconocida: %s", id)
	}
	if err := diskops.ValidMount(mount); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	return nil
}

func appDir(mount, id string) string { return filepath.Join(mount, "@apps", id) }

// mountedVolume exige que mount sea un volumen montado (distinto dispositivo que "/").
func mountedVolume(mount string) *agentproto.Error {
	var st, root syscall.Stat_t
	if err := syscall.Stat(mount, &st); err != nil {
		return agentproto.Errf(agentproto.CodeNotFound, "el volumen %s no existe", mount)
	}
	if err := syscall.Stat("/", &root); err != nil || st.Dev == root.Dev {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%s no es un volumen montado", mount)
	}
	return nil
}

// redacted es lo que queda en el journal: sin las opciones (pueden ser contraseñas).
func redacted(a *agentproto.AppUpArgs) map[string]any {
	keys := make([]string, 0, len(a.Options))
	for k := range a.Options {
		keys = append(keys, k)
	}
	return map[string]any{"op_id": a.OpID, "id": a.ID, "mount": a.Mount, "port": a.Port, "pull": a.Pull, "option_keys": keys}
}

func dockerErr(err error) *agentproto.Error {
	if errors.Is(err, appsys.ErrNoDaemon) {
		return agentproto.Errf(agentproto.CodeDockerMissing, "Docker no está corriendo")
	}
	return agentproto.Errf(agentproto.CodeFailed, "%v", err)
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- Docker -----------------------------------------------------------------------

func dockerStatus(ctx context.Context, dock *appsys.Docker) agentproto.DockerStatus {
	st := agentproto.DockerStatus{}
	if _, err := os.Stat(appsys.Dockerd); err == nil {
		st.Installed = true
	}
	if _, err := appsys.ComposeBin(); err == nil {
		st.Compose = true
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if v, err := dock.Version(cctx); err == nil {
		st.Running, st.Version = true, &v
	}
	return st
}

func installDocker(ctx context.Context, step stepFn, dock *appsys.Docker, opID string) (any, error) {
	logp := filepath.Join(appLogDir, opID+".log")
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	step(0.03, "Preparando la configuración de Docker")
	if err := appsys.RunUnit(ctx, "tango-docker-cfg", []string{self, helperArg, "write-daemon-json"}, nil, logp, nil); err != nil {
		return nil, fmt.Errorf("no se pudo escribir daemon.json: %w", err)
	}
	apt := []string{"DEBIAN_FRONTEND=noninteractive", "APT_LISTCHANGES_FRONTEND=none"}
	step(0.08, "Actualizando la lista de paquetes")
	if err := appsys.RunUnit(ctx, "tango-apt-update", []string{appsys.AptGet, "-q", "update"}, apt, logp, nil); err != nil {
		return nil, fmt.Errorf("apt-get update falló: %w", err)
	}
	// Progreso aproximado por las etapas de apt (Get → Unpacking → Setting up).
	prog := 0.15
	onLine := func(l string) {
		switch {
		case strings.HasPrefix(l, "Get:"):
			prog = min(0.55, prog+0.02)
			step(prog, "Descargando paquetes")
		case strings.HasPrefix(l, "Unpacking"):
			prog = min(0.75, max(prog, 0.55)+0.02)
			step(prog, "Desempaquetando")
		case strings.HasPrefix(l, "Setting up"):
			prog = min(0.92, max(prog, 0.75)+0.02)
			step(prog, "Configurando "+strings.Fields(l)[2])
		}
	}
	step(prog, "Instalando docker.io y docker-compose")
	argv := []string{appsys.AptGet, "-q", "-y", "--no-install-recommends", "-o", "Dpkg::Options::=--force-confold", "install", "docker.io", "docker-compose"}
	if err := appsys.RunUnit(ctx, "tango-apt-install", argv, apt, logp, onLine); err != nil {
		return nil, fmt.Errorf("apt-get install falló: %w", err)
	}
	step(0.95, "Iniciando Docker")
	if err := appsys.EnableAndStart(ctx, "docker.service"); err != nil {
		return nil, fmt.Errorf("no arrancó docker.service: %w", err)
	}
	for range 30 {
		if st := dockerStatus(ctx, dock); st.Running {
			return st, nil
		}
		time.Sleep(time.Second)
	}
	return nil, errors.New("Docker se instaló pero no responde")
}

// ---- Apps ---------------------------------------------------------------------------

// compose corre docker compose para la app y traduce su salida en etapas.
func compose(ctx context.Context, id, dir, opID string, step stepFn, from, to float64, sub ...string) error {
	base, err := appsys.ComposeBin()
	if err != nil {
		return err
	}
	argv, err := appsys.ComposeArgs(base, id, dir, sub...)
	if err != nil {
		return err
	}
	prog := from
	var pull appsys.PullProgress
	isPull := len(sub) > 0 && sub[0] == "pull"
	onLine := func(l string) {
		if isPull {
			prog = from + (to-from)*pull.Line(l)
		} else {
			prog = min(to, prog+(to-prog)*0.08)
		}
		step(prog, composeStage(l))
	}
	return appsys.RunUnit(ctx, "tango-app-"+id, argv, nil, filepath.Join(appLogDir, opID+".log"), onLine)
}

func composeStage(l string) string {
	l = strings.TrimSpace(l)
	if len(l) > 90 {
		l = l[:90] + "…"
	}
	return l
}

func appUp(ctx context.Context, step stepFn, a *agentproto.AppUpArgs, dir, env string) (any, error) {
	step(0.02, "Preparando la carpeta de la app")
	if err := ensureAppDir(a.Mount, a.ID); err != nil {
		return nil, err
	}
	compYAML, err := appcatalog.Compose(a.ID)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(filepath.Join(dir, "compose.yaml"), compYAML, 0o644); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		return nil, err
	}
	app, _ := appcatalog.Get(a.ID)
	if err := prepareData(filepath.Join(dir, "data"), app); err != nil {
		return nil, err
	}
	if a.Pull {
		step(0.05, "Descargando imágenes")
		if err := compose(ctx, a.ID, dir, a.OpID, step, 0.05, 0.8, "pull"); err != nil {
			return nil, fmt.Errorf("no se pudieron bajar las imágenes: %w", err)
		}
	}
	step(0.82, "Iniciando contenedores")
	if err := compose(ctx, a.ID, dir, a.OpID, step, 0.82, 0.98, "up", "-d", "--remove-orphans"); err != nil {
		return nil, fmt.Errorf("no se pudo iniciar la app: %w", err)
	}
	return map[string]string{"dir": dir}, nil
}

// ensureAppDir crea <mount>/@apps (si falta) y el subvolumen @apps/<id>: los datos de
// cada app entran en las instantáneas y se pueden borrar sin tocar a las demás.
func ensureAppDir(mount, id string) error {
	apps := filepath.Join(mount, "@apps")
	if _, err := os.Stat(apps); errors.Is(err, os.ErrNotExist) {
		if err := diskops.CreateSubvolume(mount, "@apps"); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("no se pudo crear @apps: %w", err)
		}
	}
	if err := diskops.CreateSubvolume(apps, id); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("no se pudo crear el subvolumen de %s: %w", id, err)
	}
	return os.Chmod(filepath.Join(apps, id), 0o711)
}

// prepareData crea el directorio de datos. Con userns-remap, el root del contenedor es
// un uid alto del host (dockremap): el directorio pasa a ese dueño para que la app pueda
// escribir. Las apps que salen del remapeo (host network) quedan con root.
func prepareData(data string, app appcatalog.App) error {
	if err := os.MkdirAll(data, 0o750); err != nil {
		return err
	}
	if app.RequiresHostNetwork {
		return os.Chown(data, 0, 0)
	}
	uid, gid, ok := remapRoot()
	if !ok {
		return nil
	}
	return os.Chown(data, uid, gid)
}

// remapRoot lee el primer uid/gid subordinado de dockremap (/etc/subuid, /etc/subgid).
func remapRoot() (int, int, bool) {
	b, err := os.ReadFile(daemonJSONPath)
	if err != nil || !strings.Contains(string(b), `"userns-remap"`) {
		return 0, 0, false
	}
	uid, ok1 := firstSub("/etc/subuid", "dockremap")
	gid, ok2 := firstSub("/etc/subgid", "dockremap")
	return uid, gid, ok1 && ok2
}

func firstSub(path, user string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), ":")
		if len(p) == 3 && p[0] == user {
			n, err := strconv.Atoi(p[1])
			return n, err == nil && n > 0
		}
	}
	return 0, false
}

func appUninstall(ctx context.Context, step stepFn, a *agentproto.AppUninstallArgs) error {
	dir := appDir(a.Mount, a.ID)
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err == nil {
		sub := []string{"down", "--remove-orphans"}
		if !a.KeepData {
			sub = append(sub, "--volumes", "--rmi", "all")
		}
		step(0.1, "Deteniendo y quitando contenedores")
		if err := compose(ctx, a.ID, dir, a.OpID, step, 0.1, 0.8, sub...); err != nil {
			return fmt.Errorf("docker compose down falló: %w", err)
		}
	}
	if a.KeepData {
		return nil
	}
	step(0.85, "Borrando los datos de la app")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := diskops.DestroySubvolume(filepath.Join(a.Mount, "@apps"), a.ID); err != nil {
		return fmt.Errorf("no se pudieron borrar los datos: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tango-tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func appStats(ctx context.Context, dock *appsys.Docker, ids []string) (any, *agentproto.Error) {
	cs, err := dock.Containers(ctx, "")
	if err != nil {
		return nil, dockerErr(err)
	}
	by := map[string][]appsys.Container{}
	for _, c := range cs {
		by[c.Project()] = append(by[c.Project()], c)
	}
	want := ids
	if len(want) == 0 {
		for id := range by {
			want = append(want, id)
		}
	}
	type res struct {
		i   int
		cpu float64
		mem uint64
		ok  bool
	}
	out := make([]agentproto.AppStat, len(want))
	ch := make(chan res)
	n := 0
	for i, id := range want {
		st, running, unhealthy := appsys.Health(by[id])
		out[i] = agentproto.AppStat{ID: id, State: st, Containers: len(by[id]), Running: running, Unhealthy: unhealthy}
		for _, c := range by[id] {
			if c.State != "running" {
				continue
			}
			n++
			go func(i int, cid string) {
				sctx, cancel := context.WithTimeout(ctx, 4*time.Second)
				defer cancel()
				u, err := dock.Stats(sctx, cid)
				ch <- res{i, u.CPU, u.Mem, err == nil}
			}(i, c.ID)
		}
	}
	for range n {
		r := <-ch
		if !r.ok {
			continue
		}
		s := &out[r.i]
		cpu, mem := r.cpu, r.mem
		if s.CPU != nil {
			cpu += *s.CPU
		}
		if s.Mem != nil {
			mem += *s.Mem
		}
		s.CPU, s.Mem = &cpu, &mem
	}
	return agentproto.AppStatsResult{Apps: out}, nil
}
