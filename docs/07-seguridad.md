# 07 — Seguridad

Este documento cubre la línea de base de seguridad de TangoOS: lo propio de WebRTC, el modelo de
identidad y la separación de privilegios concreta.

## 1. Modelo de amenazas (resumen)

| Actor | Ejemplo | Defensa principal |
|---|---|---|
| Atacante en la LAN | fuerza bruta de login, MITM en Wi-Fi | rate-limit + lockout, TLS de confianza, verificación de fingerprint DTLS |
| Usuario autenticado sin admin | leer shares ajenos, escalar | authz por método, file-worker con su UID, ACL del kernel |
| Contenido web malicioso | XSS en el nombre de un archivo, SVG subido | CSP estricta, sin `dangerouslySetInnerHTML`, vista previa de SVG/HTML en iframe *sandbox* |
| App de contenedor comprometida | escapar del contenedor | `userns-remap`, sin montar el socket de Docker en ninguna app, red bridge aislada por app, sin `--privileged` salvo apps marcadas en el catálogo |
| Compromiso de `tangod` | RCE en el server | no es root; el agente solo acepta operaciones tipadas y revalida |
| Update malicioso | servidor de updates comprometido | firma Ed25519 con la clave offline, verificación antes de aplicar |

## 2. Autenticación sobre WebRTC

1. La PC se establece **sin credenciales**. La conexión queda *gated*: solo `auth.*` y
   `system.info`.
2. `auth.login {user, password}` por `rpc`, cifrado con DTLS. La contraseña se verifica con
   **argon2id** (como go-link) y lockout exponencial por usuario + IP.
3. Si tiene **2FA**, se pide `auth.totp {code}` (TOTP, RFC 6238). WebAuthn/passkeys llegan
   en M5. WebDAV, SFTP y SMB no pueden pedir el código: con 2FA activa aceptan solo
   **contraseñas de app** (24 caracteres aleatorios, se ven una vez, solo se guarda su SHA-256,
   revocables una por una; protocol/README.md §14). Una de ellas puede ser la contraseña SMB.
4. Respuesta: `session_token` (256 bits aleatorios). El servidor guarda solo su SHA-256, con
   expiración por inactividad (configurable, 30 min por defecto) y absoluta.
5. **Reanudar** (recarga de página o reconexión): el patrón de go-link "*el host prueba
   primero*". El NAS manda `HMAC-SHA256(SHA-256(token), "tango resume\n"+nonce)` y recién
   entonces el cliente manda su token. Así un NAS falso en la LAN no puede robar el token.
6. **Dónde vive el token en el navegador:** en `sessionStorage` (por pestaña) y, si el usuario
   marca "recordar este dispositivo", en IndexedDB **cifrado con una CryptoKey no
   exportable**. En las apps nativas va en el keyring del SO.
7. Sesiones visibles y revocables desde la app Seguridad (dispositivo, IP, último uso). Al
   revocar, el NAS cierra la PC de inmediato.

## 3. TLS en la LAN (requisito, no opcional)

Sin un certificado de confianza no hay Service Worker ([03 §8](03-protocolo-webrtc.md)) y
el usuario aprende a ignorar advertencias. Opciones, por orden:

1. **DNS propio del proyecto (estilo `plex.direct`):** `192-168-1-50.<nas_id>.tangoos.net`
   resuelve a la IP privada, y el NAS obtiene un certificado Let's Encrypt por DNS-01 a través
   de un pequeño servicio del proyecto. Es la mejor experiencia. Requiere operar ese servicio,
   y algunos routers bloquean las respuestas DNS con IP privada (*DNS rebinding protection*):
   hay que documentarlo.
2. **ACME con dominio propio del usuario** (DNS-01 vía Cloudflare/Route53, etc.).
3. **CA local de TangoOS:** el usuario descarga e instala la raíz (la app nativa lo hace sola).
4. **Autofirmado**, solo en el primer arranque, mostrando el fingerprint en la consola/HDMI.

El WebSocket `/signal` y el bundle siempre van por HTTPS. El puerto 80 solo redirige.

## 4. Superficie expuesta

| Puerto | Proceso | Notas |
|---|---|---|
| 443/tcp | tangod | bundle + `/signal` |
| 8443/udp + 8443/tcp | tangod | ICE mux |
| 3478/udp+tcp | tangod (pion/turn) | STUN; TURN solo con credenciales efímeras |
| 445/tcp | smbd | si hay shares |
| 2049/tcp | nfsd | si hay exports NFS |
| 3260/tcp | LIO (iSCSI) | si hay targets; CHAP obligatorio |
| 9100–9199/tcp | tangod (proxy de apps) | uno por app instalada; TLS con el certificado de tangod; sin compuerta de sesión en el MVP |
| puertos de cada app | docker | p. ej. 8096 Jellyfin, 22000 Syncthing, 53 Pi-hole; Vaultwarden solo en 127.0.0.1 |
| 22/tcp | sshd | **desactivado** por defecto; SFTP con chroot por usuario |
| 5353/udp | avahi | descubrimiento macOS/Linux (Bonjour, Time Machine) |
| 3702/udp, 5357/tcp | tangod (WS-Discovery) | «Red» del Explorador de Windows; solo si Samba está instalado |

Firewall nftables generado desde Ajustes; por defecto permite solo la subred local.

## 5. Separación de privilegios

```
tangod (uid tango) ──unix socket──> tango-agent (root)
                                        ├─ ops de sistema: mdadm, btrfs, samba, nft, systemd, users
                                        └─ spawn file-worker (uid = usuario autenticado, gid = sus grupos)
tangod <──pipe─── file-worker(uid 1003)  → listar, leer, escribir, renombrar dentro de los shares
```

- **file-worker por usuario:** lo crea el agente con `setresuid`/`setgroups` y `openat2(RESOLVE_BENEATH | RESOLVE_NO_MAGICLINKS)` anclado en la raíz del share. Así
  el kernel aplica permisos y ACL reales y se elimina la clase entera de *path traversal* y
  *symlink escape*.
- **Allow-list del agente:** cada `op` tiene un struct Go con validación (`regexp` de nombres,
  rangos, rutas canónicas bajo `/volume*`). Un `op` desconocido o un payload con campos de más
  se rechaza (`DisallowUnknownFields`).
- Unidades systemd endurecidas: `ProtectSystem=strict`, `NoNewPrivileges` (en tangod),
  `CapabilityBoundingSet` mínimo, `SystemCallFilter`.

## 6. Auditoría

Evento `{ts, actor, session, ip, client, method, target, result, job_id}` en SQLite, con
rotación y exportación. Nunca contiene contraseñas, tokens ni contenido de archivos. Se ve en
la app Registro y se puede reenviar por syslog.

## 7. Cadena de suministro

Versiones fijadas, `govulncheck` + `npm audit` + **gitleaks** en CI (como go-link), SBOM
(CycloneDX) por release, `SHA256SUMS` firmado con Ed25519 y builds reproducibles cuando se
pueda.

## 8. Riesgos aceptados en M4 (revisar antes de v1.0)

- **Terminal de administración = root real.** Es lo que espera un admin de NAS, pero un admin
  comprometido tiene el sistema. Mitigación: solo administradores, sesión auditada (inicio/fin), 2FA
  recomendado para admins. No se graban las teclas.
- **Proxy de apps:** la cookie `tango_app` vale 12 h por app; cerrar sesión en TangoOS no la revoca
  (TODO: lista de revocación por usuario).
- **NFS con `sec=sys`:** confía en los UID/GID que declara el cliente de la red permitida. Exportar
  solo a equipos de confianza; Kerberos (`sec=krb5p`) queda para después.
- **WebDAV/SFTP** comparten el bloqueo por intentos con el login (5 → 5 min por usuario+IP).
