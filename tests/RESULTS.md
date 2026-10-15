# Resultados de pruebas

Criterio y matriz: [`docs/14-plan-de-pruebas.md`](../docs/14-plan-de-pruebas.md) · cómo correr: [`README.md`](README.md).

## Corrida completa — 2026-10-15

| Dato | Valor |
|---|---|
| Versión | `0.1.0-dev` (build final desplegado en la VM 108 a las 00:28 con los arreglos de esta corrida) |
| NAS | VM 108 `tangoos-dev` (172.17.0.44), Debian 13.7, kernel 6.12, 12 discos de datos virtuales |
| Cliente | Mac de desarrollo, cliente WebRTC real (`internal/rtc`, pion) y Chromium (Playwright 1.64.1) |
| Resultado | **Todo en verde** (las suites destructivas, aprobadas por el usuario, incluidas) |

| Capa / suite | Comando | Resultado | Duración |
|---|---|---|---|
| Unitarias Go (+ `go vet` Linux, gofmt) | `make check` | **157/157** en 28 paquetes | ~40 s |
| Unitarias web (Vitest) | `cd web && npm test` | **70/70** en 9 archivos | ~1 s |
| Seguridad | `make e2e-security` | **7/7** | 4,6 s |
| E2E de API | `make e2e-api` | **20/20** (+ 2 subtests SFTP) | 2 min 32 s |
| E2E de UI | `make e2e-ui E2E_DESTRUCTIVE=1` | **1/1** recorrido, 19 capturas | 44 s |
| Matriz de almacenamiento | `make e2e-storage` | **8/8** filas | 4 min 27 s |
| Reinicio de la VM (fila 9) | `make e2e-storage E2E_REBOOT=1` | **1/1** | 2 min 28 s |
| Instalación desde la ISO | `make iso` + VM 109 (sin objetivo e2e todavía) | no se corrió en esta batería: medida en la fase de instalador (59–80 s, `docs/08`) y usada por M4 en la VM 109 | — |

### E2E de API (20)

| Test | Qué verifica |
|---|---|
| `TestSessionLoginAndGating` | métodos públicos vs. con sesión, credenciales malas, logout invalida el token |
| `TestSessionLockout` | bloqueo al 5.º intento fallido (usuario inexistente, nunca uno real) |
| `TestSessionResumeAfterRestart` | el token sigue valiendo después de `systemctl restart tangod` |
| `TestSystemInfoMetricsRTC` | `system.info`, ≥ 8 frames de métricas en 3 s, ICE conectado, canales `rpc/events/metrics/files.*`, 1 disco de sistema |
| `TestSharesUsersPermissions` | carpeta nueva en `smbclient -L`; rw/ro/none verificados con `smbclient`; contraseña débil rechazada; cambio de contraseña aplicado a SMB; nombres inválidos |
| `TestFiles` | **1 GiB ida y vuelta con sha256 (51,6 MB/s subida, 47,0 MB/s bajada)**; mkdir/rename/move/papelera/restaurar/vaciar; búsqueda; miniatura; traversal; symlinks absolutos y relativos que escapan; permisos por usuario (none/ro) |
| `TestSnapshots` | instantánea manual, versiones, restaurar versión (contenido verificado), schedule ida y vuelta + validación, borrar |
| `TestAppsLifecycle` | Docker listo, catálogo ≥ 8, syncthing: instalar → proxy 401 sin cookie → ticket → 200 sin X-Frame-Options → ticket de un uso → stop/start/restart/update → logs → desinstalar sin datos |
| `TestAppsCookieRevokedOnLogout` | la cookie del proxy deja de valer al cerrar la sesión que la emitió |
| `TestServicesWebDAVSFTPNFS` | WebDAV PUT/GET/401; SFTP (servidor Go, traversal y clave mala); NFS exportado a loopback y montado (leído como el usuario dueño), host inválido rechazado |
| `TestServicesISCSI` | CHAP corto rechazado; LUN de 256 MB con CHAP visible por open-iscsi (`LIO`); CHAP malo no entra; borrar con nombre mal escrito rechazado |
| `TestM4AuditLogsTerminal` | auditoría con login fallido y `users.setPassword` sin contraseñas; `logs.query`; terminal root (`id -u`=0) |
| `TestM4CertAndUpdates` | `cert.info`; `updates.check`; rollback en VM 108 → `NOT_SUPPORTED` (raíz ext4); reinicio con palabra mal escrita rechazado |
| `TestM4Notifications` | ntfy (con Bearer) y SMTP llegan a un receptor en loopback; el token no vuelve en claro; `notify.json` 0600; config restaurada |
| `TestUsersDelete` | `CONFIRMATION_MISMATCH`, no borrar al propio admin, borrado real (Unix + Samba + login), `USER_NOT_FOUND` |
| `TestTOTPAndAppPasswords` | TOTP begin/confirm, login en dos pasos, código malo, código de recuperación de un solo uso, `TOTP_NOT_PENDING`; con 2FA WebDAV/SFTP rechazan la contraseña de la cuenta y aceptan la de app; revocar; no ver las de otro; desactivar 2FA |
| `TestPreviousVersionsBrowseRangesWatcher` | navegar una instantánea (`files.list/stat` con `snapshot`), rango de 4 KiB desde la instantánea, rango fuera de tamaño rechazado, **`allinfo` por SMB muestra `@GMT-`**, **`files.changed` después de escribir por SMB** |
| `TestTimeMachineShare` | carpeta `TimeMachine` con `fruit:time machine = yes` en `testparm` y `_adisk._tcp`/`adVN=TimeMachine` en Avahi |
| `TestWSDiscovery` | `tangoctl wsd-probe` desde la Mac encuentra el NAS |
| `TestUPSFake` | UPS remoto falso (`fakeupsd` en loopback): `ups.status` en línea con 87 % de batería; config restaurada |

### Seguridad (7)

`TestSecureResume` (el NAS prueba el token antes que el cliente), `TestSecurityUnauthenticated` (los 97 métodos no públicos → `UNAUTHENTICATED`), `TestSecurityNonAdmin` (31 métodos de administración → `PERMISSION_DENIED` para un usuario común), `TestSecurityRouterValidation` (método desconocido, campos de más), `TestSecurityAgentSocket` (`nobody` no habla con el agente; op desconocida, campos de más y versión inválida rechazados), `TestSecurityHTTPHeaders` (CSP sin `unsafe-eval`, HSTS, nosniff, Referrer-Policy, :80 → HTTPS, asset inexistente 404), `TestSecuritySecretsNotLogged` (ninguna contraseña de la corrida en el journal ni en `audit.jsonl`).

### Matriz de almacenamiento (discos S3, S6 de 2 GB y M1, M2 de 4 GB)

| Fila | Perfil y discos | Creado y sano en | Resultado |
|---|---|---|---|
| 1 | Básico · S3 | 2 s | ✅ + carpeta, archivo, reinicio de `tangod`, borrado, disco libre |
| 2 | RAID 1 · S3+S6 | 14 s | ✅ ídem |
| 3+6 | RAID 5 mixto · S3+S6+M1 | 28 s | ✅ capacidad 2×2 GB y nota de tamaños distintos |
| 4 | RAID 6 · S3+S6+M1+M2 | 16 s | ✅ |
| 5 | RAID 10 · S3+S6+M1+M2 | 23 s | ✅ |
| 7 | Reemplazo · RAID 5 con `mdadm --fail` sobre S6 → M2 | 26 s + rebuild | ✅ alerta activa → reemplazo (disco en uso rechazado) → sano, alerta resuelta, S6 libre |
| 8 | Ampliación · RAID 5 + M2 | 18 s + reshape | ✅ 4,29 GB → 6,43 GB |
| 9 | Persistencia · RAID 1 S3+S6 + **reinicio de la VM** | 14 s | ✅ volume1, volume2 y el nuevo sanos; archivo presente; SMB con Familia; apps corriendo |
| 10 | Negativos | — | ✅ disco del sistema, disco miembro, RAID 10 impar, RAID 6 con 3, Básico con 2, nombre repetido/inválido, token alterado, plan inexistente, ampliar RAID 1 |

En todas las filas: nombre mal escrito → `CONFIRMATION_MISMATCH`, plan de un solo uso, `mdadm --detail` y `findmnt` verificados en el NAS, y al borrar no quedan md, montaje ni firmas (`wipefs -n` vacío).

### E2E de UI — capturas (`tests/e2e-ui/artifacts/`)

`01-login` · `02-escritorio` · `03-almacenamiento-resumen` · `04-almacenamiento-discos` · `05-asistente-discos` · `06-asistente-perfil` · `07-asistente-revision` · `08-asistente-listo` · `09-volumen-e2eui` · `10-borrar-volumen` · `11-ajustes-carpetas` · `12-ajustes-usuarios` · `13-archivos-familia` · `14-apps-instaladas` · `15-monitor-webrtc` · `16-escritorio-oscuro` · `17-escritorio-claro` · `18-spotlight` · `19-sesion-cerrada`. Sin errores de JavaScript en la página.

## TangoConnect — 2026-10-15 (`make e2e-connect`)

Navegador real (Playwright 1.64.1, Chromium) · portal local (`web/apps/connect`, build con la clave pública dev) ·
signalhub de desarrollo en la Mac (`ws://172.17.0.5:8090`) · NAS = VM 108. Nunca se usó `signal.go-link.org`.

| Paso | Resultado |
|---|---|
| Vincular con el código de 9 dígitos (`connect.pair.start` → portal `/nas/vincular`) | ✅ |
| Escritorio entregado por el NAS (manifiesto Ed25519 + sha256 por archivo, servido desde `/nas-bundle/<id>/`), **sin modo desarrollo** | ✅ |
| Login como admin dentro del escritorio remoto | ✅ |
| Almacenamiento: rack de 12 bahías, volume1 RAID 1 + volume2 RAID 5 reales | ✅ |
| Archivos → «Compartir enlace…» de `Fotos/Atardecer en el Río.jpg` | ✅ |
| Enlace público en un contexto nuevo (`guest.open`) → descarga → **sha256 igual al del NAS** | ✅ |
| Recarga de `/nas/<id>`: `reach` + el NAS prueba primero + resume | ✅ |
| Revocar el vínculo del navegador → bloqueado («este navegador ya no está vinculado») | ✅ |

3 corridas seguidas en verde (23,6 s · 32,2 s · 57,1 s). Capturas: `tests/e2e-ui/artifacts/connect-01…10-*.png`.

**Bugs reales encontrados y corregidos por esta prueba**
1. **El escritorio del NAS nunca montaba en el portal** (`Failed to fetch dynamically imported module`): el Service
   Worker tenía scope `/nas-bundle/`, pero un SW solo intercepta pedidos de páginas que **controla**. Ahora scope `/`
   (solo responde `/nas-bundle/`) y el cargador espera `controllerchange` (`web/apps/connect/src/bundle/loader.ts`, `public/bundle-sw.js`).
2. **El portal cerraba la conexión al ceder la página**: el cleanup de React de `/nas/<id>` llamaba `s.close()` al
   desmontar el portal → el escritorio del NAS quedaba en «Sin conexión con el NAS» (`pages/Remote.tsx`, `handedOff`).
3. **Paso de vínculo doble** (`connect.link` / `guest.open` dos veces por conexión → `NOT_PAIRED` intermitente al
   vincular y «esta conexión no puede usar guest.open» en enlaces): `RtcTransport` corría `onRpcOpen` si el canal
   llegaba abierto **y** otra vez en el evento `open`. Ahora es idempotente, con test de regresión
   (`packages/client/src/rtc/RtcTransport.ts`, `connect.test.ts`).
4. **Enlace público de un archivo sin botón «Descargar»**: el NAS informa el tipo real (`image`, `video`…) y la página
   comparaba con `"file"` (`pages/Share.tsx`).

**Cambios de integración**: `apps/desktop` con `base: "./"` y transporte `window.__TANGO_TRANSPORT__` (sin
`installDCStream` en el portal); modo desarrollo **solo** como respaldo si el bundle no está o no monta (firma/hash/
manifiesto inválidos nunca caen al respaldo); **pin DTLS obligatorio** (huella distinta → se corta antes de la prueba
y del token, con «Olvidar y volver a vincular»); `protocol/README.md` §15 alineado (enlaces por `reach` + `guest.open`
con secreto en el fragmento, sección de pin DTLS); `cmd/connect-web/dist` fuera del repo (`.keep` + `.gitignore`).

## Instalador gráfico de la ISO — 2026-10-15 (VM 109, Proxmox, OVMF, 2 vCPU, 2 GB, discos 16 GB `TANGO-SYS` + 4 GB `LAB-D1`)

| Prueba | Resultado |
|---|---|
| `go test ./internal/installer` (código solo en pantalla local, PIN + cookie desde la LAN, bloqueo tras 5 errores, POST sin cabecera → 403, validación de disco/nombre/teclado/IP, motor falso: progreso, fallo y reintento, una sola instalación, reinicio, campos de más → 400, CSP) | ✅ 9 tests |
| ISO de producto (533 MB): arranque → instalador gráfico en pantalla | ✅ ~41 s desde `qm start`, en español, wallpaper y estilo del escritorio |
| Instalación **desde otra PC** (Playwright en la Mac → `http://172.17.0.8/`, código incorrecto rechazado, correcto aceptado) | ✅ 34,4 s; la pantalla local siguió el progreso sola |
| Instalación **en la pantalla del equipo** (clics por QMP sobre cage + cog, texto con el teclado de la VM) | ✅ ~35 s; nombre `kiosco`, teclado latam |
| Instalación gráfica con **IP fija** (172.17.0.47/24, DNS 1.1.1.1) | ✅ 33,5 s; `20-wired.network`, `/etc/hosts` y nombre correctos |
| Reinicio desde la interfaz → sistema instalado sirve HTTPS | ✅ 17–26 s; el puerto 80 ya es `tangod` (redirige) |
| Sistema instalado sin restos del instalador (cog, cage, `tango-installer`, unidades, usuario `tango-kiosk`, `live-boot`) | ✅ verificado por SSH (instalación gráfica y de texto) |
| **Modo texto** interactivo (segunda entrada, whiptail) | ✅ instalación 47 s con las teclas; HTTPS 21 s después de reiniciar |
| **Laboratorio desatendido** (`ISO_VARIANT=test`, 2 discos, `tango.disk=TANGO-SYS`) | ✅ 83–89 s de `qm start` a HTTPS (antes 90 s) |
| **Sin pantalla gráfica** (`vga: none`) | ✅ tty1 muestra `http://<ip>/` y el código; la instalación desde otra PC sigue disponible |

Bugs encontrados en el camino y corregidos: WPE (cog) no genera `click` en el kiosco (la página lo
sintetiza); su `<select>` nativo se queda con la entrada (reemplazado por controles propios); cog
arrancaba en inglés y con restos del primer cuadro de 1024×768 (idioma desde el servidor, repintado
completo); `/etc/issue` mostraba siempre `tango.local` (ahora `\n.local`); la ISO de laboratorio no
era desatendida con más de un disco (`tango.disk`); los `.sha256` de las ISOs no se regeneraban.

## Instalación por script y repositorio APT — 2026-10-15 (`install.sh`, `cmd/tango-aptrepo`)

Repositorio de prueba armado con `tango-aptrepo` (firmado con la clave real `TangoOS Archive`,
`B34312438C2849B683A053199C06A62DC9C4DB37`) y servido por HTTP con log de pedidos; nada contra producción.

| Prueba | Dónde | Resultado |
|---|---|---|
| `go test ./internal/aptrepo` (control del .deb, pool inmutable, época, Packages/Release, índices viejos borrados, clave de `install.sh` = `build/keys`) | Mac | ok |
| `shellcheck -s sh install.sh`, `publish-apt.sh`, `deploy-connect-web.sh` | Mac | limpio |
| `cat install.sh \| sh` en `debian:trixie` limpio (sin systemd, `TANGOOS_FORCE=1`) | Docker | instala `tangoos` 0.1.0-dev; usuario `tango`, grupo `tango-agent` |
| apt solo pide `InRelease`, `Packages.gz` y el `.deb` (log del servidor) | Docker | ok: nada que CloudFront contestaría con `index.html` |
| Versión mayor publicada (0.1.1-test) → `apt-get -s upgrade` la ve y la instala | Docker | ok |
| `InRelease` alterado | Docker | rechazado por `sqv` ("Message has been manipulated"); restaurado: sin avisos |
| Guardas: Debian 12, `:443` ocupado (con y sin `TANGOOS_FORCE`), usuario sin root ni sudo | Docker | aborta con mensaje claro / avisa y sigue / aborta |
| `curl … \| sh` en VM limpia Debian 13 cloud (systemd real, VM 111 temporal, 2 vCPU, 2 GB) | Proxmox | **1 min 18 s**; tangod, agente, smbd y avahi activos, NFS deshabilitado, `https://<ip>/` 200, `http` → 308 |
| Login admin por WebRTC (`tangoctl … system.info`) con la contraseña inicial | VM 111 | ok |
| `system.updates.check` ve 0.1.1-test; `system.updates.apply {scope: all}` la instala y tangod vuelve | VM 111 | ok (root ext4: sin instantánea de `@rootfs`, sin error) |

La VM 111 se borró al terminar. Hallazgo corregido: el postinstall usa `adduser`/`addgroup` y el
paquete no lo declaraba (`adduser` no es esencial en Debian): agregado a `Depends` en `build/nfpm.yaml`.

## Bugs de producto encontrados y corregidos en esta batería

| # | Gravedad | Bug | Arreglo | Test que lo cubre |
|---|---|---|---|---|
| 1 | **Alta (seguridad)** | 10 métodos sin chequeo de administrador: un usuario común podía planificar y **crear volúmenes (borra discos)**, lanzar scrub, crear carpetas e instantáneas, instalar/desinstalar/parar/actualizar apps y ver sus logs | `requireAdmin` en `internal/api/storage.go` y `internal/api/apps.go` | `TestSecurityNonAdmin` |
| 2 | Media | `users.setPassword` de **cualquier** usuario borraba `/var/lib/tango/initial-admin-password` | solo al cambiar la de `admin` (`internal/auth/auth.go`) | `internal/auth/initialpw_test.go` |
| 3 | Baja | `system.rollback.apply` con un id inexistente creaba un job que fallaba después (no reiniciaba nada) | valida el punto antes del job (`internal/api/m4.go`) | `TestM4CertAndUpdates` |
| 4 | **Alta (disponibilidad)** | sin caché de SMART: cada `storage.disks.list` consultaba los 13 discos al agente (1221 `disk.smart` en 5 min) y agotaba su cupo de 20/s → operaciones reales como `storage.volumes.plan` fallaban con `RATE_LIMITED` | `SmartCache` (1 min; 10 min sin SMART; 15 s ante error) en `internal/api/smartcache.go` | `TestSmartCache`, matriz |
| 5 | **Alta** | borrar un volumen (o carpeta) recién usado desde Archivos fallaba: "umount: target is busy". El watcher de `files.changed` mantenía un long-poll con fds en la carpeta, el worker esperaba ese long-poll al cerrarse y el vigilante relanzaba un worker sobre el volumen | el worker sale a lo sumo 2 s después del EOF (`internal/fileworker/worker_linux.go`); `ForgetUnder` + `InvalidateWorkers` antes de borrar (`internal/api/storage_m21.go`, `files_watch.go`); el desmontaje reintenta ~10 s (`internal/diskops/systemd_linux.go`) | `TestForgetUnder`, matriz filas 1–8 |
| 6 | Baja (test) | `internal/jobs` `TestInterruptedOnReload` intermitente (escritura de `jobs.json` contra el borrado de TempDir) | el test espera a que el job termine | 50 corridas seguidas sin fallar |

Además se agregó al cliente Go `DownloadRange` (descarga por rangos y desde instantáneas), usado por las pruebas y por la app nativa futura.

## Hallazgos sin arreglar (anotados)

- En la VM 108 la vuelta atrás responde `NOT_SUPPORTED` porque la raíz es ext4 (imagen cloud); el flujo completo con Btrfs se probó en la VM 109 instalada desde la ISO (fase M4).
- La CA local no se probó en la VM 108 para no cambiar el certificado que el usuario ya aceptó; se probó en la VM 109.
- La programación de instantáneas con intervalo corto no tiene perilla en producción; su ejecución y retención se validaron en M2.2 con un modo de depuración (aquí: ida y vuelta + validación).
- El widget de Almacenamiento muestra el primer volumen de la lista; con un volumen recién creado puede mostrar ese en lugar de `volume1` (cosmético).

## Estado del laboratorio al terminar

`volume1` RAID 1 (D1+D2) y `volume2` RAID 5 (S1, S2, S4, S5) sanos · carpetas Familia, Fotos y TimeMachine · usuarios `admin` y `sofia` (`e2etester` borrado con `users.delete`) · apps Jellyfin y Uptime Kuma corriendo · sin destinos iSCSI · papelera de Familia vacía · discos libres S3, S6, M1, M2, D3, D4 sin firmas. Quedó instalado `open-iscsi` en la VM (lo usa la suite de iSCSI). El export NFS de Familia a 172.17.0.46 es previo (prueba de M4 con la VM 109).
