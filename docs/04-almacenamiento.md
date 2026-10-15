# 04 — Almacenamiento

## Decisión: mdadm + Btrfs (el modelo de Synology)

Synology no usa el RAID de Btrfs. Usa `mdadm` para la redundancia, opcionalmente LVM, y Btrfs
**de un solo dispositivo** encima, para tener snapshots, subvolúmenes, checksums y compresión.
TangoOS hace lo mismo:

```
 disco A ─┐
 disco B ─┼─ /dev/md/tango-vol1 (RAID1/5/6/10, mdadm, metadata 1.2)
 disco C ─┘        │
              [dm-crypt opcional, LUKS2]
                   │
              Btrfs (single, metadata DUP) ── /volume1
                   ├─ @shares/Familia      (subvolumen = carpeta compartida)
                   ├─ @shares/Fotos
                   ├─ @apps/…              (datos de contenedores)
                   └─ @snapshots/…         (read-only, programados)
```

**Por qué no RAID5/6 de Btrfs:** el problema del *write hole* y los riesgos ante cortes de
energía siguen documentados upstream. Btrfs RAID1/RAID1c3 sí son confiables, pero mdadm
ofrece una paleta completa con una sola herramienta y una historia de recuperación conocida.

**Limitación honesta:** con Btrfs single sobre md, el checksum *detecta* la corrupción
pero no puede *repararla* desde otra copia (md no expone qué espejo está bien). Synology lo
parchea en el kernel; nosotros no. Mitigaciones: scrub de md (`check`) y de Btrfs
programados, alertas claras, y el modo "Btrfs RAID1 nativo" como opción avanzada para
volúmenes de 2 discos (ADR 0004).

## Perfiles soportados por hito

| Perfil | MVP (M2) | Después |
|---|---|---|
| Básico (1 disco) | ✅ | |
| RAID1 (2 discos) | ✅ | |
| RAID5 / RAID6 / RAID10 | | M4, con tests de fallo y rebuild |
| Híbrido tipo SHR (discos de distinto tamaño) | | M6. Se particiona en "rebanadas" de igual tamaño, se arma un md por rebanada y se une con LVM. Así lo hace SHR |
| Caché SSD (lectura) | | evaluar `dm-cache` / `bcache` |
| Cifrado de volumen (LUKS2) | | M4, desbloqueo con clave o archivo de clave en USB |
| iSCSI (LUN sobre archivo Btrfs con `nodatacow`, o LV) | | M4 |
| ZFS (pool como alternativa a md+Btrfs) | | M6, paquete opcional `tangoos-zfs` |

## Ciclo de vida de un disco

`detectado → inspeccionado → elegible → confirmado → en operación → verificado`

- **Identidad:** WWN → serie+modelo → `by-id`. Si no hay identidad estable, el disco queda
  **no elegible** y solo se puede leer.
- **No elegible si:** es el disco del sistema, tiene algo montado, es miembro de un md activo,
  tiene una firma desconocida que el usuario no reconoció explícitamente, o es USB (los USB
  solo se usan como destino de respaldo).

## Token de confirmación (operaciones destructivas)

1. `storage.volumes.plan {disks:[wwn…], profile:"raid1"}` → devuelve el plan (qué se borra,
   capacidad resultante) y un `confirm_token` = HMAC(clave de sesión, op + wwns + tamaños +
   firmas actuales + expiración de 5 min).
2. La UI muestra el modelo, la serie, el tamaño y el contenido detectado de cada disco, y pide
   escribir el **nombre del volumen**.
3. `storage.volumes.create {plan_id, confirm_token}`. El agente **recalcula** las firmas y
   aborta si algo cambió (`DEVICE_CHANGED`).

## Snapshots

- Read-only, en `@snapshots/<share>/<ISO8601>`. Las políticas (cada hora, día o semana +
  retención) se guardan en SQLite y las ejecuta un scheduler de `tangod` vía agente.
- **Restaurar un archivo:** el file-worker monta el snapshot de solo lectura y copia con
  reflink (instantáneo). **Restaurar una carpeta compartida entera:** se crea un snapshot del
  estado actual y se reemplaza el subvolumen.
- Las versiones anteriores se ven desde Windows (`vfs_shadow_copy2` en Samba) y desde la app
  Archivos ("Versiones anteriores"). Samba lee `/<vol>/@snapshots/<Carpeta>/.gmt`: un symlink por
  instantánea con el nombre exacto del formato (las programadas llevan sufijo de nivel y Samba
  no las abriría). El agente lo mantiene al crear/borrar instantáneas y al aplicar permisos.
- Replicación (M5): `btrfs send | receive` incremental sobre un DataChannel entre dos TangoOS.

## Cuotas

Se usan **simple quotas** (`btrfs quota enable --simple`, kernel ≥ 6.7; Debian 13 trae
6.12), porque las qgroups clásicas degradan el rendimiento con muchos snapshots. Hay cuotas
por carpeta compartida y por usuario (vía subvolumen home).

## Salud

- SMART con `smartctl --json`, sondeado cada 30 min y al detectar un error de I/O.
- El estado de md (`/proc/mdstat` + eventos `mdadm --monitor`) y los errores de Btrfs
  (`btrfs device stats`) se empujan como eventos.
- Scrub programado mensual (md `check` + `btrfs scrub`), con progreso en vivo.

## Batería de pruebas (discos virtuales, QEMU)

Escenarios de falla cubiertos, incluidos los propios de md:

- disco vacío / con ext4 / con Btrfs existente / con particiones montadas
- se elige por error el disco del sistema
- se quita el disco antes de confirmar y durante la operación
- corte de energía durante la creación del array y durante un rebuild
- degradar RAID1 → reemplazar disco → rebuild → verificar los checksums de los datos
- snapshot → borrar archivo → restaurar → comparar sha256
- se pierde SQLite pero el filesystem queda → la reconciliación recupera los shares

El harness usa `qemu-system-x86_64` con discos `qcow2` *throwaway* y `virtio-scsi` (para
tener seriales estables), y corre en CI.

## Implementación M2 (lo que hace hoy el código)

- **Quién hace qué:** `tangod` arma el plan (`disk.inspect` del agente: huella = SHA-256 de
  tamaño + primeros 128 KiB + último MiB; contenido detectado por firmas en Go) y firma el
  token. `tango-agent` revalida justo antes de escribir: disco entero por by-id, no del
  sistema (comparando con `/proc/1/mountinfo`), sin montajes ni holders, abierto con
  `O_EXCL` y con la misma huella. Recién entonces borra firmas (en Go: primer/último MiB y
  copias del superbloque Btrfs).
- **RAID 1:** `mdadm --create … --metadata=1.2 --homehost=any --bitmap=internal --run` (sin
  `--assume-clean`): el nombre queda `any:<volumen>` y el array se ensambla como
  `/dev/md/<volumen>` en cualquier host, también en el initramfs (que no conoce el hostname)
  y en otro NAS al que se muden los discos. La línea `ARRAY` se agrega a `mdadm.conf`.
- **Btrfs:** `mkfs.btrfs -m dup -d single`; el UUID se lee del superbloque en Go.
- **Montaje:** unidad `/etc/systemd/system/<volumen>.mount` (por `UUID=`) habilitada y
  arrancada por D-Bus; está en `multi-user.target` (un disco de datos roto no impide
  arrancar) y **sin** `After=local-fs.target` (armaba un ciclo de orden y systemd borraba
  el montaje del arranque).
- **Subvolúmenes, instantáneas y scrub:** ioctls de Btrfs desde el agente. El scrub hace
  primero `check` de md (sysfs) y después `BTRFS_IOC_SCRUB`, con progreso en vivo.
- **Samba:** una sección por carpeta en `/etc/samba/tango.d/`, índice `tango.conf`, una sola
  línea `include` en `smb.conf`; se valida con `testparm` (se revierte si falla) y se
  recarga `smbd` por D-Bus.
- **Probado en la VM** (TANGO-D1 + D2): crear RAID 1, carpeta SMB, instantánea read-only,
  scrub sin errores y reinicio con ensamblado y montaje automáticos.
- **Pendiente:** permisos de usuarios en las carpetas (hoy son `root 755`: Samba las lista
  pero no hay usuarios SMB), tamaños por cuotas, programador de instantáneas, borrar
  volúmenes/carpetas, RAID 5/6/10, reemplazo de disco y rebuild.

## Estado M2.1 (implementado y probado en la VM 108)

- **Perfiles:** Básico, RAID 1, RAID 5 (≥3), RAID 6 (≥4), RAID 10 (≥4, par), con mdadm
  `--metadata=1.2 --homehost=any --bitmap=internal`, nunca `--assume-clean`. Con discos de
  distinto tamaño md usa el del más chico y el plan lo avisa en `notes`.
- **Eliminar un volumen** (plan + token + nombre escrito): despublica sus carpetas de Samba,
  detiene y borra la unidad `.mount`, `mdadm --stop`, `--zero-superblock` en cada miembro,
  borrado de firmas en Go y quita la línea de `mdadm.conf`. Antes de tocar nada el agente
  verifica que los discos sean exactamente los miembros del array.
- **Eliminar una carpeta:** instantáneas y subvolumen con `BTRFS_IOC_SNAP_DESTROY` y su
  sección de Samba.
- **Usuarios y permisos:** usuario Unix sin login (UID ≥ 2000, `tango-users`) + cuenta Samba
  por usuario; grupos `tg-<carpeta>-rw/-ro`; ACL POSIX escrita como xattr (sin `setfacl`).
  `useradd`/`groupadd`/`gpasswd`/`smbpasswd` por allow-list (ADR 0009: editar
  `/etc/shadow` a mano en Go es más riesgo que beneficio). Por eso la unidad del agente pasó
  a `ProtectSystem=yes` (`/etc` escribible) y suma `CAP_FOWNER`/`CAP_FSETID`.
- Pendientes: reemplazo de disco/rebuild guiado, expansión, cuotas, programador de
  instantáneas, permisos de archivos creados por SMB con bit de ejecución (heredan rwx de la
  ACL por defecto).

## Mantenimiento (M2.2, implementado)

- **Reemplazar un disco:** Almacenamiento → disco fallado → «Reemplazar». Usa plan + token + nombre escrito, igual que crear (protocol/README.md §11). md reconstruye en segundo plano; el volumen muestra `task: rebuild` con velocidad y tiempo restante, y la alerta de redundancia se cierra sola al terminar.
- **Ampliar:** solo RAID 5/6/10. Se agregan discos y md reorganiza (`reshape`, puede tardar horas); al terminar, Btrfs se agranda solo. Un RAID 1 se agranda reemplazando sus discos por otros más grandes.
- **Instantáneas programadas:** cada carpeta tiene por defecto horarias ×24, diarias ×14 y semanales ×8. Las manuales nunca se borran por retención.
- **Alertas:** sin redundancia, solo lectura, errores de Btrfs, poco espacio y SMART. Se resuelven solas cuando el problema desaparece.
