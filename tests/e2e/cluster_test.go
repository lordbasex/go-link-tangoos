//go:build e2e

// Clúster GlusterFS de 3 nodos TangoOS (docs/17-cluster.md, protocol/README.md §16).
//
//	TANGO_E2E_CLUSTER=172.17.0.48,172.17.0.49,172.17.0.50  los 3 nodos (TangoOS instalado)
//	TANGO_E2E_CLUSTER_VMIDS=112,113,114                     sus VM en Proxmox (para apagar uno)
//	TANGO_E2E_PVE=172.17.0.2                                el host Proxmox
//	TANGO_E2E_DESTRUCTIVE=1
//
// Cada nodo necesita un disco libre con serial TANGO-G<n> para su volumen de TangoOS.
// Recorre: firewall (sin invitación no hay unión), unión con invitación, volumen réplica 3
// con el preset de producción, montaje + SMB, escribir en un nodo y leer en otro, apagar
// un nodo (quórum 2/3), escribir, prenderlo y ver la curación, y borrar todo.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/rtc"
)

type clusterNode struct {
	ip   string
	vmid string
	c    *client
}

func sshTo(ip, cmd string) (string, error) {
	args := []string{"-i", sshKey(), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR", "-o", "ConnectTimeout=10", "-o", "BatchMode=yes", "root@" + ip, cmd}
	var out, errb bytes.Buffer
	c := exec.Command("ssh", args...)
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return out.String(), fmt.Errorf("ssh %s %q: %w: %s", ip, cmd, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func mustSSH(t *testing.T, ip, cmd string) string {
	t.Helper()
	out, err := sshTo(ip, cmd)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// nodeAdmin abre WebRTC con un nodo y entra como admin (contraseña inicial por SSH).
func nodeAdmin(t *testing.T, ip string) *client {
	t.Helper()
	pw := strings.TrimSpace(mustSSH(t, ip, "cat /var/lib/tango/initial-admin-password"))
	var rc *rtc.Client
	var err error
	for i := 0; i < 20; i++ {
		rc, err = rtc.Dial(ctxT(t, 30*time.Second), "wss://"+ip+"/signal?v=1", true)
		if err == nil {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if err != nil {
		t.Fatalf("%s: %v", ip, err)
	}
	t.Cleanup(func() { rc.Close() })
	if _, err := rc.Call(ctxT(t, 20*time.Second), "auth.login", map[string]string{"user": "admin", "password": pw}); err != nil {
		t.Fatalf("login en %s: %v", ip, err)
	}
	return &client{Client: rc, t: t, user: "admin"}
}

type clStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Self      string `json:"self"`
	Error     string `json:"error"`
	Nodes     []struct {
		Host      string `json:"host"`
		Connected bool   `json:"connected"`
		Self      bool   `json:"self"`
		LatencyMs int    `json:"latencyMs"`
	} `json:"nodes"`
	Volumes []struct {
		Name        string            `json:"name"`
		Status      string            `json:"status"`
		Type        string            `json:"type"`
		Replica     int               `json:"replica"`
		Online      int               `json:"online"`
		HealPending int               `json:"healPending"`
		SplitBrain  int               `json:"splitBrain"`
		Options     map[string]string `json:"options"`
		Bricks      []struct {
			Host   string `json:"host"`
			Path   string `json:"path"`
			Online bool   `json:"online"`
		} `json:"bricks"`
	} `json:"volumes"`
	Orphans []struct {
		TangoVolume string `json:"tango_volume"`
		Name        string `json:"name"`
	} `json:"orphans"`
	Mounts []struct {
		Name    string `json:"name"`
		Mounted bool   `json:"mounted"`
		Share   string `json:"share"`
	} `json:"mounts"`
	Allowed []string `json:"allowed"`
}

func (c *client) cluster() clStatus {
	var s clStatus
	c.call("cluster.status", nil, &s)
	return s
}

// waitCluster espera hasta que cond se cumpla (o falla con el último estado).
func (c *client) waitCluster(what string, timeout time.Duration, cond func(clStatus) bool) clStatus {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	var s clStatus
	for time.Now().Before(deadline) {
		s = c.cluster()
		if cond(s) {
			return s
		}
		time.Sleep(3 * time.Second)
	}
	b, _ := json.MarshalIndent(s, "", " ")
	c.t.Fatalf("%s: no se cumplió en %s\n%s", what, timeout, b)
	return s
}

func connectedPeers(s clStatus) int {
	n := 0
	for _, p := range s.Nodes {
		if p.Connected {
			n++
		}
	}
	return n
}

func pve(t *testing.T, cmd string) {
	t.Helper()
	p := os.Getenv("TANGO_E2E_PVE")
	if p == "" {
		t.Skip("falta TANGO_E2E_PVE para apagar un nodo")
	}
	mustSSH(t, p, cmd)
}

func TestCluster(t *testing.T) {
	destructive(t)
	ips := strings.Split(os.Getenv("TANGO_E2E_CLUSTER"), ",")
	if len(ips) != 3 || ips[0] == "" {
		t.Skip("exportá TANGO_E2E_CLUSTER con 3 nodos")
	}
	vmids := strings.Split(os.Getenv("TANGO_E2E_CLUSTER_VMIDS"), ",")
	nodes := make([]*clusterNode, 3)
	for i, ip := range ips {
		nodes[i] = &clusterNode{ip: ip, c: nodeAdmin(t, ip)}
		if len(vmids) == 3 {
			nodes[i].vmid = vmids[i]
		}
	}
	n1, n2, n3 := nodes[0], nodes[1], nodes[2]
	top := t
	const vol = "gv_e2e"
	const smbUser = "clsmb" // usuario de TangoOS con cuenta Samba (la contraseña inicial de admin no la tiene)
	smbPW := randomSecret(8)

	t.Run("volumen de TangoOS en cada nodo", func(t *testing.T) {
		for i, n := range nodes {
			if _, ok := n.c.volumes()["volume1"]; ok {
				continue
			}
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
	})

	t.Run("instalar GlusterFS con el firewall cerrado", func(t *testing.T) {
		for _, n := range nodes {
			if s := n.c.cluster(); s.Installed && s.Running {
				continue
			}
			var r jobRef
			n.c.call("cluster.install", nil, &r)
			n.c.waitJob(r.Job, 10*time.Minute)
		}
		for _, n := range nodes {
			s := n.c.cluster()
			if !s.Installed || !s.Running || len(s.Nodes) != 1 {
				t.Fatalf("%s: %+v", n.ip, s)
			}
			// Puertos cerrados para todos menos loopback: la tabla está cargada.
			if out := mustSSH(t, n.ip, "nft list table inet tango_gluster"); !strings.Contains(out, "reject with tcp reset") {
				t.Fatalf("%s sin firewall:\n%s", n.ip, out)
			}
		}
	})

	t.Run("sin invitación no se puede unir", func(t *testing.T) {
		// Estado limpio: sin invitaciones vivas y con n1 fuera de la allow-list de n2.
		for _, n := range []*clusterNode{n2, n3} {
			n.c.call("cluster.invite.cancel", map[string]string{"host": n1.ip}, nil)
		}
		n2.c.waitCluster("n1 fuera de la allow-list de n2", 2*time.Minute, func(s clStatus) bool {
			for _, a := range s.Allowed {
				if a == n1.ip {
					return false
				}
			}
			return len(s.Nodes) == 1
		})
		e := n1.c.callErr("cluster.peer.add", map[string]string{"host": n2.ip}, "")
		t.Logf("rechazado como se esperaba: %s %s", e.Code, e.Message)
		if s := n2.c.cluster(); len(s.Nodes) != 1 {
			t.Fatalf("n2 terminó en un clúster sin haber invitado: %+v", s.Nodes)
		}
	})

	t.Run("unir con invitación de los dos lados", func(t *testing.T) {
		n2.c.call("cluster.invite", map[string]string{"host": n1.ip}, nil)
		n3.c.call("cluster.invite", map[string]string{"host": n1.ip}, nil)
		n1.c.call("cluster.peer.add", map[string]string{"host": n2.ip}, nil)
		n1.c.call("cluster.peer.add", map[string]string{"host": n3.ip}, nil)
		n1.c.waitCluster("3 nodos conectados en n1", 2*time.Minute, func(s clStatus) bool { return len(s.Nodes) == 3 && connectedPeers(s) == 3 })
		// n2 y n3 se abren entre sí solos (los nodos del clúster entran a la allow-list).
		for _, n := range []*clusterNode{n2, n3} {
			n.c.waitCluster("3 nodos conectados en "+n.ip, 3*time.Minute, func(s clStatus) bool { return len(s.Nodes) == 3 && connectedPeers(s) == 3 })
		}
		s := n2.c.cluster()
		for _, ip := range []string{n1.ip, n3.ip} {
			found := false
			for _, a := range s.Allowed {
				found = found || a == ip
			}
			if !found {
				t.Errorf("n2 no tiene a %s en la allow-list: %v", ip, s.Allowed)
			}
		}
	})

	t.Run("validaciones", func(t *testing.T) {
		n1.c.callErr("cluster.volume.create", map[string]any{"name": vol, "replica": 3, "options": map[string]string{"storage.owner-uid": "0"},
			"bricks": []map[string]string{{"host": n1.ip, "tangoVolume": "volume1"}, {"host": n2.ip, "tangoVolume": "volume1"}, {"host": n3.ip, "tangoVolume": "volume1"}}}, "INVALID_PARAMS")
		n1.c.callErr("cluster.volume.create", map[string]any{"name": vol, "replica": 3,
			"bricks": []map[string]string{{"host": n1.ip, "tangoVolume": "volume1"}, {"host": n1.ip, "tangoVolume": "volume2"}, {"host": n3.ip, "tangoVolume": "volume1"}}}, "INVALID_PARAMS")
	})

	t.Run("crear réplica 3 con el preset de producción", func(t *testing.T) {
		var r jobRef
		n1.c.call("cluster.volume.create", map[string]any{"name": vol, "replica": 3, "preset": "production-replica3", "start": true,
			"bricks": []map[string]string{{"host": n1.ip, "tangoVolume": "volume1"}, {"host": n2.ip, "tangoVolume": "volume1"}, {"host": n3.ip, "tangoVolume": "volume1"}}}, &r)
		n1.c.waitJob(r.Job, 3*time.Minute)
		s := n1.c.waitCluster("volumen arrancado", time.Minute, func(s clStatus) bool { return len(s.Volumes) == 1 && s.Volumes[0].Online == 3 })
		v := s.Volumes[0]
		if v.Type != "Replicate" || v.Replica != 3 || v.Status != "Started" {
			t.Fatalf("%+v", v)
		}
		for k, want := range map[string]string{"cluster.quorum-type": "auto", "cluster.server-quorum-type": "server", "network.ping-timeout": "10",
			"cluster.server-quorum-ratio": "51%", "performance.write-behind": "off", "cluster.granular-entry-heal": "on"} {
			if v.Options[k] != want {
				t.Errorf("%s = %q, esperaba %q", k, v.Options[k], want)
			}
		}
		var info struct {
			Text string `json:"text"`
		}
		n1.c.call("cluster.volume.info", map[string]string{"name": vol}, &info)
		if !strings.Contains(info.Text, "Number of Bricks: 1 x 3 = 3") {
			t.Errorf("info:\n%s", info.Text)
		}
		// Igual que el CLI real.
		if cli := mustSSH(t, n1.ip, "gluster volume info "+vol); !strings.Contains(cli, "Number of Bricks: 1 x 3 = 3") || !strings.Contains(cli, "cluster.server-quorum-ratio: 51%") {
			t.Errorf("CLI:\n%s", cli)
		}
		t.Logf("gluster volume info:\n%s", info.Text)
	})

	t.Run("montar en dos nodos y compartir por SMB", func(t *testing.T) {
		// Puede haber quedado de una corrida cortada a la mitad.
		_, _ = n1.c.Call(ctxT(t, 30*time.Second), "users.delete", map[string]any{"name": smbUser, "typed_name": smbUser})
		n1.c.call("users.create", map[string]any{"name": smbUser, "full": "Clúster E2E", "email": "", "password": smbPW, "admin": false, "groups": []string{}}, nil)
		var r jobRef
		n1.c.call("cluster.volume.mount", map[string]any{"name": vol, "enabled": true, "share": "ClusterE2E", "comment": "Volumen del clúster", "rw": []string{smbUser}}, &r)
		n1.c.waitJob(r.Job, 2*time.Minute)
		n2.c.call("cluster.volume.mount", map[string]any{"name": vol, "enabled": true}, &r)
		n2.c.waitJob(r.Job, 2*time.Minute)
		s := n1.c.cluster()
		if len(s.Mounts) != 1 || !s.Mounts[0].Mounted || s.Mounts[0].Share != "ClusterE2E" {
			t.Fatalf("montajes: %+v", s.Mounts)
		}
		if out := mustSSH(t, n1.ip, "findmnt -n -o FSTYPE,OPTIONS /cluster/"+vol); !strings.Contains(out, "fuse.glusterfs") {
			t.Fatalf("findmnt: %s", out)
		}
	})

	t.Run("escribir en un nodo y leer en otro", func(t *testing.T) {
		mustSSH(t, n1.ip, "echo hola-desde-n1 > /cluster/"+vol+"/saludo.txt && sync")
		var got string
		for i := 0; i < 10; i++ {
			got, _ = sshTo(n2.ip, "cat /cluster/"+vol+"/saludo.txt")
			if strings.TrimSpace(got) == "hola-desde-n1" {
				break
			}
			time.Sleep(time.Second)
		}
		if strings.TrimSpace(got) != "hola-desde-n1" {
			t.Fatalf("n2 leyó %q", got)
		}
		// La copia física está en el brick de n3.
		if out := mustSSH(t, n3.ip, "cat /volume1/@gluster/"+vol+"/saludo.txt"); strings.TrimSpace(out) != "hola-desde-n1" {
			t.Fatalf("brick de n3: %q", out)
		}
	})

	t.Run("SMB sobre el volumen montado", func(t *testing.T) {
		mustSSH(t, n3.ip, "command -v smbclient >/dev/null || DEBIAN_FRONTEND=noninteractive apt-get -qq install -y smbclient >/dev/null 2>&1")
		cred := "printf 'username=" + smbUser + "\\npassword=%s\\n' \"$(cat)\" > /tmp/smbcred && chmod 600 /tmp/smbcred"
		c := exec.Command("ssh", "-i", sshKey(), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR", "root@"+n3.ip, cred)
		c.Stdin = strings.NewReader(smbPW)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("credenciales: %v %s", err, out)
		}
		out := mustSSH(t, n3.ip, "cd /tmp && echo por-smb > smb.txt && smbclient //"+n1.ip+"/ClusterE2E -A /tmp/smbcred -c 'put smb.txt; ls' 2>&1; rm -f /tmp/smbcred")
		if !strings.Contains(out, "smb.txt") {
			t.Fatalf("smbclient:\n%s", out)
		}
		if got := mustSSH(t, n2.ip, "cat /cluster/"+vol+"/smb.txt"); strings.TrimSpace(got) != "por-smb" {
			t.Fatalf("el archivo subido por SMB no llegó a n2: %q", got)
		}
	})

	t.Run("apagar un nodo: el quórum sigue con 2 de 3", func(t *testing.T) {
		if n3.vmid == "" {
			t.Skip("falta TANGO_E2E_CLUSTER_VMIDS")
		}
		pve(t, "qm stop "+n3.vmid)
		s := n1.c.waitCluster("n3 caído", 2*time.Minute, func(s clStatus) bool { return connectedPeers(s) == 2 && s.Volumes[0].Online == 2 })
		t.Logf("con n3 apagado: %d/3 bricks", s.Volumes[0].Online)
		mustSSH(t, n1.ip, "for i in 1 2 3 4 5 6 7 8 9 10; do echo dato$i > /cluster/"+vol+"/con-n3-caido-$i.txt; done; sync")
		if out := mustSSH(t, n2.ip, "ls /cluster/"+vol+" | grep -c con-n3-caido"); strings.TrimSpace(out) != "10" {
			t.Fatalf("n2 ve %s archivos", out)
		}
		s = n1.c.waitCluster("curación pendiente", time.Minute, func(s clStatus) bool { return s.Volumes[0].HealPending > 0 })
		t.Logf("pendientes de curar con n3 apagado: %d", s.Volumes[0].HealPending)
	})

	t.Run("prender el nodo y curar", func(t *testing.T) {
		if n3.vmid == "" {
			t.Skip("falta TANGO_E2E_CLUSTER_VMIDS")
		}
		pve(t, "qm start "+n3.vmid)
		for i := 0; i < 60; i++ {
			if _, err := sshTo(n3.ip, "systemctl is-active glusterd"); err == nil {
				break
			}
			time.Sleep(5 * time.Second)
		}
		n1.c.waitCluster("n3 de vuelta", 4*time.Minute, func(s clStatus) bool { return connectedPeers(s) == 3 && s.Volumes[0].Online == 3 })
		n3.c = nodeAdmin(top, n3.ip) // n3 se reinició: la sesión WebRTC vieja murió con él (vive lo que el test padre)
		// El demonio de curación de n3 tarda unos segundos en arrancar: HEAL_NOT_READY hasta entonces.
		for i := 0; ; i++ {
			_, err := n1.c.Call(ctxT(t, 30*time.Second), "cluster.volume.heal", map[string]any{"name": vol})
			if err == nil {
				break
			}
			if i == 12 || !strings.Contains(err.Error(), "HEAL_NOT_READY") {
				t.Fatalf("cluster.volume.heal: %v", err)
			}
			time.Sleep(5 * time.Second)
		}
		s := n1.c.waitCluster("curación completa", 5*time.Minute, func(s clStatus) bool { return s.Volumes[0].HealPending == 0 && s.Volumes[0].SplitBrain == 0 })
		t.Logf("curado: %+v", s.Volumes[0].Bricks)
		if out := mustSSH(t, n3.ip, "ls /volume1/@gluster/"+vol+" | grep -c con-n3-caido"); strings.TrimSpace(out) != "10" {
			t.Fatalf("el brick de n3 tiene %s archivos después de curar", out)
		}
		// El montaje de n2 no se cayó en todo el recorrido.
		if out := mustSSH(t, n2.ip, "cat /cluster/"+vol+"/con-n3-caido-7.txt"); strings.TrimSpace(out) != "dato7" {
			t.Fatalf("n2: %q", out)
		}
	})

	t.Run("borrar todo", func(t *testing.T) {
		var r jobRef
		for _, n := range []*clusterNode{n1, n2} {
			n.c.call("cluster.volume.mount", map[string]any{"name": vol, "enabled": false}, &r)
			n.c.waitJob(r.Job, 2*time.Minute)
		}
		if out, _ := sshTo(n1.ip, "grep -l ClusterE2E /etc/samba/tango.d/*.conf"); strings.TrimSpace(out) != "" {
			t.Errorf("quedó la carpeta SMB: %s", out)
		}
		n1.c.call("users.delete", map[string]any{"name": smbUser, "typed_name": smbUser}, nil)
		// Borrar un volumen arrancado falla; primero se detiene.
		n1.c.callErr("cluster.volume.action", map[string]string{"name": vol, "action": "delete"}, "")
		n1.c.call("cluster.volume.action", map[string]string{"name": vol, "action": "stop"}, nil)
		n1.c.call("cluster.volume.action", map[string]string{"name": vol, "action": "delete"}, nil)
		for _, n := range nodes {
			s := n.c.waitCluster("brick huérfano en "+n.ip, time.Minute, func(s clStatus) bool { return len(s.Orphans) == 1 })
			n.c.call("cluster.brick.cleanup", map[string]string{"tangoVolume": s.Orphans[0].TangoVolume, "name": s.Orphans[0].Name}, nil)
		}
		n1.c.call("cluster.peer.remove", map[string]string{"host": n2.ip}, nil)
		n1.c.call("cluster.peer.remove", map[string]string{"host": n3.ip}, nil)
		n1.c.waitCluster("n1 solo", time.Minute, func(s clStatus) bool { return len(s.Nodes) == 1 && len(s.Volumes) == 0 && len(s.Orphans) == 0 })
		for _, n := range []*clusterNode{n2, n3} {
			n.c.waitCluster(n.ip+" solo", time.Minute, func(s clStatus) bool { return len(s.Nodes) == 1 })
		}
		// Sin invitaciones ni nodos, la allow-list de n1 se vacía (el bucle la sincroniza).
		n1.c.waitCluster("allow-list vacía", 2*time.Minute, func(s clStatus) bool {
			for _, a := range s.Allowed {
				if a == n2.ip || a == n3.ip {
					return false
				}
			}
			return true
		})
	})
}
