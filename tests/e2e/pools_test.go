//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Grupos de almacenamiento (protocol/README.md §16, ADR 0011, docs/14 filas 11–18).
// Usa SOLO discos libres del laboratorio: S3, S6 (2 GB), M1, M2 (4 GB), C1 y C2 (SSD de
// 2 GB) y KEY (1 GB con una partición FAT: hace de llave USB). volume1 y volume2 no se tocan.

type poolTier struct {
	Index   int      `json:"index"`
	Level   string   `json:"level"`
	Size    uint64   `json:"size"`
	Usable  uint64   `json:"usable"`
	Members []string `json:"members"`
	State   string   `json:"state"`
}

type pool struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Profile    string     `json:"profile"`
	State      string     `json:"state"`
	Size       uint64     `json:"size"`
	Free       uint64     `json:"free"`
	Disks      []string   `json:"disks"`
	CacheDisks []string   `json:"cacheDisks"`
	Tiers      []poolTier `json:"tiers"`
	Volumes    []string   `json:"volumes"`
	Task       string     `json:"task"`
}

type poolPlan struct {
	PlanID       string `json:"plan_id"`
	ConfirmToken string `json:"confirm_token"`
	Summary      struct {
		Usable uint64     `json:"usable"`
		Unused uint64     `json:"unused"`
		Tiers  []poolTier `json:"tiers"`
		Notes  []string   `json:"notes"`
	} `json:"summary"`
}

type poolVolume struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Mount     string `json:"mount"`
	Kind      string `json:"kind"`
	Pool      string `json:"pool"`
	FS        string `json:"fs"`
	Size      uint64 `json:"size"`
	State     string `json:"state"`
	Encrypted bool   `json:"encrypted"`
	Locked    bool   `json:"locked"`
	KeyDevice string `json:"keyDevice"`
	Cache     *struct {
		Mode     string   `json:"mode"`
		Disks    []string `json:"disks"`
		Size     uint64   `json:"size"`
		HitRatio *float64 `json:"hitRatio"`
	} `json:"cache"`
}

func (c *client) pools() map[string]pool {
	c.t.Helper()
	var ps []pool
	c.call("storage.pools.list", nil, &ps)
	m := map[string]pool{}
	for _, p := range ps {
		m[p.Name] = p
	}
	return m
}

func (c *client) poolVolumes() map[string]poolVolume {
	c.t.Helper()
	var vs []poolVolume
	c.call("storage.volumes.list", nil, &vs)
	m := map[string]poolVolume{}
	for _, v := range vs {
		m[v.Name] = v
	}
	return m
}

func confirm(planID, token, name string) map[string]string {
	return map[string]string{"plan_id": planID, "confirm_token": token, "typed_name": name}
}

func createPool(t *testing.T, a *client, name, profile string, serials []string) (pool, poolPlan) {
	t.Helper()
	requireFree(t, a, serials...)
	var p poolPlan
	a.call("storage.pools.plan", map[string]any{"name": name, "profile": profile, "disks": a.diskIDs(serials...)}, &p)
	a.callErr("storage.pools.create", confirm(p.PlanID, p.ConfirmToken, name+"x"), "CONFIRMATION_MISMATCH")
	var r jobRef
	a.call("storage.pools.create", confirm(p.PlanID, p.ConfirmToken, name), &r)
	a.waitJob(r.Job, 10*time.Minute)
	pl, ok := a.pools()[name]
	if !ok || pl.Profile != profile {
		t.Fatalf("el grupo %s no aparece: %+v", name, a.pools())
	}
	return pl, p
}

func createPoolVolume(t *testing.T, a *client, pl pool, name, fs string, size uint64, enc map[string]any) poolVolume {
	t.Helper()
	params := map[string]any{"pool": pl.ID, "name": name, "size": size, "fs": fs, "encryption": enc}
	var r jobRef
	a.call("storage.poolVolumes.create", params, &r)
	a.waitJob(r.Job, 10*time.Minute)
	v, ok := a.poolVolumes()[name]
	if !ok || v.Kind != "pool" || v.Pool != pl.ID || v.Mount != "/"+name || v.FS != fs {
		t.Fatalf("volumen %s: %+v", name, v)
	}
	if got := strings.Fields(onNAS(t, "findmnt -n -o FSTYPE /"+name)); len(got) == 0 || got[0] != fs {
		t.Fatalf("/%s montado como %v, esperaba %s", name, got, fs)
	}
	return v
}

func deletePoolVolume(t *testing.T, a *client, name string) {
	t.Helper()
	v, ok := a.poolVolumes()[name]
	if !ok {
		return
	}
	var dp planOut
	a.call("storage.volumes.deletePlan", map[string]string{"volume": v.ID}, &dp)
	if len(dp.Summary.Disks) != 0 {
		t.Fatalf("borrar un volumen de un grupo no borra discos: %+v", dp.Summary.Disks)
	}
	var r jobRef
	a.call("storage.volumes.delete", confirm(dp.PlanID, dp.ConfirmToken, name), &r)
	a.waitJob(r.Job, 5*time.Minute)
	if _, still := a.poolVolumes()[name]; still {
		t.Fatalf("%s sigue listado", name)
	}
}

func deletePool(t *testing.T, a *client, name string, serials []string) {
	t.Helper()
	pl, ok := a.pools()[name]
	if !ok {
		return
	}
	if len(pl.Volumes) > 0 {
		a.callErr("storage.pools.deletePlan", map[string]string{"pool": pl.ID}, "POOL_NOT_EMPTY")
		return
	}
	var dp planOut
	a.call("storage.pools.deletePlan", map[string]string{"pool": pl.ID}, &dp)
	var r jobRef
	a.call("storage.pools.delete", confirm(dp.PlanID, dp.ConfirmToken, name), &r)
	a.waitJob(r.Job, 5*time.Minute)
	deadline := time.Now().Add(30 * time.Second)
	for {
		ds := a.disks()
		var busy []string
		for _, s := range serials {
			if ds[s].Use != "free" {
				busy = append(busy, s+"="+ds[s].Use)
			}
		}
		if len(busy) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("discos no liberados: %v", busy)
		}
		time.Sleep(2 * time.Second)
	}
	if out := onNAS(t, "vgs --noheadings -o vg_name 2>/dev/null; grep -c "+name+"_ /etc/mdadm/mdadm.conf || true"); strings.Contains(out, name) {
		t.Fatalf("quedaron restos del grupo %s: %s", name, out)
	}
}

// waitPoolHealthy espera a que el grupo termine su sincronización inicial o reshape.
func waitPoolHealthy(t *testing.T, a *client, name string, timeout time.Duration) pool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var p pool
	for time.Now().Before(deadline) {
		p = a.pools()[name]
		if p.State == "healthy" && p.Task == "" {
			return p
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("el grupo %s no quedó sano en %s: %+v", name, timeout, p)
	return p
}

// cleanupPool borra (si quedaron de una corrida anterior) los volúmenes y el grupo.
func cleanupPool(t *testing.T, name string, vols, serials []string) {
	t.Helper()
	a := admin(t)
	for _, v := range vols {
		if pv, ok := a.poolVolumes()[v]; ok && pv.Locked {
			continue // bloqueado sin frase conocida: no se puede limpiar acá
		}
		if sh, ok := a.shares()["M-"+v]; ok {
			deleteShare(t, a, sh)
		}
		deletePoolVolume(t, a, v)
	}
	deletePool(t, a, name, serials)
}

func approx(got, want uint64, pct uint64) bool {
	return got >= want*(100-pct)/100 && got <= want*(100+pct)/100
}

// Fila 11: grupo RAID 5 con dos volúmenes (Btrfs y ext4), carpeta, reinicio de tangod, borrado.
func TestPoolRAID5TwoVolumes(t *testing.T) {
	destructive(t)
	a := admin(t)
	disks := []string{"TANGO-S3", "TANGO-S6", "TANGO-M1"}
	cleanupPool(t, "pe2eraid", []string{"pv1", "pv2"}, disks)
	t.Cleanup(func() { cleanupPool(t, "pe2eraid", []string{"pv1", "pv2"}, disks) })
	pl, plan := createPool(t, a, "pe2eraid", "raid5", disks)
	if !approx(plan.Summary.Usable, 2*gb2, 3) || !strings.Contains(strings.Join(plan.Summary.Notes, " "), "híbrido") {
		t.Fatalf("plan RAID 5: útil %d, notas %v", plan.Summary.Usable, plan.Summary.Notes)
	}
	v1 := createPoolVolume(t, a, pl, "pv1", "btrfs", 1536<<20, nil)
	createPoolVolume(t, a, pl, "pv2", "ext4", 1<<30, nil)
	waitPoolHealthy(t, a, "pe2eraid", 10*time.Minute)
	// Sin espacio para un tercero de 3 GB.
	a.callErr("storage.poolVolumes.create", map[string]any{"pool": pl.ID, "name": "pv3", "size": 3 << 30, "fs": "btrfs"}, "NO_SPACE")
	b := writeAndRestart(t, a, volume{ID: v1.ID, Name: "pv1"})
	deleteShare(t, b, b.shares()["M-pv1"])
	a = b
	// Un grupo con volúmenes no se borra.
	a.callErr("storage.pools.deletePlan", map[string]string{"pool": pl.ID}, "POOL_NOT_EMPTY")
}

// Filas 12–17: híbrido con discos mixtos, cifrado con llave, ampliación, caché y tamaño.
func TestPoolHybridLUKSCache(t *testing.T) {
	destructive(t)
	a := admin(t)
	disks := []string{"TANGO-S3", "TANGO-S6", "TANGO-M1"}
	all := append(append([]string{}, disks...), "TANGO-M2")
	cleanupPool(t, "pe2ehyb", []string{"pv3"}, all)
	t.Cleanup(func() { cleanupPool(t, "pe2ehyb", []string{"pv3"}, all) })

	// Fila 12: Híbrido-1 con 2+2+4 GB → RAID 5 de 3×2 (4 GB) y 2 GB reservados.
	pl, plan := createPool(t, a, "pe2ehyb", "hybrid1", disks)
	if len(plan.Summary.Tiers) != 2 || plan.Summary.Tiers[0].Level != "raid5" || plan.Summary.Tiers[1].Level != "unused" ||
		!approx(plan.Summary.Usable, 2*gb2, 3) || !approx(plan.Summary.Unused, gb2, 3) {
		t.Fatalf("plan híbrido: %+v", plan.Summary)
	}
	if out := onNAS(t, "lsblk -no PARTLABEL /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_drive-scsi11 | grep -c tg-pe2ehyb-t"); strings.TrimSpace(out) != "2" {
		t.Fatalf("M1 tendría que tener 2 particiones de tramo: %q", out)
	}

	// Fila 13: volumen cifrado con frase y archivo de clave en la "llave".
	var keys []struct {
		UUID string `json:"uuid"`
		FS   string `json:"fs"`
	}
	a.call("storage.keyDevices.list", nil, &keys)
	if len(keys) == 0 {
		t.Skip("no hay un dispositivo FAT libre para la llave (disco TANGO-KEY)")
	}
	key := keys[0].UUID
	pass := randomSecret(12)
	a.callErr("storage.poolVolumes.create", map[string]any{"pool": pl.ID, "name": "pv3", "size": 2 << 30, "fs": "btrfs", "encryption": map[string]any{"passphrase": "corta"}}, "INVALID_PARAMS")
	v := createPoolVolume(t, a, pl, "pv3", "btrfs", 2<<30, map[string]any{"passphrase": pass, "keyDevice": key})
	if !v.Encrypted || v.Locked || !strings.EqualFold(v.KeyDevice, key) {
		t.Fatalf("pv3: %+v", v)
	}
	s := ensureShare(t, a, "M-pv3", v.ID)
	f := filepath.Join(t.TempDir(), "secreto.txt")
	_ = os.WriteFile(f, []byte("dentro de LUKS\n"), 0o600)
	if _, err := a.Upload(ctxT(t, time.Minute), f, "/"+s.Name, false); err != nil {
		t.Fatal(err)
	}
	sum := onNAS(t, "sha256sum /pv3/@shares/M-pv3/secreto.txt | cut -c1-64")

	// Bloquear: desmonta y cierra.
	a.call("storage.volumes.lock", map[string]string{"volume": v.ID}, nil)
	if got := a.poolVolumes()["pv3"]; !got.Locked || got.State != "locked" || got.Mount != "" {
		t.Fatalf("bloqueado: %+v", got)
	}
	if out, _ := sshRun("ls /dev/mapper/tgc_pv3 2>&1; findmnt -n /pv3; true"); !strings.Contains(out, "No such file") {
		t.Fatalf("quedó abierto o montado: %s", out)
	}
	a.callErr("storage.volumes.unlock", map[string]any{"volume": v.ID, "passphrase": "frase equivocada 12345"}, "WRONG_PASSPHRASE")
	// Con la llave.
	a.call("storage.volumes.unlock", map[string]any{"volume": v.ID, "useKeyDevice": true}, nil)
	if got := strings.TrimSpace(onNAS(t, "sha256sum /pv3/@shares/M-pv3/secreto.txt | cut -c1-64")); got != strings.TrimSpace(sum) {
		t.Fatalf("archivo distinto después de desbloquear: %s vs %s", got, sum)
	}
	// Con la frase (y cambio de frase).
	a.call("storage.volumes.lock", map[string]string{"volume": v.ID}, nil)
	a.call("storage.volumes.unlock", map[string]any{"volume": v.ID, "passphrase": pass}, nil)
	pass2 := randomSecret(12)
	a.call("storage.volumes.passphrase", map[string]string{"volume": v.ID, "old": pass, "new": pass2}, nil)
	a.call("storage.volumes.lock", map[string]string{"volume": v.ID}, nil)
	a.callErr("storage.volumes.unlock", map[string]any{"volume": v.ID, "passphrase": pass}, "WRONG_PASSPHRASE")
	a.call("storage.volumes.unlock", map[string]any{"volume": v.ID, "passphrase": pass2}, nil)
	// Ninguna frase en logs, journal del agente ni auditoría.
	for _, secret := range []string{pass, pass2} {
		if out := onNAS(t, "journalctl -u tangod -u tango-agent --since -30min -o cat | grep -c '"+secret+"'; grep -rlF '"+secret+"' /var/lib/tango /var/lib/tango-agent 2>/dev/null | wc -l"); strings.Fields(out)[0] != "0" || strings.Fields(out)[1] != "0" {
			t.Fatalf("la frase aparece en logs o estado: %s", out)
		}
	}

	waitPoolHealthy(t, a, "pe2ehyb", 10*time.Minute)
	// Fila 14: ampliar con M2 (4 GB) → el RAID 5 pasa a 4 discos y el tramo de arriba es RAID 1.
	var ep poolPlan
	a.call("storage.pools.expandPlan", map[string]any{"pool": pl.ID, "add": a.diskIDs("TANGO-M2")}, &ep)
	if !approx(ep.Summary.Usable, 4*gb2, 3) || len(ep.Summary.Tiers) != 2 || ep.Summary.Tiers[1].Level != "raid1" {
		t.Fatalf("plan de ampliación: %+v", ep.Summary)
	}
	var r jobRef
	a.call("storage.pools.expand", confirm(ep.PlanID, ep.ConfirmToken, "pe2ehyb"), &r)
	a.waitJob(r.Job, 30*time.Minute)
	after := waitPoolHealthy(t, a, "pe2ehyb", 10*time.Minute)
	if !approx(after.Size, 4*gb2, 3) || len(after.Disks) != 4 {
		t.Fatalf("después de ampliar: %+v", after)
	}
	if got := strings.TrimSpace(onNAS(t, "sha256sum /pv3/@shares/M-pv3/secreto.txt | cut -c1-64")); got != strings.TrimSpace(sum) {
		t.Fatal("el archivo cambió con la ampliación")
	}

	// Fila 15: agrandar el volumen cifrado en línea.
	a.call("storage.poolVolumes.resize", map[string]any{"volume": v.ID, "size": 3 << 30}, &r)
	a.waitJob(r.Job, 5*time.Minute)
	if got := a.poolVolumes()["pv3"]; got.Size < (3<<30)*9/10 {
		t.Fatalf("no creció: %+v", got)
	}

	// Fila 16: caché de lectura con 1 SSD y de lectura/escritura con 2 (RAID 1).
	a.callErr("storage.cache.plan", map[string]any{"volume": v.ID, "disks": a.diskIDs("TANGO-M2"), "mode": "read"}, "")
	for _, c := range []struct {
		mode  string
		disks []string
	}{{"read", []string{"TANGO-C1"}}, {"readwrite", []string{"TANGO-C1", "TANGO-C2"}}} {
		var cp poolPlan
		a.call("storage.cache.plan", map[string]any{"volume": v.ID, "disks": a.diskIDs(c.disks...), "mode": c.mode}, &cp)
		a.call("storage.cache.create", confirm(cp.PlanID, cp.ConfirmToken, "pv3"), &r)
		a.waitJob(r.Job, 5*time.Minute)
		onNAS(t, "cat /pv3/@shares/M-pv3/secreto.txt >/dev/null; sync")
		got := a.poolVolumes()["pv3"]
		if got.Cache == nil || got.Cache.Mode != c.mode || len(got.Cache.Disks) != len(c.disks) {
			t.Fatalf("caché %s: %+v", c.mode, got.Cache)
		}
		a.call("storage.cache.remove", map[string]string{"volume": v.ID}, &r)
		a.waitJob(r.Job, 5*time.Minute)
		if got := a.poolVolumes()["pv3"]; got.Cache != nil {
			t.Fatalf("la caché sigue: %+v", got.Cache)
		}
		requireFree(t, a, c.disks...)
	}

	// Fila 17 (opcional): reinicio de la VM con la llave conectada → se desbloquea solo.
	if os.Getenv("TANGO_E2E_REBOOT") == "1" {
		_, _ = sshRun("systemctl reboot")
		time.Sleep(20 * time.Second)
		deadline := time.Now().Add(5 * time.Minute)
		for {
			if _, err := sshRun("systemctl is-active tangod"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("la VM no volvió")
			}
			time.Sleep(5 * time.Second)
		}
		a = admin(t)
		var got poolVolume
		for i := 0; i < 30; i++ {
			got = a.poolVolumes()["pv3"]
			if !got.Locked && got.Mount == "/pv3" {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if got.Locked || got.Mount != "/pv3" {
			t.Fatalf("después de reiniciar con la llave conectada: %+v", got)
		}
		if g := strings.TrimSpace(onNAS(t, "sha256sum /pv3/@shares/M-pv3/secreto.txt | cut -c1-64")); g != strings.TrimSpace(sum) {
			t.Fatal("el archivo cambió con el reinicio")
		}
		if p := a.pools()["pe2ehyb"]; p.State != "healthy" && p.State != "building" {
			t.Fatalf("grupo después del reinicio: %+v", p)
		}
	}
	deleteShare(t, a, a.shares()["M-pv3"])
	// La llave queda en el dispositivo: se quita la ranura antes de borrar.
	a.call("storage.volumes.keyDevice", map[string]any{"volume": v.ID, "passphrase": pass2, "device": nil}, nil)
}
