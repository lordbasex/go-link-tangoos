package diskops

import (
	"fmt"
	"sort"
	"strings"
)

// MountUnitName es el nombre de la unidad .mount de un volumen (/volume1 → volume1.mount).
// ValidVolumeName garantiza que no hace falta escapar nada.
func MountUnitName(name string) string { return name + ".mount" }

// MountUnit genera la unidad systemd que monta un volumen por el UUID del filesystem.
// Va en multi-user.target (no en local-fs.target): si un disco de datos falla, el NAS
// arranca igual y la UI muestra el problema. Sin After=local-fs.target: systemd ya
// ordena las .mount antes de local-fs.target y ese After armaba un ciclo que borraba
// el montaje del arranque.
func MountUnit(name, fsUUID string) string { return MountUnitFS(name, fsUUID, "btrfs") }

// mountOptions son las opciones de montaje de cada filesystem de un volumen.
var mountOptions = map[string]string{"btrfs": "noatime,space_cache=v2", "xfs": "noatime", "ext4": "noatime"}

// MountUnitFS es MountUnit para un filesystem cualquiera de un volumen (ADR 0011).
func MountUnitFS(name, fsUUID, fstype string) string {
	opts := mountOptions[fstype]
	if opts == "" {
		opts = "noatime"
	}
	return fmt.Sprintf(`# Generada por TangoOS (tango-agent). No editar: se regenera.
[Unit]
Description=TangoOS volume %[1]s
Documentation=https://github.com/lordbasex/go-link-tangoos

[Mount]
What=/dev/disk/by-uuid/%[2]s
Where=/%[1]s
Type=%[3]s
Options=%[4]s
TimeoutSec=120

[Install]
WantedBy=multi-user.target
`, name, fsUUID, fstype, opts)
}

// MDConfLine es la línea ARRAY de mdadm.conf para un array.
func MDConfLine(name string, s MDSuper) string {
	return fmt.Sprintf("ARRAY /dev/md/%s metadata=1.2 UUID=%s name=%s", name, s.UUID, s.Name)
}

// MergeMDConf agrega (o reemplaza) la línea de un array en el contenido de mdadm.conf,
// sin tocar el resto del archivo.
func MergeMDConf(conf, name, line string) string {
	var out []string
	marker := "ARRAY /dev/md/" + name + " "
	replaced := false
	for _, l := range strings.Split(strings.TrimRight(conf, "\n"), "\n") {
		if strings.HasPrefix(l, marker) {
			if !replaced {
				out = append(out, line)
				replaced = true
			}
			continue
		}
		if l == "" && len(out) == 0 {
			continue
		}
		out = append(out, l)
	}
	if !replaced {
		if len(out) == 0 {
			out = append(out, "# mdadm.conf — los arrays de TangoOS se agregan solos (tango-agent)")
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n") + "\n"
}

// ---- Samba -------------------------------------------------------------------------

// SambaIncludeLine es la única línea que TangoOS agrega a /etc/samba/smb.conf.
const SambaIncludeLine = "include = /etc/samba/tango.conf"

// ShareOpts son opciones de una carpeta por SMB (protocol/README.md §13).
type ShareOpts struct {
	TimeMachine bool  // destino de Time Machine (vfs_fruit)
	TMMaxBytes  int64 // tope para los respaldos (0 = sin tope)
}

func firstOpts(o []ShareOpts) ShareOpts {
	if len(o) > 0 {
		return o[0]
	}
	return ShareOpts{}
}

// tmLines son las opciones de Time Machine de una sección (vacío si no lo es).
func tmLines(o ShareOpts) string {
	if !o.TimeMachine {
		return ""
	}
	s := "   fruit:time machine = yes\n"
	if o.TMMaxBytes > 0 {
		gib := (o.TMMaxBytes + (1<<30 - 1)) >> 30 // en GiB, redondeado hacia arriba
		s += fmt.Sprintf("   fruit:time machine max size = %dG\n", gib)
	}
	return s
}

// SambaShare genera la sección de una carpeta compartida.
func SambaShare(name, path, comment string, opts ...ShareOpts) string {
	comment = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '[' || r == ']' {
			return ' '
		}
		return r
	}, comment)
	return fmt.Sprintf(`# Generada por TangoOS (tango-agent). No editar: se regenera.
[%s]
   comment = %s
   path = %s
   browseable = yes
   read only = no
   create mask = 0664
   directory mask = 0775
   vfs objects = %s
   fruit:metadata = stream
`, name, strings.TrimSpace(comment), path, vfsLine(path)) + tmLines(firstOpts(opts)) + shadowLines(path)
}

// SnapshotFormat es el formato strftime de los nombres de instantánea (SnapshotName).
// vfs_shadow_copy2 ignora el sufijo de nivel (".hourly", ".daily", ".weekly").
const SnapshotFormat = "%Y-%m-%dT%H-%M-%SZ"

// ShadowSnapdir es el directorio de instantáneas de una carpeta compartida
// (/<vol>/@shares/<Carpeta> → /<vol>/@snapshots/<Carpeta>), "" si la ruta no sigue
// el layout de TangoOS.
func ShadowSnapdir(path string) string {
	vol, share, ok := strings.Cut(path, "/@shares/")
	if !ok || vol == "" || share == "" || strings.Contains(share, "/") {
		return ""
	}
	return vol + "/@snapshots/" + share
}

// shadowLines publica las instantáneas como "Versiones anteriores" de Windows
// (vfs_shadow_copy2). La carpeta es un subvolumen, así que es su propio punto de
// montaje para Samba; las instantáneas se ven por los enlaces de .gmt (SyncShadowLinks).
func shadowLines(path string) string {
	snap := ShadowSnapdir(path)
	if snap == "" {
		return ""
	}
	return "   shadow:mountpoint = " + path + "\n" +
		"   shadow:snapdir = " + snap + "/" + ShadowLinkDir + "\n" +
		"   shadow:format = " + SnapshotFormat + "\n" +
		"   shadow:localtime = no\n" +
		"   shadow:sort = desc\n"
}

// vfsLine es la línea vfs objects de una sección: suma shadow_copy2 cuando hay
// directorio de instantáneas.
func vfsLine(path string) string {
	if ShadowSnapdir(path) != "" {
		return "fruit streams_xattr shadow_copy2"
	}
	return "fruit streams_xattr"
}

// SambaGlobals son los ajustes globales para clientes macOS (vfs_fruit, Time Machine).
// "multicast dns register = no": el anuncio Bonjour lo hace TangoOS (AvahiSMBService)
// para poder sumar _adisk._tcp y _device-info._tcp sin duplicar _smb._tcp.
const SambaGlobals = `[global]
   fruit:aapl = yes
   fruit:model = MacSamba
   fruit:nfs_aces = no
   fruit:posix_rename = yes
   fruit:veto_appledouble = no
   fruit:wipe_intentionally_left_blank_rfork = yes
   fruit:delete_empty_adfiles = yes
   fruit:metadata = stream
   multicast dns register = no
`

// SambaIndex genera /etc/samba/tango.conf: los globales para macOS e incluye cada
// archivo de /etc/samba/tango.d.
func SambaIndex(files []string) string {
	sort.Strings(files)
	var b strings.Builder
	b.WriteString("# Generado por TangoOS (tango-agent): globales para macOS e incluye cada carpeta compartida.\n")
	b.WriteString(SambaGlobals)
	for _, f := range files {
		b.WriteString("include = /etc/samba/tango.d/" + f + "\n")
	}
	return b.String()
}

// EnsureSambaInclude agrega la línea include al final de smb.conf si falta.
// Devuelve el contenido nuevo y si cambió.
func EnsureSambaInclude(conf string) (string, bool) {
	for _, l := range strings.Split(conf, "\n") {
		if strings.TrimSpace(l) == SambaIncludeLine {
			return conf, false
		}
	}
	if conf != "" && !strings.HasSuffix(conf, "\n") {
		conf += "\n"
	}
	return conf + "\n# TangoOS: carpetas compartidas (una sola línea; el resto vive en tango.conf)\n" + SambaIncludeLine + "\n", true
}

// ---- Bonjour (Avahi) para SMB y Time Machine ----------------------------------------

// AvahiServiceFile es el archivo de servicios de Avahi que publica TangoOS.
const AvahiServiceFile = "/etc/avahi/services/tangoos-smb.service"

// TimeMachineShares lee las carpetas con "fruit:time machine = yes" de las secciones
// de Samba (nombre de la sección), en orden.
func TimeMachineShares(sections []string) []string {
	var out []string
	for _, sec := range sections {
		name, tm := "", false
		for _, l := range strings.Split(sec, "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
				name = strings.TrimSuffix(strings.TrimPrefix(l, "["), "]")
			}
			if k, v, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == "fruit:time machine" && strings.EqualFold(strings.TrimSpace(v), "yes") {
				tm = true
			}
		}
		if tm && name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// AdiskTXT son los registros TXT de _adisk._tcp: sys + un dk<i> por carpeta de
// Time Machine (adVF=0x82: destino de Time Machine por SMB).
func AdiskTXT(tmShares []string) []string {
	out := []string{"sys=waMa=0,adVF=0x100"}
	for i, n := range tmShares {
		out = append(out, fmt.Sprintf("dk%d=adVN=%s,adVF=0x82", i, n))
	}
	return out
}

func xmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// AvahiSMBService genera el servicio de Avahi: _smb._tcp (445), _device-info._tcp
// (para que macOS muestre el ícono de Time Capsule) y, si hay carpetas de Time
// Machine, _adisk._tcp con sus registros TXT.
func AvahiSMBService(tmShares []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" standalone='no'?>
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<!-- Generado por TangoOS (tango-agent). No editar: se regenera. -->
<service-group>
  <name replace-wildcards="yes">%h</name>
  <service>
    <type>_smb._tcp</type>
    <port>445</port>
  </service>
  <service>
    <type>_device-info._tcp</type>
    <port>9</port>
    <txt-record>model=TimeCapsule8,119</txt-record>
  </service>
`)
	if len(tmShares) > 0 {
		b.WriteString("  <service>\n    <type>_adisk._tcp</type>\n    <port>9</port>\n")
		for _, t := range AdiskTXT(tmShares) {
			b.WriteString("    <txt-record>" + xmlEsc(t) + "</txt-record>\n")
		}
		b.WriteString("  </service>\n")
	}
	b.WriteString("</service-group>\n")
	return b.String()
}
