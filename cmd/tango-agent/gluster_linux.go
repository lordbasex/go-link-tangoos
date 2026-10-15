//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	sd "github.com/coreos/go-systemd/v22/dbus"

	"github.com/lordbasex/go-link-tangoos/internal/appsys"
	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Clúster GlusterFS (protocol/README.md §17, ADR 0012). El CLI habla con glusterd por
// su socket Unix (/run/glusterd.socket), así que funciona dentro del sandbox del agente
// (PrivateNetwork); lo que necesita el host (apt, nftables) va en una unidad
// transitoria, como el resto de M4.

const glusterTimeout = 25 * time.Second

// glusterReadTimeout acota las lecturas de estado: con un nodo caído que glusterd
// todavía no detectó, `volume status` espera al nodo hasta el timeout del CLI (120 s).
const glusterReadTimeout = 8

// glusterdPingTimeout es el ping-timeout entre glusterd (Debian trae 0 = nunca): con
// 0, un nodo que se apaga de golpe sigue «Connected» y cada operación lo espera.
const glusterdPingTimeout = "10"

func glusterInstalled() bool {
	return fileExists(gluster.Binary) && fileExists("/usr/sbin/glusterd")
}

// runGluster corre `gluster --mode=script --xml <args>` y devuelve la salida y el
// código de salida. args sale siempre de internal/gluster (validado).
func runGluster(ctx context.Context, args ...string) ([]byte, int, error) {
	return runGlusterT(ctx, 0, args...)
}

// runGlusterT es runGluster con el timeout propio del CLI (segundos; 0 = el de siempre).
func runGlusterT(ctx context.Context, timeout int, args ...string) ([]byte, int, error) {
	if !glusterInstalled() {
		return nil, -1, errGlusterMissing
	}
	ctx, cancel := context.WithTimeout(ctx, glusterTimeout)
	defer cancel()
	pre := []string{"--mode=script", "--xml"}
	if timeout > 0 {
		pre = append(pre, "--timeout="+strconv.Itoa(timeout))
	}
	cmd := exec.CommandContext(ctx, gluster.Binary, append(pre, args...)...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		code = ee.ExitCode()
	case err != nil:
		return nil, -1, err
	}
	if out.Len() == 0 && stderr.Len() > 0 {
		return stderr.Bytes(), code, nil
	}
	return out.Bytes(), code, nil
}

var errGlusterMissing = errors.New("GlusterFS no está instalado")

func validateGlusterInstall(a *agentproto.GlusterInstallArgs) *agentproto.Error {
	if e := validOpID(a.OpID); e != nil {
		return e
	}
	return validGlusterAllowed(a.Allowed)
}

func glusterAgentErr(err error) *agentproto.Error {
	var ge *gluster.Error
	switch {
	case errors.Is(err, errGlusterMissing):
		return agentproto.Errf(agentproto.CodeNotInstalled, "GlusterFS no está instalado")
	case errors.As(err, &ge):
		return agentproto.Errf(agentproto.CodeFailed, "GlusterFS: %s", ge.Msg)
	case errors.Is(err, context.DeadlineExceeded):
		return agentproto.Errf(agentproto.CodeFailed, "GlusterFS no respondió a tiempo")
	}
	return agentproto.Errf(agentproto.CodeFailed, "%v", err)
}

// glusterDo corre una operación sin datos y traduce el error.
func glusterDo(ctx context.Context, args []string) error {
	out, code, err := runGluster(ctx, args...)
	if err != nil {
		return err
	}
	return gluster.ParseResult(out, code)
}

func registerGluster(r *agentproto.Registry, jobs *asyncJobs, log *slog.Logger) {
	agentproto.Register(r, agentproto.OpGlusterInstall, validateGlusterInstall, func(_ context.Context, a *agentproto.GlusterInstallArgs) (any, *agentproto.Error) {
		if e := jobs.start(a.OpID, agentproto.OpGlusterInstall, map[string]any{"allowed": len(a.Allowed)}, func(ctx context.Context, step stepFn) (any, error) {
			return nil, runHelperJSON(ctx, step, "gluster-install", a.OpID, a)
		}); e != nil {
			return nil, e
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpGlusterFirewall, func(a *agentproto.GlusterFirewallArgs) *agentproto.Error {
		return validGlusterAllowed(a.Allowed)
	}, func(ctx context.Context, a *agentproto.GlusterFirewallArgs) (any, *agentproto.Error) {
		if err := writeGlusterFirewall(a.Allowed); err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
		}
		if fileExists(filepath.Join("/etc/systemd/system", gluster.FirewallUnitName)) {
			if err := reloadUnit(ctx, gluster.FirewallUnitName); err != nil {
				log.Warn("gluster firewall", "err", err)
				return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo aplicar el firewall: %v", err)
			}
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpGlusterStatus, func(a *agentproto.GlusterStatusArgs) *agentproto.Error {
		if a.Self != "" && gluster.ValidHost(a.Self) != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "nombre de nodo inválido")
		}
		return nil
	}, func(ctx context.Context, a *agentproto.GlusterStatusArgs) (any, *agentproto.Error) {
		return glusterStatus(ctx, a.Self), nil
	})

	agentproto.Register(r, agentproto.OpGlusterPeer, validateGlusterPeer, func(ctx context.Context, a *agentproto.GlusterPeerArgs) (any, *agentproto.Error) {
		args := gluster.ArgsPeerProbe(a.Host)
		if a.Action == "detach" {
			args = gluster.ArgsPeerDetach(a.Host)
		}
		if err := glusterDo(ctx, args); err != nil {
			return nil, glusterAgentErr(err)
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpGlusterVolumeCreate, validateGlusterCreate, func(ctx context.Context, a *agentproto.GlusterVolumeCreateArgs) (any, *agentproto.Error) {
		if err := glusterDo(ctx, gluster.ArgsVolumeCreate(a.Name, a.Layout, a.Bricks)); err != nil {
			return nil, glusterAgentErr(err)
		}
		// Opciones antes de arrancar; si alguna falla, el volumen recién creado (vacío,
		// sin arrancar) se borra para no dejar uno a medias.
		for _, k := range sortedKeys(a.Options) {
			if err := glusterDo(ctx, gluster.ArgsSet(a.Name, k, a.Options[k])); err != nil {
				_ = glusterDo(context.WithoutCancel(ctx), []string{"volume", "delete", a.Name})
				return nil, glusterAgentErr(fmt.Errorf("%s: %w", k, err))
			}
		}
		if a.Start {
			if err := glusterDo(ctx, []string{"volume", "start", a.Name}); err != nil {
				return nil, glusterAgentErr(err)
			}
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpGlusterVolumeAction, validateGlusterAction, func(ctx context.Context, a *agentproto.GlusterVolumeActionArgs) (any, *agentproto.Error) {
		if a.Action != "start" && isMountedAt(gluster.MountPath(a.Name)) {
			return nil, agentproto.Errf(agentproto.CodeBusy, "el volumen está montado en este nodo: desmontalo primero")
		}
		args, _ := gluster.ArgsVolumeAction(a.Name, a.Action)
		if err := glusterDo(ctx, args); err != nil {
			return nil, glusterAgentErr(err)
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpGlusterVolumeSet, validateGlusterOptions, func(ctx context.Context, a *agentproto.GlusterVolumeOptionsArgs) (any, *agentproto.Error) {
		for _, k := range sortedKeys(a.Set) {
			if err := glusterDo(ctx, gluster.ArgsSet(a.Name, k, a.Set[k])); err != nil {
				return nil, glusterAgentErr(fmt.Errorf("%s: %w", k, err))
			}
		}
		for _, k := range a.Reset {
			if err := glusterDo(ctx, gluster.ArgsReset(a.Name, k)); err != nil {
				return nil, glusterAgentErr(fmt.Errorf("%s: %w", k, err))
			}
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpGlusterHeal, validateGlusterHeal, func(ctx context.Context, a *agentproto.GlusterHealArgs) (any, *agentproto.Error) {
		if err := glusterDo(ctx, gluster.ArgsHeal(a.Name, a.Full)); err != nil {
			return nil, glusterAgentErr(err)
		}
		return struct{}{}, nil
	})

	agentproto.Register(r, agentproto.OpGlusterBricks, validateGlusterBricks, func(ctx context.Context, a *agentproto.GlusterBricksArgs) (any, *agentproto.Error) {
		var args []string
		switch a.Op {
		case "add":
			args = gluster.ArgsAddBrick(a.Name, a.Replica, a.Bricks)
		case "remove":
			args, _ = gluster.ArgsRemoveBrick(a.Name, a.Replica, a.Bricks, a.Phase)
			if a.Phase == "status" {
				out, _, err := runGluster(ctx, args...)
				if err != nil {
					return nil, glusterAgentErr(err)
				}
				t, err := gluster.ParseTask(out, hostnameOr(""))
				if err != nil {
					return nil, glusterAgentErr(err)
				}
				return agentproto.GlusterTaskResult{Task: t}, nil
			}
		case "replace":
			args = gluster.ArgsReplaceBrick(a.Name, a.From, a.To)
		}
		if err := glusterDo(ctx, args); err != nil {
			return nil, glusterAgentErr(err)
		}
		return agentproto.GlusterTaskResult{}, nil
	})

	agentproto.Register(r, agentproto.OpGlusterMount, validateGlusterMount, func(ctx context.Context, a *agentproto.GlusterMountArgs) (any, *agentproto.Error) {
		return glusterMount(ctx, a, log)
	})

	agentproto.Register(r, agentproto.OpGlusterHealInfo, func(a *agentproto.GlusterHealInfoArgs) *agentproto.Error {
		if err := gluster.ValidVolumeName(a.Name); err != nil {
			return badArgs(err)
		}
		return nil
	}, func(ctx context.Context, a *agentproto.GlusterHealInfoArgs) (any, *agentproto.Error) {
		heal, err := glusterHealInfo(ctx, a.Name)
		if err != nil {
			return nil, glusterAgentErr(err)
		}
		return heal, nil
	})

	agentproto.Register(r, agentproto.OpGlusterBrickClean, validateGlusterBrickClean, func(ctx context.Context, a *agentproto.GlusterBrickCleanArgs) (any, *agentproto.Error) {
		out, code, err := runGluster(ctx, "volume", "info", a.Name)
		if err == nil {
			err = gluster.ParseResult(out, code)
			if err == nil {
				return nil, agentproto.Errf(agentproto.CodeBusy, "el volumen %s todavía existe en el clúster", a.Name)
			}
			if !gluster.IsNotFound(err) {
				return nil, glusterAgentErr(err)
			}
		} else if !errors.Is(err, errGlusterMissing) {
			return nil, glusterAgentErr(err)
		}
		dir := gluster.BrickPath(a.TangoVolume, a.Name)
		if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe %s", dir)
		}
		// GlusterFS deja en .glusterfs archivos sin permisos (modo 000) y el agente no tiene
		// CAP_DAC_OVERRIDE: el borrado va en una unidad transitoria (root completo), que
		// vuelve a validar la ruta.
		self, _ := os.Executable()
		logp := filepath.Join(m4LogDir, "gluster-clean-"+a.Name+".log")
		if err := appsys.RunUnit(ctx, "tango-gluster-clean", []string{self, m4HelperArg, "gluster-clean", dir}, nil, logp, nil); err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo borrar %s: %v", dir, err)
		}
		return struct{}{}, nil
	})

	registerGluster2(r, log)
}

// glusterHealInfo corre `gluster volume heal <vol> info summary` en una unidad
// transitoria (red del host): glfsheal habla TCP con glusterd y con los bricks.
func glusterHealInfo(ctx context.Context, vol string) ([]gluster.HealBrick, error) {
	if !glusterInstalled() {
		return nil, errGlusterMissing
	}
	var rb [4]byte
	_, _ = rand.Read(rb[:])
	out := filepath.Join(m4LogDir, "gluster-heal-"+vol+"-"+hex.EncodeToString(rb[:])+".xml")
	defer os.Remove(out)
	argv := append([]string{gluster.Binary, "--mode=script", "--xml", "--timeout=" + strconv.Itoa(glusterReadTimeout)}, gluster.ArgsHealSummary(vol)...)
	ctx, cancel := context.WithTimeout(ctx, glusterTimeout)
	defer cancel()
	// El código de salida no importa: con un brick caído GlusterFS igual devuelve el XML.
	_ = appsys.RunUnit(ctx, "tango-gluster-heal", argv, nil, out, nil)
	b, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	if i := bytes.Index(b, []byte("<?xml")); i > 0 {
		b = b[i:] // descarta lo que haya escrito antes en stderr
	}
	return gluster.ParseHeal(b)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- Estado -----------------------------------------------------------------------

func glusterStatus(ctx context.Context, self string) agentproto.GlusterStatusResult {
	res := agentproto.GlusterStatusResult{Installed: glusterInstalled(), Peers: []gluster.Peer{}, Volumes: []gluster.Volume{},
		Orphans: []agentproto.GlusterOrphan{}, Mounts: glusterMounts(), Allowed: readGlusterAllowed(), TLS: glusterTLSState(), LVBricks: glusterLVBricks(ctx)}
	if !res.Installed {
		return res
	}
	ensureBrickParents()
	if v, err := exec.CommandContext(ctx, gluster.Binary, "--version").Output(); err == nil {
		res.Version = strings.TrimPrefix(firstLineOf(string(v)), "glusterfs ")
	}
	if self == "" {
		self, _ = os.Hostname()
	}
	out, code, err := runGlusterT(ctx, glusterReadTimeout, gluster.ArgsPoolList()...)
	if err == nil {
		if err = gluster.ParseResult(out, code); err == nil {
			res.Peers, err = gluster.ParsePool(out, self)
		}
	}
	if err != nil {
		res.Error = glusterAgentErr(err).Message
		return res
	}
	res.Running = true
	info, code, err := runGlusterT(ctx, glusterReadTimeout, gluster.ArgsVolumeInfo()...)
	if err != nil || code != 0 && !bytes.Contains(info, []byte("<cliOutput")) {
		res.Error = "no se pudo leer los volúmenes"
		return res
	}
	// status falla si no hay ningún volumen arrancado: se sigue sin él. Si no respondió a
	// tiempo (un nodo caído que glusterd todavía espera), los bricks quedan sin estado.
	status, scode, serr := runGlusterT(ctx, glusterReadTimeout, gluster.ArgsVolumeStatus()...)
	if serr == nil && scode != 0 && !bytes.Contains(status, []byte("<cliOutput")) {
		res.Error = "Algún nodo no responde: el estado de los bricks puede estar incompleto."
	}
	vols, err := gluster.ParseVolumes(info, status)
	if err != nil {
		res.Error = glusterAgentErr(err).Message
		return res
	}
	// La curación no va acá: `heal info` necesita red (glusterHealInfo) y tangod la
	// pide aparte cada tanto.
	res.Volumes = vols
	res.Orphans = glusterOrphans(vols)
	// Los LV de bricks huérfanos los calcula tangod (sabe con qué nombres e IPs se conoce a
	// este nodo; el agente no tiene red).
	return res
}

func firstLineOf(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(l)
}

var tangoVolDirRe = regexp.MustCompile(`^volume[0-9]{1,3}$`)

// ensureBrickParents deja /volumeN/@gluster en cada volumen de TangoOS montado:
// glusterd crea el directorio del brick pero no sus padres, y desde otro nodo no hay
// forma de crearlos. Un directorio común (no un subvolumen): las instantáneas de
// TangoOS son de @shares y no lo incluyen.
func ensureBrickParents() {
	tops, _ := os.ReadDir("/")
	for _, t := range tops {
		p := "/" + t.Name()
		if t.IsDir() && tangoVolDirRe.MatchString(t.Name()) && isMountedAt(p) {
			_ = os.Mkdir(gluster.BrickParent(p), 0o755)
		}
	}
}

// glusterOrphans son los /volumeN/@gluster/<vol> de este nodo cuyo volumen ya no
// existe (quedan al borrar un volumen: GlusterFS no borra los datos de los bricks).
func glusterOrphans(vols []gluster.Volume) []agentproto.GlusterOrphan {
	known := map[string]bool{}
	for _, v := range vols {
		known[v.Name] = true
	}
	out := []agentproto.GlusterOrphan{}
	tops, _ := os.ReadDir("/")
	for _, t := range tops {
		if !t.IsDir() || !tangoVolDirRe.MatchString(t.Name()) {
			continue
		}
		ents, _ := os.ReadDir(filepath.Join("/", t.Name(), "@gluster"))
		for _, e := range ents {
			if e.IsDir() && !known[e.Name()] && gluster.ValidVolumeName(e.Name()) == nil {
				out = append(out, agentproto.GlusterOrphan{TangoVolume: t.Name(), Name: e.Name(), Path: filepath.Join("/", t.Name(), "@gluster", e.Name())})
			}
		}
	}
	return out
}

var sambaPathRe = regexp.MustCompile(`(?m)^\[([^\]]+)\]$|^\s*path = (/cluster/[a-z][a-z0-9_]*)$`)

// glusterShares mapea /cluster/<vol> → carpeta SMB (de /etc/samba/tango.d).
func glusterShares() map[string]string {
	out := map[string]string{}
	files, _ := filepath.Glob("/etc/samba/tango.d/*.conf")
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		sec := ""
		for _, m := range sambaPathRe.FindAllStringSubmatch(string(b), -1) {
			if m[1] != "" {
				sec = m[1]
			} else if sec != "" {
				out[m[2]] = sec
			}
		}
	}
	return out
}

func glusterMounts() []agentproto.GlusterMountInfo {
	out := []agentproto.GlusterMountInfo{}
	units, _ := filepath.Glob("/etc/systemd/system/cluster-*.mount")
	shares := glusterShares()
	for _, u := range units {
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(u), "cluster-"), ".mount")
		if gluster.ValidVolumeName(name) != nil {
			continue
		}
		p := gluster.MountPath(name)
		out = append(out, agentproto.GlusterMountInfo{Name: name, Path: p, Mounted: isMountedAt(p), Share: shares[p]})
	}
	return out
}

// isMountedAt mira /proc/self/mountinfo (el namespace del agente recibe los montajes
// del host por propagación).
func isMountedAt(p string) bool {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if fs := strings.Fields(sc.Text()); len(fs) > 4 && fs[4] == p {
			return true
		}
	}
	return false
}

// ---- Firewall ---------------------------------------------------------------------

func writeGlusterFirewall(allowed []string) error {
	if err := os.MkdirAll(filepath.Dir(gluster.FirewallFile), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(gluster.FirewallFile, []byte(gluster.FirewallRules(allowed)), 0o644)
}

var nftElemsRe = regexp.MustCompile(`elements = \{ ([^}]*) \}`)

func readGlusterAllowed() []string {
	out := []string{}
	b, err := os.ReadFile(gluster.FirewallFile)
	if err != nil {
		return out
	}
	for _, m := range nftElemsRe.FindAllStringSubmatch(string(b), -1) {
		for _, e := range strings.Split(m[1], ",") {
			if e = strings.TrimSpace(e); e != "" {
				out = append(out, e)
			}
		}
	}
	return out
}

// ---- systemd --------------------------------------------------------------------------

func sdWait(ctx context.Context, ch <-chan string, unit string) error {
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

// reloadUnit recarga el firewall (ExecReload) o lo arranca si no corría. Nunca
// restart: glusterd lo requiere (RequiredBy) y un restart reiniciaría glusterd también.
func reloadUnit(ctx context.Context, unit string) error {
	c, err := sd.NewSystemConnectionContext(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	ch := make(chan string, 1)
	if _, err := c.ReloadOrRestartUnitContext(ctx, unit, "replace", ch); err != nil {
		return err
	}
	return sdWait(ctx, ch, unit)
}

// ---- Montaje y SMB --------------------------------------------------------------------

func glusterMount(ctx context.Context, a *agentproto.GlusterMountArgs, log *slog.Logger) (any, *agentproto.Error) {
	unit := gluster.MountUnitName(a.Name)
	unitPath := filepath.Join("/etc/systemd/system", unit)
	mnt := gluster.MountPath(a.Name)
	prevShare := glusterShares()[mnt]

	// La carpeta SMB se saca antes de desmontar (y si cambió de nombre).
	if a.Action == "" && prevShare != "" && prevShare != a.Share {
		if _, err := diskops.RemoveSambaShare(ctx, prevShare); err != nil {
			log.Warn("gluster smb", "share", prevShare, "err", err)
		}
	}
	c, err := sd.NewSystemConnectionContext(ctx)
	if err != nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "systemd: %v", err)
	}
	defer c.Close()
	if a.Action != "" {
		if !fileExists(unitPath) {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "%s no está configurado para montarse", a.Name)
		}
		ch := make(chan string, 1)
		var err error
		if a.Action == "stop" {
			_, err = c.StopUnitContext(ctx, unit, "replace", ch)
		} else {
			_, err = c.StartUnitContext(ctx, unit, "replace", ch)
		}
		if err == nil {
			err = sdWait(ctx, ch, unit)
		}
		if err != nil {
			return nil, agentproto.Errf(agentproto.CodeBusy, "%s %s: %v", a.Action, mnt, err)
		}
		return struct{}{}, nil
	}
	if !a.Enabled {
		ch := make(chan string, 1)
		if _, err := c.StopUnitContext(ctx, unit, "replace", ch); err == nil {
			if err := sdWait(ctx, ch, unit); err != nil {
				return nil, agentproto.Errf(agentproto.CodeBusy, "no se pudo desmontar %s (¿hay archivos abiertos?): %v", mnt, err)
			}
		}
		_, _ = c.DisableUnitFilesContext(ctx, []string{unit}, false)
		_ = os.Remove(unitPath)
		_ = c.ReloadContext(ctx)
		return struct{}{}, nil
	}
	if !glusterInstalled() {
		return nil, agentproto.Errf(agentproto.CodeNotInstalled, "GlusterFS no está instalado")
	}
	if err := writeFileAtomic(unitPath, []byte(gluster.MountUnit(a.Name, a.Backups)), 0o644); err != nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
	}
	if err := c.ReloadContext(ctx); err != nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "systemd: %v", err)
	}
	if _, _, err := c.EnableUnitFilesContext(ctx, []string{unitPath}, false, true); err != nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "systemd: %v", err)
	}
	// Un montaje que existe pero no responde (cliente FUSE colgado, por ejemplo después de
	// cambiar el TLS) se rehace.
	if isMountedAt(mnt) {
		if _, err := os.Stat(mnt + "/."); err != nil {
			ch := make(chan string, 1)
			if _, err := c.StopUnitContext(ctx, unit, "replace", ch); err == nil {
				_ = sdWait(ctx, ch, unit)
			}
		}
	}
	if !isMountedAt(mnt) {
		ch := make(chan string, 1)
		if _, err := c.StartUnitContext(ctx, unit, "replace", ch); err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "systemd: %v", err)
		}
		if err := sdWait(ctx, ch, unit); err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo montar %s: %v (¿el volumen está arrancado?)", a.Name, err)
		}
		for i := 0; i < 50 && !isMountedAt(mnt); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if !isMountedAt(mnt) {
			return nil, agentproto.Errf(agentproto.CodeFailed, "systemd montó %s pero no aparece", mnt)
		}
	}
	// Recién montado, el cliente FUSE tarda un instante en conectar con los bricks (más con
	// TLS): hasta entonces cualquier acceso da "transport endpoint is not connected".
	for i := 0; i < 60; i++ {
		if _, err := os.Stat(mnt + "/."); err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if a.Share == "" {
		return struct{}{}, nil
	}
	for _, u := range append(append([]string{}, a.RW...), a.RO...) {
		if _, err := diskops.EnsureUser(ctx, u, ""); err != nil {
			return nil, userErr(err, log)
		}
	}
	rwG, roG := diskops.ShareGroups(a.Share)
	// GID fijo por nombre: las ACL quedan guardadas en GlusterFS y valen en todos los nodos.
	rwGID, err := diskops.EnsureClusterGroup(ctx, rwG)
	if err != nil {
		return nil, userErr(err, log)
	}
	roGID, err := diskops.EnsureClusterGroup(ctx, roG)
	if err != nil {
		return nil, userErr(err, log)
	}
	if err := diskops.SetGroupMembers(ctx, rwG, sorted(a.RW)); err != nil {
		return nil, userErr(err, log)
	}
	if err := diskops.SetGroupMembers(ctx, roG, sorted(a.RO)); err != nil {
		return nil, userErr(err, log)
	}
	if err := diskops.ApplyShareACL(mnt, rwGID, roGID); err != nil {
		log.Warn("gluster acl", "vol", a.Name, "err", err)
		return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudieron aplicar los permisos: %v", err)
	}
	if _, err := diskops.ApplySambaShareWithPerms(ctx, a.Share, mnt, a.Comment); err != nil {
		if errors.Is(err, diskops.ErrSambaMissing) {
			return nil, agentproto.Errf(agentproto.CodeNotInstalled, "Samba no está instalado")
		}
		log.Warn("gluster smb", "err", err, "output", execOutput(err))
		return nil, agentproto.Errf(agentproto.CodeFailed, "Samba rechazó la configuración")
	}
	return struct{}{}, nil
}

// ---- Unidades transitorias (root fuera del sandbox) ---------------------------------

var brickDirRe = regexp.MustCompile(`^/volume[0-9]{1,3}/@gluster/[a-z][a-z0-9_]{0,31}$`)

// helperGlusterClean borra un brick huérfano (la op ya comprobó que su volumen no
// existe). Valida la ruta de nuevo: este proceso corre con todos los permisos.
func helperGlusterClean(dir string) error {
	if !brickDirRe.MatchString(dir) {
		return fmt.Errorf("ruta inválida: %q", dir)
	}
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("no es un directorio: %s", dir)
	}
	return os.RemoveAll(dir)
}

// helperGlusterInstall: primero nftables y el firewall cargado, después
// glusterfs-server (su postinst arranca glusterd, que ya encuentra los puertos cerrados).
func helperGlusterInstall(ctx context.Context, a *agentproto.GlusterInstallArgs) error {
	stage(0.1, "Actualizando la lista de paquetes")
	if err := runCmd(ctx, "/usr/bin/apt-get", "-q", "update"); err != nil {
		return err
	}
	if !fileExists("/usr/sbin/nft") {
		stage(0.25, "Instalando nftables")
		if err := runCmd(ctx, "/usr/bin/apt-get", "-q", "-y", "--no-install-recommends", "install", "nftables"); err != nil {
			return err
		}
	}
	stage(0.4, "Cerrando los puertos de GlusterFS para equipos no permitidos")
	if err := writeGlusterFirewall(a.Allowed); err != nil {
		return err
	}
	unitPath := filepath.Join("/etc/systemd/system", gluster.FirewallUnitName)
	if err := writeFileAtomic(unitPath, []byte(gluster.FirewallUnit), 0o644); err != nil {
		return err
	}
	if err := runCmd(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := runCmd(ctx, "/usr/bin/systemctl", "enable", "--now", gluster.FirewallUnitName); err != nil {
		return err
	}
	if !glusterInstalled() {
		stage(0.55, "Instalando GlusterFS (glusterfs-server)")
		if err := runCmd(ctx, "/usr/bin/apt-get", "-q", "-y", "--no-install-recommends", "install", "glusterfs-server", "glusterfs-client"); err != nil {
			return err
		}
	}
	changed, err := setGlusterdPingTimeout("/etc/glusterfs/glusterd.vol")
	if err != nil {
		return err
	}
	stage(0.9, "Arrancando glusterd")
	if err := runCmd(ctx, "/usr/bin/systemctl", "enable", "--now", "glusterd.service"); err != nil {
		return err
	}
	if changed {
		return runCmd(ctx, "/usr/bin/systemctl", "restart", "glusterd.service")
	}
	return nil
}

// setGlusterdPingTimeout deja `option ping-timeout` de glusterd.vol en
// glusterdPingTimeout. Devuelve si cambió el archivo.
func setGlusterdPingTimeout(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out, changed := gluster.SetVolOption(string(b), "ping-timeout", glusterdPingTimeout)
	if !changed {
		return false, nil
	}
	return true, writeFileAtomic(path, []byte(out), 0o644)
}
