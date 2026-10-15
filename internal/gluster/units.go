package gluster

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// ---- Montaje local ------------------------------------------------------------------

// MountPath es donde TangoOS monta un volumen del clúster.
func MountPath(vol string) string { return MountRoot + "/" + vol }

// MountUnitName es la unidad .mount de /cluster/<vol> (ValidVolumeName garantiza
// que no hay nada que escapar: sin guiones ni puntos).
func MountUnitName(vol string) string { return "cluster-" + vol + ".mount" }

// MountUnit monta el volumen por FUSE desde el glusterd local, con los otros nodos
// como servidores de respaldo del volfile (si este nodo arranca y el resto no, el
// montaje igual se resuelve en cuanto vuelve alguno). `acl` habilita las POSIX ACL
// que usan las carpetas compartidas por SMB.
func MountUnit(vol string, backups []string) string {
	opts := "_netdev,acl"
	var bk []string
	for _, h := range backups {
		if ValidHost(h) == nil {
			bk = append(bk, h)
		}
	}
	if len(bk) > 0 {
		opts += ",backup-volfile-servers=" + strings.Join(bk, ":")
	}
	return fmt.Sprintf(`# Generada por TangoOS (tango-agent). No editar: se regenera.
[Unit]
Description=TangoOS cluster volume %[1]s
Documentation=https://github.com/lordbasex/go-link-tangoos
After=network-online.target glusterd.service
Wants=network-online.target glusterd.service

[Mount]
What=localhost:/%[1]s
Where=%[2]s
Type=glusterfs
Options=%[3]s
TimeoutSec=90

[Install]
WantedBy=remote-fs.target
`, vol, MountPath(vol), opts)
}

// ---- Firewall de los puertos de GlusterFS --------------------------------------------
//
// glusterd acepta un `peer probe` de cualquiera que llegue al 24007: sin esto,
// cualquier equipo de la red podría sumar este NAS a su clúster y leer sus bricks.
// TangoOS cierra los puertos de GlusterFS para todos salvo los nodos permitidos: los
// que ya están en el clúster y las invitaciones abiertas a mano en este equipo.

// FirewallFile es el ruleset que carga tango-gluster-fw.service.
const FirewallFile = "/etc/tango/gluster.nft"

// FirewallUnitName es la unidad que aplica el ruleset (antes de glusterd).
const FirewallUnitName = "tango-gluster-fw.service"

// SplitAddrs separa IPv4 e IPv6 válidas, sin repetidos y ordenadas.
func SplitAddrs(addrs []string) (v4, v6 []string) {
	seen := map[string]bool{}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil || seen[ip.String()] {
			continue
		}
		seen[ip.String()] = true
		if ip.To4() != nil {
			v4 = append(v4, ip.To4().String())
		} else {
			v6 = append(v6, ip.String())
		}
	}
	sort.Strings(v4)
	sort.Strings(v6)
	return v4, v6
}

// FirewallRules arma el ruleset de nftables: una tabla propia (se reemplaza entera,
// sin tocar otras reglas del equipo) que solo deja entrar a los puertos de GlusterFS
// desde loopback y las direcciones permitidas.
func FirewallRules(allowed []string) string {
	v4, v6 := SplitAddrs(allowed)
	ports := fmt.Sprintf("{ %d, %d, %d-%d }", PortGlusterd, PortRDMA, PortBrickMin, PortBrickMax)
	set := func(name, typ string, elems []string) string {
		s := fmt.Sprintf("\tset %s {\n\t\ttype %s\n", name, typ)
		if len(elems) > 0 {
			s += "\t\telements = { " + strings.Join(elems, ", ") + " }\n"
		}
		return s + "\t}\n"
	}
	var b strings.Builder
	b.WriteString("# Generado por TangoOS (tango-agent). No editar: se regenera.\n")
	b.WriteString("# Puertos de GlusterFS solo para los nodos permitidos (docs/17 §3).\n")
	// Crear y borrar antes: así `nft -f` reemplaza la tabla de forma atómica.
	b.WriteString("table inet tango_gluster\ndelete table inet tango_gluster\n")
	b.WriteString("table inet tango_gluster {\n")
	b.WriteString(set("peers4", "ipv4_addr", v4))
	b.WriteString(set("peers6", "ipv6_addr", v6))
	b.WriteString("\tchain input {\n\t\ttype filter hook input priority -10; policy accept;\n")
	b.WriteString("\t\tiif \"lo\" accept\n")
	fmt.Fprintf(&b, "\t\ttcp dport %s ip saddr @peers4 accept\n", ports)
	fmt.Fprintf(&b, "\t\ttcp dport %s ip6 saddr @peers6 accept\n", ports)
	// reject y no drop: un nodo no invitado falla al instante en vez de esperar el timeout.
	fmt.Fprintf(&b, "\t\ttcp dport %s reject with tcp reset\n", ports)
	fmt.Fprintf(&b, "\t\tudp dport %d drop\n", PortGlusterd)
	b.WriteString("\t}\n}\n")
	return b.String()
}

// FirewallUnit aplica el ruleset en el arranque, antes de que glusterd escuche. Sin
// ExecStop a propósito: detener la unidad no abre los puertos (falla cerrado), y con
// RequiredBy glusterd no arranca si el ruleset no se pudo cargar.
const FirewallUnit = `# Generada por TangoOS (tango-agent). No editar: se regenera.
[Unit]
Description=TangoOS: GlusterFS ports only for cluster nodes
Documentation=https://github.com/lordbasex/go-link-tangoos
DefaultDependencies=no
Before=network-pre.target glusterd.service
Wants=network-pre.target
ConditionPathExists=` + FirewallFile + `

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f ` + FirewallFile + `
ExecReload=/usr/sbin/nft -f ` + FirewallFile + `

[Install]
WantedBy=multi-user.target
RequiredBy=glusterd.service
`
