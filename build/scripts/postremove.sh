#!/bin/sh
set -e
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
fi
# purge removes state (TLS certificate); plain remove keeps it.
if [ "$1" = "purge" ]; then
    if command -v deb-systemd-helper >/dev/null 2>&1; then
        deb-systemd-helper purge tangod.service tango-agent.socket >/dev/null || true
    fi
    rm -rf /var/lib/tango /var/lib/tango-agent
    if getent group tango-agent >/dev/null; then delgroup --system --quiet tango-agent || true; fi
    if getent passwd tango >/dev/null; then deluser --system --quiet tango || true; fi
fi
