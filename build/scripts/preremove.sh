#!/bin/sh
# Simétrico al postinstall: parar solo si systemd corre; deshabilitar también en chroot.
set -e
if [ -d /run/systemd/system ]; then
    systemctl stop tangod.service || true
    systemctl stop tango-agent.socket tango-agent.service || true
fi
if [ "$1" = "remove" ] || [ "$1" = "purge" ]; then
    if command -v deb-systemd-helper >/dev/null 2>&1; then
        deb-systemd-helper disable tangod.service tango-agent.socket >/dev/null || true
    elif [ -d /run/systemd/system ]; then
        systemctl disable tangod.service tango-agent.socket >/dev/null || true
    fi
fi
