# ADR 0009 — 100% Go nativo: syscalls, ioctl, netlink y D-Bus antes que `exec`

- Estado: Aceptado
- Contexto: el backend es 100% Go y la única pieza de software que se instala es nuestro `.deb`.
  No hay nginx, ni Node, ni Python, ni otro servidor para la web. Se quiere evitar
  `exec.Command` y wrappers de shell: son frágiles (parsean texto, dependen de la versión y del
  locale), lentos y amplían la superficie de ataque.
- Decisión: **regla de prioridad** para cada operación del sistema:
  1. **Kernel directo**: syscalls, `ioctl`, sysfs/procfs, netlink (`golang.org/x/sys/unix`).
  2. **D-Bus** hacia servicios que ya corren (systemd, logind, udisks no: no se usa).
  3. **Librería Go pura** que implemente el formato o protocolo.
  4. **`exec` como último recurso**: solo binarios de una allow-list, con argumentos armados por
     código (nunca un string de shell), salida en JSON cuando la herramienta lo soporta
     (`--json`) y siempre dentro de `tango-agent`.
  Cada uso de `exec` queda registrado en la tabla de abajo y tiene un ticket para reemplazarlo.
- Consecuencias: más código propio y tests de integración en VM. A cambio, errores tipados,
  cero parseo de texto en lo crítico y un binario autosuficiente. HTTPS, señalización,
  STUN/TURN y WebRTC ya son Go puro dentro de `tangod`.

## Mapa por área

| Área | Nativo en Go | Cómo | `exec` restante |
|---|---|---|---|
| Web, señalización, STUN/TURN, WebRTC | ✅ | `net/http` + `embed.FS`, `pion/webrtc`, `pion/turn` | — |
| Inventario de discos | ✅ | sysfs (`/sys/block/*`), `/dev/disk/by-id`, eventos udev por netlink (`NETLINK_KOBJECT_UEVENT`) | — |
| SMART | ✅ | `ioctl` SG_IO (ATA PASS-THROUGH) y NVMe admin (`NVME_IOCTL_ADMIN_CMD`); librería de referencia `github.com/anatol/smart.go` | `smartctl --json` solo como *fallback* para controladoras raras |
| Particionado GPT | ✅ | `github.com/diskfs/go-diskfs` + `BLKRRPART` | — |
| Estado de md (RAID) | ✅ | sysfs `/sys/block/mdX/md/*` (estado, `sync_action`, `degraded`, progreso) + `/proc/mdstat` | — |
| Crear / ensamblar md | 🟡 fase 2 | ioctl `SET_ARRAY_INFO` / `ADD_NEW_DISK` / `RUN_ARRAY` + escritura del superblock v1.2 en Go | `mdadm --create/--assemble/--manage` en el MVP (formato crítico: se reemplaza cuando haya tests de compatibilidad con mdadm) |
| Disparar scrub/check de md | ✅ | escribir `check` en `/sys/block/mdX/md/sync_action` | — |
| Btrfs: subvolúmenes, snapshots, cuotas, scrub, device stats, info | ✅ | ioctls `BTRFS_IOC_*` (lib de referencia `github.com/dennwc/btrfs`) | — |
| Btrfs: `mkfs` | ❌ | No hay implementación Go confiable del formato | `mkfs.btrfs` (allow-list) |
| Montar / desmontar | ✅ | `unix.Mount` / `unix.Unmount`, unidades `.mount` vía D-Bus para que persistan | — |
| LUKS2 | 🟡 | abrir: `dm-crypt` por ioctl de device-mapper | `cryptsetup luksFormat` (M4) |
| LVM (híbrido tipo SHR, M6) | 🟡 | device-mapper por ioctl para leer/activar | `lvm` para crear (evaluar) |
| Servicios (Samba, NFS, etc.) | ✅ | systemd por **D-Bus** (`github.com/coreos/go-systemd/v22/dbus`): start/stop/reload/enable | — |
| Config de Samba | ✅ | generar `smb.conf.d/` desde plantillas + `reload` por D-Bus | `testparm` para validar (evaluar validador propio) |
| Usuarios de Samba | ❌ | la base `passdb.tdb` es TDB | `pdbedit` / `smbpasswd` (allow-list) |
| NFS exports | ✅ | escribir `/etc/exports.d/tango.exports` + reload por D-Bus | — |
| Red (IP, rutas, enlaces, VLAN, bonding) | ✅ | netlink (`github.com/vishvananda/netlink`) | — |
| Firewall | ✅ | nftables por netlink (`github.com/google/nftables`) | — |
| Hostname, hora, NTP, zona | ✅ | `org.freedesktop.hostname1` / `timedate1` por D-Bus | — |
| Usuarios Unix | 🟡 | edición atómica de `/etc/passwd`, `shadow` y `group` con lock (`lckpwdf` semántica) | `useradd` en el MVP |
| Métricas (CPU, RAM, red, I/O, temperaturas) | ✅ | procfs, sysfs, hwmon | — |
| Logs del sistema | ✅ | lectura del journal (`sdjournal`) o del socket de journald | — |
| Apagar / reiniciar | ✅ | `org.freedesktop.login1` por D-Bus | — |
| Paquetes / updates (apt) | ❌ | — | `apt-get` (allow-list, dentro de un job con log) |
| Contenedores (M3.5) | ✅ | Docker Engine API por socket Unix (REST local), vía el agente | — |
| Snapshot del root antes del update | ✅ | ioctl Btrfs | — |

Leyenda: ✅ nativo desde el MVP · 🟡 nativo en una fase posterior · ❌ `exec` sin plan inmediato.

## Servicios externos que sí corren (y por qué)

Solo los que **implementan protocolos de red para clientes** y que no tiene sentido reescribir:

- **Samba** (`smbd`): SMB3 para Windows y macOS. No hay un servidor SMB en Go apto para producción.
- **nfsd**: está en el kernel; solo se configura.
- **sshd** (opcional, apagado por defecto).
- **avahi-daemon**: mDNS. A evaluar: reemplazarlo con mDNS en Go dentro de `tangod`
  (`github.com/pion/mdns` ya está en el árbol de dependencias por WebRTC).
- **wsdd2**: WS-Discovery para Windows. A evaluar: implementación Go propia (es un protocolo chico).

La interfaz, la gestión y el tiempo real: **solo `tangod` + `tango-agent`**.
