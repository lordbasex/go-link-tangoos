# 09 — Roadmap

Each milestone has a **gate**: we do not move to the next one until it is met. Durations are
rough estimates for one person working with agents.

**Where we are (v0.1, October 2026):** M0–M3.5 are done except for the items still unchecked
below. M4 (v1.0) is mostly done and in testing on VMs. From M5, TangoConnect is done. M6 has not
started. Every check below was verified against the code and [`tests/RESULTS.md`](../tests/RESULTS.md).

## M0 — Foundations and prototype (2–3 weeks)

- [ ] Monorepo, CI (Go + web + gitleaks + govulncheck), MIT license, ADRs.
      _Done: monorepo, MIT license, ADRs, `make check` (gofmt, go vet, go test). Missing: CI, gitleaks and
      govulncheck._
- [x] **Navigable visual prototype** with `MockTransport` ([05](05-desktop-and-themes.md#prototype-the-next-thing-to-build)).
- [x] **WebRTC spike:** minimal `tangod` with embed.FS + `/signal` + pion + `rpc` channel
      (`system.info`) + embedded STUN.
- [ ] **Throughput benchmark** for browser↔pion and pion↔pion DataChannels ([03 §7](03-webrtc-protocol.md)).
      _Done: pion↔pion with one channel and the 1 GiB end-to-end transfer. Missing: browser↔pion and the
      channel-count matrix._
- [x] Proof of concept of Service Worker → DataChannel with `<video>` and Range.

**Gate:** the prototype looks on par with Mac-OS-Desktop, and the throughput numbers are
documented and accepted.

## M1 — Platform (4–5 weeks)

- [ ] `protocol/` + Go/TS codegen + shared vectors.
      _Done: `protocol/` and the shared vectors. Missing: codegen; the TS types are written by hand._
- [x] `pkg/rtcrpc`: router, declarative authz, events with `seq`/resume, cancellation.
- [x] Login, sessions, argon2id, lockout, TOTP, "the host proves first" resume.
- [x] `tango-agent` with a versioned protocol, `SO_PEERCRED`, journal and the first read-only ops.
- [x] Durable jobs + audit + reconciliation.
- [x] Real shell connected: Monitor (`metrics` channel) and Log.
- [x] `.deb` with nFPM + development apt repo + test VM with Debian 13 minimal and virtual disks.

**Gate:** real login and live Monitor/Log over WebRTC; no destructive operations.

## M2 — Storage (5–6 weeks)

- [x] Disk inventory (sysfs + udev + native SMART), read-only.
- [ ] Detect volumes from other NAS systems (without mounting them).
      _Only in the UI with simulated data; the backend does not detect them yet._
- [x] Create a Basic / RAID1 volume with a plan + confirmation token.
- [x] Shared folders = subvolumes; snapshots and retention; restore a file.
- [x] Scrub, degraded alerts, replace a disk and rebuild.
- [ ] QEMU harness with injected faults in CI.
      _The fault-injection matrix exists (`tests/e2e`) and passes, but it runs by hand against a VM, not in CI._

**Gate:** the whole battery in [04](04-storage.md#test-battery-qemu-virtual-disks) passes.

## M3 — Files, SMB and native app (5–6 weeks)

- [x] Per-UID file-worker with `openat2`; complete Files app; trash.
- [ ] Transfers with a channel pool, resumable, and a Service Worker for downloads and previews.
      _Done: channel pool, ranges and Service Worker (`/dc/stream`, `/dc/download`, 206). Missing: resuming uploads._
- [x] Users, groups, permissions; Samba + Time Machine + `vfs_shadow_copy2`; NFS; SFTP with chroot; mDNS + WS-Discovery.
      _Users with create, delete (`users.delete`) and app passwords; Time Machine (vfs_fruit + Bonjour `_adisk._tcp`);
      Windows "Previous Versions" (`vfs_shadow_copy2` over the `.gmt` links); native Go WS-Discovery (no wsdd2).
      Verified on VM 108 (protocol/README.md §14)._
- [ ] `tango-desk` for Windows and Linux (webview + pion + bridge), pairing and SMB mounting.
- [ ] macOS and Windows client tests.
- [ ] Volumes from other NAS systems: mount read-only and browse them in Files ([12](12-strategy-vs-commercial-nas.md)).

**Gate:** the [MVP success metric](01-vision-and-scope.md#mvp-success-metric) is met.

## M3.5 — App center (moved forward at the product's request)

- [x] Install Docker Engine (Debian's `docker.io` + `docker-compose`) from the NAS, as a job with progress.
- [x] `tangod` → Docker Engine API over a Unix socket, through the agent (no `exec`, ADR 0009).
- [x] App catalog (own manifest + Compose): Jellyfin, Immich, Home Assistant, Nextcloud, Pi-hole, Vaultwarden…
- [x] Install / start / stop / update / uninstall / live logs; data in the volume's `@apps/<app>`.
- [x] Each app opens its own desktop window (local proxy to the container's port).

**Gate:** install, use, update and uninstall 3 catalog apps without a terminal, with data that survives the update.

## M4 — Installable product (5–6 weeks) → **v1.0** (in testing)

- [x] Signed apt repo, ISO, first boot, apt updates with a root snapshot and rollback.
      _The ISO is our own image (debootstrap + squashfs) with a graphical installer, which can also be driven
      from another computer on the network; the netinst + preseed image remains as `make iso-netinst`. Rollback
      swaps the `@rootfs` Btrfs subvolume (our own, not grub-btrfs). Also `install.sh` over an existing Debian 13.
      Verified on VMs 109 and 111._
- [x] iSCSI (LIO), WebDAV.
- [ ] Certificates (project DNS / ACME / local CA).
      _Done: self-signed and local CA, swapped without a restart. Missing: ACME and the project's DNS._
- [ ] RAID5/6/10, LUKS2, notifications (email/ntfy/Telegram), UPS (NUT).
      _Done and verified on the VM: RAID 5/6/10, notifications and UPS over NUT (local over USB or remote,
      orderly shutdown). Missing: LUKS2._
- [ ] Configuration backup/import; recovery mode.
- [ ] Import wizard for volumes from other NAS systems + tests with real volumes.
- [x] Mission Control and widgets.
- [ ] Release with SBOM, signed sums and a hardware matrix.
      _Today there are only `.sha256` files for the ISO._

**Gate:** clean install → update → forced rollback → the data is intact.

## Next (post v1.0)

Priorities from the comparison with commercial NAS systems, minus what was already done in the v1.0 phase
(app proxy with session, TOTP 2FA, NUT, NFS, SFTP, Time Machine).

- **P0, credibility:**
  - [ ] Reproducible `tests/RESULTS.md`: QEMU harness with injected faults, and Playwright, in CI.
  - [x] Updates from the UI with an `@rootfs` snapshot, one-click rollback and a signed apt repo.
  - [ ] `.tangobak` configuration backup.
  - [x] Windows "Previous Versions" (`vfs_shadow_copy2`).
  - [ ] Trusted TLS: the project's domain and local CA. _Local CA done._
  - [x] 2FA in WebDAV and SFTP too: with 2FA on they accept only app passwords (and SMB uses an app password).
- **P1, differentiation:**
  - [ ] Guided 3-2-1 backup (restic/Kopia to USB, S3, B2 or another TangoOS) and `btrfs send` replication.
        _ZFS replication to another pool or another TangoOS done ([ADR 0016](adr/0016-zfs-usb-key-and-replication.md))._
  - [ ] Remote access: Tailscale/WireGuard in one click, and TangoConnect P2P with TURN.
        _TangoConnect done ([16](16-tangoconnect.md)). Missing: Tailscale/WireGuard._
  - [ ] Migration from another NAS: mount hybrid RAID/RAID read-only and browse it.
  - [ ] Jellyfin with QSV/VAAPI and automatic `/dev/dri`.
  - [ ] Native Btrfs RAID1 for two disks.
  - [ ] Quotas, LUKS2 and power profiles.
  - [ ] Larger catalog with an advanced compose mode.
- **P2, scope:**
  - [ ] `tango-desk` (Windows/Linux) and mobile app.
  - [ ] Share by link, file requests and resumable upload. _Share links done._
  - [ ] SHR-style mixed disks.
  - [ ] Hot spare and level migration. _Growing a RAID 5 (reshape) is done._
  - [ ] LDAP/AD and passkeys.
  - [ ] ARM64, English UI and a second theme. _English UI done ([ADR 0017](adr/0017-interface-languages.md)); the
        installer also speaks Portuguese and asks for the time zone._
- **P3, optional:**
  - [x] ZFS, SSD cache. _ZFS with USB key unlock and replication; iSCSI LUNs on a ZFS block volume or a pool LV._
  - [ ] PC backup with an agent.
  - [ ] AI assistant / MCP with confirmation and step-up auth.

## M5 — Ecosystem (started)

- [ ] Media app (Jellyfin + Intel QSV). _Jellyfin installs from the catalog, without QSV._
- [x] **TangoConnect** (remote access through signalhub + coturn), with share links.
- [ ] Snapshot replication. _ZFS volumes done (`zfs send`, resumable, encrypted volumes stay encrypted); Btrfs pending._
- [ ] Backup (restic).
- [ ] WebAuthn.
- [ ] ARM64 reference build.
- [ ] Second theme ("Arrabal").

## M6 — Advanced (not started)

- [ ] SHR-style hybrid volume (md slices + LVM).
- [ ] Optional ZFS (`tangoos-zfs`).
- [ ] SSD cache.
- [ ] Sync client (Drive).
- [ ] LDAP/AD.
- [ ] Per-user quotas. _The field and the UI bar exist; nothing is enforced yet._
