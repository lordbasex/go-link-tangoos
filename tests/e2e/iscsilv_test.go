//go:build e2e

// LUN de iSCSI en un volumen lógico de un grupo de almacenamiento (protocol §12).
//
//	TANGO_E2E_ISCSI_LV=172.17.0.50   (NAS con un grupo de almacenamiento con espacio libre)
//	TANGO_E2E_DESTRUCTIVE=1
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

type poolE2E struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Free uint64 `json:"free"`
	LUNs []struct {
		Name string `json:"name"`
		Dev  string `json:"dev"`
		Size uint64 `json:"size"`
		Thin bool   `json:"thin"`
	} `json:"luns"`
}

func (c *client) poolList() map[string]poolE2E {
	var ps []poolE2E
	c.call("storage.pools.list", nil, &ps)
	m := map[string]poolE2E{}
	for _, p := range ps {
		m[p.Name] = p
	}
	return m
}

func TestIscsiOnLV(t *testing.T) {
	destructive(t)
	ip := os.Getenv("TANGO_E2E_ISCSI_LV")
	if ip == "" {
		t.Skip("exportá TANGO_E2E_ISCSI_LV con la IP de un NAS con un grupo de almacenamiento")
	}
	a := nodeAdmin(t, ip)
	var pool poolE2E
	for _, p := range a.poolList() {
		if p.Free >= 3<<30 {
			pool = p
		}
	}
	if pool.ID == "" {
		t.Skip("no hay un grupo con 3 GB libres")
	}
	mustSSH(t, ip, "command -v iscsiadm >/dev/null || DEBIAN_FRONTEND=noninteractive apt-get install -y -q open-iscsi >/dev/null")
	var svcs []struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	a.call("services.list", nil, &svcs)
	for _, s := range svcs {
		if s.ID == "iscsi" && !s.Enabled {
			var r jobRef
			a.call("services.set", map[string]any{"id": "iscsi", "enabled": true}, &r)
			if r.Job != "" {
				a.waitJob(r.Job, 3*time.Minute)
			}
		}
	}
	cleanup := func(names ...string) {
		_, _ = sshTo(ip, "iscsiadm -m node -u >/dev/null 2>&1; iscsiadm -m node -o delete >/dev/null 2>&1; true")
		for _, n := range names {
			var r jobRef
			if res, err := a.Call(ctxT(t, time.Minute), "iscsi.targets.delete", map[string]string{"name": n, "typed": n}); err == nil {
				_ = json.Unmarshal(res, &r)
				if r.Job != "" {
					a.waitJobAny(r.Job, 2*time.Minute)
				}
			}
		}
	}
	cleanup("lv-grueso", "lv-thin")
	t.Cleanup(func() { cleanup("lv-grueso", "lv-thin") })

	a.callErr("iscsi.targets.create", map[string]any{"name": "thin", "pool": pool.ID, "sizeBytes": 256 << 20}, "INVALID_PARAMS")
	a.callErr("iscsi.targets.create", map[string]any{"name": "lv-enorme", "pool": pool.ID, "sizeBytes": pool.Free + (1 << 30)}, "NO_SPACE")
	for _, c := range []struct {
		name string
		thin bool
	}{{"lv-grueso", false}, {"lv-thin", true}} {
		var r jobRef
		a.call("iscsi.targets.create", map[string]any{"name": c.name, "pool": pool.ID, "sizeBytes": 1 << 30, "thin": c.thin}, &r)
		a.waitJob(r.Job, 3*time.Minute)
	}
	p := a.poolList()[pool.Name]
	if len(p.LUNs) != 2 {
		t.Fatalf("LUN del grupo: %+v", p.LUNs)
	}
	for _, l := range p.LUNs {
		if l.Thin != (l.Name == "lv-thin") || l.Dev != "/dev/"+pool.Name+"/il_"+l.Name {
			t.Fatalf("LUN: %+v", l)
		}
		// LIO lo exporta con un backstore iblock sobre el LV.
		out := strings.TrimSpace(mustSSH(t, ip, "cat /sys/kernel/config/target/core/iblock_0/"+l.Name+"/udev_path"))
		if out != l.Dev {
			t.Fatalf("udev_path de %s = %q", l.Name, out)
		}
	}
	// Los LUN no son volúmenes de TangoOS.
	for _, v := range a.poolVolumes() {
		if strings.HasPrefix(v.Name, "il_") {
			t.Fatalf("un LUN aparece como volumen: %+v", v)
		}
	}
	// Un grupo con LUN no se borra.
	a.callErr("storage.pools.deletePlan", map[string]string{"pool": pool.ID}, "POOL_NOT_EMPTY")

	// Login real: el disco aparece con su tamaño y se puede escribir.
	iqn := "iqn.2026-10.local.tango:lv-grueso"
	out := mustSSH(t, ip, fmt.Sprintf(`set -e
iscsiadm -m discovery -t st -p 127.0.0.1 >/dev/null
iscsiadm -m node -T %[1]s -p 127.0.0.1 -l >/dev/null
sleep 2
D=$(lsblk -S -dno NAME,VENDOR,MODEL | awk '/LIO/ && /lv-grueso|IBLOCK/ {print $1; exit}')
[ -n "$D" ] || D=$(lsblk -S -dno NAME,VENDOR | awk '/LIO/{print $1}' | tail -1)
blockdev --getsize64 /dev/$D
dd if=/dev/urandom of=/dev/$D bs=1M count=8 oflag=direct status=none && echo escrito
iscsiadm -m node -T %[1]s -p 127.0.0.1 -u >/dev/null`, iqn))
	if !strings.Contains(out, "escrito") || !strings.Contains(out, fmt.Sprint(1<<30)) {
		t.Fatalf("login iSCSI sobre LV:\n%s", out)
	}

	// Borrar el destino borra el LV (y el pool thin al irse el último thin).
	cleanup("lv-grueso", "lv-thin")
	if p := a.poolList()[pool.Name]; len(p.LUNs) != 0 {
		t.Fatalf("quedaron LUN: %+v", p.LUNs)
	}
	if out := mustSSH(t, ip, "lvs --noheadings -o lv_name "+pool.Name+" | grep -c il_ || true"); strings.TrimSpace(out) != "0" {
		t.Fatalf("quedaron LV il_*: %s", out)
	}
}
