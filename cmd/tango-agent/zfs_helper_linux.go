//go:build linux

package main

// Tareas "zfs-*" de la unidad transitoria (runM4Helper): corren como root sin el
// sandbox del agente y en el namespace de montaje del sistema. Cada tarea vuelve a
// validar sus argumentos (vienen de un archivo 0600 del agente) y arma el argv con
// internal/zfs. Ninguna recibe frases de cifrado.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/smart"
	"github.com/lordbasex/go-link-tangoos/internal/zfs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

func runZfsHelper(ctx context.Context, task string, args []string) error {
	switch task {
	case "zfs-enable":
		return helperZfsEnable(ctx)
	case "zfs-create":
		var a agentproto.ZfsPoolCreateArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperZfsCreate(ctx, &a)
	case "zfs-add":
		var a agentproto.ZfsPoolAddArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperZfsAdd(ctx, &a)
	case "zfs-aux":
		var a agentproto.ZfsAuxArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperZfsAux(ctx, &a)
	case "zfs-replace":
		var a agentproto.ZfsReplaceArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperZfsReplace(ctx, &a)
	case "zfs-import":
		var a agentproto.ZfsImportArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperZfsImport(ctx, &a)
	case "zfs-export", "zfs-destroy":
		var a agentproto.ZfsPoolArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperZfsDropPool(ctx, a.Name, task == "zfs-destroy")
	case "zfs-volcreate":
		var a agentproto.ZfsVolCreateArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperZfsVolCreate(ctx, &a)
	case "zfs-voldelete", "zfs-mount", "zfs-unmount":
		var a agentproto.ZfsVolArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		if e := validZfsVol(a.Pool, a.Name); e != nil {
			return e
		}
		ds := zfs.VolumeDataset(a.Pool, a.Name)
		switch task {
		case "zfs-voldelete":
			return helperZfsVolDelete(ctx, ds, "/"+a.Name)
		case "zfs-mount":
			return zfsMountTree(ctx, ds)
		}
		return zfsUnmountTree(ctx, ds)
	case "zfs-zvol", "zfs-zvoldel":
		var a agentproto.ZfsZvolArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		if task == "zfs-zvoldel" {
			if err := zfs.ValidZvolName(a.Name); err != nil {
				return err
			}
			if err := zfs.ValidPoolName(a.Pool); err != nil {
				return err
			}
			_, err := zrun(ctx, zfs.ZfsBin, nil, "destroy", zfs.ZvolDataset(a.Pool, a.Name))
			return err
		}
		argv, err := zfs.ArgsZvolCreate(a.Pool, a.Name, a.Size, a.Sparse)
		if err != nil {
			return err
		}
		if _, err := zrun(ctx, zfs.ZfsBin, nil, argv...); err != nil {
			return err
		}
		return waitPath(zfs.ZvolDevice(a.Pool, a.Name), 15*time.Second)
	case "zfs-subvol":
		var a agentproto.SubvolCreateArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperZfsSubvol(ctx, &a)
	case "zfs-snap", "zfs-snapdel":
		var a agentproto.SnapshotDeleteArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperZfsSnap(ctx, &a, task == "zfs-snapdel")
	case "zfs-sharedel":
		var a agentproto.ShareDeleteArgs
		if err := readArgs(args, &a); err != nil {
			return err
		}
		return helperZfsShareDelete(ctx, &a)
	}
	return fmt.Errorf("tarea desconocida %q", task)
}

// ---- Habilitar ZFS ---------------------------------------------------------------------

// helperZfsEnable: contrib (zfs-dkms y zfsutils están ahí por la licencia CDDL), headers
// del kernel corriendo y de su sabor (para que DKMS recompile con cada kernel nuevo),
// zfs-dkms + zfsutils-linux, y el módulo cargado al arrancar.
func helperZfsEnable(ctx context.Context) error {
	// Un intento anterior cortado (apagón a mitad de DKMS) deja zfs-dkms sin configurar
	// y apt-get no instala nada hasta terminarlo: se termina primero (no hace nada si no
	// hay nada pendiente). Encontrado en el laboratorio.
	stage(0.02, "Terminando una instalación anterior, si quedó a medias")
	if err := runCmd(ctx, "/usr/bin/dpkg", "--configure", "-a"); err != nil {
		fmt.Println("aviso: dpkg --configure -a:", err)
	}
	stage(0.03, "Habilitando el componente contrib de Debian")
	src, err := contribSources()
	if err != nil {
		return err
	}
	if src != "" {
		if err := writeFileAtomic("/etc/apt/sources.list.d/tangoos-contrib.sources", []byte(src), 0o644); err != nil {
			return err
		}
	}
	stage(0.08, "Actualizando la lista de paquetes")
	if err := runCmd(ctx, "/usr/bin/apt-get", "-q", "update"); err != nil {
		return err
	}
	var u unix.Utsname
	_ = unix.Uname(&u)
	rel := strings.TrimRight(string(u.Release[:]), "\x00")
	if !kernelRelRe.MatchString(rel) {
		return fmt.Errorf("versión de kernel inesperada: %q", rel)
	}
	pkgs := []string{"linux-headers-" + rel}
	if f := kernelFlavor(rel); f != "" {
		pkgs = append(pkgs, "linux-headers-"+f)
	}
	stage(0.15, "Instalando los headers del kernel ("+rel+")")
	if err := runCmd(ctx, "/usr/bin/apt-get", append([]string{"-q", "-y", "--no-install-recommends", "install"}, pkgs...)...); err != nil {
		return err
	}
	// La compilación con DKMS tarda varios minutos y apt no da progreso: se estima
	// por tiempo (en la VM de laboratorio, 4 núcleos: ~4 min).
	stop := make(chan struct{})
	go func() {
		t0 := time.Now()
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				el := time.Since(t0)
				p := 0.3 + 0.6*min(1, el.Seconds()/(6*60))
				stage(p, fmt.Sprintf("Compilando el módulo ZFS con DKMS (%d min)", int(el.Minutes())))
			}
		}
	}()
	stage(0.3, "Instalando ZFS y compilando el módulo (DKMS): tarda unos minutos")
	err = runCmd(ctx, "/usr/bin/apt-get", "-q", "-y", "--no-install-recommends", "install", "zfs-dkms", "zfsutils-linux", "zfs-zed")
	close(stop)
	if err != nil {
		return err
	}
	stage(0.92, "Cargando el módulo")
	if err := runCmd(ctx, "/usr/sbin/modprobe", "zfs"); err != nil {
		return fmt.Errorf("el módulo zfs no cargó: %w", err)
	}
	if err := writeFileAtomic("/etc/modules-load.d/tangoos-zfs.conf", []byte("# TangoOS: ZFS habilitado desde Almacenamiento\nzfs\n"), 0o644); err != nil {
		return err
	}
	for _, u := range []string{"zfs-import-cache.service", "zfs-mount.service", "zfs-zed.service", "zfs.target"} {
		_ = runCmd(ctx, "/usr/bin/systemctl", "enable", u)
	}
	stage(0.98, "ZFS listo")
	return nil
}

var kernelRelRe = regexp.MustCompile(`^[0-9]+\.[0-9]+[0-9A-Za-z.+~_-]*$`)

// kernelFlavor: "6.12.111+deb13-cloud-amd64" → "cloud-amd64"; "6.12.1+deb13-amd64" → "amd64".
func kernelFlavor(rel string) string {
	i := strings.Index(rel, "-")
	if i < 0 {
		return ""
	}
	f := rel[i+1:]
	if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(f) {
		return ""
	}
	return f
}

// contribSources arma una fuente deb822 con contrib para cada fuente de Debian que
// tenga main (los .sources y la sources.list de una línea). "" si ya hay contrib.
func contribSources() (string, error) {
	type src struct{ uris, suites, signed string }
	var found []src
	files, _ := filepath.Glob("/etc/apt/sources.list.d/*.sources")
	for _, f := range files {
		if strings.HasSuffix(f, "/tangoos-contrib.sources") || strings.Contains(f, "tangoos") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, stanza := range strings.Split(string(b), "\n\n") {
			kv := map[string]string{}
			for _, l := range strings.Split(stanza, "\n") {
				k, v, ok := strings.Cut(l, ":")
				if ok && !strings.HasPrefix(l, "#") {
					kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
			comps := " " + kv["Components"] + " "
			if !strings.Contains(kv["Types"], "deb") || !strings.Contains(comps, " main ") {
				continue
			}
			if strings.Contains(comps, " contrib ") {
				return "", nil
			}
			found = append(found, src{kv["URIs"], kv["Suites"], kv["Signed-By"]})
		}
	}
	if b, err := os.ReadFile("/etc/apt/sources.list"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) < 4 || f[0] != "deb" || strings.HasPrefix(f[1], "[") {
				continue
			}
			comps := " " + strings.Join(f[3:], " ") + " "
			if !strings.Contains(comps, " main ") {
				continue
			}
			if strings.Contains(comps, " contrib ") {
				return "", nil
			}
			found = append(found, src{f[1], f[2], ""})
		}
	}
	if len(found) == 0 {
		return "", errors.New("no encontré una fuente de Debian con main: agregá contrib a mano")
	}
	var sb strings.Builder
	sb.WriteString("# TangoOS: contrib para ZFS (zfs-dkms y zfsutils-linux, licencia CDDL)\n")
	for i, s := range found {
		if i > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "Types: deb\nURIs: %s\nSuites: %s\nComponents: contrib\n", s.uris, s.suites)
		if s.signed != "" {
			fmt.Fprintf(&sb, "Signed-By: %s\n", s.signed)
		}
	}
	return sb.String(), nil
}

// ---- Pools ---------------------------------------------------------------------------------

// prepareDisks resuelve, verifica la huella y limpia los discos (incluida la zona donde
// zpool pone su partición, ver diskops.WipeZFSRegions).
func prepareDisks(disks []agentproto.VolumeDisk) ([]string, error) {
	var ids []string
	for _, d := range disks {
		if err := zfs.ValidByID(d.DevID); err != nil {
			return nil, err
		}
		dev, e := smart.DefaultPaths.Resolve(d.DevID)
		if e != nil {
			return nil, e
		}
		if ok, reason := diskops.DefaultEnv.Eligible(diskops.DevName(dev)); !ok {
			return nil, fmt.Errorf("%s: %s", d.DevID, reason)
		}
		if err := diskops.Wipe(dev, d.Fingerprint); err != nil {
			if errors.Is(err, diskops.ErrChanged) {
				return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "%s cambió desde el plan", d.DevID)
			}
			return nil, err
		}
		if err := diskops.WipeZFSRegions(dev); err != nil {
			return nil, err
		}
		ids = append(ids, d.DevID)
	}
	_ = runCmd(context.Background(), "/usr/bin/udevadm", "settle", "--timeout=10")
	return ids, nil
}

func helperZfsCreate(ctx context.Context, a *agentproto.ZfsPoolCreateArgs) error {
	if e := validateZfsCreate(a); e != nil {
		return e
	}
	stage(0.1, "Preparando los discos")
	ids, err := prepareDisks(a.Disks)
	if err != nil {
		return err
	}
	argv, err := zfs.ArgsPoolCreate(a.Name, a.Layout, ids)
	if err != nil {
		return err
	}
	stage(0.5, "Creando el pool "+a.Name)
	if _, err := zrun(ctx, zfs.ZpoolBin, nil, argv...); err != nil {
		return err
	}
	stage(0.98, "Pool listo")
	return nil
}

func helperZfsAdd(ctx context.Context, a *agentproto.ZfsPoolAddArgs) error {
	if e := validateZfsVdev(a.Name, a.Layout, a.Disks); e != nil {
		return e
	}
	stage(0.1, "Preparando los discos")
	ids, err := prepareDisks(a.Disks)
	if err != nil {
		return err
	}
	argv, err := zfs.ArgsPoolAdd(a.Name, a.Layout, ids)
	if err != nil {
		return err
	}
	stage(0.6, "Sumando el vdev al pool")
	_, err = zrun(ctx, zfs.ZpoolBin, nil, argv...)
	return err
}

func helperZfsAux(ctx context.Context, a *agentproto.ZfsAuxArgs) error {
	if e := validateZfsAux(a); e != nil {
		return e
	}
	if len(a.Remove) > 0 {
		for _, v := range a.Remove {
			argv, err := zfs.ArgsRemove(a.Name, v)
			if err != nil {
				return err
			}
			stage(0.5, "Quitando "+v)
			if _, err := zrun(ctx, zfs.ZpoolBin, nil, argv...); err != nil {
				return err
			}
		}
		return nil
	}
	ids, err := prepareDisks(a.Disks)
	if err != nil {
		return err
	}
	argv, err := zfs.ArgsAuxAdd(a.Name, a.Kind, ids)
	if err != nil {
		return err
	}
	stage(0.6, map[string]string{"cache": "Agregando la caché de lectura (L2ARC)", "log": "Agregando el registro de escritura (SLOG)"}[a.Kind])
	_, err = zrun(ctx, zfs.ZpoolBin, nil, argv...)
	return err
}

// helperZfsReplace reemplaza y espera el resilver con progreso.
func helperZfsReplace(ctx context.Context, a *agentproto.ZfsReplaceArgs) error {
	if err := zfs.ValidPoolName(a.Name); err != nil {
		return err
	}
	stage(0.05, "Preparando el disco nuevo")
	ids, err := prepareDisks([]agentproto.VolumeDisk{a.New})
	if err != nil {
		return err
	}
	argv, err := zfs.ArgsReplace(a.Name, a.Old, ids[0])
	if err != nil {
		return err
	}
	stage(0.1, "Reemplazando el disco")
	if _, err := zrun(ctx, zfs.ZpoolBin, nil, argv...); err != nil {
		return err
	}
	return waitScan(ctx, a.Name, "resilver", 0.1, 0.88, "Reconstruyendo (resilver)")
}

// waitScan sigue el scrub/resilver de un pool hasta que termina.
func waitScan(ctx context.Context, pool, fn string, from, span float64, label string) error {
	time.Sleep(2 * time.Second)
	for {
		b, err := zrun(ctx, zfs.ZpoolBin, nil, "status", "-j", "-p", pool)
		if err != nil {
			return err
		}
		ps, err := zfs.ParseStatus(b, nil)
		if err != nil || len(ps) == 0 {
			return err
		}
		sc := ps[0].Scan
		if sc == nil || sc.Function != fn || sc.State != "scanning" {
			stage(from+span, "Reconstrucción terminada")
			return nil
		}
		stage(from+span*sc.Progress, fmt.Sprintf("%s: %.0f %%", label, sc.Progress*100))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// helperZfsImport: con -N no se monta nada al importar. Solo lectura: debajo de
// /import/<pool> (altroot) y todo en solo lectura, para revisar un pool de otro equipo.
// Lectura y escritura: se montan los volúmenes con el layout de TangoOS
// (<pool>/<vol> en /<vol>) y sus carpetas.
func helperZfsImport(ctx context.Context, a *agentproto.ZfsImportArgs) error {
	argv, err := zfs.ArgsImport(a.Pool, "", a.ReadOnly)
	if err != nil {
		return err
	}
	if a.ReadOnly {
		argv = append(argv[:len(argv)-1], "-R", "/import/"+a.Pool, argv[len(argv)-1])
	}
	stage(0.2, "Importando el pool")
	if _, err := zrun(ctx, zfs.ZpoolBin, nil, argv...); err != nil {
		return err
	}
	rep, err := zfsReport(ctx, false)
	if err != nil {
		return err
	}
	stage(0.6, "Montando los volúmenes")
	for _, p := range rep.Pools {
		if p.Name != a.Pool && p.GUID != a.Pool {
			continue
		}
		for _, d := range p.Datasets {
			if d.Type != "filesystem" || d.Mounted || d.Mountpoint == "" || (d.Encrypted && !d.KeyLoaded) {
				continue
			}
			if !a.ReadOnly && !tangoLayout(p.Name, d) {
				continue
			}
			if _, err := zrun(ctx, zfs.ZfsBin, nil, "mount", d.Name); err != nil {
				fmt.Println("aviso:", err)
			}
		}
		if !a.ReadOnly {
			for _, d := range p.Datasets {
				// Solo si quedó montado: un volumen cifrado sin desbloquear no se monta y el
				// directorio terminaría en el disco del sistema, debajo del punto de montaje.
				if vol, ok := volumeOfDataset(p.Name, d); ok && isMountedAt("/"+vol) {
					_ = os.MkdirAll("/"+vol+"/@snapshots", 0o755)
				}
			}
		}
	}
	return nil
}

// tangoLayout: el volumen <pool>/<vol> montado en /<vol> y lo que cuelga de él.
func tangoLayout(pool string, d agentproto.ZfsDataset) bool {
	rest, ok := strings.CutPrefix(d.Name, pool+"/")
	if !ok {
		return false
	}
	vol, _, _ := strings.Cut(rest, "/")
	if zfs.ValidVolumeName(vol) != nil {
		return false
	}
	return d.Mountpoint == "/"+vol || strings.HasPrefix(d.Mountpoint, "/"+vol+"/@")
}

func volumeOfDataset(pool string, d agentproto.ZfsDataset) (string, bool) {
	rest, ok := strings.CutPrefix(d.Name, pool+"/")
	if !ok || strings.Contains(rest, "/") || zfs.ValidVolumeName(rest) != nil || d.Mountpoint != "/"+rest {
		return "", false
	}
	return rest, true
}

// helperZfsDropPool exporta o destruye: primero saca de la red las carpetas de sus
// volúmenes y al destruir borra las etiquetas ZFS de los discos.
func helperZfsDropPool(ctx context.Context, name string, destroy bool) error {
	if err := zfs.ValidPoolName(name); err != nil {
		return err
	}
	rep, err := zfsReport(ctx, false)
	if err != nil {
		return err
	}
	var pi *agentproto.ZfsPoolInfo
	for i := range rep.Pools {
		if rep.Pools[i].Name == name {
			pi = &rep.Pools[i]
		}
	}
	if pi == nil {
		return fmt.Errorf("no existe el pool %s", name)
	}
	stage(0.1, "Quitando las carpetas compartidas de la red")
	var mounts []string
	for _, d := range pi.Datasets {
		if vol, ok := volumeOfDataset(name, d); ok {
			mounts = append(mounts, "/"+vol)
			if _, err := diskops.RemoveSambaSharesUnder(ctx, "/"+vol); err != nil {
				fmt.Println("aviso samba:", err)
			}
		}
	}
	var parts []string
	for _, v := range append(append([]agentproto.ZfsVdev{}, pi.Vdevs...), pi.Logs...) {
		for _, l := range v.Disks {
			if l.Disk != "" {
				parts = append(parts, l.Disk)
			}
		}
	}
	for _, l := range pi.Cache {
		if l.Disk != "" {
			parts = append(parts, l.Disk)
		}
	}
	if destroy {
		stage(0.4, "Destruyendo el pool "+name)
		argv, _ := zfs.ArgsDestroy(name)
		if _, err := zrun(ctx, zfs.ZpoolBin, nil, argv...); err != nil {
			return err
		}
		stage(0.7, "Borrando las etiquetas ZFS de los discos")
		for _, d := range parts {
			for _, p := range []string{"/dev/" + d + "1", "/dev/" + d} {
				if fileExists(p) {
					_, _ = zrun(ctx, zfs.ZpoolBin, nil, "labelclear", "-f", p)
				}
			}
		}
	} else {
		stage(0.4, "Exportando el pool "+name)
		argv, _ := zfs.ArgsExport(name)
		if _, err := zrun(ctx, zfs.ZpoolBin, nil, argv...); err != nil {
			return err
		}
	}
	for _, m := range mounts {
		_ = os.Remove(m) // queda el directorio vacío del punto de montaje
	}
	stage(0.98, "Listo")
	return nil
}

// ---- Volúmenes -------------------------------------------------------------------------

func helperZfsVolCreate(ctx context.Context, a *agentproto.ZfsVolCreateArgs) error {
	if e := validZfsVol(a.Pool, a.Name); e != nil {
		return e
	}
	ds, mount := zfs.VolumeDataset(a.Pool, a.Name), "/"+a.Name
	created := false
	if !zfsExists(ctx, ds) {
		argv, err := zfs.ArgsVolumeCreate(a.Pool, a.Name, zfs.VolumeOpts{Quota: a.Quota, Reservation: a.Reservation})
		if err != nil {
			return err
		}
		stage(0.2, "Creando el volumen "+a.Name)
		if _, err := zrun(ctx, zfs.ZfsBin, nil, argv...); err != nil {
			return err
		}
		created = true
	}
	undo := func(err error) error {
		if created {
			_, _ = zrun(context.Background(), zfs.ZfsBin, nil, "destroy", "-r", ds)
			_ = os.Remove(mount)
		}
		return err
	}
	stage(0.5, "Montando "+mount)
	if _, err := zrun(ctx, zfs.ZfsBin, nil, "mount", ds); err != nil && !strings.Contains(err.Error(), "already mounted") {
		return undo(err)
	}
	for _, kind := range []string{"shares", "apps"} {
		argv, _ := zfs.ArgsParentCreate(ds, mount, kind)
		if !zfsExists(ctx, argv[len(argv)-1]) {
			if _, err := zrun(ctx, zfs.ZfsBin, nil, argv...); err != nil {
				return undo(err)
			}
		}
	}
	for _, d := range []string{"@shares", "@apps", "@snapshots"} {
		if err := os.MkdirAll(filepath.Join(mount, d), 0o755); err != nil {
			return undo(err)
		}
	}
	stage(0.98, "Volumen listo")
	return nil
}

func zfsExists(ctx context.Context, ds string) bool {
	_, err := zrun(ctx, zfs.ZfsBin, nil, "list", "-H", "-o", "name", ds)
	return err == nil
}

func helperZfsVolDelete(ctx context.Context, ds, mount string) error {
	stage(0.1, "Quitando las carpetas compartidas de la red")
	if _, err := diskops.RemoveSambaSharesUnder(ctx, mount); err != nil {
		fmt.Println("aviso samba:", err)
	}
	stage(0.4, "Borrando el volumen")
	if _, err := zrun(ctx, zfs.ZfsBin, nil, "destroy", "-r", "-f", ds); err != nil {
		return err
	}
	_ = os.Remove(mount)
	return nil
}

// zfsMountTree monta un volumen y sus carpetas (de arriba hacia abajo).
func zfsMountTree(ctx context.Context, ds string) error {
	names, err := zfsChildren(ctx, ds)
	if err != nil {
		return err
	}
	for _, n := range names {
		if _, err := zrun(ctx, zfs.ZfsBin, nil, "mount", n); err != nil &&
			!strings.Contains(err.Error(), "already mounted") && !strings.Contains(err.Error(), "canmount") {
			return err
		}
	}
	// Un volumen importado bloqueado no tenía @snapshots: se crea ahora que está montado.
	if pool, vol, ok := strings.Cut(ds, "/"); ok && zfs.ValidPoolName(pool) == nil && isMountedAt("/"+vol) {
		_ = os.MkdirAll("/"+vol+"/@snapshots", 0o755)
	}
	return nil
}

// zfsUnmountTree desmonta de abajo hacia arriba.
func zfsUnmountTree(ctx context.Context, ds string) error {
	names, err := zfsChildren(ctx, ds)
	if err != nil {
		return err
	}
	for i := len(names) - 1; i >= 0; i-- {
		if _, err := zrun(ctx, zfs.ZfsBin, nil, "unmount", names[i]); err != nil &&
			!strings.Contains(err.Error(), "not currently mounted") {
			return err
		}
	}
	return nil
}

// zfsChildren: el dataset y sus filesystems montables, ordenados por profundidad.
func zfsChildren(ctx context.Context, ds string) ([]string, error) {
	out, err := zrun(ctx, zfs.ZfsBin, nil, "list", "-H", "-r", "-t", "filesystem", "-o", "name,canmount", ds)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(l, "\t")
		if len(f) == 2 && f[1] == "on" {
			names = append(names, f[0])
		}
	}
	sort.SliceStable(names, func(i, j int) bool { return strings.Count(names[i], "/") < strings.Count(names[j], "/") })
	return names, nil
}

// ---- Carpetas e instantáneas (las ops subvol/snapshot/share de §7 en un volumen ZFS) --

func zfsVolumeDS(mount string) (string, error) {
	if err := diskops.ValidMount(mount); err != nil {
		return "", err
	}
	ds, ok := zfsVolumeOf(diskops.DefaultEnv, mount)
	if !ok {
		return "", fmt.Errorf("%s no es un volumen ZFS montado", mount)
	}
	return ds, nil
}

func helperZfsSubvol(ctx context.Context, a *agentproto.SubvolCreateArgs) error {
	vds, err := zfsVolumeDS(a.Mount)
	if err != nil {
		return err
	}
	child, err := zfs.ChildDataset(vds, a.Path)
	if err != nil {
		return err
	}
	if zfsExists(ctx, child) {
		return agentproto.Errf(agentproto.CodeExists, "ya existe %s", a.Path)
	}
	// El padre (shares/apps) puede faltar en un volumen importado.
	top, _, _ := strings.Cut(a.Path, "/")
	kind := strings.TrimPrefix(top, "@")
	if argv, err := zfs.ArgsParentCreate(vds, a.Mount, kind); err == nil && !zfsExists(ctx, argv[len(argv)-1]) {
		if _, err := zrun(ctx, zfs.ZfsBin, nil, argv...); err != nil {
			return err
		}
	}
	if _, err := zrun(ctx, zfs.ZfsBin, nil, zfs.ArgsChildCreate(child)...); err != nil {
		return err
	}
	if _, err := zrun(ctx, zfs.ZfsBin, nil, "mount", child); err != nil {
		_, _ = zrun(context.Background(), zfs.ZfsBin, nil, "destroy", child)
		return err
	}
	return os.Chmod(filepath.Join(a.Mount, a.Path), 0o755)
}

// helperZfsSnap crea (o borra) <volumen>/shares/<Carpeta>@<nombre> y el enlace
// /vol/@snapshots/<Carpeta>/<nombre> → ../../@shares/<Carpeta>/.zfs/snapshot/<nombre>,
// que es lo que leen la lista de instantáneas, Archivos y vfs_shadow_copy2 (.gmt).
func helperZfsSnap(ctx context.Context, a *agentproto.SnapshotDeleteArgs, del bool) error {
	vds, err := zfsVolumeDS(a.Mount)
	if err != nil {
		return err
	}
	if err := diskops.ValidShareName(a.Share); err != nil {
		return err
	}
	if err := zfs.ValidSnapName(a.Name); err != nil {
		return err
	}
	child, _ := zfs.ChildDataset(vds, "@shares/"+a.Share)
	dir := filepath.Join(a.Mount, "@snapshots", a.Share)
	link := filepath.Join(dir, a.Name)
	if del {
		argv, _ := zfs.ArgsSnapDestroy(child, a.Name)
		if _, err := zrun(ctx, zfs.ZfsBin, nil, argv...); err != nil && !strings.Contains(err.Error(), "could not find") {
			return err
		}
		_ = os.Remove(link)
		return diskops.SyncShadowLinks(dir)
	}
	if !zfsExists(ctx, child) {
		return agentproto.Errf(agentproto.CodeNotFound, "no existe la carpeta %s", a.Share)
	}
	argv, _ := zfs.ArgsSnapshot(child, a.Name)
	if _, err := zrun(ctx, zfs.ZfsBin, nil, argv...); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return agentproto.Errf(agentproto.CodeExists, "ya existe la instantánea %s", a.Name)
		}
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join("..", "..", "@shares", a.Share, ".zfs", "snapshot", a.Name)
	if err := os.Symlink(target, link); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return diskops.SyncShadowLinks(dir)
}

func helperZfsShareDelete(ctx context.Context, a *agentproto.ShareDeleteArgs) error {
	vds, err := zfsVolumeDS(a.Mount)
	if err != nil {
		return err
	}
	if err := diskops.ValidShareName(a.Share); err != nil {
		return err
	}
	child, _ := zfs.ChildDataset(vds, "@shares/"+a.Share)
	if !zfsExists(ctx, child) {
		return agentproto.Errf(agentproto.CodeNotFound, "no existe la carpeta %s", a.Share)
	}
	// -r: también sus instantáneas.
	if _, err := zrun(ctx, zfs.ZfsBin, nil, "destroy", "-r", "-f", child); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(a.Mount, "@snapshots", a.Share)) // solo enlaces
	_ = os.Remove(filepath.Join(a.Mount, "@shares", a.Share))
	return nil
}

func waitPath(p string, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fileExists(p) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("no apareció %s", p)
}
