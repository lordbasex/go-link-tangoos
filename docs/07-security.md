# 07 — Security

This document covers the TangoOS security baseline: the WebRTC-specific parts, the
identity model and the concrete privilege separation.

## 1. Threat model (summary)

| Actor | Example | Main defense |
|---|---|---|
| Attacker on the LAN | login brute force, MITM on Wi-Fi | rate limit + lockout, trusted TLS, DTLS fingerprint verification |
| Authenticated non-admin user | read other users' shares, escalate | per-method authz, file-worker with their UID, kernel ACLs |
| Malicious web content | XSS in a file name, uploaded SVG | strict CSP, no `dangerouslySetInnerHTML`, SVG/HTML preview in a *sandbox* iframe |
| Compromised container app | escape the container | `userns-remap`, the Docker socket is not mounted into any app, isolated bridge network per app, no `--privileged` except apps flagged in the catalog |
| `tangod` compromise | RCE on the server | it is not root; the agent only accepts typed operations and revalidates them |
| Malicious update | compromised update server | apt repository signed with GPG (key outside the repo) and the desktop bundle signed with Ed25519, both verified before use |

## 2. Authentication over WebRTC

1. The PeerConnection is established **without credentials**. The connection is *gated*: only `auth.*` and
   `system.info`.
2. `auth.login {user, password}` over `rpc`, encrypted with DTLS. The password is verified with
   **argon2id** (like go-link) with exponential lockout per user + IP.
3. If the user has **2FA**, `auth.totp {code}` is requested (TOTP, RFC 6238). WebAuthn/passkeys arrive
   in M5. WebDAV, SFTP and SMB cannot ask for the code: with 2FA on they only accept
   **app passwords** (24 random characters, shown once, only their SHA-256 is stored,
   revocable one by one; protocol/README.md §14). One of them can be the SMB password.
4. Response: `session_token` (256 random bits). The server stores only its SHA-256, with
   an idle expiration (configurable, 30 min by default) and an absolute one.
5. **Resume** (page reload or reconnection): the go-link "*the host proves
   first*" pattern. The NAS sends `HMAC-SHA256(SHA-256(token), "tango resume\n"+nonce)` and only
   then does the client send its token. That way a fake NAS on the LAN cannot steal the token.
6. **Where the token lives in the browser:** in `sessionStorage` (per tab) and, if the user
   ticks "remember this device", in IndexedDB **encrypted with a non-extractable
   CryptoKey**. In the native apps it goes in the OS keyring. Today only `sessionStorage` is implemented; "remember this device" and the native keyring are planned.
7. Sessions are visible and revocable from the Security app (device, IP, last use). On
   revoke, the NAS closes the PeerConnection immediately.

## 3. TLS on the LAN (a requirement, not optional)

Without a trusted certificate there is no Service Worker ([03 §8](03-webrtc-protocol.md)) and
the user learns to ignore warnings. Options, in order:

1. **The project's own DNS (`plex.direct` style):** `192-168-1-50.<nas_id>.tangoos.net`
   resolves to the private IP, and the NAS gets a Let's Encrypt certificate via DNS-01 through
   a small project service. It is the best experience. It requires running that service,
   and some routers block DNS responses with private IPs (*DNS rebinding protection*):
   this must be documented.
2. **ACME with the user's own domain** (DNS-01 via Cloudflare/Route53, etc.).
3. **TangoOS local CA:** the user downloads and installs the root (the native app does it on its own).
4. **Self-signed**, only on first boot, showing the fingerprint on the console/HDMI.

**Implemented today:** 4 (self-signed on first run, `internal/tlscert`) and 3 (TangoOS local CA,
`internal/certs`: `cert.localca`, `cert.caDownload`; switches without a restart). Options 1 and 2 are not
implemented.

The `/signal` WebSocket and the bundle always go over HTTPS. Port 80 only redirects.

## 4. Exposed surface

| Port | Process | Notes |
|---|---|---|
| 443/tcp | tangod | bundle + `/signal` |
| 8443/udp + 8443/tcp | tangod | ICE mux |
| 3478/udp+tcp | tangod (pion/turn) | STUN only (every TURN allocation is rejected) |
| 445/tcp | smbd | if there are shares |
| 2049/tcp | nfsd | if there are NFS exports |
| 3260/tcp | LIO (iSCSI) | if there are targets; CHAP mandatory |
| 9100–9199/tcp | tangod (app proxy) | one per installed app; TLS with tangod's certificate; no session gate in the MVP |
| each app's ports | docker | e.g. 8096 Jellyfin, 22000 Syncthing, 53 Pi-hole; Vaultwarden only on 127.0.0.1 |
| 22/tcp | sshd | **disabled** by default in the product ISO |
| 2222/tcp | tangod (SFTP) | own Go SFTP server, no shell, TangoOS passwords, files through the file-worker; only if enabled |
| 5353/udp | avahi | macOS/Linux discovery (Bonjour, Time Machine) |
| 3702/udp, 5357/tcp | tangod (WS-Discovery) | "Network" in Windows Explorer; only if Samba is installed |

Planned, **not implemented**: an nftables firewall generated from Settings that by default allows only
the local subnet. Today TangoOS does not manage the firewall.

## 5. Privilege separation

```
tangod (uid tango) ──unix socket──> tango-agent (root)
                                        ├─ system ops: mdadm, btrfs, samba, nft, systemd, users
                                        └─ spawn file-worker (uid = authenticated user, gid = their groups)
tangod <──pipe─── file-worker(uid 1003)  → list, read, write, rename inside the shares
```

- **Per-user file-worker:** the agent creates it with `setresuid`/`setgroups` and `openat2(RESOLVE_BENEATH | RESOLVE_NO_MAGICLINKS)` anchored at the share root. That way
  the kernel enforces the real permissions and ACLs, and the whole class of *path traversal* and
  *symlink escape* bugs is eliminated.
- **Agent allow-list:** each `op` has a Go struct with validation (name `regexp`,
  ranges, canonical paths under `/volume*`). An unknown `op` or a payload with extra fields
  is rejected (`DisallowUnknownFields`).
- Hardened systemd units: `NoNewPrivileges`, minimal `CapabilityBoundingSet` and
  `SystemCallFilter=@system-service` on both; `ProtectSystem=strict` on `tangod`, `ProtectSystem=yes` on
  the agent (it has to write `/etc`: Samba, `.mount` units, users).

## 6. Auditing

Event `{ts, actor, session, ip, client, method, target, result, job_id}` in
`/var/lib/tango/audit.jsonl` (0600, rotated at 10 MB × 5). It never contains passwords, tokens or file
contents. It is shown in the Log app (live `audit.event` for admins). Planned: export and forwarding over
syslog.

## 7. Supply chain

Pinned versions, `govulncheck` + `npm audit` + **gitleaks** in CI (like go-link), an SBOM
(CycloneDX) per release, `SHA256SUMS` signed with Ed25519, and reproducible builds where
possible. Today only the pinned versions apply: there is no CI workflow, SBOM or `SHA256SUMS` yet (the
desktop bundle manifest and the apt repository are signed).

## 8. Risks accepted in M4 (review before v1.0)

- **Admin terminal = real root.** It is what a NAS admin expects, but a compromised
  admin owns the system. Mitigation: administrators only, audited session (start/end), 2FA
  recommended for admins. Keystrokes are not recorded.
- **App proxy:** the `tango_app` cookie is valid for 12 h per app; signing out of TangoOS does not revoke it
  (TODO: per-user revocation list).
- **NFS with `sec=sys`:** it trusts the UIDs/GIDs declared by the client on the allowed network. Export
  only to trusted machines; Kerberos (`sec=krb5p`) is left for later.
- **WebDAV/SFTP** share the failed-attempt lockout with the login (5 → 5 min per user+IP).
