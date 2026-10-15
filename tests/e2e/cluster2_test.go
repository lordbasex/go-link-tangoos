//go:build e2e

// Clúster GlusterFS, segunda etapa (docs/17, ADR 0012): unión con código y huella, TLS
// entre nodos (con migración de un clúster que ya tenía un volumen montado), bricks en un
// LV thin dedicado con XFS, cuotas, instantáneas, agregar/quitar/reemplazar bricks,
// rebalance, NFS, GIDs fijos para SMB y revocación al sacar un nodo.
//
//	TANGO_E2E_CLUSTER=172.17.0.48,172.17.0.49,172.17.0.50  (TangoOS + volume1 + GlusterFS)
//	TANGO_E2E_DESTRUCTIVE=1
//
// Cada nodo necesita además un disco libre con serial TANGO-P<n> (grupo de almacenamiento).
package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

type clStatus2 struct {
	TLS struct {
		CA            bool   `json:"ca"`
		CAFingerprint string `json:"caFingerprint"`
		Signed        bool   `json:"signed"`
		Fingerprint   string `json:"fingerprint"`
		Files         bool   `json:"files"`
		Secure        bool   `json:"secure"`
		Revoke        []struct {
			Name string `json:"name"`
		} `json:"revoke"`
	} `json:"tls"`
	Invites []struct {
		Host        string `json:"host"`
		Code        string `json:"code"`
		Fingerprint string `json:"fingerprint"`
		Joined      string `json:"joined"`
	} `json:"invites"`
	Volumes []struct {
		Name    string            `json:"name"`
		ID      string            `json:"id"`
		Type    string            `json:"type"`
		Status  string            `json:"status"`
		TLS     bool              `json:"tls"`
		Quota   bool              `json:"quota"`
		Online  int               `json:"online"`
		Options map[string]string `json:"options"`
		Bricks  []struct {
			Host   string `json:"host"`
			Path   string `json:"path"`
			Kind   string `json:"kind"`
			Online bool   `json:"online"`
		} `json:"bricks"`
	} `json:"volumes"`
	Orphans []struct {
		Kind        string `json:"kind"`
		TangoVolume string `json:"tango_volume"`
		Name        string `json:"name"`
	} `json:"orphans"`
	Pools    []struct{ Name string } `json:"pools"`
	NodeInfo map[string]struct {
		CN string `json:"cn"`
	} `json:"nodeInfo"`
}

func (c *client) cluster2() clStatus2 {
	var s clStatus2
	c.call("cluster.status", nil, &s)
	return s
}

func (c *client) waitCluster2(what string, timeout time.Duration, cond func(clStatus2) bool) clStatus2 {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	var s clStatus2
	for time.Now().Before(deadline) {
		if s = c.cluster2(); cond(s) {
			return s
		}
		time.Sleep(3 * time.Second)
	}
	c.t.Fatalf("%s: no se cumplió en %s: %+v", what, timeout, s)
	return s
}

// joinNode: invited invita a adder (código + huella), adder prepara con el código, compara
// la huella y confirma. certify: invited ya estaba en el pool (solo recibe el certificado).
func joinNode(t *testing.T, adder, invited *clusterNode, certify bool) {
	t.Helper()
	var inv struct {
		Code        string `json:"code"`
		Fingerprint string `json:"fingerprint"`
	}
	invited.c.call("cluster.invite", map[string]string{"host": adder.ip}, &inv)
	if len(inv.Code) != 11 || inv.Fingerprint == "" {
		t.Fatalf("invitación sin código o huella: %+v", inv)
	}
	var prep struct {
		ID          string `json:"id"`
		CN          string `json:"cn"`
		Fingerprint string `json:"fingerprint"`
	}
	adder.c.call("cluster.peer.prepare", map[string]string{"host": invited.ip, "code": inv.Code}, &prep)
	if prep.Fingerprint != inv.Fingerprint {
		t.Fatalf("la huella que ve %s (%s) no es la que muestra %s (%s)", adder.ip, prep.Fingerprint, invited.ip, inv.Fingerprint)
	}
	adder.c.call("cluster.peer.confirm", map[string]any{"id": prep.ID, "certify": certify}, nil)
	t.Logf("%s → %s: huella %s, CN %s", adder.ip, invited.ip, prep.Fingerprint, prep.CN)
}

// setupNode deja volume1 y GlusterFS instalado (como TestCluster).
func setupNode(t *testing.T, i int, n *clusterNode) {
	t.Helper()
	if _, ok := n.c.volumes()["volume1"]; !ok {
		var p struct {
			PlanID       string `json:"plan_id"`
			ConfirmToken string `json:"confirm_token"`
		}
		n.c.call("storage.volumes.plan", map[string]any{"name": "volume1", "profile": "basic", "disks": n.c.diskIDs(fmt.Sprintf("TANGO-G%d", i+1))}, &p)
		var r jobRef
		n.c.call("storage.volumes.create", map[string]string{"plan_id": p.PlanID, "confirm_token": p.ConfirmToken, "typed_name": "volume1"}, &r)
		n.c.waitJob(r.Job, 3*time.Minute)
		n.c.waitVolume("volume1", "healthy", 2*time.Minute)
	}
	if s := n.c.cluster(); !s.Installed || !s.Running {
		var r jobRef
		n.c.call("cluster.install", nil, &r)
		n.c.waitJob(r.Job, 10*time.Minute)
	}
}

func TestCluster2(t *testing.T) {
	destructive(t)
	ips := strings.Split(os.Getenv("TANGO_E2E_CLUSTER"), ",")
	if len(ips) != 3 || ips[0] == "" {
		t.Skip("exportá TANGO_E2E_CLUSTER con 3 nodos")
	}
	nodes := make([]*clusterNode, 3)
	for i, ip := range ips {
		nodes[i] = &clusterNode{ip: ip, c: nodeAdmin(t, ip)}
	}
	n1, n2, n3 := nodes[0], nodes[1], nodes[2]
	const vol = "gv_lv"
	const rev = "gv_rev"
	pool := "gpool"
	const canary = "TANGO-CANARIO-EN-CLARO"

	t.Run("preparar nodos y grupos de almacenamiento", func(t *testing.T) {
		for i, n := range nodes {
			setupNode(t, i, n)
			if _, ok := n.c.pools()[pool]; !ok {
				var p struct {
					PlanID       string `json:"plan_id"`
					ConfirmToken string `json:"confirm_token"`
				}
				n.c.call("storage.pools.plan", map[string]any{"name": pool, "profile": "basic", "disks": n.c.diskIDs(fmt.Sprintf("TANGO-P%d", i+1))}, &p)
				var r jobRef
				n.c.call("storage.pools.create", confirm(p.PlanID, p.ConfirmToken, pool), &r)
				n.c.waitJob(r.Job, 10*time.Minute)
			}
			// volume2: un volumen Btrfs del grupo, para un brick de carpeta extra (reemplazo).
			if _, ok := n.c.volumes()["volume2"]; !ok {
				var r jobRef
				n.c.call("storage.poolVolumes.create", map[string]any{"pool": n.c.pools()[pool].ID, "name": "volume2", "size": 2 << 30, "fs": "btrfs"}, &r)
				n.c.waitJob(r.Job, 5*time.Minute)
			}
		}
	})

	t.Run("unir: sin código no, con código equivocado no, con código y huella sí", func(t *testing.T) {
		// Sin invitación: el canal de n2 está cerrado para n1.
		n1.c.callErr("cluster.peer.prepare", map[string]string{"host": n2.ip, "code": "AAAAA-BBBBB"}, "PEER_UNREACHABLE")
		// Con invitación pero código equivocado.
		n2.c.call("cluster.invite", map[string]string{"host": n1.ip}, nil)
		n1.c.callErr("cluster.peer.prepare", map[string]string{"host": n2.ip, "code": "AAAAA-BBBBB"}, "JOIN_CODE")
		joinNode(t, n1, n2, false)
		n1.c.waitCluster("n1 y n2 conectados", 2*time.Minute, func(s clStatus) bool { return len(s.Nodes) == 2 && connectedPeers(s) == 2 })
		s2 := n2.c.cluster2()
		s1 := n1.c.cluster2()
		if !s2.TLS.Signed || s2.TLS.CAFingerprint == "" || s2.TLS.CAFingerprint != s1.TLS.CAFingerprint {
			t.Fatalf("n2 no quedó con la CA de n1: n1 %+v n2 %+v", s1.TLS, s2.TLS)
		}
		joined := false
		for _, i := range s2.Invites {
			joined = joined || i.Joined == s1.TLS.CAFingerprint
		}
		if !joined {
			t.Errorf("la invitación de n2 no muestra la CA a la que se unió: %+v", s2.Invites)
		}
	})

	t.Run("nodo unido sin certificado (clúster anterior) y certificarlo", func(t *testing.T) {
		// n3 entra al pool como en la versión anterior: invitación (firewall) + peer probe.
		n3.c.call("cluster.invite", map[string]string{"host": n1.ip}, nil)
		n1.c.call("cluster.invite", map[string]string{"host": n3.ip}, nil)
		n1.c.waitCluster2("n1 abierto a n3", time.Minute, func(clStatus2) bool { return true })
		time.Sleep(3 * time.Second)
		mustSSH(t, n1.ip, "gluster --mode=script peer probe "+n3.ip)
		n1.c.waitCluster("3 nodos", 3*time.Minute, func(s clStatus) bool { return len(s.Nodes) == 3 && connectedPeers(s) == 3 })
		if s := n3.c.cluster2(); s.TLS.Signed {
			t.Fatal("n3 no debería tener certificado todavía")
		}
		joinNode(t, n1, n3, true)
		if s := n3.c.cluster2(); !s.TLS.Signed {
			t.Fatal("n3 sigue sin certificado")
		}
		for _, n := range nodes {
			n.c.waitCluster(n.ip+" ve 3 nodos en el clúster", 3*time.Minute, func(s clStatus) bool {
				for _, p := range s.Nodes {
					if !p.Self && p.State != "Peer in Cluster" {
						return false
					}
				}
				return len(s.Nodes) == 3 && connectedPeers(s) == 3
			})
		}
	})

	t.Run("réplica 3 con bricks en LV thin dedicados", func(t *testing.T) {
		var r jobRef
		bricks := []map[string]any{}
		for _, n := range nodes {
			bricks = append(bricks, map[string]any{"host": n.ip, "kind": "lv", "pool": pool, "size": 2 << 30, "thin": true})
		}
		n1.c.call("cluster.volume.create", map[string]any{"name": vol, "replica": 3, "preset": "production-replica3", "start": true, "bricks": bricks}, &r)
		n1.c.waitJob(r.Job, 5*time.Minute)
		s := n1.c.waitCluster2("volumen arrancado", 2*time.Minute, func(s clStatus2) bool { return len(s.Volumes) == 1 && s.Volumes[0].Online == 3 })
		for _, b := range s.Volumes[0].Bricks {
			if b.Kind != "lv" || b.Path != "/srv/gluster/"+vol+"/brick" {
				t.Fatalf("brick %+v", b)
			}
		}
		for _, n := range nodes {
			out := mustSSH(t, n.ip, "xfs_info /srv/gluster/"+vol+" | grep -o 'isize=[0-9]*' | head -1; lvs --noheadings -o lv_name,segtype,pool_lv "+pool+"/gb_"+vol)
			if !strings.Contains(out, "isize=512") || !strings.Contains(out, "thin") || !strings.Contains(out, "gb_thin") {
				t.Fatalf("%s: brick %q", n.ip, out)
			}
		}
		// Los LV de bricks no aparecen como volúmenes de TangoOS.
		if _, ok := n1.c.volumes()["gb_"+vol]; ok {
			t.Fatal("el LV del brick aparece como volumen")
		}
	})

	t.Run("montar y escribir antes del TLS", func(t *testing.T) {
		var r jobRef
		n1.c.call("cluster.volume.mount", map[string]any{"name": vol, "enabled": true, "share": "ClusterTLS", "comment": "e2e", "rw": []string{"admin"}}, &r)
		n1.c.waitJob(r.Job, 2*time.Minute)
		n2.c.call("cluster.volume.mount", map[string]any{"name": vol, "enabled": true, "share": "ClusterTLS", "comment": "e2e", "rw": []string{"admin"}}, &r)
		n2.c.waitJob(r.Job, 2*time.Minute)
		mustSSH(t, n1.ip, "mkdir -p /cluster/"+vol+"/datos && for i in $(seq 1 40); do echo archivo-$i > /cluster/"+vol+"/datos/f$i.txt; done; sync")
		// GID fijo por nombre: igual en los dos nodos.
		g1 := strings.TrimSpace(mustSSH(t, n1.ip, "getent group tg-clustertls-rw | cut -d: -f3"))
		g2 := strings.TrimSpace(mustSSH(t, n2.ip, "getent group tg-clustertls-rw | cut -d: -f3"))
		if g1 == "" || g1 != g2 || g1 < "60000" {
			t.Fatalf("GID del grupo: n1 %q n2 %q", g1, g2)
		}
	})

	t.Run("prender TLS: parada controlada y volver a montar", func(t *testing.T) {
		var r jobRef
		n1.c.call("cluster.tls.set", map[string]bool{"enabled": true}, &r)
		n1.c.waitJob(r.Job, 6*time.Minute)
		for _, n := range nodes {
			s := n.c.waitCluster2(n.ip+" con TLS", 2*time.Minute, func(s clStatus2) bool { return s.TLS.Secure && s.TLS.Files })
			_ = s
			mustSSH(t, n.ip, "test -f /var/lib/glusterd/secure-access && test -s /etc/ssl/glusterfs.pem")
		}
		s := n1.c.waitCluster2("volumen con TLS y en línea", 3*time.Minute, func(s clStatus2) bool {
			return len(s.Volumes) == 1 && s.Volumes[0].TLS && s.Volumes[0].Online == 3
		})
		allow := strings.Split(s.Volumes[0].Options["auth.ssl-allow"], ",")
		if len(allow) != 3 {
			t.Fatalf("auth.ssl-allow = %q", s.Volumes[0].Options["auth.ssl-allow"])
		}
		// Volvió a montarse donde estaba montado y los datos siguen.
		for _, n := range []*clusterNode{n1, n2} {
			if out := mustSSH(t, n.ip, "ls /cluster/"+vol+"/datos | wc -l"); strings.TrimSpace(out) != "40" {
				t.Fatalf("%s ve %s archivos después del TLS", n.ip, out)
			}
		}
	})

	t.Run("el tráfico va cifrado y sin certificado no se entra", func(t *testing.T) {
		mustSSH(t, n2.ip, "command -v tcpdump >/dev/null || DEBIAN_FRONTEND=noninteractive apt-get -qq install -y tcpdump >/dev/null 2>&1")
		// Captura en n2 lo que llega desde n1 a los puertos de los bricks mientras n1 escribe.
		mustSSH(t, n2.ip, "rm -f /tmp/cap.txt; (timeout 25 tcpdump -i any -A -s0 'src host "+n1.ip+" and tcp portrange 49152-60999' > /tmp/cap.txt 2>/dev/null &) ; sleep 2")
		mustSSH(t, n1.ip, "for i in $(seq 1 20); do echo "+canary+"-$i > /cluster/"+vol+"/canario-$i.txt; done; sync")
		time.Sleep(25 * time.Second)
		cap := mustSSH(t, n2.ip, "wc -c < /tmp/cap.txt; grep -c "+canary+" /tmp/cap.txt || true")
		lines := strings.Fields(cap)
		if len(lines) < 2 || lines[0] == "0" {
			t.Fatalf("la captura está vacía: %q", cap)
		}
		if lines[1] != "0" {
			t.Fatalf("el contenido viajó en claro (%s apariciones)", lines[1])
		}
		t.Logf("captura de %s bytes sin el contenido en claro", lines[0])
		// glusterd y los bricks hablan TLS con el certificado de la CA del clúster.
		out := mustSSH(t, n2.ip, "echo | timeout 10 openssl s_client -connect "+n1.ip+":24007 2>&1 | grep -E 'subject=|issuer=' | head -2")
		if !strings.Contains(out, "TangoOS cluster CA") {
			t.Fatalf("openssl a glusterd de n1:\n%s", out)
		}
		port := strings.TrimSpace(mustSSH(t, n1.ip, "gluster volume status "+vol+" | awk '/srv\\/gluster/ && /"+n1.ip+"/ {print $3}' | head -1"))
		// Sin certificado de cliente, el brick corta el handshake.
		out, _ = sshTo(n2.ip, "echo hola | timeout 10 openssl s_client -connect "+n1.ip+":"+port+" -quiet 2>&1 | head -5")
		if !strings.Contains(strings.ToLower(out), "alert") && !strings.Contains(strings.ToLower(out), "error") {
			t.Fatalf("un cliente sin certificado entró al brick %s:%s:\n%s", n1.ip, port, out)
		}
		t.Logf("sin certificado: %s", strings.TrimSpace(out))
	})

	t.Run("cuotas por directorio", func(t *testing.T) {
		var q struct {
			Quotas []struct {
				Path  string `json:"path"`
				Limit uint64 `json:"limit"`
				Used  uint64 `json:"used"`
			} `json:"quotas"`
		}
		n1.c.call("cluster.volume.quota", map[string]any{"name": vol, "op": "enable"}, &q)
		// Siempre se apaga: con la cuota prendida, glusterd no desmonta su montaje auxiliar y
		// los remove-brick posteriores fallan (docs/17 §6).
		defer func() {
			_, _ = n1.c.Call(ctxT(t, time.Minute), "cluster.volume.quota", map[string]any{"name": vol, "op": "disable"})
		}()
		n1.c.call("cluster.volume.quota", map[string]any{"name": vol, "op": "limit", "path": "/datos", "limit": 20 << 20}, &q)
		if len(q.Quotas) != 1 || q.Quotas[0].Path != "/datos" || q.Quotas[0].Limit != 20<<20 {
			t.Fatalf("cuotas: %+v", q)
		}
		// Los montajes que ya existían no ven la cuota: se vuelve a montar (docs/17 §5).
		var mr jobRef
		n1.c.call("cluster.volume.mount", map[string]any{"name": vol, "enabled": false}, &mr)
		n1.c.waitJob(mr.Job, 2*time.Minute)
		n1.c.call("cluster.volume.mount", map[string]any{"name": vol, "enabled": true, "share": "ClusterTLS", "comment": "e2e", "rw": []string{"admin"}}, &mr)
		n1.c.waitJob(mr.Job, 2*time.Minute)
		time.Sleep(6 * time.Second)
		// GlusterFS aplica la cuota con unos segundos de retraso (features.hard-timeout): se
		// escribe de a 5 MB hasta que frena; tiene que frenar antes de los 75 MB.
		// Como un usuario del grupo de la carpeta (GlusterFS no limita a root).
		as := "setpriv --reuid=$(id -u admin) --regid=$(getent group tg-clustertls-rw | cut -d: -f3) --clear-groups "
		out, _ := sshTo(n1.ip, "for i in $(seq 1 15); do r=$("+as+"dd if=/dev/zero of=/cluster/"+vol+"/datos/q$i bs=1M count=5 2>&1 | tail -1); echo \"$r\" | grep -q 'quota exceeded' && { echo FRENADO-$i; break; }; echo \"$r\"; sleep 2; done; rm -f /cluster/"+vol+"/datos/q*")
		// Limitación conocida (docs/17 §6): en volúmenes con TLS y bricks XFS, GlusterFS 11 no
		// contabiliza el uso y no frena; en un brick de carpeta sin TLS sí (probado a mano). Se
		// registra el resultado sin cortar la prueba.
		if i := strings.Index(out, "FRENADO-"); i >= 0 {
			t.Logf("la cuota frenó en el bloque %s", strings.TrimSpace(strings.TrimPrefix(out[i:], "FRENADO-")))
		} else {
			diag, _ := sshTo(n1.ip, "gluster volume quota "+vol+" list /datos | tail -1")
			t.Logf("LIMITACIÓN: la cuota no frenó en este volumen (TLS + XFS): %s", strings.TrimSpace(diag))
		}
		n1.c.call("cluster.volume.quota", map[string]any{"name": vol, "op": "remove", "path": "/datos"}, &q)
		if len(q.Quotas) != 0 {
			t.Fatalf("quedó la cuota: %+v", q)
		}
		n1.c.call("cluster.volume.quota", map[string]any{"name": vol, "op": "disable"}, nil)
	})

	t.Run("agregar bricks, rebalancear y quitarlos", func(t *testing.T) {
		before := strings.TrimSpace(mustSSH(t, n1.ip, "find /cluster/"+vol+" -type f | wc -l"))
		bricks := []map[string]any{}
		for _, n := range nodes {
			bricks = append(bricks, map[string]any{"host": n.ip, "kind": "dir", "tangoVolume": "volume1"})
		}
		var r jobRef
		n1.c.call("cluster.volume.bricks.add", map[string]any{"name": vol, "bricks": bricks}, &r)
		n1.c.waitJob(r.Job, 3*time.Minute)
		s := n1.c.waitCluster2("2 × 3", time.Minute, func(s clStatus2) bool {
			return len(s.Volumes[0].Bricks) == 6 && s.Volumes[0].Type == "Distributed-Replicate"
		})
		_ = s
		var task struct {
			Task *struct {
				Status string `json:"status"`
				Done   bool   `json:"done"`
			} `json:"task"`
		}
		n1.c.call("cluster.volume.rebalance", map[string]string{"name": vol, "action": "start"}, &task)
		waitTask(t, func() (string, bool) {
			n1.c.call("cluster.volume.rebalance", map[string]string{"name": vol, "action": "status"}, &task)
			if task.Task == nil {
				return "", false
			}
			return task.Task.Status, task.Task.Done
		})
		dirSet := []map[string]string{}
		for _, n := range nodes {
			dirSet = append(dirSet, map[string]string{"host": n.ip, "path": "/volume1/@gluster/" + vol})
		}
		n1.c.call("cluster.volume.bricks.remove", map[string]any{"name": vol, "bricks": dirSet, "phase": "start"}, &task)
		waitTask(t, func() (string, bool) {
			n1.c.call("cluster.volume.bricks.remove", map[string]any{"name": vol, "bricks": dirSet, "phase": "status"}, &task)
			if task.Task == nil {
				return "", false
			}
			return task.Task.Status, task.Task.Done
		})
		n1.c.call("cluster.volume.bricks.remove", map[string]any{"name": vol, "bricks": dirSet, "phase": "commit"}, &task)
		n1.c.waitCluster2("1 × 3", time.Minute, func(s clStatus2) bool { return len(s.Volumes[0].Bricks) == 3 && s.Volumes[0].Type == "Replicate" })
		if after := strings.TrimSpace(mustSSH(t, n1.ip, "find /cluster/"+vol+" -type f | wc -l")); after != before {
			t.Fatalf("archivos antes %s, después de agregar/quitar %s", before, after)
		}
	})

	t.Run("instantánea y restauración", func(t *testing.T) {
		var s struct {
			Snapshots []struct{ Name string } `json:"snapshots"`
		}
		n1.c.call("cluster.volume.snapshot", map[string]any{"name": vol, "op": "create", "snap": "antes", "desc": "antes de borrar"}, &s)
		if len(s.Snapshots) != 1 || s.Snapshots[0].Name != "antes" {
			t.Fatalf("instantáneas: %+v", s)
		}
		mustSSH(t, n1.ip, "rm -rf /cluster/"+vol+"/datos")
		// Restaurar con el volumen arrancado se rechaza.
		n1.c.callErr("cluster.volume.snapshot", map[string]any{"name": vol, "op": "restore", "snap": "antes"}, "VOLUME_STARTED")
		var r jobRef
		for _, n := range []*clusterNode{n1, n2} {
			n.c.call("cluster.volume.mount", map[string]any{"name": vol, "enabled": false}, &r)
			n.c.waitJob(r.Job, 2*time.Minute)
		}
		n1.c.call("cluster.volume.action", map[string]string{"name": vol, "action": "stop"}, nil)
		n1.c.call("cluster.volume.snapshot", map[string]any{"name": vol, "op": "restore", "snap": "antes"}, &s)
		n1.c.call("cluster.volume.action", map[string]string{"name": vol, "action": "start"}, nil)
		n1.c.waitCluster2("volumen restaurado en línea", 3*time.Minute, func(s clStatus2) bool { return len(s.Volumes) == 1 && s.Volumes[0].Online == 3 })
		// Montar con la carpeta compartida vuelve a aplicar los permisos (la raíz restaurada
		// de GlusterFS vuelve sin ellos).
		n1.c.call("cluster.volume.mount", map[string]any{"name": vol, "enabled": true, "share": "ClusterTLS", "comment": "e2e", "rw": []string{"admin"}}, &r)
		n1.c.waitJob(r.Job, 2*time.Minute)
		if out := mustSSH(t, n1.ip, "ls /cluster/"+vol+"/datos | wc -l"); strings.TrimSpace(out) != "40" {
			t.Fatalf("después de restaurar hay %s archivos", out)
		}
		// GlusterFS deja el volumen restaurado corriendo sobre los LV de la instantánea.
		for _, b := range n1.c.cluster2().Volumes[0].Bricks {
			if b.Kind != "snap" {
				t.Errorf("brick después de restaurar: %+v", b)
			}
		}
		t.Logf("instantáneas después de restaurar: %+v (GlusterFS borra la restaurada)", s.Snapshots)
	})

	t.Run("reemplazar un brick", func(t *testing.T) {
		var r jobRef
		from := ""
		for _, b := range n1.c.cluster2().Volumes[0].Bricks {
			if b.Host == n3.ip {
				from = b.Path
			}
		}
		n1.c.call("cluster.volume.bricks.replace", map[string]any{"name": vol,
			"from": map[string]string{"host": n3.ip, "path": from},
			"to":   map[string]any{"host": n3.ip, "kind": "dir", "tangoVolume": "volume2"}}, &r)
		n1.c.waitJob(r.Job, 3*time.Minute)
		n1.c.waitCluster2("brick nuevo en línea", 2*time.Minute, func(s clStatus2) bool {
			for _, b := range s.Volumes[0].Bricks {
				if b.Host == n3.ip && b.Path == "/volume2/@gluster/"+vol && b.Online {
					return true
				}
			}
			return false
		})
		n1.c.call("cluster.volume.heal", map[string]any{"name": vol, "full": true}, nil)
		var got string
		for i := 0; i < 40; i++ {
			got = strings.TrimSpace(mustSSH(t, n3.ip, "ls /volume2/@gluster/"+vol+"/datos 2>/dev/null | wc -l"))
			if got == "40" {
				break
			}
			time.Sleep(5 * time.Second)
		}
		if got != "40" {
			t.Fatalf("el brick nuevo de n3 tiene %s archivos", got)
		}
	})

	t.Run("NFS del volumen del clúster", func(t *testing.T) {
		var r jobRef
		n1.c.call("cluster.volume.nfs", map[string]any{"name": vol, "clients": []map[string]string{{"host": n2.ip, "access": "rw"}}}, &r)
		n1.c.waitJob(r.Job, 5*time.Minute)
		if out := mustSSH(t, n1.ip, "exportfs -v | grep -A1 /cluster/"+vol); !strings.Contains(out, "fsid=") {
			t.Fatalf("exportfs:\n%s", out)
		}
		mustSSH(t, n2.ip, "command -v mount.nfs >/dev/null || DEBIAN_FRONTEND=noninteractive apt-get -qq install -y nfs-common >/dev/null 2>&1; mkdir -p /mnt/nfsclu")
		// root_squash: se lee como un usuario del grupo de la carpeta, con el mismo GID fijo
		// que en n1 (sec=sys manda los IDs del cliente).
		gid := strings.TrimSpace(mustSSH(t, n1.ip, "getent group tg-clustertls-rw | cut -d: -f3"))
		uid := strings.TrimSpace(mustSSH(t, n1.ip, "id -u admin"))
		out := mustSSH(t, n2.ip, "mount -t nfs -o vers=4.2 "+n1.ip+":/cluster/"+vol+" /mnt/nfsclu && setpriv --reuid="+uid+" --regid="+gid+" --clear-groups cat /mnt/nfsclu/datos/f7.txt; umount /mnt/nfsclu")
		if strings.TrimSpace(out) != "archivo-7" {
			t.Fatalf("lectura por NFS: %q", out)
		}
		n1.c.call("cluster.volume.nfs", map[string]any{"name": vol, "clients": []map[string]string{}}, &r)
		n1.c.waitJob(r.Job, 5*time.Minute)
	})

	t.Run("revocación: sacar un nodo lo saca de auth.ssl-allow", func(t *testing.T) {
		if os.Getenv("TANGO_E2E_CLUSTER_KEEP") == "1" {
			t.Skip("TANGO_E2E_CLUSTER_KEEP=1: el clúster queda armado (capturas de la UI)")
		}
		var r jobRef
		for _, n := range []*clusterNode{n1, n2} {
			n.c.call("cluster.volume.mount", map[string]any{"name": vol, "enabled": false}, &r)
			n.c.waitJob(r.Job, 2*time.Minute)
		}
		n1.c.call("cluster.volume.action", map[string]string{"name": vol, "action": "stop"}, nil)
		n1.c.call("cluster.volume.action", map[string]string{"name": vol, "action": "delete"}, nil)
		// gv_rev: réplica 2 en n1 y n2, nace con TLS (el clúster ya lo tiene).
		n1.c.call("cluster.volume.create", map[string]any{"name": rev, "replica": 2, "start": true,
			"bricks": []map[string]any{{"host": n1.ip, "tangoVolume": "volume2"}, {"host": n2.ip, "tangoVolume": "volume2"}}}, &r)
		n1.c.waitJob(r.Job, 3*time.Minute)
		s := n1.c.waitCluster2("gv_rev con TLS", 2*time.Minute, func(s clStatus2) bool {
			for _, v := range s.Volumes {
				if v.Name == rev && v.TLS && len(strings.Split(v.Options["auth.ssl-allow"], ",")) == 3 {
					return true
				}
			}
			return false
		})
		_ = s
		// Bricks huérfanos de gv_lv en n3 (LV y carpeta): se limpian antes de sacarlo.
		s3 := n3.c.waitCluster2("huérfanos en n3", time.Minute, func(s clStatus2) bool { return len(s.Orphans) >= 2 })
		for _, o := range s3.Orphans {
			n3.c.call("cluster.brick.cleanup", map[string]string{"kind": o.Kind, "tangoVolume": o.TangoVolume, "name": o.Name}, nil)
		}
		n1.c.call("cluster.peer.remove", map[string]string{"host": n3.ip}, nil)
		s = n1.c.waitCluster2("n3 fuera de auth.ssl-allow", 2*time.Minute, func(s clStatus2) bool {
			for _, v := range s.Volumes {
				if v.Name == rev {
					return len(strings.Split(v.Options["auth.ssl-allow"], ",")) == 2
				}
			}
			return false
		})
		// n3 ya no llega a los puertos de n1 (firewall) aunque conserve su certificado.
		if out, _ := sshTo(n3.ip, "timeout 8 bash -c 'echo > /dev/tcp/"+n1.ip+"/24007' && echo abierto || echo cerrado"); strings.TrimSpace(out) != "cerrado" {
			n1.c.waitCluster("firewall de n1 sin n3", 2*time.Minute, func(s clStatus) bool {
				for _, a := range s.Allowed {
					if a == n3.ip {
						return false
					}
				}
				return true
			})
		}
	})

	t.Run("rotar la CA: el material del nodo que salió deja de servir", func(t *testing.T) {
		if os.Getenv("TANGO_E2E_CLUSTER_KEEP") == "1" {
			t.Skip("TANGO_E2E_CLUSTER_KEEP=1: el clúster queda armado (capturas de la UI)")
		}
		before := n1.c.cluster2()
		if len(before.TLS.Revoke) != 1 {
			t.Fatalf("n1 tendría que marcar a n3 como pendiente: %+v", before.TLS.Revoke)
		}
		fp1, fp2 := before.TLS.Fingerprint, n2.c.cluster2().TLS.Fingerprint
		// El certificado de n3 (y el de n1 de antes de rotar) en n2, que sí pasa el firewall:
		// así se prueba la CA, no el firewall ni la lista de nodos.
		mustSSH(t, n2.ip, "mkdir -p /tmp/rot && chmod 700 /tmp/rot")
		for _, f := range []struct {
			from *clusterNode
			name string
		}{{n3, "n3"}, {n1, "n1"}} {
			for _, ext := range []string{"pem", "key"} {
				b := mustSSH(t, f.from.ip, "cat /var/lib/tango/cluster/node."+ext)
				mustSSH(t, n2.ip, "cat > /tmp/rot/"+f.name+"."+ext+" <<'EOF'\n"+strings.TrimSpace(b)+"\nEOF")
			}
		}
		ctl := func(name string) string {
			return strings.TrimSpace(mustSSH(t, n2.ip, "curl -sk -o /dev/null -w '%{http_code}' --max-time 10 --cert /tmp/rot/"+name+".pem --key /tmp/rot/"+name+".key https://"+n1.ip+":24010/v1/info || true"))
		}
		// Antes de rotar, el certificado de n3 abre el canal de n1 desde una IP del clúster.
		if code := ctl("n3"); code != "200" {
			t.Fatalf("antes de rotar, el certificado de n3 desde n2 dio %s (se esperaba 200)", code)
		}
		var r jobRef
		n1.c.call("cluster.volume.mount", map[string]any{"name": rev, "enabled": true}, &r)
		n1.c.waitJob(r.Job, 2*time.Minute)
		mustSSH(t, n1.ip, "echo antes-de-rotar > /cluster/"+rev+"/rotacion.txt && sync")

		n1.c.call("cluster.ca.rotate", map[string]any{}, &r)
		n1.c.waitJob(r.Job, 8*time.Minute)

		s1 := n1.c.waitCluster2("gv_rev en línea con la CA nueva", 3*time.Minute, func(s clStatus2) bool {
			for _, v := range s.Volumes {
				if v.Name == rev {
					return v.TLS && v.Online == 2 && s.TLS.CAFingerprint != before.TLS.CAFingerprint
				}
			}
			return false
		})
		s2 := n2.c.cluster2()
		if s2.TLS.CAFingerprint != s1.TLS.CAFingerprint {
			t.Fatalf("n1 y n2 muestran CA distintas: %q / %q", s1.TLS.CAFingerprint, s2.TLS.CAFingerprint)
		}
		if s1.TLS.Fingerprint != fp1 || s2.TLS.Fingerprint != fp2 {
			t.Fatal("las huellas de los nodos cambiaron al rotar")
		}
		if len(s1.TLS.Revoke) != 0 {
			t.Fatalf("la lista de pendientes no se limpió: %+v", s1.TLS.Revoke)
		}
		for _, n := range []*clusterNode{n1, n2} {
			if out := mustSSH(t, n.ip, "grep -c 'BEGIN CERTIFICATE' /etc/ssl/glusterfs.ca /var/lib/tango/cluster/ca.pem; ls /var/lib/tango/cluster"); strings.Contains(out, ":2") || strings.Contains(out, "next") || strings.Contains(out, "prev") {
				t.Fatalf("%s quedó a medio rotar:\n%s", n.ip, out)
			}
		}
		if out := mustSSH(t, n1.ip, "cat /cluster/"+rev+"/rotacion.txt"); strings.TrimSpace(out) != "antes-de-rotar" {
			t.Fatalf("después de rotar, gv_rev no está montado o perdió el archivo: %q", out)
		}
		// El canal y los bricks rechazan lo firmado con la CA anterior.
		if code := ctl("n3"); code == "200" {
			t.Fatal("después de rotar, el certificado de n3 sigue abriendo el canal de control")
		}
		if code := ctl("n1"); code == "200" {
			t.Fatal("después de rotar, el certificado anterior de n1 sigue abriendo el canal de control")
		}
		port := strings.TrimSpace(mustSSH(t, n1.ip, "gluster volume status "+rev+" | awk '/volume2/ && /"+n1.ip+"/ {print $3}' | head -1"))
		out, _ := sshTo(n2.ip, "echo hola | timeout 10 openssl s_client -connect "+n1.ip+":"+port+" -cert /tmp/rot/n1.pem -key /tmp/rot/n1.key -quiet 2>&1 | head -5")
		if l := strings.ToLower(out); !strings.Contains(l, "alert") && !strings.Contains(l, "error") {
			t.Fatalf("el brick %s:%s aceptó el certificado anterior de n1:\n%s", n1.ip, port, out)
		}
		mustSSH(t, n2.ip, "rm -rf /tmp/rot")
		n1.c.call("cluster.volume.mount", map[string]any{"name": rev, "enabled": false}, &r)
		n1.c.waitJob(r.Job, 2*time.Minute)
	})

	t.Run("apagar TLS y desarmar", func(t *testing.T) {
		if os.Getenv("TANGO_E2E_CLUSTER_KEEP") == "1" {
			t.Skip("TANGO_E2E_CLUSTER_KEEP=1: el clúster queda armado (capturas de la UI)")
		}
		var r jobRef
		n1.c.call("cluster.tls.set", map[string]bool{"enabled": false}, &r)
		n1.c.waitJob(r.Job, 6*time.Minute)
		s := n1.c.waitCluster2("sin TLS", 2*time.Minute, func(s clStatus2) bool { return !s.TLS.Secure && len(s.Volumes) == 1 && !s.Volumes[0].TLS })
		_ = s
		n1.c.call("cluster.volume.action", map[string]string{"name": rev, "action": "stop"}, nil)
		n1.c.call("cluster.volume.action", map[string]string{"name": rev, "action": "delete"}, nil)
		for _, n := range []*clusterNode{n1, n2} {
			s := n.c.waitCluster2("huérfanos de gv_rev en "+n.ip, time.Minute, func(s clStatus2) bool {
				for _, o := range s.Orphans {
					if o.Name == rev {
						return true
					}
				}
				return false
			})
			for _, o := range s.Orphans {
				n.c.call("cluster.brick.cleanup", map[string]string{"kind": o.Kind, "tangoVolume": o.TangoVolume, "name": o.Name}, nil)
			}
		}
		n1.c.call("cluster.peer.remove", map[string]string{"host": n2.ip}, nil)
		// n3 quedó solo con TLS de management prendido: se apaga ahí también.
		n3.c.call("cluster.tls.set", map[string]bool{"enabled": false}, &r)
		n3.c.waitJob(r.Job, 3*time.Minute)
		for _, n := range nodes {
			n.c.waitCluster2(n.ip+" solo y limpio", 2*time.Minute, func(s clStatus2) bool { return !s.TLS.Secure && len(s.Volumes) == 0 && len(s.Orphans) == 0 })
		}
	})
}

func waitTask(t *testing.T, poll func() (string, bool)) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	var st string
	for time.Now().Before(deadline) {
		var done bool
		if st, done = poll(); done {
			if st != "completed" {
				t.Fatalf("la tarea terminó en %q", st)
			}
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("la tarea no terminó (último estado %q)", st)
}
