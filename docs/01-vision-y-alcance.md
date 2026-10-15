# 01 — Visión y alcance

## Misión

Un NAS que se sienta como un sistema operativo de escritorio pulido: abrís `https://tango.local`
(o la app nativa) y estás en un escritorio con Dock, ventanas y menú superior, donde todo se
actualiza en tiempo real. Open source (MIT), sin telemetría, sin depender de una nube propietaria.

## Principios de producto

1. **Los datos primero.** Ninguna operación destructiva sin confirmación ligada al disco; nada se
   formatea "por las dudas".
2. **Tiempo real por defecto.** No hay botón "refrescar". Discos, jobs, usuarios conectados y
   métricas se empujan desde el servidor.
3. **Un solo escritorio, muchos clientes.** El mismo bundle corre en el navegador, en la app de
   Windows y en la de Linux.
4. **Estado real o explícitamente demo.** La UI nunca inventa valores.
5. **Todo es auditable.** Cada acción administrativa deja un evento con usuario, origen y resultado.
6. **Calidad visual de producto.** La vara es Mac-OS-Desktop / DSM: animaciones a 60 fps,
   tipografía cuidada, estados vacíos útiles.

## Mapa de paridad con Synology DSM

Los nombres son provisorios y en castellano (la UI tiene i18n). Ver [11](11-licencias-y-marca.md)
para evitar nombres de marca.

| Equivalente DSM | App TangoOS | MVP | Más adelante |
|---|---|---|---|
| Storage Manager | **Almacenamiento** | discos, SMART, volumen mdadm+Btrfs (single/RAID1), scrub | RAID5/6/10, híbrido tipo SHR, caché SSD, expansión online |
| File Station | **Archivos** | navegar, subir/bajar, renombrar, mover, papelera, vista previa | compartir por enlace, búsqueda indexada, compresión |
| Control Panel | **Ajustes** | usuarios, grupos, carpetas compartidas, permisos, red, hora, SMB | LDAP/AD, cuotas, UPS, notificaciones, hardware/energía |
| Snapshot Replication | **Instantáneas** | snapshots programados, retención, restaurar archivo | replicación `btrfs send` a otro TangoOS |
| Hyper Backup | **Respaldo** | — | rsync/restic hacia USB, otro NAS o S3 |
| Resource Monitor | **Monitor** | CPU, RAM, red, I/O de disco en vivo | historial, por proceso/servicio |
| Log Center | **Registro** | auditoría + logs de sistema filtrables | reenvío syslog |
| Package Center + Container Manager | **Centro de apps** | — | catálogo de apps en contenedores (Compose) |
| Security Advisor | **Seguridad** | sesiones activas, 2FA, bloqueo por IP | escáner de configuración |
| QuickConnect | **TangoConnect** | — | acceso remoto vía signalhub + coturn |
| Synology Drive / Photos | — | — | apps del Centro de apps |
| iSCSI Manager | **iSCSI** (en Almacenamiento) | — | targets/LUNs con LIO, CHAP |
| Terminal (SSH) | **Terminal** | terminal admin sobre canal `term` (xterm.js) | — |
| Notificaciones | **Centro de notificaciones** | eventos del sistema en vivo | email / ntfy / Telegram |

Servicios de red del MVP: **SMB3** (con soporte de Time Machine vía `vfs_fruit`),
**NFS** (v4.2, `nfs-kernel-server`), **descubrimiento** (Avahi mDNS + WS-Discovery nativo en
`tangod` para que aparezca en Windows) y **SSH/SFTP** (SFTP con chroot por usuario). En M4: **iSCSI** (LIO /
`targetcli-fb`, LUN = archivo o zvol/LV), **WebDAV** y rsync daemon.

## Público

1. Primero: usuario técnico / homelab que hoy usa DSM, TrueNAS u OMV y quiere algo más lindo y moderno.
2. Después: pymes (el AD/LDAP y el respaldo son condición).

## No-objetivos (por ahora)

- Soporte universal de placas ARM: x86-64 primero; ARM64 con **una** placa de referencia en M5.
- ZFS en el MVP. Entra en M6 como opción (`tangoos-zfs`, con `zfs-dkms` de `contrib`), porque la licencia CDDL impide meterlo en el kernel y DKMS complica las actualizaciones de kernel.
- Máquinas virtuales, Kubernetes, clustering.
- Un sistema de archivos o un servidor SMB propios.
- Telemetría.

## Métrica de éxito del MVP

Alguien instala TangoOS en un mini-PC con 2 discos, crea un volumen RAID1, crea una carpeta
compartida, la monta desde macOS y Windows, sube archivos desde el escritorio web, borra uno
y lo recupera desde una instantánea. Todo sin abrir una terminal, y viendo el progreso en vivo
en cada paso.
