# Instalador ISO de TangoOS

Dos formas de construir la ISO. Las dos instalan **Debian 13 minimal + el `.deb` de TangoOS** (ADR 0008).

| | Fase 1: netinst + preseed | Fase 2: por imagen (**recomendada**) |
|---|---|---|
| Script | `build.sh` | `image/build-image.sh` |
| Cómo instala | debian-installer baja Debian de `deb.debian.org` | copia un Debian ya armado (squashfs) al disco |
| Internet al instalar | sí | **no** |
| Tiempo medido (VM, 2 vCPU, 2 GB) | 422 s (UEFI) | **83–89 s** UEFI desatendida; gráfica: 33–35 s de instalación |
| Tamaño | 621 MB | 533 MB (388 MB sin el instalador gráfico; sha256 junto a la ISO) |
| Interfaz | texto (d-i) | **gráfica por defecto** (pantalla local + otra PC con código); modo texto opcional |
| Preguntas | disco (si hay más de uno) + confirmar borrado | idioma/teclado, disco + confirmar borrado, nombre, red (DHCP/IP fija) |
| Make | `make iso-netinst` | `make iso` |

Tiempos = desde `qm start` hasta que `https://<ip>/` sirve el escritorio de TangoOS (incluye los
5 s del menú de GRUB y el reinicio). Medido el 2026-10-14 en el Proxmox de desarrollo (VM 109).
La versión anterior de la imagen (328 MB, sin dependencias ni listas de apt) tardaba 59 s BIOS /
68–76 s UEFI; agregar Samba, NFS y las listas de apt sumó ~60 MB y ~15–20 s.

ISOs generadas en el Proxmox (`local:iso/`):

| Archivo | Qué es |
|---|---|
| `tangoos-0.1.0-dev-amd64.iso` | **producto**: instalador gráfico por defecto (o modo texto), DHCP, sin SSH |
| `tangoos-0.1.0-dev-amd64-test.iso` | laboratorio: entrada por defecto sin preguntas (disco `TANGO-SYS`, IP 172.17.0.46), SSH de root con la llave de desarrollo (también en el live) |
| `tangoos-0.1.0-dev-amd64-netinst-test.iso` | fase 1 (netinst + preseed), laboratorio, IP 172.17.0.45 |

## Fase 2: ISO por imagen (`image/`)

`build-image.sh` (en un Debian/Proxmox, como root):

1. `debootstrap --variant=minbase trixie` con kernel, GRUB (BIOS + UEFI), Btrfs, systemd-networkd,
   Avahi, `live-boot` y `whiptail`. Se cachea en `$WORK/rootfs-base`; los rebuilds solo cambian el `.deb`.
2. Configura dentro del rootfs: hostname `tango`, `es_AR.UTF-8`, teclado `latam`, zona Buenos Aires,
   fuentes de apt (trixie, trixie-updates, trixie-security), sin recomendados, root bloqueado,
   `apt-get update` + instala `tangoos.deb` **con sus dependencias** y los servicios que maneja
   (`samba`, `nfs-kernel-server`; ver `RUNTIME_PKGS`), habilita `tangod` y `tango-agent.socket`,
   deja NFS apagado, escribe un `smb.conf` mínimo con el `include` de TangoOS y **conserva las
   listas de apt** (solo borra cachés).
3. Arma la **capa del instalador gráfico** con overlayfs sobre ese rootfs: `cage cog libpam-systemd
   fonts-dejavu-core`, el binario `tango-installer` (Go), `tango-kiosk`, las unidades, DHCP del live y
   un drop-in para que `tangod` no arranque en modo instalador. La capa es solo la diferencia
   (`installer.squashfs`, ~152 MB) y **nunca se copia al disco**.
4. Comprime el sistema en `live/filesystem.squashfs` (zstd), escribe `live/filesystem.module`
   (orden de capas) y arma la ISO híbrida BIOS/UEFI con `grub-mkrescue` + su `.sha256`.

Menú de arranque (GRUB, 5 s):

| Entrada | Parámetro | Qué corre |
|---|---|---|
| **Instalar TangoOS** (por defecto) | `tango.installer` | `tango-installer.service` (web :80) + `tango-kiosk.service` (cage + cog en tty1) |
| Instalar TangoOS en modo texto | `tango.installer=text` | `tango-installer-text.service` (whiptail en tty1) |
| Probar TangoOS sin instalar | — | el sistema completo en RAM |
| Arrancar el TangoOS instalado | — | por defecto si encuentra `tangoos-boot` |
| Instalar TangoOS (laboratorio, desatendido) | `tango.installer=text tango.auto tango.disk=… tango.ip=…` | solo en la variante `test`, y es su entrada por defecto |

El modo gráfico se ve en la pantalla del equipo y desde otra PC en `http://<ip>/` con el código de
6 dígitos que muestra la pantalla (sin pantalla gráfica, tty1 muestra la URL y el código). Detalles
de diseño y seguridad: docs/08 §Instalador gráfico. Los dos modos usan el mismo motor, `tango-install`
(`--list-disks`; `--disk … --yes [--hostname] [--keyboard] [--ip] [--no-reboot] [--progress]`):

1. **Disco**: lista los discos (nunca el medio de instalación). Si hay más de uno pregunta cuál;
   siempre pide confirmar el borrado mostrando modelo, serie y tamaño (docs/04).
2. **Particiones** (GPT): `bios_grub` 1 MB · ESP 512 MB · `/boot` ext4 1 GB · raíz **Btrfs** con
   subvolúmenes `@rootfs`, `@var-log`, `@tango`, `@snapshots` (docs/08) y `compress=zstd:1`.
3. `unsquashfs` del sistema al disco, `fstab` por UUID, nombre del equipo, teclado, red DHCP o IP fija
   (systemd-networkd).
4. Quita `live-boot`, regenera el initramfs, instala GRUB para BIOS **y** UEFI (también en la ruta
   removible `EFI/BOOT/BOOTX64.EFI`, así arranca aunque el firmware pierda la entrada NVRAM).
5. Reinicia. `tangod` arranca solo y la consola muestra `https://<nombre>.local` y `https://<ip>`
   (`/etc/issue`; no hay usuarios de consola: root queda bloqueado).

Si el medio queda puesto y el firmware arranca primero desde USB/CD, el GRUB de la ISO busca la
partición `tangoos-boot` y, si existe, arranca el sistema instalado por defecto (sin esto, en BIOS
se reinstalaba en bucle). La opción "Probar TangoOS sin instalar" levanta el sistema completo en RAM.

Parámetros de kernel (solo laboratorio): `tango.auto` (no pregunta si hay un único disco),
`tango.disk=TANGO-SYS` (con `tango.auto`: el disco por serie o ruta aunque haya varios) y
`tango.ip=172.17.0.46/24,172.17.0.1,8.8.8.8` (IP fija). La variante `test` los agrega a su entrada
por defecto y copia `authorized_keys` para SSH de root (instalado y live); la variante `product` no
trae SSH habilitado.

## Fase 1: netinst + preseed (`build.sh`)

Remasteriza `debian-13.4.0-amd64-netinst.iso` con `xorriso` sin desarmarla:

- agrega `preseed.cfg` al `initrd.gz` del instalador de texto (cpio concatenado);
- reemplaza los menús de GRUB (UEFI) e isolinux (BIOS) por "Instalar TangoOS" con 5 s de espera;
- copia el `.deb` a `/tangoos/` en la ISO; `preseed/late_command` lo instala y habilita `tangod`;
- reutiliza los registros de arranque originales (`-boot_image any replay`): híbrida BIOS/UEFI/USB.

Archivos: `preseed.cfg` (común), `preseed-product.cfg` (DHCP, sin SSH, **pide confirmar** el borrado),
`preseed-test.cfg` (laboratorio: sin preguntas, IP fija, llave SSH).

Detalles que costaron:
- d-i no acepta `!` como contraseña de root: se pone el hash de una clave aleatoria y `late_command`
  hace `passwd -l root`.
- Con `console=ttyS0` el instalador solo ofrece inglés (la consola serie no muestra UTF-8) y pregunta
  el idioma: la variante de laboratorio no usa consola serie.
- `postinstall` del `.deb` no habilita `tangod` dentro de un chroot (no hay systemd corriendo):
  el instalador hace `systemctl enable tangod.service` explícito.

## Usar la ISO

- **Proxmox**: las ISOs quedan en `local:iso/`. VM q35, OVMF o SeaBIOS, disco ≥ 16 GB, 2 GB de RAM.
- **USB**: es híbrida → `sudo dd if=tangoos-0.1.0-dev-amd64.iso of=/dev/sdX bs=4M status=progress conv=fsync`
  (en macOS `/dev/rdiskN`). Ojo: elegí bien el dispositivo; `dd` no pregunta.

## Construir

```bash
make iso                        # fase 2, variante product → /var/lib/vz/template/iso/tangoos-<ver>-amd64.iso en el Proxmox
make iso ISO_VARIANT=test       # laboratorio
make iso-netinst                # fase 1
```

Dependencias en el host de build (instaladas en el Proxmox para este trabajo):
`xorriso debootstrap squashfs-tools mtools` (+ `grub-pc-bin grub-efi-amd64-bin`, que Proxmox ya trae).
El primer build hace debootstrap (~3 min) y lo cachea; los siguientes tardan ~2–2,5 min (la capa del
instalador gráfico se reinstala con apt; los `.deb` quedan en `$WORK/apt-archives`). `make iso` compila
también `dist/tango-installer` y lo sube junto a los scripts.

Pendientes conocidos:
- `grub-mkrescue` usa el GRUB del host (en el Proxmox es `2.12-9+pmx2`); para builds reproducibles
  conviene correrlo con el GRUB del propio rootfs.
- Secure Boot: la ISO no está firmada (se probó con OVMF sin claves precargadas).
- Resuelto: el `postinstall` del `.deb` usa `deb-systemd-helper` y habilita `tangod` también dentro
  de un chroot; los instaladores igual lo habilitan explícitamente (inocuo).
