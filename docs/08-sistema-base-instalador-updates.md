# 08 — Sistema base, instalador y actualizaciones

## Modelo: Debian 13 minimal + "la cara" de TangoOS

TangoOS **no es una distro nueva ni una imagen inmutable**. Es un Debian 13 "trixie" minimal
real, con una capa propia encima, del mismo modo que DSM es un Linux con capas:

| Capa | Qué hace | En DSM | En TangoOS |
|---|---|---|---|
| Kernel + base | arranque, hardware, paquetes | Linux propio | **Debian 13 minimal** (kernel 6.12 LTS) |
| Almacenamiento | discos → volúmenes redundantes | mdadm + LVM + Btrfs ("SHR") | mdadm (+ LVM para el híbrido) + Btrfs; ZFS opcional ([04](04-almacenamiento.md)) |
| Servicios de red | compartir archivos | Samba, NFS, AFP, iSCSI | Samba, NFS, WebDAV, SFTP, iSCSI (LIO) |
| Daemon de gestión | traduce clics a operaciones del sistema | propietario | `tangod` + `tango-agent` (Go) |
| Interfaz | el escritorio | web | escritorio web + apps nativas sobre WebRTC |

Consecuencia: todo lo que suma TangoOS se instala como **paquetes `.deb`** y se actualiza con
**apt**. Debian sigue entregando los parches de seguridad del sistema base con su ciclo normal.

## Paquetes de TangoOS

| Paquete | Contenido |
|---|---|
| `tangoos` | `tangod`, `tango-agent`, `tangoctl`, unidades systemd, plantillas (`/usr/share/tangoos/templates`) |
| `tangoos-system` | metapaquete con las dependencias: `mdadm lvm2 btrfs-progs cryptsetup samba nfs-kernel-server targetcli-fb openssh-server avahi-daemon wsdd2 smartmontools nftables chrony nut-client libvips-tools ffmpegthumbnailer` |
| `tangoos-branding` | GRUB/Plymouth, `motd`, `/etc/os-release` extendido (`VARIANT=TangoOS`), consola con URL/QR del NAS |
| Docker (bajo demanda) | `docker.io` + `docker-compose` de Debian; se instala desde el Centro de apps, no viene por defecto |
| `tangoos-zfs` (opcional, M6) | `zfs-dkms` + `zfsutils-linux` desde `contrib` |

- Se construyen con **nFPM** (como go-link). Se publican en un **repositorio apt propio**
  (`deb https://apt.tangoos.org trixie main`) firmado con una clave GPG offline. Así queda
  todo bajo la política de seguridad de apt (`signed-by=`).
- Los archivos de config de Debian que TangoOS gestiona (`smb.conf`, `exports`, nftables)
  **no se pisan**: se generan en `*.d/` o `include` y se declaran con `dpkg-divert` cuando no
  hay otra salida. La regla es que un `apt upgrade` de Debian nunca rompa la capa de TangoOS.

## Instalación: dos caminos

1. **Sobre un Debian 13 ya instalado: instalación por script** (quien ya tiene Debian, VPS, pruebas):
   ```bash
   curl -fsSL https://tangoos.go-link.org/install.sh | sh
   ```
   [`install.sh`](../web/apps/connect/public/install.sh) (se publica con el portal) verifica Debian 13
   amd64 con systemd y que `:80`/`:443` estén libres (`TANGOOS_FORCE=1` para seguir igual), agrega el
   **repositorio APT firmado** `https://tangoos.go-link.org/apt` (suite `stable`, fuente deb822 en
   `/etc/apt/sources.list.d/tangoos.sources` con `Signed-By`) e instala `tangoos` + Samba, NFS (apagado
   hasta activarlo en Ajustes, como en la ISO) y Avahi. Al final muestra `https://<ip>/` y dónde está la
   contraseña inicial de admin (`/var/lib/tango/initial-admin-password`, 0600; no la imprime).
   - La clave pública del repo (`TangoOS Archive`, huella `B343 1243 8C28 49B6 83A0  5319 9C06 A62D C9C4 DB37`)
     va **embebida en el script** (no se descarga aparte) y en `build/keys/tangoos-archive-keyring.gpg`;
     un test (`internal/aptrepo`) verifica que sean la misma. La privada vive fuera del repo, en
     `~/.config/tangoos/apt-gnupg` (respaldarla junto con la de releases del escritorio).
   - Las **actualizaciones** de TangoOS llegan por ese repo con el mecanismo de siempre (Ajustes →
     Actualizaciones, `system.updates.*`).
   - **Diferencia con la ISO:** usa el Debian tal como está (ext4 u otro): **no hay instantánea de
     `@rootfs` ni vuelta atrás del sistema** antes de cada actualización. El script lo avisa.
   - Publicar una versión: `make deb apt-repo` (copia el `.deb` al pool de `dist/apt`, regenera
     `Packages`/`Release` y firma `InRelease`/`Release.gpg` con [`cmd/tango-aptrepo`](../cmd/tango-aptrepo))
     y `make apt-publish` ([`scripts/publish-apt.sh`](../scripts/publish-apt.sh): verifica la firma con
     `gpgv`, sube el pool como inmutable y los índices sin caché, invalida CloudFront). Una versión
     publicada no se puede reemplazar con otro contenido: hay que subir el número.
   - **CloudFront** responde `index.html` (200) a cualquier ruta inexistente (fallback del portal).
     Por eso `Release` declara `Acquire-By-Hash: no` y lista solo `Packages` y `Packages.gz`: apt no
     pide nada más (sin `Translation-*`, `by-hash` ni `Contents`), verificado con el log del servidor.
     `scripts/deploy-connect-web.sh` excluye `apt/*` de su `--delete` y sube `install.sh` como
     `text/plain`.
2. **ISO de TangoOS** (usuario final): **instalación por imagen**, implementada en
   [`system/iso/`](../system/iso/README.md). La ISO trae un Debian 13 minimal ya armado
   (debootstrap + `tangoos.deb`, comprimido en squashfs); el instalador lo copia al disco elegido.
   - **No necesita internet** para instalar. Después, apt actualiza desde `deb.debian.org`.
   - La imagen trae **TangoOS con todas sus dependencias preinstaladas** (mdadm, btrfs-progs,
     Samba, nfs-kernel-server, passwd, Avahi) y **las listas de apt**: `apt-get install` funciona
     sin un `apt-get update` previo. Docker no viene: se instala bajo demanda (ADR 0010).
   - `smb.conf` mínimo: solo publica las carpetas de TangoOS (sin `[homes]` ni impresoras), SMB2
     como mínimo y `vfs_fruit` para Mac. NFS viene instalado pero **apagado** hasta que se active
     en Ajustes.
   - **Instalador gráfico por defecto** (ver abajo); el **modo texto** (whiptail) queda como segunda
     entrada del menú de arranque. Los dos usan el mismo motor (`tango-install`).
   - Preguntas: **idioma y teclado**, **disco de sistema** (con confirmación de borrado que muestra
     modelo, serie y tamaño), **nombre del equipo** y **red** (DHCP o IP fija). Zona Buenos Aires
     fija; se cambia después desde el escritorio. Los demás discos **no se tocan**.
   - Al terminar, la consola muestra `https://<nombre>.local` y `https://<ip>`. El resto (admin,
     nombre, certificado, primer volumen) va en el **asistente de primer arranque** del escritorio web.
   - Si el USB/CD queda puesto, el menú de la ISO detecta el TangoOS instalado y arranca ese por
     defecto, así no se reinstala en bucle.

   **Tiempos medidos** (VM Proxmox, 2 vCPU, 2 GB RAM, disco de 16 GB), desde que la VM
   arranca la ISO hasta que `https://<ip>/` sirve el escritorio:

   | ISO | Firmware | Tiempo total | Copia + GRUB |
   |---|---|---|---|
   | Por imagen con dependencias (laboratorio, 388 MB, 2026-10-14 23:10) | UEFI (OVMF) | **90 s** (2 corridas) | — |
   | Por imagen sin dependencias (laboratorio, 328 MB, versión anterior) | UEFI (OVMF) | 68–76 s | 30–33 s |
   | Por imagen sin dependencias (versión anterior) | BIOS (SeaBIOS) | 59 s | 30 s |
   | Por imagen sin dependencias (producto, 2 teclas, versión anterior) | UEFI | ~80 s de máquina | 33 s |
   | netinst + preseed (descarga de internet) | UEFI | 422 s (7 min) | — |

   | Por imagen, instalador gráfico (producto, 533 MB, 2026-10-15) | UEFI | ~41 s hasta ver el instalador; instalación **33–35 s**; reinicio → HTTPS 17–26 s | 33–35 s |
   | Por imagen, laboratorio desatendido (533 MB, 2026-10-15, VM con 2 discos) | UEFI | **83–89 s** | 32 s |
   | Por imagen, modo texto interactivo (producto, 2026-10-15) | UEFI | instalación 47 s con las teclas; reinicio → HTTPS 21 s | — |

   El camino netinst + preseed (`make iso-netinst`) queda como alternativa para hardware que el
   kernel de la imagen no soporte o para quien quiera el instalador oficial de Debian.

### Instalador gráfico

Como el *Web Assistant* de Synology, pero también en la pantalla del equipo:

```
GRUB ─ Instalar TangoOS (por defecto) ──► live + capa installer.squashfs
       │                                   ├─ tango-installer.service  Go, HTTP :80 (internal/installer)
       │                                   └─ tango-kiosk.service      cage + cog (WPE) en tty1 → http://127.0.0.1/
       │                                         sin pantalla gráfica → consola con URL + código
       ├─ Instalar TangoOS en modo texto ──► tango-installer-text.service (whiptail)
       ├─ Probar TangoOS sin instalar
       └─ Arrancar el TangoOS instalado (por defecto si existe)
                       ambos modos ──► tango-install (motor único: particiones, copia, GRUB)
```

- **Una sola lógica de instalación**: `tango-install` es el motor. El modo texto lo usa interactivo;
  la interfaz gráfica lo llama con parámetros (`--disk … --yes --hostname --keyboard --ip --progress`)
  y lee líneas `@@ <pct> <texto>`. El servidor Go solo valida (disco de la lista del motor, nombre,
  teclado de una lista, IPv4/máscara/puerta de enlace en la misma red) y pasa argumentos sin shell.
- **Dos capas squashfs** apiladas por live-boot (`live/filesystem.module`): `filesystem.squashfs` es
  el sistema que se copia al disco; `installer.squashfs` (cage, cog, el servidor, las unidades, DHCP
  del live y un drop-in que no arranca `tangod` con `tango.installer`) **nunca llega al disco**:
  verificado por SSH en sistemas instalados por las dos vías.
- **Desde otra PC**: la pantalla local muestra `http://<ip>/` y un **código de 6 dígitos**. La LAN
  necesita ese código (cookie `HttpOnly`, `SameSite=Strict`, 12 h); 5 errores bloquean 30 s y el
  bloqueo se duplica. La pantalla local (127.0.0.1) no lo pide. Los POST exigen
  `X-Tango-Installer: 1` + JSON (sin CSRF) y hay CSP estricta. El servidor existe solo en el live:
  después de instalar no queda nada escuchando.
- **Navegador del kiosco**: medido como capa sobre el rootfs (squashfs zstd‑15): **cog 158 MB**,
  firefox-esr 192 MB, chromium 247 MB. Se eligió cog (WPE WebKit, pensado para kiosco). La ISO pasó
  de 388 MB a **533 MB**; el grueso es libLLVM + gallium (render por software sin GPU) y WebKit.
- Peculiaridades de cog que la interfaz contempla: no genera `click` a partir de
  `pointerdown/up` (la página lo sintetiza si no llega), su lista nativa de `<select>` se queda con
  la entrada (la interfaz no usa `<select>`), arranca a 1024×768 antes de tomar la pantalla (repinta
  entera al cargar) y no informa un idioma útil (el servidor manda el de la pantalla local, `es`).
- Laboratorio: `tango.auto tango.disk=TANGO-SYS` (Makefile `ISO_TEST_DISK`) instala sin preguntas
  aunque haya varios discos; la ISO de laboratorio también habilita SSH **en el live**
  (`tango-live-ssh.service`, solo si el medio trae `authorized_keys`).

### Disco de sistema dedicado

```
bios_grub 1M | ESP 512M (vfat) | /boot 1G (ext4, label tangoos-boot) | root (resto, Btrfs, compress=zstd:1)
                                      ├─ @rootfs      → /
                                      ├─ @var-log     → /var/log      (fuera de los snapshots del root)
                                      ├─ @tango       → /var/lib/tango (SQLite, journal del agente)
                                      └─ @snapshots   → snapshots del root antes de cada update
```

Se usa un disco aparte (SSD/NVMe/USB de calidad), más simple y seguro que el modelo Synology de
replicar el sistema en todos los discos de datos. El modelo Synology (RAID1 md del sistema en
todas las bahías) queda como opción a evaluar.

## Actualizaciones

- **Desde la UI** (app Ajustes → Actualizaciones): `tangod` le pide al agente
  `system.update.check` / `system.update.apply`. Se muestra en vivo, como un job, qué paquetes
  cambian (los de Debian y los de TangoOS separados) y el log.
- **Antes de aplicar:** snapshot Btrfs de `@rootfs` + backup de SQLite. Si `tangod` no vuelve
  *healthy* tras el reinicio/restart, la UI (o el menú de GRUB, con entradas para los snapshots
  generadas por `grub-btrfs`) permite **volver al snapshot anterior**.
- Los parches de seguridad de Debian se instalan solos (`unattended-upgrades`, solo
  `trixie-security`), configurable. Las versiones de TangoOS nunca se instalan solas por
  defecto.
- **Migraciones de SQLite:** *forward-only*, ejecutadas por `tangod` al arrancar, con backup
  previo automático.
- Salto de versión mayor de Debian (trixie → forky): procedimiento guiado, solo cuando TangoOS
  lo valide. Nunca automático.

## Endurecimiento del Debian base

- Sin servidor gráfico, sin paquetes recomendados (`APT::Install-Recommends "0"`).
- SSH desactivado por defecto; cuando se activa, solo con clave y sin root.
- nftables por defecto: solo la subred local para 443, 8443, 3478, 445, 2049, 3260.
- `systemd-journald` con límite de tamaño; `chrony` para la hora (relevante para TOTP y TLS).
- AppArmor activo (viene en Debian) con perfiles para `tangod` y el file-worker.

## Modo recuperación

Una entrada de GRUB arranca el **snapshot anterior** o el root en `systemd.unit=tango-rescue.target`:
`tangod` arranca en modo recuperación y los volúmenes de datos se montan **solo lectura**. El mismo
escritorio web expone Almacenamiento (inspección), Registro, Terminal y "Restaurar configuración".
Nunca formatea. Exporta un diagnóstico sin secretos.

## Backup de configuración

`system.config.export` genera un `.tangobak`: SQLite + plantillas, cifrado con una contraseña
(age). La importación valida versión y esquema, y muestra un diff antes de aplicar.
