# 16 · TangoOS Connect (portal web)

Portal público para llegar al NAS desde internet: **https://tangoos.go-link.org** (más adelante
`connect.tangoos.org` puede ser un CNAME). Contrato: `protocol/README.md` §15. Copy: "TangoOS Connect ·
sobre la red de go-link". Código: `web/apps/connect` (Vite + React 19 + TS, workspace `@tango/connect`).

## 1. Páginas

| Ruta | Qué hace |
|---|---|
| `/` | Landing (mismo diseño que desk.go-link.org: `tokens.css`, `landing.css`, `webapp.css` copiados de go-link-desk, MIT, mismo autor). Capturas WebP 800w/1600w con `srcset`; si falta una, queda un hueco con el nombre del archivo. |
| `/demo` | Escritorio completo de TangoOS sobre `MockTransport("lab")`, en el navegador. Carga diferida. **Sin red**: `WebSocket`, `RTCPeerConnection` y `EventSource` reemplazados por constructores que fallan, `fetch` solo al propio origen, `sendBeacon` no-op. Se entra y sale con recarga completa. |
| `/nas` | Mis NAS: vínculos guardados en IndexedDB (`tangoos-connect` / `nas`) del origen del portal. |
| `/nas/vincular` | Código de 9 dígitos (segmentado, pegar/teclado) → `claim`. |
| `/nas/<device_id>` | `reach` → el NAS prueba primero → escritorio del NAS (canal `bundle`) o, si no hay bundle firmado, modo desarrollo. |
| `/s/<device_id>.<link_id>#<secreto>` | Enlace público (sin cuenta): `reach` + `guest.open`. El secreto va en el fragmento: nunca llega al servidor. |

**Regla de dominio:** ninguna cookie con `Domain=.go-link.org`; el portal no usa cookies. Los tokens
(vínculo del navegador) viven en IndexedDB del origen; la sesión de TangoOS en `sessionStorage`
(`tango.session`) y se descarta al cambiar de NAS (`tangoconnect.device`).

## 2. Señalización

`src/signal/socket.ts → joinSignal(target)` abre un WebSocket nuevo a signalhub
(`VITE_SIGNAL_URL`, por defecto `wss://signal.go-link.org/ws?v=1`, app `tangoos`) en cada (re)conexión:

- `claim {code}` (primera vez) o `reach {device_id}` (vinculado / enlace público) → `paired {remote}`.
- El `payload` de cada `signal` es el `SignalPayload` de tangod (`{"kind":"offer"|"answer"|"candidate",…}`)
  **sin envoltura**; el NAS no manda `hello` (lo implementa `internal/connect/service.go`).
- ICE: los de signalhub (STUN + TURN efímero); `mergeIce` descarta STUN/TURN de redes privadas.

El transporte es `rtcFromSignal(join, {remote:{beforeAuth}})` de `@tango/client`: hello sintético con
esos ICE, métodos por `connect.hello`, y `beforeAuth` corre el paso de vínculo **antes** del `auth.resume`
en cada reconexión. Errores fijos de signalhub (`invalid or expired code`, `app not allowed`, …) se
traducen al castellano; los que no se arreglan reintentando cortan la sesión.

## 3. Vínculo del navegador

```
claim  → connect.link {label}            → {linkId, token, deviceId, fingerprint}  (se guarda; después: reach)
reach  → connect.challenge {linkId, nonce} → {proof}
         proof == base64url(HMAC-SHA256(SHA-256(token), "tangoconnect link proof\n"+nonce)) sin padding
         (si no coincide: NO se envía el token)
       → connect.resume {linkId, token}
guest  → guest.open {link, secret} → guest.info / guest.unlock / guest.list / guest.download.begin (files.N)
```

`fingerprint` (DTLS del NAS) se guarda y se compara con el SDP en cada conexión: si cambia es **solo un
aviso** (la prueba HMAC ya identificó al NAS). Vector compartido: `protocol/vectors/tangoconnect.json`
(generado con `internal/connect.LinkProof`).

## 4. Escritorio servido por el NAS (canal `bundle`)

1. `bundle.manifest` → `{version, files, signed, signature, keyId}`. El portal verifica Ed25519 sobre los
   bytes exactos de `signed` (JSON de `internal/connect.Manifest`, lo produce `cmd/tango-bundlesign`) con la
   clave pública **embebida en el portal** (`VITE_BUNDLE_PUBKEY`, base64 de 32 B) — nunca con
   `.tango-manifest.pub` que trae el NAS — y usa la lista de archivos de adentro de `signed`.
   Sin firma (build de desarrollo) → no se abre.
2. Valida forma (rutas relativas sin `..`, sha256 hex, ≤ 2000 archivos, ≤ 200 MB, `index.html`).
3. `RtcTransport.fetchBundleFile(path)` por cada archivo y sha256 contra el manifiesto **firmado**.
4. Cache Storage (`tangoos-bundle-<device>-<version>`) + Service Worker `/bundle-sw.js` (scope
   `/nas-bundle/`, nunca va a la red) los sirve en `/nas-bundle/<device>/…`.
5. Se libera el React del portal, se inyectan CSS y el `<script type=module>` del `index.html` del NAS, y
   el escritorio toma el transporte ya abierto de `window.__TANGO_TRANSPORT__`.

**Requisitos pendientes del lado del escritorio** (no los toca el portal):
- `web/apps/desktop/vite.config.ts` con `base: "./"` (hoy los chunks se piden con `/assets/…`, que en el
  portal apuntan al origen equivocado).
- `web/apps/desktop/src/main.tsx`: si existe `window.__TANGO_TRANSPORT__`, usarlo en vez de crear uno.

Mientras tanto `/nas/<id>` cae a **modo desarrollo**: monta el escritorio que trae el propio portal sobre
la conexión remota, con un banner que lo dice.

**Límite honesto (§15):** quien controle el portal puede servir otro cargador. Mitigación: portal mínimo,
open source, build reproducible, self-hosteable (`cmd/connect-web`).

## 5. Correr local

```
make connect-web-preview     # capturas WebP + build + vite preview en http://localhost:5174
cd web/apps/connect && npm run dev   # desarrollo (puerto 5174)
make connect-web             # además: binario dist/connect-web (embed + cabeceras de seguridad)
./dist/connect-web -addr :8080 -signal wss://signal.go-link.org
```

Variables de build: `VITE_SIGNAL_URL`, `VITE_BUNDLE_PUBKEY`. Para probar contra un NAS real, signalhub
tiene que tener `tangoos` en `ALLOWED_APPS` (si no: "app not allowed", que el portal explica).

Tests: `cd web && npx vitest run apps/connect` (código de 9 dígitos, IndexedDB, firma del manifiesto con el
vector de Go, prueba de vínculo con el vector de Go, señalización con un signalhub falso, frames, rutas) y
`go test ./cmd/connect-web`.

## 6. `cmd/connect-web`

Servidor estático para self-hosting: embebe `web/apps/connect/dist` (placeholder si no se construyó),
fallback SPA para las rutas, 404 para archivos inexistentes, `immutable` para `/assets/` y `/img/opt/`,
`no-cache` para el resto, solo GET/HEAD, sin cookies. Cabeceras:

```
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline';
  img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:;
  connect-src 'self' <signal>; worker-src 'self'; frame-src 'self' data: blob:;
  object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
X-Content-Type-Options: nosniff · Referrer-Policy: no-referrer · X-Frame-Options: DENY
Cross-Origin-Opener-Policy: same-origin · Permissions-Policy: camera=(), microphone=(), geolocation=()
Strict-Transport-Security (detrás de TLS)
```

## 7. Publicación (tangoos.go-link.org)

Mismo esquema que desk.go-link.org (`go-link-desk/scripts/deploy-web.sh`): bucket S3 privado detrás de
CloudFront. `scripts/deploy-connect-web.sh` (no se corre en CI ni automáticamente):

- exige `TANGOOS_DISTRIBUTION`; `TANGOOS_BUCKET` (por defecto `tangoos.go-link.org`), `AWS_PROFILE`;
- se niega si hay `.map` o PNG originales en `dist/img`;
- sube `assets/` e `img/opt/` con `max-age=31536000, immutable`, después las páginas con `no-cache`,
  borra lo viejo (salvo assets) e invalida `/*`.

En CloudFront: error 403/404 → `/index.html` 200 (rutas SPA) y una *response headers policy* con las
mismas cabeceras de §6 (`connect-src 'self' wss://signal.go-link.org`). Sin cookies en el comportamiento.

## 8. Pendientes

- Escritorio servido por el NAS: `base: "./"` + `window.__TANGO_TRANSPORT__` en `apps/desktop` (§4), y
  publicar la clave pública de release en `VITE_BUNDLE_PUBKEY` del build del portal.
- La huella DTLS es solo aviso; no hay pinning duro.
- Estado "en línea" de cada NAS en `/nas`: no se sondea (signalhub limita `reach` a 5/min); se muestra el
  último acceso.
- `protocol/README.md` §15 todavía describe enlaces públicos con `join {invite}`; la implementación real
  (y el portal) usan `reach` + `guest.open {link, secret}` con URL `/s/<device>.<link>#<secreto>`.
- No probado contra un NAS real ni contra signalhub de producción desde este portal (falta `tangoos` en
  `ALLOWED_APPS` y desplegar). Probado: build, tests, demo en preview local.
