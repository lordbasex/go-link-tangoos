#!/bin/sh
# Construye la ISO de instalación de TangoOS remasterizando la netinst oficial de Debian 13.
# Requiere: xorriso, cpio, gzip, openssl (Debian: apt install xorriso cpio).
#
#   ./build.sh --iso debian-13.4.0-amd64-netinst.iso --deb tangoos_0.1.0-dev_amd64.deb \
#              --out tangoos-0.1.0-dev-amd64.iso [--variant product|test] \
#              [--authorized-keys keys.pub --ip 172.17.0.45/24 --gw 172.17.0.1]
#
# Qué hace (docs/08):
#   1. agrega preseed.cfg al initrd del instalador de texto (cpio concatenado, sin desarmar el original)
#   2. reemplaza los menús de GRUB (UEFI) e isolinux (BIOS) por "Instalar TangoOS" con 5 s de espera
#   3. copia el .deb de TangoOS a /tangoos/ dentro de la ISO; late_command lo instala
#   4. reempaqueta con xorriso reutilizando los registros de arranque originales (BIOS + UEFI, híbrida USB)
set -eu

VARIANT=product
ISO_IN= DEB= OUT= KEYS= IP= GW=
while [ $# -gt 0 ]; do
    case "$1" in
        --iso) ISO_IN=$2; shift 2 ;;
        --deb) DEB=$2; shift 2 ;;
        --out) OUT=$2; shift 2 ;;
        --variant) VARIANT=$2; shift 2 ;;
        --authorized-keys) KEYS=$2; shift 2 ;;
        --ip) IP=$2; shift 2 ;;
        --gw) GW=$2; shift 2 ;;
        *) echo "argumento desconocido: $1" >&2; exit 2 ;;
    esac
done
[ -f "$ISO_IN" ] && [ -f "$DEB" ] && [ -n "$OUT" ] || { echo "faltan --iso, --deb o --out" >&2; exit 2; }
case "$VARIANT" in
    product) ;;
    test) [ -f "$KEYS" ] && [ -n "$IP" ] && [ -n "$GW" ] || { echo "la variante test necesita --authorized-keys, --ip y --gw" >&2; exit 2; } ;;
    *) echo "variante inválida: $VARIANT" >&2; exit 2 ;;
esac

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/pre" "$WORK/iso/tangoos" "$WORK/iso/boot/grub" "$WORK/iso/isolinux" "$WORK/iso/install.amd"

# 1. preseed = común + variante
ROOT_HASH=$(openssl passwd -6 "$(head -c 32 /dev/urandom | base64)")
sed "s|@@ROOT_HASH@@|$ROOT_HASH|" "$HERE/preseed.cfg" > "$WORK/pre/preseed.cfg"
if [ "$VARIANT" = test ]; then
    addr=${IP%/*}
    sed -e "s|@@IP@@|$addr|" -e "s|@@GW@@|$GW|" "$HERE/preseed-test.cfg" >> "$WORK/pre/preseed.cfg"
    cp "$KEYS" "$WORK/iso/tangoos/authorized_keys"
else
    cat "$HERE/preseed-product.cfg" >> "$WORK/pre/preseed.cfg"
fi
cp "$WORK/pre/preseed.cfg" "$WORK/iso/tangoos/preseed.cfg"   # copia de referencia, visible en la ISO

xorriso -osirrox on -indev "$ISO_IN" -extract /install.amd/initrd.gz "$WORK/initrd.orig.gz" >/dev/null 2>&1
chmod u+w "$WORK/initrd.orig.gz"
( cd "$WORK/pre" && echo preseed.cfg | cpio -o -H newc --quiet ) | gzip -9 > "$WORK/preseed.cpio.gz"
cat "$WORK/initrd.orig.gz" "$WORK/preseed.cpio.gz" > "$WORK/iso/install.amd/initrd.gz"

# 2. menús de arranque
PARAMS="priority=high locale=es_AR.UTF-8 keymap=latam"
cat > "$WORK/iso/boot/grub/grub.cfg" <<GRUB
set default=0
set timeout=5
insmod all_video
insmod gfxterm
set menu_color_normal=white/black
set menu_color_highlight=black/light-gray
menuentry 'Instalar TangoOS' {
    linux    /install.amd/vmlinuz $PARAMS --- quiet
    initrd   /install.amd/initrd.gz
}
menuentry 'Modo rescate (Debian)' {
    linux    /install.amd/vmlinuz rescue/enable=true --- quiet
    initrd   /install.amd/initrd.gz
}
GRUB
cat > "$WORK/iso/isolinux/isolinux.cfg" <<ISOLINUX
# TangoOS — arranque BIOS
path
prompt 0
timeout 50
default tango
label tango
	menu label Instalar TangoOS
	kernel /install.amd/vmlinuz
	append $PARAMS initrd=/install.amd/initrd.gz --- quiet
label rescue
	menu label Modo rescate (Debian)
	kernel /install.amd/vmlinuz
	append rescue/enable=true initrd=/install.amd/initrd.gz --- quiet
ISOLINUX

# 3. paquete de TangoOS
cp "$DEB" "$WORK/iso/tangoos/tangoos.deb"

# 4. reempaquetar conservando el arranque híbrido BIOS/UEFI original
rm -f "$OUT"
xorriso -indev "$ISO_IN" -outdev "$OUT" \
    -volid "TANGOOS_AMD64" \
    -map "$WORK/iso/install.amd/initrd.gz" /install.amd/initrd.gz \
    -map "$WORK/iso/boot/grub/grub.cfg" /boot/grub/grub.cfg \
    -map "$WORK/iso/isolinux/isolinux.cfg" /isolinux/isolinux.cfg \
    -map "$WORK/iso/tangoos" /tangoos \
    -rm_r /install.amd/gtk /install.amd/xen -- \
    -boot_image any replay >/dev/null 2>&1
echo "ISO lista: $OUT ($(du -h "$OUT" | cut -f1), variante $VARIANT)"
