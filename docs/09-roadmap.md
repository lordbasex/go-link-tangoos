# 09 — Roadmap

Cada hito tiene un **gate**: no se avanza al siguiente sin cumplirlo. Las duraciones son
orientativas para una persona con ayuda de agentes.

## M0 — Fundaciones y prototipo (2–3 semanas)

- [ ] Monorepo, CI (Go + web + gitleaks + govulncheck), licencia MIT, ADRs.
- [x] **Prototipo visual navegable** con `MockTransport` ([05](05-escritorio-y-temas.md#prototipo-lo-siguiente-a-construir)).
- [x] **Spike WebRTC:** `tangod` mínimo con embed.FS + `/signal` + pion + canal `rpc`
      (`system.info`) + STUN embebido.
- [ ] **Benchmark de throughput** de DataChannels navegador↔pion y pion↔pion ([03 §7](03-protocolo-webrtc.md)).
- [x] Prueba de concepto del Service Worker → DataChannel con `<video>` y Range.

**Gate:** el prototipo se ve a la altura de Mac-OS-Desktop y los números de throughput están
documentados y aceptados.

## M1 — Plataforma (4–5 semanas)

- [ ] `protocol/` + codegen Go/TS + vectores compartidos.
- [x] `pkg/rtcrpc`: router, authz declarativa, eventos con `seq`/resume, cancelación.
- [x] Login, sesiones, argon2id, lockout, TOTP, resume "el host prueba primero".
- [x] `tango-agent` con protocolo versionado, `SO_PEERCRED`, journal y los primeros ops de solo lectura.
- [x] Jobs durables + auditoría + reconciliación.
- [x] Shell real conectado: Monitor (canal `metrics`) y Registro.
- [x] `.deb` con nFPM + repo apt de desarrollo + VM QEMU con Debian 13 minimal y discos virtuales.

**Gate:** login real y Monitor/Registro en vivo sobre WebRTC; sin operaciones destructivas.

## M2 — Almacenamiento (5–6 semanas)

- [ ] Inventario de discos (udev + `lsblk --json` + SMART), solo lectura; detecta volúmenes Synology (sin montar).
- [x] Crear volumen Básico / RAID1 con plan + token de confirmación.
- [x] Carpetas compartidas = subvolúmenes; snapshots y retención; restaurar archivo.
- [ ] Scrub, alertas de degradado, reemplazar disco y rebuild.
- [ ] Harness QEMU con fallos inyectados en CI.

**Gate:** toda la batería de [04](04-almacenamiento.md#batería-de-pruebas-discos-virtuales-qemu) en verde.

## M3 — Archivos, SMB y app nativa (5–6 semanas)

- [x] file-worker por UID con `openat2`; app Archivos completa; papelera.
- [ ] Transferencias con pool de canales, reanudables, y Service Worker para descargas y vistas previas.
      _Hecho: pool de canales, rangos y Service Worker (`/dc/stream`, `/dc/download`, 206). Falta: reanudar subidas._
- [x] Usuarios, grupos, permisos; Samba + Time Machine + `vfs_shadow_copy2`; NFS; SFTP con chroot; mDNS + WS-Discovery.
      _Usuarios con alta, baja (`users.delete`) y contraseñas de app; Time Machine (vfs_fruit + Bonjour `_adisk._tcp`);
      «Versiones anteriores» (`vfs_shadow_copy2` sobre los enlaces `.gmt`); WS-Discovery nativo en Go (sin wsdd2).
      Verificado en la VM 108 (protocol/README.md §14)._
- [ ] `tango-desk` para Windows y Linux (webview + pion + bridge), vinculación y montaje SMB.
- [ ] Tests de clientes macOS y Windows.
- [ ] Volúmenes Synology: montar en solo lectura y navegarlos en Archivos ([12](12-estrategia-vs-synology.md)).

**Gate:** se cumple la [métrica de éxito del MVP](01-vision-y-alcance.md#métrica-de-éxito-del-mvp).

## M3.5 — Centro de apps (adelantado por pedido del producto)

- [x] Instalar Docker Engine (`docker.io` + `docker-compose` de Debian) desde el NAS, como job con progreso.
- [x] `tangod` → Docker Engine API por socket Unix, vía el agente (sin `exec`, ADR 0009).
- [x] Catálogo de apps (manifiesto propio + Compose): Jellyfin, Immich, Home Assistant, Nextcloud, Pi-hole, Vaultwarden…
- [x] Instalar / iniciar / detener / actualizar / desinstalar / logs en vivo; datos en `@apps/<app>` del volumen.
- [x] Cada app abre su propia ventana del escritorio (proxy local hacia el puerto del contenedor).

**Gate:** instalar, usar, actualizar y desinstalar 3 apps del catálogo sin terminal, con datos que sobreviven a la actualización.

## M4 — Producto instalable (5–6 semanas) → **v1.0**

- [ ] Repo apt firmado, ISO (debian-installer + preseed/simple-cdd), primer arranque, updates por apt con snapshot del root y rollback (grub-btrfs).
- [x] iSCSI (LIO), WebDAV.
- [ ] Certificados (DNS del proyecto / ACME / CA local).
      _En curso: hay ACME en `internal/tlscert`; falta el DNS del proyecto._
- [ ] RAID5/6/10, LUKS2, notificaciones (email/ntfy/Telegram), UPS (NUT).
      _Hecho: UPS por NUT (local por USB o remoto, apagado ordenado) y notificaciones. Falta validar el resto en VM._
- [ ] Backup/import de configuración; modo recuperación.
- [ ] Asistente de importación de volúmenes Synology + tests con volúmenes reales DSM 6/7.
- [ ] Mission Control y widgets.
- [ ] Release con SBOM, sumas firmadas y matriz de hardware.

**Gate:** instalación limpia → update → rollback forzado → los datos están intactos.

## Próximos (post v1.0)

Prioridades de [la comparativa con Synology](../reports/Comparativa%20NAS%20Synology%20vs%20TangoOS.md),
sin lo que ya quedó hecho en la fase v1.0 (proxy de apps con sesión, 2FA TOTP, NUT, NFS, SFTP, Time Machine).

- **P0, credibilidad:**
  - [ ] `tests/RESULTS.md` reproducible: harness QEMU con fallos inyectados y Playwright.
  - [ ] Updates desde la UI con instantánea de `@rootfs`, rollback en un clic y repo apt firmado.
  - [ ] Backup de configuración `.tangobak`.
  - [x] "Versiones anteriores" de Windows (`vfs_shadow_copy2`).
  - [ ] TLS de confianza: dominio del proyecto y CA local.
  - [x] 2FA también en WebDAV y SFTP: con 2FA activa aceptan solo contraseñas de app (y SMB usa una de app).
- **P1, diferenciación:**
  - [ ] Respaldo 3-2-1 con asistente (restic/Kopia a USB, S3, B2 u otro TangoOS) y replicación `btrfs send`.
  - [ ] Acceso remoto: Tailscale/WireGuard en un clic y después TangoConnect P2P con TURN.
  - [ ] Migración desde Synology: montar SHR/RAID en solo lectura y navegarlo.
  - [ ] Jellyfin con QSV/VAAPI y `/dev/dri` automático.
  - [ ] Btrfs RAID1 nativo para dos discos.
  - [ ] Cuotas, LUKS2 y perfiles de energía.
  - [ ] Catálogo más grande con modo compose avanzado.
- **P2, alcance:**
  - [ ] `tango-desk` (Windows/Linux) y app móvil.
  - [ ] Compartir por enlace, pedido de archivos y subida reanudable.
  - [ ] Discos mixtos tipo SHR.
  - [ ] Hot spare y migración de nivel.
  - [ ] LDAP/AD y passkeys.
  - [ ] ARM64, inglés y segundo tema.
- **P3, opcional:**
  - [ ] ZFS, caché SSD.
  - [ ] Respaldo de PC con agente.
  - [ ] Asistente de IA / MCP con confirmación y step-up auth.

## M5 — Ecosistema

App Media (Jellyfin + Intel QSV), **TangoConnect**
(acceso remoto vía signalhub + coturn), replicación de snapshots, Respaldo (restic),
WebAuthn, ARM64 de referencia y el segundo tema ("Arrabal").

## M6 — Avanzado

Volumen híbrido tipo SHR (md por rebanadas + LVM), ZFS opcional (`tangoos-zfs`), caché SSD, cliente de sincronización (Drive), LDAP/AD, cuotas por usuario.
