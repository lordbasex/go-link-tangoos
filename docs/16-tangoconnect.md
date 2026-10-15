# 16 · TangoOS Connect (web portal)

Public portal for reaching the NAS from the internet: **https://tangoos.go-link.org** (later on
`connect.tangoos.org` can be a CNAME). Contract: `protocol/README.md` §15. Copy: "TangoOS Connect ·
on the go-link network". Code: `web/apps/connect` (Vite + React 19 + TS, workspace `@tango/connect`).

## 1. Pages

| Route | What it does |
|---|---|
| `/` | Landing (same design as desk.go-link.org: `tokens.css`, `landing.css`, `webapp.css` copied from go-link-desk, MIT, same author). WebP screenshots at 800w/1600w with `srcset`; if one is missing, a gap is left with the file name. |
| `/demo` | Full TangoOS desktop on `MockTransport("lab")`, in the browser. Lazy-loaded. **No network**: `WebSocket`, `RTCPeerConnection` and `EventSource` are replaced with constructors that fail, `fetch` only to its own origin, `sendBeacon` is a no-op. Entering and leaving does a full reload. |
| `/nas` | My NAS: links stored in IndexedDB (`tangoos-connect` / `nas`) of the portal's origin. |
| `/nas/vincular` | 9-digit code (segmented, paste/keyboard) → `claim`. |
| `/nas/<device_id>` | `reach` → the NAS proves itself first → the NAS's desktop (`bundle` channel) or, if there is no signed bundle, development mode. |
| `/s/<device_id>.<link_id>#<secret>` | Public link (no account): `reach` + `guest.open`. The secret goes in the fragment: it never reaches the server. |

**Domain rule:** no cookie with `Domain=.go-link.org`; the portal does not use cookies. The tokens
(browser link) live in the origin's IndexedDB; the TangoOS session lives in `sessionStorage`
(`tango.session`) and is discarded when switching NAS (`tangoconnect.device`).

## 2. Signaling

`src/signal/socket.ts → joinSignal(target)` opens a new WebSocket to signalhub
(`VITE_SIGNAL_URL`, by default `wss://signal.go-link.org/ws?v=1`, app `tangoos`) on every (re)connection:

- `claim {code}` (first time) or `reach {device_id}` (linked / public link) → `paired {remote}`.
- The `payload` of each `signal` is tangod's `SignalPayload` (`{"kind":"offer"|"answer"|"candidate",…}`)
  **with no wrapper**; the NAS does not send `hello` (implemented in `internal/connect/service.go`).
- ICE: the ones from signalhub (STUN + ephemeral TURN); `mergeIce` discards STUN/TURN on private networks.

The transport is `rtcFromSignal(join, {remote:{beforeAuth}})` from `@tango/client`: a synthetic hello with
those ICE servers, methods via `connect.hello`, and `beforeAuth` runs the link step **before** `auth.resume`
on every reconnection. Fixed signalhub errors (`invalid or expired code`, `app not allowed`, …) are
translated into Spanish; the ones that retrying will not fix end the session.

## 3. Browser link

```
claim  → connect.link {label}            → {linkId, token, deviceId, fingerprint}  (stored; afterwards: reach)
reach  → connect.challenge {linkId, nonce} → {proof}
         proof == base64url(HMAC-SHA256(SHA-256(token), "tangoconnect link proof\n"+nonce)) without padding
         (if it does not match: the token is NOT sent)
       → connect.resume {linkId, token}
guest  → guest.open {link, secret} → guest.info / guest.unlock / guest.list / guest.download.begin (files.N)
```

`fingerprint` (the NAS's DTLS) is stored and compared with the SDP on every connection, before
`connect.challenge`: if it changed, the portal **aborts** (`FINGERPRINT_CHANGED`) without sending a proof or the
token, and the user decides whether to forget the NAS and link it again (e.g. after a reinstall). Shared vector: `protocol/vectors/tangoconnect.json`
(generated with `internal/connect.LinkProof`).

## 4. Desktop served by the NAS (`bundle` channel)

1. `bundle.manifest` → `{version, files, signed, signature, keyId}`. The portal verifies Ed25519 over the
   exact bytes of `signed` (JSON of `internal/connect.Manifest`, produced by `cmd/tango-bundlesign`) with the
   public key **embedded in the portal** (`VITE_BUNDLE_PUBKEY`, base64 of 32 B) — never with the
   `.tango-manifest.pub` that the NAS ships — and uses the file list from inside `signed`.
   Unsigned (development build) → it is not opened.
2. Validates the shape (relative paths without `..`, hex sha256, ≤ 2000 files, ≤ 200 MB, `index.html`).
3. `RtcTransport.fetchBundleFile(path)` for each file, and sha256 checked against the **signed** manifest.
4. Cache Storage (`tangoos-bundle-<device>-<version>`) + Service Worker `/bundle-sw.js` (scope
   `/nas-bundle/`, never goes to the network) serve them at `/nas-bundle/<device>/…`.
5. The portal's React is released, the CSS and the `<script type=module>` from the NAS's `index.html` are injected, and
   the desktop takes the already open transport from `window.__TANGO_TRANSPORT__`.

Desktop side (done): `web/apps/desktop/vite.config.ts` builds with `base: "./"` (the same build is served by
tangod at `/` and by the portal from `/nas-bundle/<device>/`), and `web/apps/desktop/src/main.tsx` uses
`window.__TANGO_TRANSPORT__` when it exists instead of opening a new transport.

Public key: `make connect-web-build` sets `VITE_BUNDLE_PUBKEY` from `build/keys/release-ed25519.pub` (in the
repo; the private seed lives outside it and is passed to `cmd/tango-bundlesign` with `TANGO_BUNDLE_KEY`,
see `make release-vm`). `make web` signs with the development key (`build/keys/dev-ed25519`) by default, and
`make connect-portal-dev` builds a portal that trusts that dev key (used by `make e2e-connect`).

**Development mode** is only a fallback: `/nas/<id>` mounts the desktop that the portal itself ships on top of
the remote connection, with a banner saying why, when the portal has no public key, the NAS does not serve
`bundle.manifest`, or the NAS desktop fails to mount. A security failure (bad signature, bad hash, bad
manifest) never falls back: the session stops with an error.

**Honest limit (§15):** whoever controls the portal can serve a different loader. Mitigation: minimal portal,
open source, reproducible build, self-hostable (`cmd/connect-web`).

## 5. Running locally

```
make connect-web-preview     # WebP screenshots + build + vite preview at http://localhost:5174
cd web/apps/connect && npm run dev   # development (port 5174)
make connect-web             # also: dist/connect-web binary (embed + security headers)
./dist/connect-web -addr :8080 -signal wss://signal.go-link.org
```

Build variables: `VITE_SIGNAL_URL`, `VITE_BUNDLE_PUBKEY`. To test against a real NAS, signalhub
must have `tangoos` in `ALLOWED_APPS` (otherwise: "app not allowed", which the portal explains).

Tests: `cd web && npx vitest run apps/connect` (9-digit code, IndexedDB, manifest signature with the
Go vector, link proof with the Go vector, signaling with a fake signalhub, frames, routes) and
`go test ./cmd/connect-web`.

## 6. `cmd/connect-web`

Static server for self-hosting: embeds `web/apps/connect/dist` (a placeholder if it was not built),
SPA fallback for routes, 404 for nonexistent files, `immutable` for `/assets/` and `/img/opt/`,
`no-cache` for the rest, GET/HEAD only, no cookies. Headers:

```
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline';
  img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:;
  connect-src 'self' <signal>; worker-src 'self'; frame-src 'self' data: blob:;
  object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
X-Content-Type-Options: nosniff · Referrer-Policy: no-referrer · X-Frame-Options: DENY
Cross-Origin-Opener-Policy: same-origin · Permissions-Policy: camera=(), microphone=(), geolocation=()
Strict-Transport-Security (behind TLS)
```

## 7. Publishing (tangoos.go-link.org)

Same scheme as desk.go-link.org (`go-link-desk/scripts/deploy-web.sh`): private S3 bucket behind
CloudFront. `scripts/deploy-connect-web.sh` (not run in CI or automatically):

- requires `TANGOOS_DISTRIBUTION`; `TANGOOS_BUCKET` (default `tangoos.go-link.org`), `AWS_PROFILE`;
- refuses if there are `.map` files or original PNGs in `dist/img`;
- uploads `assets/` and `img/opt/` with `max-age=31536000, immutable`, then the pages with `no-cache`,
  deletes old files (except assets) and invalidates `/*`.

In CloudFront: error 403/404 → `/index.html` 200 (SPA routes) and a *response headers policy* with the
same headers as §6 (`connect-src 'self' wss://signal.go-link.org`). No cookies in the behavior.

## 8. Pending

- "Online" status of each NAS in `/nas`: it is not polled (signalhub limits `reach` to 5/min); the
  last access is shown.
- Tested end to end with a real browser against the lab NAS and a local development signalhub
  (`make e2e-connect`, tests/README.md); that run never uses the production signal server. Production
  signalhub must have `tangoos` in `ALLOWED_APPS`.
