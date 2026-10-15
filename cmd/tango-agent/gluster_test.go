package main

import (
	"testing"

	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

func glBricks(vol string, hosts ...string) []gluster.Brick {
	var out []gluster.Brick
	for _, h := range hosts {
		out = append(out, gluster.Brick{Host: h, Path: gluster.BrickPath("volume1", vol)})
	}
	return out
}

func TestGlusterValidation(t *testing.T) {
	prod, _ := gluster.PresetByID("production-replica3")
	ok := []func() *agentproto.Error{
		func() *agentproto.Error {
			return validateGlusterCreate(&agentproto.GlusterVolumeCreateArgs{Name: "gv0", Layout: gluster.Layout{Replica: 3}, Bricks: glBricks("gv0", "n1", "n2", "n3"), Options: prod.Options})
		},
		func() *agentproto.Error {
			return validateGlusterPeer(&agentproto.GlusterPeerArgs{Action: "probe", Host: "172.17.0.49"})
		},
		func() *agentproto.Error {
			return validateGlusterOptions(&agentproto.GlusterVolumeOptionsArgs{Name: "gv0", Set: map[string]string{"network.ping-timeout": "20"}, Reset: []string{"performance.read-ahead"}})
		},
		func() *agentproto.Error {
			return validateGlusterMount(&agentproto.GlusterMountArgs{Name: "gv0", Enabled: true, Backups: []string{"n2", "n3"}, Share: "Cluster", RW: []string{"sofia"}})
		},
		func() *agentproto.Error {
			return validateGlusterBricks(&agentproto.GlusterBricksArgs{Name: "gv0", Op: "replace", From: glBricks("gv0", "n3")[0], To: glBricks("gv0", "n4")[0]})
		},
		func() *agentproto.Error { return validGlusterAllowed([]string{"172.17.0.49", "fe80::1"}) },
		func() *agentproto.Error {
			return validateGlusterBrickClean(&agentproto.GlusterBrickCleanArgs{TangoVolume: "volume1", Name: "gv0"})
		},
	}
	for i, f := range ok {
		if e := f(); e != nil {
			t.Errorf("caso válido %d: %v", i, e.Message)
		}
	}
	bad := map[string]func() *agentproto.Error{
		"opción fuera de la lista": func() *agentproto.Error {
			return validateGlusterCreate(&agentproto.GlusterVolumeCreateArgs{Name: "gv0", Layout: gluster.Layout{Replica: 3}, Bricks: glBricks("gv0", "n1", "n2", "n3"), Options: map[string]string{"storage.owner-uid": "0"}})
		},
		"peer con acción rara": func() *agentproto.Error {
			return validateGlusterPeer(&agentproto.GlusterPeerArgs{Action: "force", Host: "n2"})
		},
		"peer que parece opción": func() *agentproto.Error {
			return validateGlusterPeer(&agentproto.GlusterPeerArgs{Action: "probe", Host: "--remote-host=x"})
		},
		"reset de global": func() *agentproto.Error {
			return validateGlusterOptions(&agentproto.GlusterVolumeOptionsArgs{Name: "gv0", Reset: []string{"cluster.server-quorum-ratio"}})
		},
		"reset desconocido": func() *agentproto.Error {
			return validateGlusterOptions(&agentproto.GlusterVolumeOptionsArgs{Name: "gv0", Reset: []string{"features.trash"}})
		},
		"set y reset juntos": func() *agentproto.Error {
			return validateGlusterOptions(&agentproto.GlusterVolumeOptionsArgs{Name: "gv0", Set: map[string]string{"network.ping-timeout": "20"}, Reset: []string{"network.ping-timeout"}})
		},
		"compartir sin montar": func() *agentproto.Error {
			return validateGlusterMount(&agentproto.GlusterMountArgs{Name: "gv0", Share: "Cluster"})
		},
		"usuario de sistema": func() *agentproto.Error {
			return validateGlusterMount(&agentproto.GlusterMountArgs{Name: "gv0", Enabled: true, Share: "Cluster", RW: []string{"root"}})
		},
		"allow con red": func() *agentproto.Error { return validGlusterAllowed([]string{"172.17.0.0/24"}) },
		"allow todo":    func() *agentproto.Error { return validGlusterAllowed([]string{"0.0.0.0"}) },
		"limpiar fuera": func() *agentproto.Error {
			return validateGlusterBrickClean(&agentproto.GlusterBrickCleanArgs{TangoVolume: "etc", Name: "gv0"})
		},
		"brick de otro vol": func() *agentproto.Error {
			return validateGlusterBricks(&agentproto.GlusterBricksArgs{Name: "gv0", Op: "add", Bricks: glBricks("gv1", "n4")})
		},
		"acción de volumen": func() *agentproto.Error {
			return validateGlusterAction(&agentproto.GlusterVolumeActionArgs{Name: "gv0", Action: "rebalance"})
		},
	}
	for name, f := range bad {
		if f() == nil {
			t.Errorf("%s: debería rechazarse", name)
		}
	}
}
