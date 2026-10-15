# ADR 0010 — Docker (no Podman) para el Centro de apps

- Estado: Aceptado (reemplaza la mención a Podman en docs/07, 08, 09 y ADR 0009)
- Contexto: el usuario pide "instalar Docker y apps tipo Synology". Synology (Container Manager) y casi todo el ecosistema homelab publican `docker-compose.yml`; la compatibilidad con ese catálogo vale más que el modelo rootless de Podman para el público inicial.
- Decisión: Docker Engine desde los repos de Debian (`docker.io`, `docker-compose`), instalado bajo demanda desde el Centro de apps. `tangod` nunca habla directo con `/var/run/docker.sock`: lo hace el `tango-agent` vía la Engine API (HTTP sobre socket Unix, Go nativo), con un allow-list de operaciones (crear desde manifiesto del catálogo, start/stop, logs, pull, remove).
- Consecuencias: el daemon de Docker corre como root → se mitiga con `userns-remap`, redes por app, sin socket montado en contenedores y sin `--privileged` por defecto. Se reevalúa Podman cuando su compatibilidad con Compose sea equivalente.

## Implementación (M3.5)

- `docker.io` + `docker-compose` de Debian 13 (Docker 26.1.5 en la VM de prueba), instalados por el agente con
  `apt-get` en una unidad transitoria de systemd (fuera del sandbox del agente).
- `daemon.json`: `userns-remap: default`, `log-driver: journald`, `live-restore: true`. Probado: Uptime Kuma,
  Vaultwarden y Jellyfin escriben en `data/` con el uid remapeado (231072); Home Assistant usa `userns_mode: host`.
- Sin CLI de docker: compose standalone + Engine API desde Go.
