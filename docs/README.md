# TangoOS — Design documentation

> An open-source (MIT) NAS operating system built to compete with commercial NAS systems on product quality, on your hardware and with your disks,
> a web desktop with a macOS look, and **all administration traffic over WebRTC**.

Status: **v0.1 (pre-release). M0–M3.5 implemented, M4 (v1.0) mostly done and in testing** on VMs, and TangoConnect
from M5 done. M6 has not started. TangoOS manages real disks; progress milestone by milestone is in [09](09-roadmap.md),
and what is pending is in [09 → Next](09-roadmap.md#next-post-v10).

## Index

| # | Document | What it covers |
|---|---|---|
| 01 | [Vision and scope](01-vision-and-scope.md) | Parity with DSM, MVP, non-goals |
| 02 | [Architecture](02-architecture.md) | Processes, layers, the flow of an operation |
| 03 | [WebRTC protocol](03-webrtc-protocol.md) | Signaling, embedded STUN, channels, RPC, events, files |
| 04 | [Storage](04-storage.md) | mdadm + Btrfs (the commercial NAS model), snapshots, shares |
| 05 | [Desktop and themes](05-desktop-and-themes.md) | Window manager, Dock, theme system, quality bar |
| 06 | [Native apps](06-native-apps.md) | Windows / Linux (and later macOS) with the same desktop |
| 07 | [Security](07-security.md) | Threat model, auth, privileged agent |
| 08 | [Base system, installer and updates](08-base-system-installer-updates.md) | Debian 13 minimal + apt repo, ISO, updates with rollback |
| 09 | [Roadmap](09-roadmap.md) | Milestones M0–M6 with gates, and their current status |
| 10 | [Repo structure and conventions](10-repo-structure-and-conventions.md) | Monorepo, pinned stack, build |
| 11 | [Licenses and brand](11-licenses-and-brand.md) | MIT, Mac-OS-Desktop, Apple, other vendors, name |
| 12 | [Strategy vs commercial NAS](12-strategy-vs-commercial-nas.md) | Where commercial NAS systems are weak, our wedges, migration |
| 13 | [Desktop prototype](13-desktop-prototype.md) | Full spec: screens, shell, tokens, motion, apps, mock, acceptance |
| 14 | [Test plan](14-test-plan.md) | Unit, API and UI end-to-end, storage matrix, installer |
| 16 | [TangoConnect](16-tangoconnect.md) | Remote access and share links through signalhub |
| ADR | [adr/](adr/) | Recorded decisions |

## Key decisions (summary)

1. **WebRTC instead of REST.** One encrypted (DTLS) `RTCPeerConnection` per client, with
   one DataChannel per purpose: `rpc`, `events`, `files`, `term`, `metrics`. HTTP is
   reduced to serving the static bundle + a signaling WebSocket. See [03](03-webrtc-protocol.md).
2. **`tangod` itself is the signaling and STUN server** (embedded pion/turn, UDP 3478) and
   exposes ICE on a single multiplexed UDP port. It is LAN-first; remote access
   (TangoConnect) reuses go-link's infrastructure (signalhub + coturn).
3. **Same stack as go-link-desk:** Go 1.26 + `pion/webrtc/v4`, React 19 + TypeScript + Vite in
   npm workspaces, no state library, `{type,…}` JSON envelopes, nFPM, Ed25519-signed sums.
4. **Storage like commercial NAS systems:** RAID with `mdadm` + single-device Btrfs on top
   (not Btrfs's native RAID5/6). Shares = subvolumes.
5. **Our own desktop, from scratch.** Mac-OS-Desktop is the *visual reference*; no code
   or assets are copied (it has no license and ships Apple artwork). The default theme follows the macOS look;
   the theme engine allows a second theme of our own without touching the apps.
6. **Native apps = the same desktop inside a webview**, with WebRTC in Go (pion) on the
   native side and a *bridge*. This avoids depending on WebKitGTK's WebRTC and gives
   faster transfers.
7. **Debian 13 minimal + "our face".** TangoOS is not a new distro: it is the management
   layer + interface, shipped as a `.deb` in a signed apt repo, on top of real Debian.
   See [08](08-base-system-installer-updates.md).
8. **100% native Go.** There is no other server for the web: only `tangod` and `tango-agent`. We
   talk to the kernel (ioctl, sysfs, netlink) and to systemd over D-Bus; `exec` is the documented
   exception ([ADR 0009](adr/0009-native-go-without-exec.md)).
9. **Separate privileged agent** (`tango-agent`, root, Unix socket, typed operations in an
   allow-list). `tangod` runs unprivileged.
