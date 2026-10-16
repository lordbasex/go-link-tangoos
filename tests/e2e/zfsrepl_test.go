//go:build e2e

// Llave USB de volúmenes ZFS y replicación con zfs send (ADR 0016), en dos NAS reales.
//
//	TANGO_E2E_REPL=172.17.0.48,172.17.0.49   (origen, destino)
//	TANGO_E2E_DESTRUCTIVE=1
//
// Discos de laboratorio: en cada NAS TANGO-Z<n> (8 GB, pool ZFS) y TANGO-KEY<n> (1 GB): en
// el origen es la llave USB (se formatea FAT), en el destino un segundo pool para probar
// la replicación local.
package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

type replStatusE2E struct {
	Identity struct {
		Name        string `json:"name"`
		Fingerprint string `json:"fingerprint"`
	} `json:"identity"`
	Receive struct {
		Enabled bool `json:"enabled"`
	} `json:"receive"`
	Targets []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Fingerprint string `json:"fingerprint"`
	} `json:"targets"`
	Sources []struct {
		ID          string `json:"id"`
		Fingerprint string `json:"fingerprint"`
	} `json:"sources"`
	Tasks []struct {
		ID     string `json:"id"`
		Vol    string `json:"vol"`
		Target string `json:"target"`
		Job    string `json:"job"`
		Last   *struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
			Bytes uint64 `json:"bytes"`
		} `json:"last"`
	} `json:"tasks"`
	Replicas []struct {
		Pool      string `json:"pool"`
		Origin    string `json:"origin"`
		Vol       string `json:"vol"`
		Encrypted bool   `json:"encrypted"`
		Partial   bool   `json:"partial"`
		Snapshots int    `json:"snapshots"`
	} `json:"replicas"`
}

func (c *client) repl() replStatusE2E {
	var s replStatusE2E
	c.call("storage.zfs.repl.status", nil, &s)
	return s
}

func ensureZfs(t *testing.T, c *client) {
	t.Helper()
	if !c.zfs().Enabled {
		var r jobRef
		c.call("storage.zfs.enable", nil, &r)
		c.waitJob(r.Job, 20*time.Minute)
	}
}

// runTask corre una tarea y espera el job; devuelve los bytes enviados.
func runTask(t *testing.T, c *client, id string, wantOK bool) uint64 {
	t.Helper()
	var r jobRef
	c.call("storage.zfs.repl.tasks.run", map[string]string{"id": id}, &r)
	j := c.waitJobAny(r.Job, 15*time.Minute)
	if (j.State == "succeeded") != wantOK {
		t.Fatalf("envío %s terminó %s: %s", id, j.State, j.Detail)
	}
	for _, tk := range c.repl().Tasks {
		if tk.ID == id && tk.Last != nil {
			return tk.Last.Bytes
		}
	}
	return 0
}

func TestZfsKeyAndReplication(t *testing.T) {
	destructive(t)
	ips := strings.Split(os.Getenv("TANGO_E2E_REPL"), ",")
	if len(ips) != 2 || ips[0] == "" {
		t.Skip("exportá TANGO_E2E_REPL con origen,destino")
	}
	srcIP, dstIP := ips[0], ips[1]
	top := t
	src, dst := nodeAdmin(t, srcIP), nodeAdmin(t, dstIP)
	pass := randomSecret(14)
	const data = "datos"
	const secret = "secreto"

	t.Run("preparar ZFS, pools y volúmenes", func(t *testing.T) {
		ensureZfs(t, src)
		ensureZfs(t, dst)
		// Restos de una corrida anterior.
		for _, ip := range ips {
			_, _ = sshTo(ip, "for p in tanque copias respaldo; do zpool list $p >/dev/null 2>&1 && zpool destroy -f $p; done; rm -rf /var/lib/tango/repl; systemctl restart tangod; true")
		}
		time.Sleep(3 * time.Second) // el agente guarda zfs.report 2 s
		src, dst = nodeAdmin(top, srcIP), nodeAdmin(top, dstIP)
		mustSSH(t, srcIP, `D=$(lsblk -dnpo NAME,SERIAL | awk '$2=="TANGO-KEY1"{print $1}'); wipefs -a $D >/dev/null; mkfs.vfat -n LLAVE $D >/dev/null && udevadm settle`)
		mustSSH(t, dstIP, `D=$(lsblk -dnpo NAME,SERIAL | awk '$2=="TANGO-KEY2"{print $1}'); wipefs -a $D >/dev/null; udevadm settle`)
		p, _ := createZpool(t, src, "tanque", "stripe", []string{"TANGO-Z1"})
		createZpool(t, dst, "copias", "stripe", []string{"TANGO-Z2"})
		createZpool(t, dst, "respaldo", "stripe", []string{"TANGO-KEY2"})
		v := createZvolume(t, src, p, data, "")
		e := createZvolume(t, src, p, secret, pass)
		for _, x := range []poolVolume{v, e} {
			ensureShare(t, src, "Docs"+x.Name, x.ID)
		}
		mustSSH(t, srcIP, "head -c 64M /dev/urandom >/datos/@shares/Docsdatos/a.bin; echo hola >/secreto/@shares/Docssecreto/nota.txt; sync")
	})

	t.Run("llave USB de un volumen ZFS", func(t *testing.T) {
		var keys []struct {
			UUID string `json:"uuid"`
			FS   string `json:"fs"`
		}
		src.call("storage.keyDevices.list", nil, &keys)
		if len(keys) != 1 || keys[0].FS != "vfat" {
			t.Fatalf("llaves: %+v", keys)
		}
		key := keys[0].UUID
		v := src.poolVolumes()[secret]
		src.callErr("storage.volumes.keyDevice", map[string]any{"volume": v.ID, "passphrase": "otra frase cualquiera", "device": key}, "WRONG_PASSPHRASE")
		src.call("storage.volumes.keyDevice", map[string]any{"volume": v.ID, "passphrase": pass, "device": key}, nil)
		if v = src.poolVolumes()[secret]; !strings.EqualFold(v.KeyDevice, key) {
			t.Fatalf("keyDevice = %q", v.KeyDevice)
		}
		// En el pendrive hay un archivo de 32 bytes y en el volumen la frase sellada (no en claro).
		if out := mustSSH(t, srcIP, "zfs get -H -o value tangoos:keyseal tanque/secreto"); !strings.HasPrefix(out, "v1.") || strings.Contains(out, pass) {
			t.Fatalf("sello: %q", out)
		}
		src.call("storage.volumes.lock", map[string]string{"volume": v.ID}, nil)
		src.waitVolume(secret, "locked", time.Minute)
		src.call("storage.volumes.unlock", map[string]any{"volume": v.ID, "useKeyDevice": true}, nil)
		if out := mustSSH(t, srcIP, "cat /secreto/@shares/Docssecreto/nota.txt"); strings.TrimSpace(out) != "hola" {
			t.Fatalf("después de abrir con la llave: %q", out)
		}
		// Cambiar la frase: la llave sigue abriendo (sello nuevo) y la frase nueva también.
		pass2 := randomSecret(14)
		src.call("storage.volumes.passphrase", map[string]string{"volume": v.ID, "old": pass, "new": pass2}, nil)
		src.call("storage.volumes.lock", map[string]string{"volume": v.ID}, nil)
		src.call("storage.volumes.unlock", map[string]any{"volume": v.ID, "useKeyDevice": true}, nil)
		src.call("storage.volumes.lock", map[string]string{"volume": v.ID}, nil)
		src.callErr("storage.volumes.unlock", map[string]any{"volume": v.ID, "passphrase": pass}, "WRONG_PASSPHRASE")
		src.call("storage.volumes.unlock", map[string]any{"volume": v.ID, "passphrase": pass2}, nil)
		pass = pass2
		// Al arrancar con la llave puesta se desbloquea solo (tangod la prueba cada 10 s).
		src.call("storage.volumes.lock", map[string]string{"volume": v.ID}, nil)
		mustSSH(t, srcIP, "systemctl restart tangod")
		src = nodeAdmin(top, srcIP)
		src.waitVolume(secret, "healthy", 2*time.Minute)
		// Quitar la llave: sin sello y no abre más con la llave.
		v = src.poolVolumes()[secret]
		src.call("storage.volumes.keyDevice", map[string]any{"volume": v.ID, "passphrase": pass, "device": nil}, nil)
		if out := strings.TrimSpace(mustSSH(t, srcIP, "zfs get -H -o value tangoos:keyseal tanque/secreto")); out != "-" {
			t.Fatalf("el sello quedó: %q", out)
		}
		// Se vuelve a poner para la réplica (abre también en el otro NAS).
		src.call("storage.volumes.keyDevice", map[string]any{"volume": v.ID, "passphrase": pass, "device": key}, nil)
	})

	var target string
	t.Run("vincular con código y huella", func(t *testing.T) {
		dst.callErr("storage.zfs.repl.invite", nil, "NOT_SUPPORTED")
		dst.call("storage.zfs.repl.receive", map[string]any{"enabled": true, "pool": "copias"}, nil)
		var inv struct {
			Code string `json:"code"`
		}
		dst.call("storage.zfs.repl.invite", nil, &inv)
		var probe struct {
			Probe       string `json:"probe"`
			Fingerprint string `json:"fingerprint"`
		}
		src.call("storage.zfs.repl.targets.probe", map[string]string{"address": dstIP}, &probe)
		if probe.Fingerprint != dst.repl().Identity.Fingerprint {
			t.Fatalf("huella vista %q, la del destino %q", probe.Fingerprint, dst.repl().Identity.Fingerprint)
		}
		src.callErr("storage.zfs.repl.targets.add", map[string]string{"probe": probe.Probe, "code": "AAAAA-BBBBB"}, "PAIR_FAILED")
		src.call("storage.zfs.repl.targets.probe", map[string]string{"address": dstIP}, &probe)
		var tg struct {
			ID string `json:"id"`
		}
		src.call("storage.zfs.repl.targets.add", map[string]string{"probe": probe.Probe, "code": inv.Code}, &tg)
		target = tg.ID
		// El código era de un solo uso.
		src.call("storage.zfs.repl.targets.probe", map[string]string{"address": dstIP}, &probe)
		src.callErr("storage.zfs.repl.targets.add", map[string]string{"probe": probe.Probe, "code": inv.Code}, "PAIR_FAILED")
		if s := dst.repl(); len(s.Sources) != 1 || s.Sources[0].Fingerprint != src.repl().Identity.Fingerprint {
			t.Fatalf("orígenes en el destino: %+v", s.Sources)
		}
	})

	tasks := map[string]string{}
	t.Run("primer envío completo, cifrado sin descifrar", func(t *testing.T) {
		for _, name := range []string{data, secret} {
			var r struct {
				ID string `json:"id"`
			}
			src.call("storage.zfs.repl.tasks.create", map[string]any{"volume": src.poolVolumes()[name].ID, "target": target, "every": 24}, &r)
			tasks[name] = r.ID
			if b := runTask(t, src, r.ID, true); name == data && b < 60<<20 {
				t.Fatalf("el primer envío de %s mandó solo %d bytes", name, b)
			}
		}
		s := dst.repl()
		if len(s.Replicas) != 2 {
			t.Fatalf("réplicas: %+v", s.Replicas)
		}
		origin := s.Sources[0].ID
		// La réplica cifrada llegó sin la clave: el destino nunca la tuvo.
		ks := strings.TrimSpace(mustSSH(t, dstIP, "zfs get -H -o value keystatus,encryption copias/tgrepl/"+origin+"/secreto"))
		if !strings.Contains(ks, "unavailable") || !strings.Contains(ks, "aes-256-gcm") {
			t.Fatalf("réplica cifrada: %q", ks)
		}
		if out := mustSSH(t, dstIP, "zfs get -H -o value mounted -r copias/tgrepl | sort -u"); strings.Contains(out, "yes") {
			t.Fatal("hay algo montado de las réplicas")
		}
	})

	t.Run("segundo envío: solo los cambios, con las instantáneas", func(t *testing.T) {
		mustSSH(t, srcIP, "head -c 4M /dev/urandom >/datos/@shares/Docsdatos/b.bin; sync")
		var sj jobRef
		src.call("snapshots.create", map[string]string{"share": src.shares()["Docsdatos"].ID}, &sj)
		src.waitJob(sj.Job, 2*time.Minute)
		b := runTask(t, src, tasks[data], true)
		if b > 20<<20 {
			t.Fatalf("el segundo envío mandó %d bytes (tendría que ser solo lo nuevo)", b)
		}
		origin := dst.repl().Sources[0].ID
		out := mustSSH(t, dstIP, "zfs list -H -t snapshot -o name copias/tgrepl/"+origin+"/datos/shares/Docsdatos")
		if !strings.Contains(out, "@tgrepl-") || strings.Count(out, "@tgrepl-") != 1 {
			t.Fatalf("instantáneas en la réplica: %s", out)
		}
		if !strings.Contains(out, "Z\n") && !strings.Contains(out, "Z.") { // 2026-10-16T12-00-00Z: la programada/manual
			t.Fatalf("no llegó la instantánea de la carpeta: %s", out)
		}
	})

	t.Run("un envío cortado sigue donde quedó", func(t *testing.T) {
		mustSSH(t, srcIP, "head -c 900M /dev/urandom >/datos/@shares/Docsdatos/grande.bin; sync")
		var r jobRef
		src.call("storage.zfs.repl.tasks.run", map[string]string{"id": tasks[data]}, &r)
		// Se corta la conexión a mitad: tangod del destino se reinicia.
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			out, _ := sshTo(dstIP, "zfs list -H -o name -r copias/tgrepl 2>/dev/null | grep -c recv; ps -eo cmd | grep -c '[z]fs receive'")
			if strings.Contains(out, "\n1") || strings.HasSuffix(strings.TrimSpace(out), "1") {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		time.Sleep(3 * time.Second)
		mustSSH(t, dstIP, "systemctl restart tangod")
		if j := src.waitJobAny(r.Job, 5*time.Minute); j.State == "succeeded" {
			t.Skip("el envío terminó antes del corte")
		}
		dst = nodeAdmin(top, dstIP)
		partial := false
		for _, rp := range dst.repl().Replicas {
			partial = partial || rp.Vol == data && rp.Partial
		}
		if !partial {
			t.Fatal("la réplica no quedó con un envío a medias (token)")
		}
		b := runTask(t, src, tasks[data], true)
		if b > 880<<20 {
			t.Fatalf("el envío retomado mandó %d bytes: empezó de cero", b)
		}
		t.Logf("retomado: %d MiB", b>>20)
	})

	t.Run("convertir las réplicas en volúmenes", func(t *testing.T) {
		sum := func(ip, path string) string {
			return strings.Fields(mustSSH(t, ip, "sha256sum "+path))[0]
		}
		want := sum(srcIP, "/datos/@shares/Docsdatos/grande.bin")
		s := dst.repl()
		origin := s.Sources[0].ID
		dst.callErr("storage.zfs.repl.replicas.promote", map[string]any{"pool": "copias", "origin": origin, "vol": secret, "name": "cifrado2", "passphrase": "no es la frase"}, "WRONG_PASSPHRASE")
		var res struct {
			Locked bool `json:"locked"`
		}
		dst.call("storage.zfs.repl.replicas.promote", map[string]any{"pool": "copias", "origin": origin, "vol": data, "name": "datos2"}, &res)
		dst.call("storage.zfs.repl.replicas.promote", map[string]any{"pool": "copias", "origin": origin, "vol": secret, "name": "cifrado2", "passphrase": pass}, &res)
		if res.Locked {
			t.Fatal("cifrado2 quedó bloqueado con la frase")
		}
		if got := sum(dstIP, "/datos2/@shares/Docsdatos/grande.bin"); got != want {
			t.Fatalf("grande.bin difiere: %s vs %s", got, want)
		}
		if out := mustSSH(t, dstIP, "cat /cifrado2/@shares/Docssecreto/nota.txt; zfs get -H -o value encryptionroot -r copias/cifrado2 | sort -u"); !strings.HasPrefix(out, "hola\n") || strings.Count(strings.TrimSpace(out), "\n") != 1 {
			t.Fatalf("cifrado2: %q (todas las carpetas tienen que heredar la clave del volumen)", out)
		}
		vs := dst.poolVolumes()
		if vs["datos2"].Kind != "zfs" || vs["cifrado2"].Locked {
			t.Fatalf("volúmenes: %+v %+v", vs["datos2"], vs["cifrado2"])
		}
		if len(dst.repl().Replicas) != 0 {
			t.Fatal("las réplicas siguen listadas")
		}
	})

	t.Run("réplica a otro pool del mismo NAS", func(t *testing.T) {
		var r struct {
			ID string `json:"id"`
		}
		p, _ := dst.zpool("copias")
		chico := createZvolume(t, dst, p, "chico", "")
		ensureShare(t, dst, "Chico", chico.ID)
		mustSSH(t, dstIP, "head -c 20M /dev/urandom >/chico/@shares/Chico/c.bin; sync")
		dst.callErr("storage.zfs.repl.tasks.create", map[string]any{"volume": chico.ID, "target": "local:copias", "every": 0}, "INVALID_PARAMS")
		dst.call("storage.zfs.repl.tasks.create", map[string]any{"volume": chico.ID, "target": "local:respaldo", "every": 0}, &r)
		runTask(t, dst, r.ID, true)
		rs := dst.repl().Replicas
		if len(rs) != 1 || rs[0].Origin != "local" || rs[0].Pool != "respaldo" || rs[0].Vol != "chico" {
			t.Fatalf("réplica local: %+v", rs)
		}
		dst.call("storage.zfs.repl.tasks.delete", map[string]string{"id": r.ID}, nil)
		dst.call("storage.zfs.repl.replicas.delete", map[string]string{"pool": "respaldo", "origin": "local", "vol": "chico"}, nil)
		if len(dst.repl().Replicas) != 0 {
			t.Fatal("la réplica local sigue")
		}
	})

	t.Run("desarmar", func(t *testing.T) {
		for _, id := range tasks {
			src.call("storage.zfs.repl.tasks.delete", map[string]string{"id": id}, nil)
		}
		src.call("storage.zfs.repl.targets.remove", map[string]string{"id": target}, nil)
		for _, s := range dst.repl().Sources {
			dst.call("storage.zfs.repl.sources.remove", map[string]string{"id": s.ID}, nil)
		}
		dst.call("storage.zfs.repl.receive", map[string]any{"enabled": false, "pool": ""}, nil)
		// Con la recepción apagada el puerto no responde.
		if out, _ := sshTo(srcIP, fmt.Sprintf("timeout 3 bash -c '</dev/tcp/%s/24020' && echo abierto || echo cerrado", dstIP)); !strings.Contains(out, "cerrado") {
			t.Fatalf("el puerto 24020 sigue abierto: %q", out)
		}
		for _, sh := range []string{"Docsdatos", "Docssecreto"} {
			if s, ok := src.shares()[sh]; ok {
				deleteShare(t, src, s)
			}
		}
		destroyZpool(t, src, "tanque")
		for _, sh := range dst.shares() {
			deleteShare(t, dst, sh)
		}
		destroyZpool(t, dst, "copias")
		destroyZpool(t, dst, "respaldo")
	})
}
