//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Matriz de almacenamiento (docs/14 filas 1–10). Usa SOLO discos libres del laboratorio:
// S3, S6 (2 GB), M1, M2 (4 GB). volume1 (D1+D2) y volume2 (S1,S2,S4,S5) no se tocan.
var freeSmall = []string{"TANGO-S3", "TANGO-S6", "TANGO-M1", "TANGO-M2"}

type planOut struct {
	PlanID       string `json:"plan_id"`
	ConfirmToken string `json:"confirm_token"`
	Summary      struct {
		Name    string   `json:"name"`
		Profile string   `json:"profile"`
		Usable  uint64   `json:"usable"`
		Notes   []string `json:"notes"`
		Disks   []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"disks"`
	} `json:"summary"`
}

func requireFree(t *testing.T, a *client, serials ...string) {
	t.Helper()
	ds := a.disks()
	for _, s := range serials {
		if d := ds[s]; d.Use != "free" {
			t.Fatalf("el disco %s no está libre (%s %s): la matriz no puede seguir", s, d.Use, d.Volume)
		}
	}
}

// createVolume corre plan → confirmación → job → healthy y verifica md/montaje en el NAS.
func createVolume(t *testing.T, a *client, name, profile string, serials []string, wantUsable uint64, wantNotes bool) volume {
	t.Helper()
	requireFree(t, a, serials...)
	var p planOut
	a.call("storage.volumes.plan", map[string]any{"name": name, "profile": profile, "disks": a.diskIDs(serials...)}, &p)
	if p.Summary.Profile != profile || len(p.Summary.Disks) != len(serials) {
		t.Fatalf("plan inesperado: %+v", p.Summary)
	}
	if wantUsable > 0 && (p.Summary.Usable < wantUsable*85/100 || p.Summary.Usable > wantUsable*101/100) {
		t.Fatalf("%s: capacidad útil %d, esperaba ~%d", profile, p.Summary.Usable, wantUsable)
	}
	if wantNotes && len(p.Summary.Notes) == 0 {
		t.Fatalf("%s con discos de distinto tamaño sin nota", profile)
	}
	// Nombre mal escrito → no crea.
	a.callErr("storage.volumes.create", map[string]string{"plan_id": p.PlanID, "confirm_token": p.ConfirmToken, "typed_name": name + "x"}, "CONFIRMATION_MISMATCH")
	var r jobRef
	a.call("storage.volumes.create", map[string]string{"plan_id": p.PlanID, "confirm_token": p.ConfirmToken, "typed_name": name}, &r)
	start := time.Now()
	a.waitJob(r.Job, 10*time.Minute)
	v := a.waitVolume(name, "healthy", 15*time.Minute)
	t.Logf("%s %s creado y sano en %s (tamaño %d)", name, profile, time.Since(start).Round(time.Second), v.Size)
	// Plan de un solo uso.
	a.callErr("storage.volumes.create", map[string]string{"plan_id": p.PlanID, "confirm_token": p.ConfirmToken, "typed_name": name}, "")
	// Verificación en el NAS.
	mnt := onNAS(t, "findmnt -n -o FSTYPE,SOURCE /"+name)
	if !strings.HasPrefix(strings.TrimSpace(mnt), "btrfs") {
		t.Fatalf("/%s no está montado en btrfs: %q", name, mnt)
	}
	if profile != "basic" {
		det := onNAS(t, "mdadm --detail /dev/md/"+name)
		if !strings.Contains(det, "Raid Level : "+profile) || !(strings.Contains(det, "State : clean") || strings.Contains(det, "State : active")) {
			t.Fatalf("mdadm --detail inesperado:\n%s", det)
		}
	}
	return v
}

// writeAndRestart crea una carpeta en el volumen, sube un archivo, reinicia tangod y verifica.
func writeAndRestart(t *testing.T, a *client, v volume) *client {
	t.Helper()
	s := ensureShare(t, a, "M-"+v.Name, v.ID)
	f := filepath.Join(t.TempDir(), "prueba.txt")
	_ = os.WriteFile(f, []byte("matriz "+v.Name+"\n"), 0o600)
	if _, err := a.Upload(ctxT(t, time.Minute), f, "/"+s.Name, false); err != nil {
		t.Fatal(err)
	}
	onNAS(t, "systemctl restart tangod")
	time.Sleep(3 * time.Second)
	b := admin(t)
	if got := b.volumes()[v.Name]; got.State != "healthy" {
		t.Fatalf("después de reiniciar tangod: %+v", got)
	}
	if _, ok := b.ls("/" + s.Name)["prueba.txt"]; !ok {
		t.Fatal("el archivo no está después de reiniciar tangod")
	}
	return b
}

// deleteVolume corre deletePlan → confirmación → job y verifica que los discos quedan libres.
func deleteVolume(t *testing.T, a *client, name string, serials []string) {
	t.Helper()
	v, ok := a.volumes()[name]
	if !ok {
		return
	}
	var dp planOut
	a.call("storage.volumes.deletePlan", map[string]string{"volume": v.ID}, &dp)
	a.callErr("storage.volumes.delete", map[string]string{"plan_id": dp.PlanID, "confirm_token": dp.ConfirmToken, "typed_name": "otro"}, "CONFIRMATION_MISMATCH")
	var r jobRef
	a.call("storage.volumes.delete", map[string]string{"plan_id": dp.PlanID, "confirm_token": dp.ConfirmToken, "typed_name": name}, &r)
	a.waitJob(r.Job, 5*time.Minute)
	a.waitVolume(name, "", time.Minute)
	if out, _ := sshRun("test -e /dev/md/" + name + " && echo QUEDA; findmnt -n /" + name + " && echo MONTADO; true"); strings.Contains(out, "QUEDA") || strings.Contains(out, "MONTADO") {
		t.Fatalf("quedaron restos de %s: %s", name, out)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		ds := a.disks()
		busy := []string{}
		for _, s := range serials {
			if ds[s].Use != "free" {
				busy = append(busy, s+"="+ds[s].Use)
			}
		}
		if len(busy) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("discos no liberados: %v", busy)
		}
		time.Sleep(2 * time.Second)
	}
}

const gb2 = uint64(2) << 30

func TestStorageMatrix(t *testing.T) {
	destructive(t)
	a := admin(t)
	requireFree(t, a, freeSmall...)
	// Seguridad: si la corrida se corta, intentar limpiar todo lo que la matriz creó.
	t.Cleanup(func() {
		c := admin(t)
		for name := range c.volumes() {
			if strings.HasPrefix(name, "e2em") {
				deleteVolume(t, c, name, nil)
			}
		}
	})

	t.Run("1-basic", func(t *testing.T) {
		a := admin(t) // sesión nueva por fila: las filas reinician tangod
		v := createVolume(t, a, "e2em1", "basic", []string{"TANGO-S3"}, gb2, false)
		b := writeAndRestart(t, a, v)
		deleteVolume(t, b, "e2em1", []string{"TANGO-S3"})
	})
	t.Run("2-raid1", func(t *testing.T) {
		a := admin(t) // sesión nueva por fila: las filas reinician tangod
		v := createVolume(t, a, "e2em2", "raid1", []string{"TANGO-S3", "TANGO-S6"}, gb2, false)
		b := writeAndRestart(t, a, v)
		deleteVolume(t, b, "e2em2", []string{"TANGO-S3", "TANGO-S6"})
	})
	t.Run("3+6-raid5-mixto", func(t *testing.T) {
		a := admin(t) // sesión nueva por fila: las filas reinician tangod
		disks := []string{"TANGO-S3", "TANGO-S6", "TANGO-M1"}
		v := createVolume(t, a, "e2em3", "raid5", disks, 2*gb2, true)
		b := writeAndRestart(t, a, v)
		deleteVolume(t, b, "e2em3", disks)
	})
	t.Run("4-raid6", func(t *testing.T) {
		a := admin(t) // sesión nueva por fila: las filas reinician tangod
		v := createVolume(t, a, "e2em4", "raid6", freeSmall, 2*gb2, true)
		b := writeAndRestart(t, a, v)
		deleteVolume(t, b, "e2em4", freeSmall)
	})
	t.Run("5-raid10", func(t *testing.T) {
		a := admin(t) // sesión nueva por fila: las filas reinician tangod
		v := createVolume(t, a, "e2em5", "raid10", freeSmall, 2*gb2, true)
		b := writeAndRestart(t, a, v)
		deleteVolume(t, b, "e2em5", freeSmall)
	})
	t.Run("7-reemplazo", func(t *testing.T) {
		a := admin(t) // sesión nueva por fila: las filas reinician tangod
		disks := []string{"TANGO-S3", "TANGO-S6", "TANGO-M1"}
		v := createVolume(t, a, "e2em7", "raid5", disks, 2*gb2, true)
		dev := a.disks()["TANGO-S6"].Dev
		onNAS(t, fmt.Sprintf("mdadm --manage /dev/md/e2em7 --fail /dev/%s", dev))
		a.waitVolume("e2em7", "degraded", 2*time.Minute)
		waitAlert(t, a, "e2em7", true, 2*time.Minute)
		failed := a.disks()["TANGO-S6"].ID
		var p planOut
		a.call("storage.disks.replacePlan", map[string]any{"volume": v.ID, "failed": failed, "replacement": a.disks()["TANGO-M2"].ID}, &p)
		a.callErr("storage.disks.replacePlan", map[string]any{"volume": v.ID, "failed": failed, "replacement": a.disks()["TANGO-D1"].ID}, "")
		var r jobRef
		a.call("storage.disks.replace", map[string]string{"plan_id": p.PlanID, "confirm_token": p.ConfirmToken, "typed_name": "e2em7"}, &r)
		a.waitJob(r.Job, 15*time.Minute)
		a.waitVolume("e2em7", "healthy", 15*time.Minute)
		waitAlert(t, a, "e2em7", false, 2*time.Minute)
		if u := a.disks()["TANGO-S6"].Use; u != "free" {
			t.Fatalf("el disco reemplazado no quedó libre: %s", u)
		}
		deleteVolume(t, a, "e2em7", []string{"TANGO-S3", "TANGO-M1", "TANGO-M2"})
	})
	t.Run("8-ampliacion", func(t *testing.T) {
		a := admin(t) // sesión nueva por fila: las filas reinician tangod
		disks := []string{"TANGO-S3", "TANGO-S6", "TANGO-M1"}
		v := createVolume(t, a, "e2em8", "raid5", disks, 2*gb2, true)
		before := v.Size
		var p planOut
		a.call("storage.volumes.expandPlan", map[string]any{"volume": v.ID, "add": a.diskIDs("TANGO-M2")}, &p)
		var r jobRef
		a.call("storage.volumes.expand", map[string]string{"plan_id": p.PlanID, "confirm_token": p.ConfirmToken, "typed_name": "e2em8"}, &r)
		a.waitJob(r.Job, 30*time.Minute)
		after := a.waitVolume("e2em8", "healthy", 30*time.Minute).Size
		if after < before*14/10 {
			t.Fatalf("la capacidad no creció: %d → %d", before, after)
		}
		t.Logf("ampliación: %d → %d bytes", before, after)
		deleteVolume(t, a, "e2em8", freeSmall)
	})
	t.Run("10-negativos", func(t *testing.T) {
		a := admin(t) // sesión nueva por fila: las filas reinician tangod
		ds := a.disks()
		var sys, member string
		for _, d := range ds {
			if d.Use == "system" {
				sys = d.ID
			}
		}
		member = ds["TANGO-D1"].ID
		a.callErr("storage.volumes.plan", map[string]any{"name": "e2emneg", "profile": "basic", "disks": []string{sys}}, "DEVICE_NOT_ELIGIBLE")
		a.callErr("storage.volumes.plan", map[string]any{"name": "e2emneg", "profile": "basic", "disks": []string{member}}, "DEVICE_NOT_ELIGIBLE")
		a.callErr("storage.volumes.plan", map[string]any{"name": "e2emneg", "profile": "raid10", "disks": a.diskIDs("TANGO-S3", "TANGO-S6", "TANGO-M1")}, "")
		a.callErr("storage.volumes.plan", map[string]any{"name": "e2emneg", "profile": "raid6", "disks": a.diskIDs("TANGO-S3", "TANGO-S6", "TANGO-M1")}, "")
		a.callErr("storage.volumes.plan", map[string]any{"name": "e2emneg", "profile": "basic", "disks": a.diskIDs("TANGO-S3", "TANGO-S6")}, "")
		a.callErr("storage.volumes.plan", map[string]any{"name": "volume1", "profile": "basic", "disks": a.diskIDs("TANGO-S3")}, "")
		a.callErr("storage.volumes.plan", map[string]any{"name": "../etc", "profile": "basic", "disks": a.diskIDs("TANGO-S3")}, "")
		var p planOut
		a.call("storage.volumes.plan", map[string]any{"name": "e2emneg", "profile": "basic", "disks": a.diskIDs("TANGO-S3")}, &p)
		a.callErr("storage.volumes.create", map[string]string{"plan_id": p.PlanID, "confirm_token": p.ConfirmToken[:len(p.ConfirmToken)-2] + "zz", "typed_name": "e2emneg"}, "CONFIRMATION_MISMATCH")
		a.callErr("storage.volumes.create", map[string]string{"plan_id": "p_inexistente", "confirm_token": p.ConfirmToken, "typed_name": "e2emneg"}, "PLAN_EXPIRED")
		a.callErr("storage.volumes.expandPlan", map[string]any{"volume": a.volumes()["volume1"].ID, "add": a.diskIDs("TANGO-S3")}, "")
		requireFree(t, a, freeSmall...)
	})
}

// waitAlert espera una alerta activa (o resuelta) que mencione al volumen.
func waitAlert(t *testing.T, a *client, vol string, active bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var al []struct {
			Title    string `json:"title"`
			Body     string `json:"body"`
			Level    string `json:"level"`
			Resolved bool   `json:"resolved"`
		}
		a.call("alerts.list", nil, &al)
		found := false
		for _, x := range al {
			if strings.Contains(x.Title+x.Body, vol) && !x.Resolved {
				found = true
			}
		}
		if found == active {
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("alerta de %s activa=%v no llegó en %s", vol, active, timeout)
}

// TestStorageReboot (fila 9): RAID 1 + carpeta + archivo, reinicio de la VM entera y
// verificación de md ensamblado, montaje, SMB y apps. Va al final de la corrida.
func TestStorageReboot(t *testing.T) {
	destructive(t)
	if os.Getenv("TANGO_E2E_REBOOT") != "1" {
		t.Skip("reinicio de la VM: exportá TANGO_E2E_REBOOT=1")
	}
	a := admin(t)
	disks := []string{"TANGO-S3", "TANGO-S6"}
	v := createVolume(t, a, "e2em9", "raid1", disks, gb2, false)
	s := ensureShare(t, a, "M-e2em9", v.ID)
	f := filepath.Join(t.TempDir(), "persiste.txt")
	_ = os.WriteFile(f, []byte("sobrevive al reinicio\n"), 0o600)
	if _, err := a.Upload(ctxT(t, time.Minute), f, "/"+s.Name, false); err != nil {
		t.Fatal(err)
	}
	a.callErr("system.reboot", map[string]string{"typed": "reiniciar"}, "")
	a.call("system.reboot", map[string]string{"typed": "REINICIAR"}, nil)
	time.Sleep(20 * time.Second)
	deadline := time.Now().Add(5 * time.Minute)
	for {
		if _, err := sshRun("systemctl is-active tangod"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("la VM no volvió en 5 minutos")
		}
		time.Sleep(5 * time.Second)
	}
	time.Sleep(10 * time.Second)
	b := admin(t)
	vols := b.volumes()
	for _, n := range []string{"volume1", "volume2", "e2em9"} {
		if vols[n].State != "healthy" {
			t.Errorf("después del reinicio %s está %q", n, vols[n].State)
		}
	}
	if _, ok := b.ls("/" + s.Name)["persiste.txt"]; !ok {
		t.Fatal("el archivo no sobrevivió al reinicio")
	}
	if out := onNAS(t, "smbclient -L localhost -N 2>&1 || true"); !strings.Contains(out, s.Name) || !strings.Contains(out, "Familia") {
		t.Fatalf("SMB no publica las carpetas después del reinicio:\n%s", out)
	}
	var apps []struct{ ID, State string }
	b.call("apps.list", nil, &apps)
	for _, x := range apps {
		if x.State != "running" {
			t.Errorf("app %s en estado %s después del reinicio", x.ID, x.State)
		}
	}
	deleteVolume(t, b, "e2em9", disks)
}
