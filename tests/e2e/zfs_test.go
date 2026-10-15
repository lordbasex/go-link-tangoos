//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Pools ZFS (protocol/README.md §18, ADR 0013, docs/14 filas 19–26). Usa SOLO discos
// libres del laboratorio: S3, S6 (2 GB), M1, M2 (4 GB), D3, D4 (16 GB), C1 y C2 (SSD de
// 2 GB). volume1 y volume2 (y sus discos) no se tocan.
//
//   TANGO_E2E_ZFS_REINSTALL=1  purga OpenZFS y lo vuelve a habilitar desde la API (DKMS)
//   TANGO_E2E_REBOOT=1         reinicia la VM con un volumen cifrado (queda bloqueado)

type zfsLeaf struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	GUID  string `json:"guid"`
	State string `json:"state"`
}

type zfsVdev struct {
	Name  string    `json:"name"`
	Type  string    `json:"type"`
	State string    `json:"state"`
	Disks []zfsLeaf `json:"disks"`
}

type zfsPool struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Health   string    `json:"health"`
	State    string    `json:"state"`
	ReadOnly bool      `json:"readonly"`
	Layout   string    `json:"layout"`
	Size     uint64    `json:"size"`
	Vdevs    []zfsVdev `json:"vdevs"`
	Logs     []zfsVdev `json:"logs"`
	Cache    []zfsLeaf `json:"cache"`
	Volumes  []string  `json:"volumes"`
	Zvols    []struct {
		Name   string `json:"name"`
		Size   uint64 `json:"size"`
		Device string `json:"device"`
	} `json:"zvols"`
	Scan *struct {
		Function string `json:"function"`
		State    string `json:"state"`
		Errors   uint64 `json:"errors"`
	} `json:"scan"`
}

type zfsStatus struct {
	Enabled bool      `json:"enabled"`
	Version string    `json:"version"`
	Pools   []zfsPool `json:"pools"`
}

type zfsPlan struct {
	PlanID       string `json:"plan_id"`
	ConfirmToken string `json:"confirm_token"`
	Summary      struct {
		Usable    uint64   `json:"usable"`
		Tolerance int      `json:"tolerance"`
		Notes     []string `json:"notes"`
	} `json:"summary"`
}

func (c *client) zfs() zfsStatus {
	var st zfsStatus
	c.call("storage.zfs.status", nil, &st)
	return st
}

func (c *client) zpool(name string) (zfsPool, bool) {
	for _, p := range c.zfs().Pools {
		if p.Name == name {
			return p, true
		}
	}
	return zfsPool{}, false
}

func waitZpool(t *testing.T, a *client, name string, ok func(zfsPool) bool, timeout time.Duration) zfsPool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p, found := a.zpool(name); found && ok(p) {
			return p
		}
		time.Sleep(2 * time.Second)
	}
	p, _ := a.zpool(name)
	t.Fatalf("el pool %s no llegó al estado esperado: %+v", name, p)
	return p
}

func createZpool(t *testing.T, a *client, name, layout string, serials []string) (zfsPool, zfsPlan) {
	t.Helper()
	var plan zfsPlan
	a.call("storage.zfs.plan", map[string]any{"name": name, "layout": layout, "disks": a.diskIDs(serials...)}, &plan)
	var r jobRef
	a.call("storage.zfs.create", confirm(plan.PlanID, plan.ConfirmToken, name), &r)
	a.waitJob(r.Job, 5*time.Minute)
	p, ok := a.zpool(name)
	if !ok {
		t.Fatalf("no aparece el pool %s", name)
	}
	return p, plan
}

func destroyZpool(t *testing.T, a *client, name string) {
	t.Helper()
	p, ok := a.zpool(name)
	if !ok {
		return
	}
	var dp struct {
		PlanID       string `json:"plan_id"`
		ConfirmToken string `json:"confirm_token"`
	}
	a.call("storage.zfs.destroyPlan", map[string]string{"pool": p.ID}, &dp)
	var r jobRef
	a.call("storage.zfs.destroy", confirm(dp.PlanID, dp.ConfirmToken, name), &r)
	a.waitJob(r.Job, 5*time.Minute)
}

// zfsCleanup deja los discos libres aunque una corrida anterior haya fallado a mitad.
func zfsCleanup(t *testing.T, pools ...string) {
	for _, p := range pools {
		_, _ = sshRun("zpool list " + p + " >/dev/null 2>&1 && zpool destroy -f " + p + "; true")
	}
}

func createZvolume(t *testing.T, a *client, pool zfsPool, name, pass string) poolVolume {
	t.Helper()
	params := map[string]any{"pool": pool.ID, "name": name}
	if pass != "" {
		params["encryption"] = map[string]any{"passphrase": pass}
	}
	var r jobRef
	a.call("storage.zfs.volumes.create", params, &r)
	a.waitJob(r.Job, 3*time.Minute)
	v, ok := a.poolVolumes()[name]
	if !ok {
		t.Fatalf("no aparece el volumen %s", name)
	}
	return v
}

func TestZfsEnable(t *testing.T) {
	destructive(t)
	a := admin(t)
	if os.Getenv("TANGO_E2E_ZFS_REINSTALL") == "1" {
		if out, err := sshRun("zpool list -H 2>/dev/null | wc -l"); err == nil && strings.TrimSpace(out) != "0" {
			t.Fatalf("hay pools importados: no se purga ZFS (%s)", out)
		}
		onNAS(t, "modprobe -r zfs 2>/dev/null; DEBIAN_FRONTEND=noninteractive apt-get -y -q purge zfs-dkms zfsutils-linux zfs-zed >/dev/null 2>&1; rm -f /etc/modules-load.d/tangoos-zfs.conf /etc/apt/sources.list.d/tangoos-contrib.sources; true")
		time.Sleep(3 * time.Second)
	}
	if a.zfs().Enabled {
		t.Log("ZFS ya estaba habilitado (TANGO_E2E_ZFS_REINSTALL=1 para probar la instalación)")
		a.callErr("storage.zfs.enable", nil, "INVALID_PARAMS")
		return
	}
	a.callErr("storage.zfs.plan", map[string]any{"name": "x", "layout": "mirror", "disks": []string{}}, "ZFS_UNAVAILABLE")
	var r jobRef
	t0 := time.Now()
	a.call("storage.zfs.enable", nil, &r)
	a.waitJob(r.Job, 25*time.Minute)
	t.Logf("ZFS habilitado en %s", time.Since(t0).Round(time.Second))
	st := a.zfs()
	if !st.Enabled || !strings.HasPrefix(st.Version, "2.") {
		t.Fatalf("estado después de habilitar: %+v", st)
	}
	if out := onNAS(t, "cat /etc/modules-load.d/tangoos-zfs.conf; grep -c contrib /etc/apt/sources.list.d/tangoos-contrib.sources"); !strings.Contains(out, "zfs") {
		t.Fatalf("falta la carga del módulo al arrancar: %s", out)
	}
}

// TestZfsMirrorShareSnapshotsISCSIReplace: filas 20–23.
func TestZfsMirrorShareSnapshotsISCSIReplace(t *testing.T) {
	destructive(t)
	a := admin(t)
	if !a.zfs().Enabled {
		t.Skip("ZFS no está habilitado (correr TestZfsEnable)")
	}
	zfsCleanup(t, "tze2e1")
	t.Cleanup(func() {
		// El destino iSCSI primero: con el zvol tomado por LIO, zpool destroy se cuelga.
		if _, err := admin(t).Call(ctxT(t, time.Minute), "iscsi.targets.delete", map[string]string{"name": "zfs-e2e", "typed": "zfs-e2e"}); err == nil {
			time.Sleep(5 * time.Second)
		}
		zfsCleanup(t, "tze2e1")
		_, _ = sshRun("rmdir /zv1 2>/dev/null; true")
	})

	// Validaciones: raidz1 con 2 discos, nombre reservado, disco en uso (volume1).
	a.callErr("storage.zfs.plan", map[string]any{"name": "tze2e1", "layout": "raidz1", "disks": a.diskIDs("TANGO-S3", "TANGO-S6")}, "INVALID_PARAMS")
	a.callErr("storage.zfs.plan", map[string]any{"name": "mirror", "layout": "mirror", "disks": a.diskIDs("TANGO-S3", "TANGO-S6")}, "INVALID_PARAMS")
	a.callErr("storage.zfs.plan", map[string]any{"name": "tze2e1", "layout": "mirror", "disks": a.diskIDs("TANGO-S3", "TANGO-D1")}, "DEVICE_NOT_ELIGIBLE")

	// Fila 20: espejo S3 + S6.
	p, plan := createZpool(t, a, "tze2e1", "mirror", []string{"TANGO-S3", "TANGO-S6"})
	if plan.Summary.Tolerance != 1 || !approx(plan.Summary.Usable, gb2, 3) {
		t.Fatalf("plan espejo: %+v", plan.Summary)
	}
	if p.Layout != "mirror" || p.Health != "ONLINE" || len(p.Vdevs) != 1 || len(p.Vdevs[0].Disks) != 2 {
		t.Fatalf("pool: %+v", p)
	}
	ds := a.disks()
	for _, s := range []string{"TANGO-S3", "TANGO-S6"} {
		for _, d := range ds {
			if d.Serial == s && d.Use != "member" {
				t.Fatalf("%s tendría que figurar como miembro: %+v", s, d)
			}
		}
	}
	// OpenZFS 2.2+ muestra xattr=sa como "on" (on es alias de sa desde 2.2).
	if out := onNAS(t, "zfs get -H -o value compression,xattr,acltype,atime tze2e1 | tr '\\n' ' '"); strings.TrimSpace(out) != "lz4 on posix off" && strings.TrimSpace(out) != "lz4 sa posix off" {
		t.Fatalf("propiedades del pool: %q", out)
	}

	// Fila 21: volumen + carpeta SMB + instantánea + Versiones anteriores.
	v := createZvolume(t, a, p, "zv1", "")
	if v.Mount != "/zv1" || v.FS != "zfs" {
		t.Fatalf("volumen: %+v", v)
	}
	s := ensureShare(t, a, "Z-Fotos", v.ID)
	if out := onNAS(t, "zfs list -H -o name,mountpoint tze2e1/zv1/shares/Z-Fotos"); !strings.Contains(out, "/zv1/@shares/Z-Fotos") {
		t.Fatalf("la carpeta no es un dataset: %s", out)
	}
	f := filepath.Join(t.TempDir(), "foto.txt")
	_ = os.WriteFile(f, []byte("versión 1\n"), 0o600)
	if _, err := a.Upload(ctxT(t, time.Minute), f, "/"+s.Name, false); err != nil {
		t.Fatal(err)
	}
	var sr jobRef
	a.call("snapshots.create", map[string]string{"share": s.ID}, &sr)
	a.waitJob(sr.Job, time.Minute)
	var snaps []struct {
		ID string `json:"id"`
	}
	a.call("snapshots.list", map[string]string{"share": s.ID}, &snaps)
	if len(snaps) != 1 {
		t.Fatalf("instantáneas: %+v", snaps)
	}
	_ = os.WriteFile(f, []byte("versión 2, más larga\n"), 0o600)
	if _, err := a.Upload(ctxT(t, time.Minute), f, "/"+s.Name, true); err != nil {
		t.Fatal(err)
	}
	var vers []struct {
		Size int64 `json:"size"`
	}
	a.call("files.versions", map[string]string{"path": "/" + s.Name + "/foto.txt"}, &vers)
	if len(vers) != 1 || vers[0].Size != int64(len("versión 1\n")) {
		t.Fatalf("versiones del archivo: %+v", vers)
	}
	if out := onNAS(t, "ls -l /zv1/@snapshots/Z-Fotos/ /zv1/@snapshots/Z-Fotos/.gmt/; cat /zv1/@snapshots/Z-Fotos/*/foto.txt"); !strings.Contains(out, ".zfs/snapshot") || !strings.Contains(out, "versión 1") {
		t.Fatalf("enlaces de instantánea: %s", out)
	}
	pw := ensureTestUser(t, a)
	setUserAccess(t, a, s.ID, testUser, "rw")
	if out, err := smbAs(t, testUser, pw, s.Name, "allinfo foto.txt"); err != nil || !strings.Contains(out, "@GMT-") {
		t.Fatalf("Versiones anteriores por SMB: %v %s", err, out)
	}

	// Fila 22: zvol + LUN iSCSI de bloque.
	a.call("storage.zfs.zvols.create", map[string]any{"pool": p.ID, "name": "lun-e2e", "size": 256 << 20, "sparse": true}, nil)
	p, _ = a.zpool("tze2e1")
	if len(p.Zvols) != 1 || p.Zvols[0].Device != "/dev/zvol/tze2e1/zvol/lun-e2e" {
		t.Fatalf("zvol: %+v", p.Zvols)
	}
	var ir jobRef
	a.call("iscsi.targets.create", map[string]any{"name": "zfs-e2e", "zvol": p.Zvols[0].Device}, &ir)
	a.waitJob(ir.Job, 2*time.Minute)
	if out := onNAS(t, "cat /sys/kernel/config/target/core/iblock_0/zfs-e2e/udev_path; cat /sys/kernel/config/target/iscsi/iqn.2026-10.local.tango:zfs-e2e/tpgt_1/enable"); !strings.Contains(out, "/dev/zvol/tze2e1/zvol/lun-e2e") {
		t.Fatalf("LUN iblock: %s", out)
	}
	a.callErr("storage.zfs.zvols.delete", map[string]string{"pool": p.ID, "name": "lun-e2e"}, "VOLUME_BUSY")
	a.call("iscsi.targets.delete", map[string]string{"name": "zfs-e2e", "typed": "zfs-e2e"}, &ir)
	a.waitJob(ir.Job, 2*time.Minute)
	a.call("storage.zfs.zvols.delete", map[string]string{"pool": p.ID, "name": "lun-e2e"}, nil)

	// Fila 23: reemplazar S6 por M1 (más grande) con resilver.
	var old zfsLeaf
	for _, l := range p.Vdevs[0].Disks {
		if l.ID == a.diskIDs("TANGO-S6")[0] {
			old = l
		}
	}
	var rp zfsPlan
	a.call("storage.zfs.replacePlan", map[string]any{"pool": p.ID, "old": old.Name, "replacement": a.diskIDs("TANGO-M1")[0]}, &rp)
	var rr jobRef
	a.call("storage.zfs.replace", confirm(rp.PlanID, rp.ConfirmToken, "tze2e1"), &rr)
	a.waitJob(rr.Job, 10*time.Minute)
	p = waitZpool(t, a, "tze2e1", func(p zfsPool) bool { return p.Health == "ONLINE" && len(p.Vdevs[0].Disks) == 2 }, 5*time.Minute)
	if p.Vdevs[0].Disks[0].ID != a.diskIDs("TANGO-M1")[0] && p.Vdevs[0].Disks[1].ID != a.diskIDs("TANGO-M1")[0] {
		t.Fatalf("M1 no quedó en el espejo: %+v", p.Vdevs)
	}
	if out := onNAS(t, "cat /zv1/@shares/Z-Fotos/foto.txt"); !strings.Contains(out, "versión 2") {
		t.Fatalf("datos después del resilver: %s", out)
	}

	// Limpieza por la API: borrar el volumen (con su carpeta) y destruir el pool.
	deleteShare(t, a, s)
	var dp struct {
		PlanID       string `json:"plan_id"`
		ConfirmToken string `json:"confirm_token"`
	}
	a.call("storage.zfs.volumes.deletePlan", map[string]string{"volume": v.ID}, &dp)
	var dr jobRef
	a.call("storage.zfs.volumes.delete", confirm(dp.PlanID, dp.ConfirmToken, "zv1"), &dr)
	a.waitJob(dr.Job, 3*time.Minute)
	destroyZpool(t, a, "tze2e1")
	for _, d := range a.disks() {
		if (d.Serial == "TANGO-S3" || d.Serial == "TANGO-M1") && d.Use != "free" {
			t.Fatalf("%s no quedó libre: %+v", d.Serial, d)
		}
	}
}

// TestZfsRaidzEncryptionAuxImport: filas 24–26.
func TestZfsRaidzEncryptionAuxImport(t *testing.T) {
	destructive(t)
	a := admin(t)
	if !a.zfs().Enabled {
		t.Skip("ZFS no está habilitado (correr TestZfsEnable)")
	}
	zfsCleanup(t, "tze2e2")
	t.Cleanup(func() { zfsCleanup(t, "tze2e2"); _, _ = sshRun("rmdir /zenc /import/tze2e2 2>/dev/null; true") })

	// Fila 24: raidz1 M2 + D3 + D4 (4 + 16 + 16 GB: se usan 4 GB de cada uno).
	p, plan := createZpool(t, a, "tze2e2", "raidz1", []string{"TANGO-M2", "TANGO-D3", "TANGO-D4"})
	if plan.Summary.Tolerance != 1 || !approx(plan.Summary.Usable, 8*gb2/2, 3) {
		t.Fatalf("plan raidz1: %+v", plan.Summary)
	}
	if p.Layout != "raidz1" {
		t.Fatalf("layout %s", p.Layout)
	}
	// Caché L2ARC (C1) y SLOG (C2).
	for kind, ssd := range map[string]string{"cache": "TANGO-C1", "log": "TANGO-C2"} {
		var ap zfsPlan
		a.call("storage.zfs.addPlan", map[string]any{"pool": p.ID, "kind": kind, "disks": a.diskIDs(ssd)}, &ap)
		var r jobRef
		a.call("storage.zfs.add", confirm(ap.PlanID, ap.ConfirmToken, "tze2e2"), &r)
		a.waitJob(r.Job, 3*time.Minute)
	}
	p, _ = a.zpool("tze2e2")
	if len(p.Cache) != 1 || len(p.Logs) != 1 {
		t.Fatalf("caché/log: %+v %+v", p.Cache, p.Logs)
	}

	// Fila 25: volumen cifrado: bloquear, frase equivocada, desbloquear, cambiar la frase.
	pass := randomSecret(12)
	a.callErr("storage.zfs.volumes.create", map[string]any{"pool": p.ID, "name": "zenc", "encryption": map[string]any{"passphrase": "corta"}}, "INVALID_PARAMS")
	v := createZvolume(t, a, p, "zenc", pass)
	s := ensureShare(t, a, "Z-Secreto", v.ID)
	f := filepath.Join(t.TempDir(), "secreto.txt")
	_ = os.WriteFile(f, []byte("dentro de ZFS cifrado\n"), 0o600)
	if _, err := a.Upload(ctxT(t, time.Minute), f, "/"+s.Name, false); err != nil {
		t.Fatal(err)
	}
	if out := onNAS(t, "zfs get -H -o value encryption,keyformat tze2e2/zenc tze2e2/zenc/shares/Z-Secreto | tr '\\n' ' '"); !strings.Contains(out, "aes-256-gcm passphrase aes-256-gcm passphrase") {
		t.Fatalf("cifrado heredado: %q", out)
	}
	a.call("storage.volumes.lock", map[string]string{"volume": v.ID}, nil)
	if got := a.poolVolumes()["zenc"]; !got.Locked || got.Mount != "" {
		t.Fatalf("bloqueado: %+v", got)
	}
	if out := onNAS(t, "zfs get -H -o value keystatus,mounted tze2e2/zenc | tr '\\n' ' '"); strings.TrimSpace(out) != "unavailable no" {
		t.Fatalf("clave cargada o montado: %q", out)
	}
	a.callErr("storage.volumes.unlock", map[string]any{"volume": v.ID, "passphrase": "frase equivocada 12345"}, "WRONG_PASSPHRASE")
	a.call("storage.volumes.unlock", map[string]any{"volume": v.ID, "passphrase": pass}, nil)
	if out := onNAS(t, "cat /zenc/@shares/Z-Secreto/secreto.txt"); !strings.Contains(out, "cifrado") {
		t.Fatalf("después de desbloquear: %s", out)
	}
	pass2 := randomSecret(12)
	a.callErr("storage.volumes.passphrase", map[string]string{"volume": v.ID, "old": "no es esta 12345", "new": pass2}, "WRONG_PASSPHRASE")
	a.call("storage.volumes.passphrase", map[string]string{"volume": v.ID, "old": pass, "new": pass2}, nil)
	if os.Getenv("TANGO_E2E_REBOOT") == "1" {
		_, _ = sshRun("systemctl reboot")
		time.Sleep(20 * time.Second)
		waitSSH(t, 5*time.Minute)
		a = admin(t)
		if got := a.poolVolumes()["zenc"]; !got.Locked {
			t.Fatalf("después de reiniciar tendría que estar bloqueado: %+v", got)
		}
		if p, ok := a.zpool("tze2e2"); !ok || p.Health != "ONLINE" || len(p.Cache) != 1 {
			t.Fatalf("pool después de reiniciar: %+v", p)
		}
	} else {
		a.call("storage.volumes.lock", map[string]string{"volume": v.ID}, nil)
	}
	a.callErr("storage.volumes.unlock", map[string]any{"volume": v.ID, "passphrase": pass}, "WRONG_PASSPHRASE")
	a.call("storage.volumes.unlock", map[string]any{"volume": v.ID, "passphrase": pass2}, nil)
	if out := onNAS(t, "cat /zenc/@shares/Z-Secreto/secreto.txt"); !strings.Contains(out, "cifrado") {
		t.Fatalf("después de desbloquear con la frase nueva: %s", out)
	}
	for _, secret := range []string{pass, pass2} {
		if out := onNAS(t, "journalctl -u tangod -u tango-agent --since -60min -o cat | grep -c '"+secret+"'; grep -rlF '"+secret+"' /var/lib/tango /var/lib/tango-agent 2>/dev/null | wc -l"); strings.Fields(out)[0] != "0" || strings.Fields(out)[1] != "0" {
			t.Fatalf("la frase aparece en logs o estado: %s", out)
		}
	}

	// Scrub del pool (por la API de volúmenes: el de ZFS es del pool entero).
	var sr jobRef
	a.call("storage.scrub.start", map[string]string{"volume": v.ID}, &sr)
	a.waitJob(sr.Job, 10*time.Minute)
	p, _ = a.zpool("tze2e2")
	if p.Scan == nil || p.Scan.Function != "scrub" || p.Scan.State != "finished" || p.Scan.Errors != 0 {
		t.Fatalf("scrub: %+v", p.Scan)
	}

	// Fila 26: exportar, ver como importable, importar en solo lectura, exportar e
	// importar en lectura y escritura (los volúmenes vuelven a montarse).
	deleteShare(t, a, s)
	a.call("storage.volumes.lock", map[string]string{"volume": v.ID}, nil)
	var er jobRef
	a.call("storage.zfs.export", map[string]string{"pool": p.ID}, &er)
	a.waitJob(er.Job, 3*time.Minute)
	if _, ok := a.zpool("tze2e2"); ok {
		t.Fatal("sigue importado después de exportar")
	}
	var imp []struct {
		Name  string   `json:"name"`
		GUID  string   `json:"guid"`
		Disks []string `json:"disks"`
	}
	a.call("storage.zfs.importable", nil, &imp)
	found := false
	for _, im := range imp {
		found = found || (im.Name == "tze2e2" && len(im.Disks) >= 3)
	}
	if !found {
		t.Fatalf("no aparece como importable: %+v", imp)
	}
	var ir jobRef
	a.call("storage.zfs.import", map[string]any{"pool": "tze2e2", "readonly": true}, &ir)
	a.waitJob(ir.Job, 3*time.Minute)
	if p, ok := a.zpool("tze2e2"); !ok || !p.ReadOnly {
		t.Fatalf("importado en solo lectura: %+v", p)
	}
	a.call("storage.zfs.export", map[string]string{"pool": "tze2e2"}, &er)
	a.waitJob(er.Job, 3*time.Minute)
	a.call("storage.zfs.import", map[string]any{"pool": "tze2e2", "readonly": false}, &ir)
	a.waitJob(ir.Job, 3*time.Minute)
	if got := a.poolVolumes()["zenc"]; !got.Locked {
		t.Fatalf("zenc tendría que volver bloqueado: %+v", got)
	}
	destroyZpool(t, a, "tze2e2")
	for _, d := range a.disks() {
		for _, s := range []string{"TANGO-M2", "TANGO-D3", "TANGO-D4", "TANGO-C1", "TANGO-C2"} {
			if d.Serial == s && d.Use != "free" {
				t.Fatalf("%s no quedó libre: %+v", s, d)
			}
		}
	}
}

func waitSSH(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if out, err := sshRun("systemctl is-active tangod"); err == nil && strings.TrimSpace(out) == "active" {
			time.Sleep(5 * time.Second)
			return
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatal("la VM no volvió")
}
