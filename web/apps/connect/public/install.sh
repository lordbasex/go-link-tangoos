#!/bin/sh
# TangoOS: instalación sobre un Debian 13 (trixie) ya instalado.
#
#   curl -fsSL https://tangoos.go-link.org/install.sh | sh
#
# Agrega el repositorio APT firmado de TangoOS (https://tangoos.go-link.org/apt) e instala el
# paquete tangoos con los servicios que maneja (Samba, NFS, Avahi). Las actualizaciones llegan
# después por apt, desde Ajustes → Actualizaciones del escritorio.
#
# Diferencia con la ISO: la ISO instala en un disco dedicado con Btrfs y guarda una instantánea
# del sistema (@rootfs) antes de cada actualización, con vuelta atrás desde el menú de arranque.
# Esta instalación usa el Debian tal como está: no hay esa vuelta atrás del sistema.
#
# Variables:
#   TANGOOS_CHANNEL   canal del repositorio (por ahora solo "stable")
#   TANGOOS_FORCE=1   seguir aunque falte systemd o :80/:443 estén ocupados
#   TANGOOS_APT_URL   otro repositorio (pruebas)
#
# Todo pasa dentro de main, que se llama en la última línea: si la descarga se corta a la mitad,
# no se ejecuta nada.

set -eu

APT_URL="${TANGOOS_APT_URL:-https://tangoos.go-link.org/apt}"
CHANNEL="${TANGOOS_CHANNEL:-stable}"
FORCE="${TANGOOS_FORCE:-0}"
KEYRING=/usr/share/keyrings/tangoos-archive-keyring.gpg
SOURCES=/etc/apt/sources.list.d/tangoos.sources
STATE=/var/lib/tango

# Clave pública del repositorio (TangoOS Archive, huella B34312438C2849B683A053199C06A62DC9C4DB37).
# Va acá y no se descarga aparte: quien confía en este script confía en esta clave.
# Es la misma que build/keys/tangoos-archive-keyring.gpg (lo verifica internal/aptrepo).
KEY_B64='
mDMEatDRkBYJKwYBBAHaRw8BAQdAr+Skp7PIzZeWydokciH2gFs7nG4WGDxNEG9Livk2drK0JlRh
bmdvT1MgQXJjaGl2ZSA8bG9yZC5iYXNleEBnbWFpbC5jb20+iK8EExYKAFcWIQSzQxJDjChJtoOg
UxmcBqYtycTbNwUCatDRkBsUgAAAAAAEAA5tYW51MiwyLjUrMS4xMiwwLDMCGwMFCwkIBwICIgIG
FQoJCAsCBBYCAwECHgcCF4AACgkQnAamLcnE2zc5fwEArsGgyYdPzoWOXgVACLs+DqsLNJpTEGAd
MXzwL5/q6gsBAKY/OfnAc9nQx+3fR/Bara7DrzLbnLhCy1IJgPiGApMA
'

say() { printf '\033[1m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33maviso:\033[0m %s\n' "$*" >&2; }
die() {
	printf '\033[31merror:\033[0m %s\n' "$*" >&2
	exit 1
}

# as_root ejecuta un comando como root: directo si ya lo somos, si no con sudo.
as_root() {
	if [ "$(id -u)" -eq 0 ]; then
		"$@"
	else
		sudo "$@"
	fi
}

check_root() {
	if [ "$(id -u)" -ne 0 ]; then
		command -v sudo >/dev/null 2>&1 || die "hace falta ser root: ejecutalo como root o instalá sudo"
		say "Se necesitan permisos de administrador (sudo)"
		sudo -v || die "sudo no dio permisos"
	fi
}

check_system() {
	[ -r /etc/os-release ] || die "no se pudo leer /etc/os-release"
	# shellcheck disable=SC1091
	. /etc/os-release
	if [ "${ID:-}" != debian ] || [ "${VERSION_CODENAME:-}" != trixie ]; then
		die "TangoOS necesita Debian 13 (trixie); este sistema es ${PRETTY_NAME:-desconocido}. Para un equipo nuevo usá la ISO: https://tangoos.go-link.org"
	fi
	arch=$(dpkg --print-architecture)
	[ "$arch" = amd64 ] || die "TangoOS solo está disponible para amd64 (este equipo es $arch)"
	if [ ! -d /run/systemd/system ]; then
		[ "$FORCE" = 1 ] || die "TangoOS necesita systemd como sistema de arranque (TANGOOS_FORCE=1 para seguir igual)"
		warn "sin systemd: los servicios de TangoOS no van a arrancar solos"
	fi
	case "$CHANNEL" in
	stable) ;;
	*) die "canal desconocido: $CHANNEL (por ahora solo existe stable)" ;;
	esac
}

# port_busy dice si hay algo escuchando en el puerto TCP $1 (lee /proc: no depende de ss/netstat).
port_busy() {
	hex=$(printf '%04X' "$1")
	cat /proc/net/tcp /proc/net/tcp6 2>/dev/null |
		awk -v p=":$hex" '$4 == "0A" && substr($2, length($2) - 4) == p { found = 1 } END { exit !found }'
}

check_ports() {
	# Si TangoOS ya está instalado, los puertos son suyos: es una actualización.
	if systemctl is-active --quiet tangod 2>/dev/null; then
		return
	fi
	busy=""
	for p in 80 443; do
		if port_busy "$p"; then busy="$busy :$p"; fi
	done
	[ -z "$busy" ] && return
	if [ "$FORCE" = 1 ]; then
		warn "ya hay un servicio escuchando en$busy: tangod no va a poder usarlo"
		return
	fi
	die "ya hay un servicio escuchando en$busy y TangoOS sirve su escritorio ahí. Apagalo, o seguí con TANGOOS_FORCE=1"
}

apt_get() {
	as_root env DEBIAN_FRONTEND=noninteractive apt-get -y -q -o Dpkg::Use-Pty=0 "$@"
}

add_repo() {
	say "Agregando el repositorio de TangoOS ($APT_URL, canal $CHANNEL)"
	if ! dpkg-query -W -f='${Status}' ca-certificates 2>/dev/null | grep -q 'ok installed'; then
		apt_get update >/dev/null
		apt_get install --no-install-recommends ca-certificates >/dev/null
	fi
	tmp=$(mktemp)
	printf '%s' "$KEY_B64" | base64 -d >"$tmp" || die "la clave del repositorio está dañada"
	as_root install -D -m 0644 "$tmp" "$KEYRING"
	rm -f "$tmp"
	printf 'Types: deb\nURIs: %s\nSuites: %s\nComponents: main\nArchitectures: amd64\nSigned-By: %s\n' \
		"$APT_URL" "$CHANNEL" "$KEYRING" | as_root tee "$SOURCES" >/dev/null
}

install_pkgs() {
	say "Actualizando las listas de paquetes"
	apt_get update >/dev/null || die "apt-get update falló (¿hay conexión a $APT_URL?)"
	had_nfs=0
	if dpkg-query -W -f='${Status}' nfs-kernel-server 2>/dev/null | grep -q 'ok installed'; then had_nfs=1; fi
	say "Instalando TangoOS (tangoos, Samba, NFS, Avahi)"
	apt_get install tangoos samba nfs-kernel-server avahi-daemon
	# Como en la ISO: NFS queda apagado hasta que se active en Ajustes (si ya estaba, no se toca).
	if [ "$had_nfs" = 0 ] && [ -d /run/systemd/system ]; then
		as_root systemctl disable --now nfs-server.service >/dev/null 2>&1 || true
	fi
}

finish() {
	if [ -d /run/systemd/system ]; then
		i=0
		while [ $i -lt 30 ] && ! systemctl is-active --quiet tangod; do
			sleep 1
			i=$((i + 1))
		done
		systemctl is-active --quiet tangod || warn "tangod no está corriendo: revisá 'journalctl -u tangod'"
	fi
	version=$(dpkg-query -W -f='${Version}' tangoos 2>/dev/null || echo "?")
	echo
	say "TangoOS $version instalado"
	echo
	echo "  Abrí el escritorio desde otra computadora de tu red:"
	for ip in $(hostname -I 2>/dev/null); do
		case "$ip" in
		*:*) echo "    https://[$ip]/" ;;
		*) echo "    https://$ip/" ;;
		esac
	done
	echo "    https://$(hostname).local/"
	echo "  El certificado es propio del equipo: el navegador va a pedir confirmarlo la primera vez."
	echo
	if as_root test -f "$STATE/initial-admin-password"; then
		echo "  Usuario: admin. Ver la contraseña inicial con:"
		echo "    sudo cat $STATE/initial-admin-password"
		echo "  (deja de valer cuando la cambiás en Ajustes → Usuarios)"
	fi
	echo
	echo "  Las actualizaciones de TangoOS llegan por apt (Ajustes → Actualizaciones)."
	echo "  Instalado sobre Debian: no hay vuelta atrás del sistema con instantáneas como en la ISO."
}

main() {
	check_root
	check_system
	check_ports
	add_repo
	install_pkgs
	finish
}

main "$@"
