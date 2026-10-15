#!/bin/sh
# Postinstall de tangoos. Tiene que funcionar también dentro de un chroot (instalador
# ISO, debootstrap): ahí no hay systemd corriendo, así que se habilita con
# deb-systemd-helper (crea los enlaces sin hablar con PID 1) y solo se arranca o
# reinicia si systemd está activo.
set -e
if ! getent passwd tango >/dev/null; then
    adduser --system --group --no-create-home --home /var/lib/tango --quiet tango
fi
# tango-agent: el grupo dueño del socket del agente; tangod entra por pertenecer a él.
if ! getent group tango-agent >/dev/null; then
    addgroup --system --quiet tango-agent
fi
adduser --quiet tango tango-agent >/dev/null 2>&1 || true

enable() {
    if command -v deb-systemd-helper >/dev/null 2>&1; then
        deb-systemd-helper unmask "$1" >/dev/null || true
        deb-systemd-helper enable "$1" >/dev/null || true
    elif [ -d /run/systemd/system ]; then
        systemctl enable "$1" >/dev/null || true
    fi
}
enable tango-agent.socket
enable tangod.service

# Módulos de device-mapper de los grupos de almacenamiento (ADR 0011): el agente no
# puede cargarlos (ProtectKernelModules); al arrancar los carga modules-load.d.
if [ -d /run/systemd/system ]; then
    modprobe -a dm_crypt dm_cache dm_cache_smq dm_thin_pool 2>/dev/null || true
fi

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl restart tango-agent.socket
    # el agente lo activa la primera conexión; si ya corría, se reinicia con el binario nuevo
    if systemctl is-active --quiet tango-agent.service; then systemctl restart tango-agent.service; fi
    # restart, not start: an upgrade must pick up the new binary
    systemctl restart tangod.service
fi
