# ADR 0010 — Docker (not Podman) for the App Center

- Status: Accepted (replaces the mention of Podman in docs/07, 08, 09 and ADR 0009)
- Context: the user asks to "install Docker and Synology-style apps". Synology (Container Manager) and almost the whole homelab ecosystem publish `docker-compose.yml`; compatibility with that catalog is worth more than Podman's rootless model for the initial audience.
- Decision: Docker Engine from the Debian repos (`docker.io`, `docker-compose`), installed on demand from the App Center. `tangod` never talks directly to `/var/run/docker.sock`: `tango-agent` does, via the Engine API (HTTP over a Unix socket, native Go), with an allow-list of operations (create from a catalog manifest, start/stop, logs, pull, remove).
- Consequences: the Docker daemon runs as root → mitigated with `userns-remap`, per-app networks, no socket mounted into containers and no `--privileged` by default. Podman will be re-evaluated when its Compose compatibility is equivalent.

## Implementation (M3.5)

- `docker.io` + `docker-compose` from Debian 13 (Docker 26.1.5 on the test VM), installed by the agent with
  `apt-get` in a transient systemd unit (outside the agent's sandbox).
- `daemon.json`: `userns-remap: default`, `log-driver: journald`, `live-restore: true`. Tested: Uptime Kuma,
  Vaultwarden and Jellyfin write to `data/` with the remapped uid (231072); Home Assistant uses `userns_mode: host`.
- No docker CLI: standalone compose + Engine API from Go.
