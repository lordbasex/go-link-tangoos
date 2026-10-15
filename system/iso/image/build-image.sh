#!/bin/sh
# ISO de TangoOS por imagen (docs/08, fase 2): Debian 13 minimal preconstruido + instalador que lo copia.
# Dos capas squashfs que live-boot apila (live/filesystem.module):
#   filesystem.squashfs  el sistema que se instala (y lo único que se copia al disco);
#   installer.squashfs   solo para el live: instalador gráfico (tango-installer + cage + cog).
# Corre en un Debian/Proxmox como root. Requiere: debootstrap squashfs-tools xorriso mtools grub-pc-bin grub-efi-amd64-bin.
#
#   ./build-image.sh --deb tangoos_0.1.0-dev_amd64.deb --out tangoos-0.1.0-dev-amd64.iso \
#                    [--installer tango-installer] \
#                    [--variant product|test --authorized-keys keys.pub --ip 172.17.0.45/24 --gw 172.17.0.1
#                     --disk TANGO-SYS]   (test: serie o ruta del disco de sistema, aunque haya varios)
#
# El rootfs se cachea en $WORK/rootfs-base (debootstrap tarda; reconstruir solo cambia el .deb).
set -eu
DEB= OUT= INSTALLER= VARIANT=product LABDISK= KEYS= IP= GW= WORK=${WORK:-/root/tangoos-iso/image-work} MIRROR=${MIRROR:-http://deb.debian.org/debian}
while [ $# -gt 0 ]; do
    case "$1" in
        --deb) DEB=$2; shift 2 ;; --out) OUT=$2; shift 2 ;; --variant) VARIANT=$2; shift 2 ;;
        --installer) INSTALLER=$2; shift 2 ;; --disk) LABDISK=$2; shift 2 ;;
        --authorized-keys) KEYS=$2; shift 2 ;; --ip) IP=$2; shift 2 ;; --gw) GW=$2; shift 2 ;;
        *) echo "argumento desconocido: $1" >&2; exit 2 ;;
    esac
done
[ -f "$DEB" ] && [ -n "$OUT" ] || { echo "faltan --deb o --out" >&2; exit 2; }
HERE=$(cd "$(dirname "$0")" && pwd)
INSTALLER=${INSTALLER:-$HERE/tango-installer}
[ -x "$INSTALLER" ] || { echo "falta el binario del instalador gráfico ($INSTALLER): make iso lo compila" >&2; exit 2; }
mkdir -p "$WORK"
BASE=$WORK/rootfs-base
R=$WORK/rootfs
ISO=$WORK/iso

# Paquetes del sistema instalado. Sin recomendados: Debian mínimo + lo que TangoOS necesita para arrancar.
PKGS=systemd-sysv,systemd-resolved,systemd-timesyncd,dbus,linux-image-amd64,initramfs-tools,\
grub-pc-bin,grub-efi-amd64-bin,grub2-common,efibootmgr,btrfs-progs,e2fsprogs,dosfstools,gdisk,\
parted,squashfs-tools,whiptail,avahi-daemon,libnss-mdns,ca-certificates,locales,tzdata,\
keyboard-configuration,openssh-server,apt,eject,zstd,live-boot,live-boot-initramfs-tools,\
iproute2,iputils-ping,curl,less,nano

# Lo que tangod usa en tiempo de ejecución además de los Depends del .deb (M2–M4).
RUNTIME_PKGS="samba nfs-kernel-server xfsprogs"  # xfsprogs: volúmenes XFS de los grupos (ADR 0011); lvm2 y cryptsetup llegan como Depends del .deb

# Solo en la capa del live (installer.squashfs): pantalla del instalador gráfico.
# cage = compositor Wayland de una sola app; cog = navegador WPE WebKit para kiosco (el más chico
# de los medidos: cog 158 MB, firefox-esr 192 MB, chromium 247 MB en squashfs; docs/08).
KIOSK_PKGS="cage cog libpam-systemd fonts-dejavu-core"

t() { echo "[$(date +%T)] $*"; }

# 1. rootfs base (cacheado)
if [ ! -f "$BASE/.ok" ]; then
    t "debootstrap trixie (minbase)"
    rm -rf "$BASE"
    debootstrap --variant=minbase --include="$PKGS" trixie "$BASE" "$MIRROR" >/dev/null
    touch "$BASE/.ok"
fi
t "copiando rootfs base"
rm -rf "$R"; cp -a "$BASE" "$R"

# 2. configurar el sistema + instalar TangoOS
cp "$DEB" "$R/tmp/tangoos.deb"
install -m 755 "$HERE/tango-install" "$R/usr/local/sbin/tango-install"
install -m 644 "$HERE/tango-installer-text.service" "$R/etc/systemd/system/tango-installer-text.service"
for m in dev dev/pts proc sys; do mount --bind /$m "$R/$m"; done
trap 'for m in sys proc dev/pts dev; do umount -l "$R/$m" 2>/dev/null || true; done' EXIT
chroot "$R" sh -eu <<CHROOT
export DEBIAN_FRONTEND=noninteractive
echo tango > /etc/hostname
printf '127.0.0.1\tlocalhost\n127.0.1.1\ttango.local tango\n::1\tlocalhost ip6-localhost ip6-loopback\n' > /etc/hosts
cat > /etc/apt/sources.list.d/debian.sources <<SRC
Types: deb
URIs: $MIRROR
Suites: trixie trixie-updates
Components: main non-free-firmware
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg

Types: deb
URIs: http://security.debian.org/debian-security
Suites: trixie-security
Components: main non-free-firmware
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg
SRC
rm -f /etc/apt/sources.list
echo 'APT::Install-Recommends "0";' > /etc/apt/apt.conf.d/99tangoos
sed -i 's/^# *es_AR.UTF-8/es_AR.UTF-8/; s/^# *en_US.UTF-8/en_US.UTF-8/' /etc/locale.gen
locale-gen >/dev/null
echo 'LANG=es_AR.UTF-8' > /etc/default/locale
ln -sf /usr/share/zoneinfo/America/Argentina/Buenos_Aires /etc/localtime
echo America/Argentina/Buenos_Aires > /etc/timezone
printf 'XKBMODEL="pc105"\nXKBLAYOUT="latam"\n' > /etc/default/keyboard
# TangoOS + sus dependencias (Depends del .deb + los servicios que maneja: SMB y NFS) vienen
# preinstalados: el sistema instalado funciona sin internet. Docker se instala bajo demanda (ADR 0010).
apt-get update -q >/dev/null
apt-get install -y -q --no-install-recommends /tmp/tangoos.deb $RUNTIME_PKGS >/dev/null
rm -f /tmp/tangoos.deb
systemctl enable tangod.service tango-agent.socket systemd-networkd systemd-resolved avahi-daemon tango-installer-text.service >/dev/null 2>&1
# Los servicios de archivos arrancan apagados: tangod los enciende desde Ajustes (services.set).
systemctl disable nfs-server.service >/dev/null 2>&1 || true
# smb.conf mínimo: solo las carpetas que publica TangoOS (sin [homes] ni impresoras), SMB2+ y
# compatibilidad con Mac. tangod agrega cada carpeta en /etc/samba/tango.d/ vía tango.conf.
cat > /etc/samba/smb.conf <<'SMB'
[global]
   workgroup = WORKGROUP
   server string = TangoOS
   server role = standalone server
   security = user
   passdb backend = tdbsam
   map to guest = never
   server min protocol = SMB2_10
   load printers = no
   printing = bsd
   printcap name = /dev/null
   disable spoolss = yes
   usershare max shares = 0
   vfs objects = fruit streams_xattr
   fruit:metadata = stream
   fruit:model = MacSamba
   logging = file
   log file = /var/log/samba/log.%m
   max log size = 1000
include = /etc/samba/tango.conf
SMB
: > /etc/samba/tango.conf
testparm -s >/dev/null 2>&1
systemctl disable ssh.service >/dev/null 2>&1 || true
passwd -l root >/dev/null
# Solo vienen grub-*-bin (sin el paquete que crea /etc/default/grub): lo escribimos nosotros.
cat > /etc/default/grub <<GRUBDEF
GRUB_DEFAULT=0
GRUB_TIMEOUT=2
GRUB_DISTRIBUTOR=TangoOS
GRUB_CMDLINE_LINUX_DEFAULT="quiet"
GRUB_CMDLINE_LINUX=""
GRUBDEF
update-initramfs -u -k all >/dev/null 2>&1
apt-get clean
# Las listas de apt quedan (apt-get install funciona sin "apt-get update"); solo se borran cachés.
rm -rf /var/cache/apt/*.bin /var/log/*.log /tmp/*
: > /etc/machine-id
CHROOT
for m in sys proc dev/pts dev; do umount -l "$R/$m"; done
trap - EXIT
# Consola: no hay usuarios de consola; agetty muestra dónde abrir el escritorio (\n = nombre, \4 = IPv4).
printf '\n  TangoOS - Debian 13\n\n  Abri el escritorio en  https://\\n.local  o  https://\\4\n\n' > "$R/etc/issue"

# 2b. Capa del instalador gráfico: overlay sobre el rootfs ya configurado; la capa es solo la
# diferencia (upperdir). Nunca llega al disco instalado: tango-install copia filesystem.squashfs.
L=$WORK/installer-layer
t "capa del instalador gráfico ($KIOSK_PKGS)"
umount -R "$L/m" 2>/dev/null || true
rm -rf "$L/up" "$L/wk"; mkdir -p "$L/up" "$L/wk" "$L/m" "$WORK/apt-archives/partial"
mount -t overlay overlay -o "lowerdir=$R,upperdir=$L/up,workdir=$L/wk" "$L/m"
for m in dev dev/pts proc sys; do mount --bind /$m "$L/m/$m"; done
mount --bind "$WORK/apt-archives" "$L/m/var/cache/apt/archives"
trap 'umount -R "$L/m" 2>/dev/null || true' EXIT
mkdir -p "$L/m/run/systemd/resolve"; cp -L /etc/resolv.conf "$L/m/run/systemd/resolve/stub-resolv.conf"
install -m 755 "$INSTALLER" "$L/m/usr/local/sbin/tango-installer"
install -m 755 "$HERE/tango-kiosk" "$L/m/usr/local/sbin/tango-kiosk"
install -m 644 "$HERE/tango-installer.service" "$HERE/tango-kiosk.service" "$HERE/tango-live-ssh.service" "$L/m/etc/systemd/system/"
install -D -m 644 "$HERE/tangod-installer.conf" "$L/m/etc/systemd/system/tangod.service.d/installer.conf"
install -m 644 "$HERE/20-live.network" "$L/m/etc/systemd/network/20-live.network"
chroot "$L/m" sh -eu <<CHROOT
export DEBIAN_FRONTEND=noninteractive
apt-get install -y -q --no-install-recommends $KIOSK_PKGS >/dev/null
useradd -r -m -d /var/lib/tango-kiosk -s /usr/sbin/nologin -G video,input,render tango-kiosk
systemctl enable tango-installer.service tango-kiosk.service tango-live-ssh.service >/dev/null 2>&1
CHROOT
umount -R "$L/m"
trap - EXIT
# Fuera de la capa: lo efímero y lo que el kiosco no usa (docs, man, traducciones de otros idiomas).
rm -rf "$L/up/run" "$L/up/tmp" "$L/up/var/cache" "$L/up/var/log" "$L/up/usr/share/doc" "$L/up/usr/share/man"
find "$L/up/usr/share/locale" -mindepth 1 -maxdepth 1 ! -name 'es*' ! -name 'en*' ! -name 'pt*' ! -name 'locale.alias' -exec rm -rf {} + 2>/dev/null || true
t "installer.squashfs"
mksquashfs "$L/up" "$WORK/installer.squashfs" -comp zstd -Xcompression-level 15 -noappend -quiet

# 3. ISO: kernel + initrd (con live-boot) + squashfs + GRUB BIOS/UEFI
t "squashfs"
rm -rf "$ISO"; mkdir -p "$ISO/live" "$ISO/boot/grub" "$ISO/tangoos"
cp "$R"/boot/vmlinuz-* "$ISO/live/vmlinuz"
cp "$R"/boot/initrd.img-* "$ISO/live/initrd.img"
mksquashfs "$R" "$ISO/live/filesystem.squashfs" -comp zstd -Xcompression-level 15 -e boot/efi -noappend -quiet
cp "$WORK/installer.squashfs" "$ISO/live/installer.squashfs"
# Orden de las capas de live-boot: de abajo hacia arriba.
printf 'filesystem.squashfs\ninstaller.squashfs\n' > "$ISO/live/filesystem.module"
cp "$DEB" "$ISO/tangoos/"
[ "$VARIANT" = test ] && cp "$KEYS" "$ISO/tangoos/authorized_keys"
# Laboratorio (test): la entrada por defecto instala sola en modo texto (sin preguntas, IP fija).
LAB=""
[ "$VARIANT" = test ] && LAB="tango.auto tango.ip=$IP,$GW,8.8.8.8${LABDISK:+ tango.disk=$LABDISK}"
# Si el medio queda puesto después de instalar (USB o CD primero en el orden de arranque), por defecto
# se arranca el TangoOS ya instalado y no se vuelve a ofrecer instalar: evita reinstalar en bucle.
# Menú: instalar en modo gráfico (por defecto) o en modo texto, probar sin instalar, o arrancar el
# sistema ya instalado (por defecto si existe: el medio puede quedar puesto sin reinstalar en bucle).
DEFAULT=graphical
LABENTRY=""
if [ "$VARIANT" = test ]; then
    DEFAULT=lab
    LABENTRY="menuentry 'Instalar TangoOS (laboratorio, desatendido)' --id lab {
    linux /live/vmlinuz boot=live components quiet splash=0 tango.installer=text $LAB
    initrd /live/initrd.img
}"
fi
cat > "$ISO/boot/grub/grub.cfg" <<GRUB
set default=$DEFAULT
set timeout=5
insmod all_video
insmod gfxterm
insmod part_gpt
insmod ext2
if search --no-floppy --label tangoos-boot --set=installed; then
    set default=installed
fi
$LABENTRY
menuentry 'Instalar TangoOS' --id graphical {
    linux /live/vmlinuz boot=live components quiet splash=0 vt.global_cursor_default=0 tango.installer
    initrd /live/initrd.img
}
menuentry 'Instalar TangoOS en modo texto' --id text {
    linux /live/vmlinuz boot=live components quiet splash=0 tango.installer=text
    initrd /live/initrd.img
}
menuentry 'Probar TangoOS sin instalar' --id live {
    linux /live/vmlinuz boot=live components quiet
    initrd /live/initrd.img
}
menuentry 'Arrancar el TangoOS instalado' --id installed {
    search --no-floppy --label tangoos-boot --set=root
    configfile /grub/grub.cfg
}
GRUB
t "grub-mkrescue"
grub-mkrescue -o "$OUT" "$ISO" -- -volid TANGOOS_AMD64 >/dev/null 2>&1
(cd "$(dirname "$OUT")" && sha256sum "$(basename "$OUT")" > "$(basename "$OUT").sha256")
t "ISO lista: $OUT ($(du -h "$OUT" | cut -f1), variante $VARIANT)"
