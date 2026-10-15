# 14 — Plan de pruebas (criterio del 100 %)

Una función está terminada cuando tiene **las cuatro capas** en verde y está ejecutada sobre la
VM de laboratorio. Los resultados de cada corrida se registran en `tests/RESULTS.md`
(fecha, commit/versión, resumen, fallos).

| Capa | Herramienta | Dónde corre | Comando |
|---|---|---|---|
| Unitarias Go | `go test` | Mac/CI (+ `GOOS=linux go vet`) | `make check` |
| Unitarias web | Vitest | Mac/CI | `cd web && npm test` |
| E2E de API | `tests/e2e` (Go, cliente pion real) | Mac → VM 108 | `make e2e-api` |
| E2E de UI | Playwright (Chromium) | Mac → VM 108 | `make e2e-ui` |
| Matriz de almacenamiento | `tests/e2e/storage_matrix_test.go` | Mac → VM 108 (discos S*/M*/D3/D4) | `make e2e-storage` |
| Seguridad | `tests/e2e/security_test.go` + revisión | Mac → VM 108 | `make e2e-security` |
| Instalación | `system/iso` + VM 109 | Proxmox | `make e2e-install` |

## Laboratorio

- VM 108 `tangoos-dev` (172.17.0.44): 12 discos de datos — D1–D4 16 GB, S1–S6 2 GB, M1–M2 4 GB.
- `volume1` (RAID 1 D1+D2) y `volume2` (RAID 5) son **fixtures permanentes**; la matriz usa solo
  discos libres y deja todo como lo encontró.
- VM 109: pruebas de instalación desde la ISO.

## Matriz de almacenamiento (e2e-storage)

Cada fila: plan → confirmación → creación → verificación (mdstat, findmnt, statfs, API) →
carpeta + escritura → reinicio de `tangod` → (fila marcada) reinicio de la VM → borrado → discos libres.

| # | Perfil | Discos | Verificaciones extra |
|---|---|---|---|
| 1 | Básico | S4 | Btrfs sin md |
| 2 | RAID 1 | S4+S5 | resync, `[UU]` |
| 3 | RAID 5 | S4+S5+S6 | capacidad = 2×min |
| 4 | RAID 6 | S3+S4+S5+S6 | capacidad = 2×min |
| 5 | RAID 10 | S3+S4+S5+S6 | par de discos obligatorio |
| 6 | RAID 5 mixto | S6+M1+M2 | nota de tamaños distintos, capacidad = 2×2 GB |
| 7 | Reemplazo | RAID 5 #3 con fallo forzado en S5 → M1 | alerta, rebuild, alerta resuelta |
| 8 | Ampliación | RAID 5 #3 + M2 | reshape, `btrfs resize`, capacidad crece |
| 9 | Persistencia | RAID 1 #2 | **reinicio de VM**: md ensamblado, montado, SMB activo |
| 10 | Negativos | — | disco del sistema, disco en uso, token vencido/alterado, nombre mal escrito, RAID10 impar, RAID6 con 3 |

## E2E de API (por dominio)

| Dominio | Casos mínimos |
|---|---|
| Sesión | login ok/err, lockout, resume tras reinicio, logout, gating antes de login |
| Sistema | `system.info`, métricas 4 Hz, `rtc.stats` |
| Carpetas | crear/borrar, SMB visible, permisos rw/ro/none verificados con `smbclient` |
| Usuarios | crear, contraseña débil rechazada, cambio de contraseña, login SMB |
| Archivos | listar, subir 1 GB (sha256), bajar, renombrar, mover, papelera/restaurar/vaciar, versiones, miniatura, permisos por usuario, traversal/symlink |
| Instantáneas | manual, programada (intervalo corto de prueba), retención, borrar, restaurar versión |
| Apps | Docker instalado, instalar/parar/arrancar/actualizar/logs/desinstalar, proxy sin X-Frame-Options |
| Servicios (M4) | NFS montable, WebDAV, SFTP chroot, iSCSI login |
| M4 | primer arranque, actualización con instantánea del root y rollback, certificado CA local, notificaciones (SMTP de prueba, ntfy) |

## E2E de UI (Playwright)

Recorridos sobre el producto real (no mock): login → escritorio → widgets con datos reales →
Almacenamiento (crear RAID 5 desde el asistente con S4–S6, ver chasis, borrar) → Ajustes (carpeta,
usuario, permisos) → Archivos (subir, vista rápida, papelera, versiones) → Centro de apps (instalar
y abrir Uptime Kuma) → Monitor (WebRTC) → bloqueo/cierre de sesión. Capturas por paso en
`tests/e2e-ui/artifacts/` y regresión visual del escritorio en claro/oscuro.

## Seguridad

Sin sesión → `UNAUTHENTICATED` en todo método no público; usuario no admin → `PERMISSION_DENIED`
en métodos de administración; path traversal/symlink escape en Archivos; payload con campos de
más/op desconocida al agente; peer no autorizado al socket del agente; secretos ausentes de
logs (`journalctl | grep` de contraseñas de prueba); headers de seguridad HTTP; rate-limit de login.
