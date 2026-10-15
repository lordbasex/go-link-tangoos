# 08 — Base system, installer and updates

## Model: minimal Debian 13 + the TangoOS "face"

TangoOS **is not a new distro or an immutable image**. It is a real minimal Debian 13 "trixie",
with its own layer on top, in the same way that DSM is a Linux with layers:

| Layer | What it does | In DSM | In TangoOS |
|---|---|---|---|
| Kernel + base | boot, hardware, packages | own Linux | **minimal Debian 13** (kernel 6.12 LTS) |
| Storage | disks → redundant volumes | mdadm + LVM + Btrfs ("SHR") | mdadm (+ LVM for the hybrid) + Btrfs; optional ZFS ([04](04-storage.md)) |
| Network services | file sharing | Samba, NFS, AFP, iSCSI | Samba, NFS, WebDAV, SFTP, iSCSI (LIO) |
| Management daemon | turns clicks into system operations | proprietary | `tangod` + `tango-agent` (Go) |
| Interface | the desktop | web | web desktop + native apps over WebRTC |

Consequence: everything TangoOS adds is installed as **`.deb` packages** and updated with
**apt**. Debian keeps delivering the base system's security patches on its normal cycle.

## TangoOS packages

| Package | Contents |
|---|---|
| `tangoos` | `tangod`, `tango-agent` and their systemd units (`build/nfpm.yaml`). Depends on `mdadm btrfs-progs passwd adduser`, recommends `avahi-daemon samba`. **This is the only package that exists today.** |
| `tangoos-system` (planned) | metapackage with the dependencies: `mdadm lvm2 btrfs-progs cryptsetup samba nfs-kernel-server targetcli-fb openssh-server avahi-daemon wsdd2 smartmontools nftables chrony nut-client libvips-tools ffmpegthumbnailer` |
| `tangoos-branding` (planned) | GRUB/Plymouth, `motd`, extended `/etc/os-release` (`VARIANT=TangoOS`), console with the NAS URL/QR |
| Docker (on demand) | Debian's `docker.io` + `docker-compose`; installed from the App Center, not included by default |
| `tangoos-zfs` (optional, M6) | `zfs-dkms` + `zfsutils-linux` from `contrib` |

- They are built with **nFPM** (like go-link). They are published in **our own apt repository**
  (`https://tangoos.go-link.org/apt`, suite `stable`, see below) signed with a GPG key kept outside the
  repo. That way everything falls under apt's security policy (`signed-by=`).
- The Debian config files that TangoOS manages (`smb.conf`, `exports`)
  **are not overwritten**: they are generated in `*.d/` or via `include`, and declared with `dpkg-divert` when there
  is no other way. The rule is that a Debian `apt upgrade` must never break the TangoOS layer.

## Installation: two paths

1. **On an already installed Debian 13: script installation** (people who already have Debian, VPS, testing):
   ```bash
   curl -fsSL https://tangoos.go-link.org/install.sh | sh
   ```
   [`install.sh`](../web/apps/connect/public/install.sh) (published with the portal) checks for Debian 13
   amd64 with systemd and that `:80`/`:443` are free (`TANGOOS_FORCE=1` to continue anyway), adds the
   **signed APT repository** `https://tangoos.go-link.org/apt` (suite `stable`, deb822 source in
   `/etc/apt/sources.list.d/tangoos.sources` with `Signed-By`) and installs `tangoos` + Samba, NFS (off
   until enabled in Settings, as in the ISO) and Avahi. At the end it shows `https://<ip>/` and where the
   initial admin password is (`/var/lib/tango/initial-admin-password`, 0600; it does not print it).
   - The repo's public key (`TangoOS Archive`, fingerprint `B343 1243 8C28 49B6 83A0  5319 9C06 A62D C9C4 DB37`)
     is **embedded in the script** (not downloaded separately) and in `build/keys/tangoos-archive-keyring.gpg`;
     a test (`internal/aptrepo`) checks that they are the same. The private key lives outside the repo, in
     `~/.config/tangoos/apt-gnupg` (back it up together with the desktop release key).
   - TangoOS **updates** arrive through that repo with the usual mechanism (Settings →
     Updates, `system.updates.*`).
   - **Difference from the ISO:** it uses Debian as it is (ext4 or something else): **there is no `@rootfs`
     snapshot and no system rollback** before each update. The script warns about this.
   - Publishing a version: `make deb apt-repo` (copies the `.deb` into the `dist/apt` pool, regenerates
     `Packages`/`Release` and signs `InRelease`/`Release.gpg` with [`cmd/tango-aptrepo`](../cmd/tango-aptrepo))
     and `make apt-publish` ([`scripts/publish-apt.sh`](../scripts/publish-apt.sh): verifies the signature with
     `gpgv`, uploads the pool as immutable and the indexes without cache, invalidates CloudFront). A published
     version cannot be replaced with different content: the number has to be bumped.
   - **CloudFront** answers `index.html` (200) for any nonexistent path (the portal fallback).
     That is why `Release` declares `Acquire-By-Hash: no` and lists only `Packages` and `Packages.gz`: apt does not
     request anything else (no `Translation-*`, `by-hash` or `Contents`), verified with the server log.
     `scripts/deploy-connect-web.sh` excludes `apt/*` from its `--delete` and uploads `install.sh` as
     `text/plain`.
2. **TangoOS ISO** (end user): **image installation**, implemented in
   [`system/iso/`](../system/iso/README.md). The ISO carries a ready-made minimal Debian 13
   (debootstrap + `tangoos.deb`, compressed in squashfs); the installer copies it to the chosen disk.
   - **It does not need internet** to install. Afterwards, apt updates from `deb.debian.org`.
   - The image ships **TangoOS with all its dependencies preinstalled** (mdadm, btrfs-progs,
     Samba, nfs-kernel-server, passwd, Avahi) and **the apt lists**: `apt-get install` works
     without a prior `apt-get update`. Docker is not included: it is installed on demand (ADR 0010).
   - Minimal `smb.conf`: it only publishes the TangoOS folders (no `[homes]` or printers), SMB2
     as the minimum and `vfs_fruit` for Mac. NFS is installed but **off** until it is enabled
     in Settings.
   - **Graphical installer by default** (see below); the **text mode** (whiptail) remains as the second
     boot menu entry. Both use the same engine (`tango-install`).
   - Questions: **language and keyboard**, **system disk** (with an erase confirmation that shows
     model, serial and size), **computer name** and **network** (DHCP or static IP). The time zone is fixed to Buenos Aires;
     it is changed later from the desktop. The other disks **are not touched**.
   - When it finishes, the console shows `https://<name>.local` and `https://<ip>`. The rest (admin,
     name, certificate, first volume) goes in the web desktop's **first-boot wizard**.
   - If the USB/CD is left in, the ISO menu detects the installed TangoOS and boots it by
     default, so it does not reinstall in a loop.

   **Measured times** (Proxmox VM, 2 vCPU, 2 GB RAM, 16 GB disk), from when the VM
   boots the ISO until `https://<ip>/` serves the desktop:

   | ISO | Firmware | Total time | Copy + GRUB |
   |---|---|---|---|
   | Image with dependencies (lab, 388 MB, 2026-10-14 23:10) | UEFI (OVMF) | **90 s** (2 runs) | — |
   | Image without dependencies (lab, 328 MB, previous version) | UEFI (OVMF) | 68–76 s | 30–33 s |
   | Image without dependencies (previous version) | BIOS (SeaBIOS) | 59 s | 30 s |
   | Image without dependencies (product, 2 keys, previous version) | UEFI | ~80 s of machine time | 33 s |
   | netinst + preseed (downloads from internet) | UEFI | 422 s (7 min) | — |

   | Image, graphical installer (product, 533 MB, 2026-10-15) | UEFI | ~41 s until the installer shows; installation **33–35 s**; reboot → HTTPS 17–26 s | 33–35 s |
   | Image, unattended lab (533 MB, 2026-10-15, VM with 2 disks) | UEFI | **83–89 s** | 32 s |
   | Image, interactive text mode (product, 2026-10-15) | UEFI | installation 47 s with the keystrokes; reboot → HTTPS 21 s | — |

   The netinst + preseed path (`make iso-netinst`) remains as an alternative for hardware that the
   image's kernel does not support, or for anyone who wants the official Debian installer.

### Graphical installer

Like Synology's *Web Assistant*, but also on the machine's own screen:

```
GRUB ─ "Instalar TangoOS" (Install TangoOS, default) ──► live + installer.squashfs layer
       │                                   ├─ tango-installer.service  Go, HTTP :80 (internal/installer)
       │                                   └─ tango-kiosk.service      cage + cog (WPE) on tty1 → http://127.0.0.1/
       │                                         no graphical display → console with URL + code
       ├─ "Instalar TangoOS en modo texto" (text mode) ──► tango-installer-text.service (whiptail)
       ├─ "Probar TangoOS sin instalar" (try without installing)
       └─ "Arrancar el TangoOS instalado" (boot the installed TangoOS; default if present)
                       both modes ──► tango-install (single engine: partitions, copy, GRUB)
```

- **A single installation logic**: `tango-install` is the engine. Text mode uses it interactively;
  the graphical interface calls it with parameters (`--disk … --yes --hostname --keyboard --ip --progress`)
  and reads `@@ <pct> <text>` lines. The Go server only validates (disk from the engine's list, name,
  keyboard from a list, IPv4/netmask/gateway on the same network) and passes arguments without a shell.
- **Two squashfs layers** stacked by live-boot (`live/filesystem.module`): `filesystem.squashfs` is
  the system that is copied to the disk; `installer.squashfs` (cage, cog, the server, the units, the live
  system's DHCP and a drop-in that does not start `tangod` with `tango.installer`) **never reaches the disk**:
  verified over SSH on systems installed both ways.
- **From another PC**: the local screen shows `http://<ip>/` and a **6-digit code**. The LAN
  needs that code (`HttpOnly`, `SameSite=Strict` cookie, 12 h); 5 failures lock for 30 s and the
  lock doubles. The local screen (127.0.0.1) does not ask for it. POSTs require
  `X-Tango-Installer: 1` + JSON (no CSRF) and there is a strict CSP. The server only exists in the live system:
  after installing, nothing is left listening.
- **Kiosk browser**: measured as a layer on top of the rootfs (squashfs zstd‑15): **cog 158 MB**,
  firefox-esr 192 MB, chromium 247 MB. cog was chosen (WPE WebKit, designed for kiosks). The ISO went
  from 388 MB to **533 MB**; the bulk is libLLVM + gallium (software rendering without a GPU) and WebKit.
- cog quirks the interface accounts for: it does not generate `click` from
  `pointerdown/up` (the page synthesizes it if it does not arrive), its native `<select>` list keeps
  the input (the interface does not use `<select>`), it starts at 1024×768 before taking over the screen (it repaints
  everything on load) and it does not report a useful language (the server sends the local screen's language, `es`).
- Lab: `tango.auto tango.disk=TANGO-SYS` (Makefile `ISO_TEST_DISK`) installs without questions
  even if there are several disks; the lab ISO also enables SSH **in the live system**
  (`tango-live-ssh.service`, only if the media carries `authorized_keys`).

### Dedicated system disk

```
bios_grub 1M | ESP 512M (vfat) | /boot 1G (ext4, label tangoos-boot) | root (rest, Btrfs, compress=zstd:1)
                                      ├─ @rootfs      → /
                                      ├─ @var-log     → /var/log      (outside the root snapshots)
                                      ├─ @tango       → /var/lib/tango (state: JSON files, audit log)
                                      └─ @snapshots   → /.snapshots: root snapshots before each update
```

A separate disk is used (SSD/NVMe/good-quality USB), which is simpler and safer than the Synology model of
replicating the system on every data disk. The Synology model (md RAID1 of the system across
all bays) remains an option to evaluate.

## Updates

- **From the UI** (Settings app → Updates): `tangod` calls `system.updates.check` /
  `system.updates.apply {scope: "all"|"security"}`, and the agent runs `apt-get` as a transient unit
  (`tango-agent m4-helper updates-apply`). Debian's and TangoOS's packages are shown separately, and the
  progress and log are shown live, as a job.
- **Before applying** (only when `/` is on Btrfs, i.e. installed from the ISO): a read-only snapshot
  `/.snapshots/rootfs-<date>` of `@rootfs` plus a copy of `/boot` (`rootfs-<date>.boot`, since `/boot` is a
  separate ext4).
- **Rollback** (Settings → Updates, `system.rollback.list` / `system.rollback.apply`, typed confirmation):
  the agent mounts the Btrfs top level, renames `@rootfs` to `@rootfs-reemplazado-<date>` ("replaced"),
  creates a new writable `@rootfs` from the chosen snapshot, restores `/boot` and reboots. fstab and GRUB
  always point at `subvol=@rootfs`, so they boot the previous system. There is **no grub-btrfs** and no
  GRUB entry per snapshot; rolling back is done from the UI. On a system without a Btrfs root (script
  installation) `system.rollback.list` answers `NOT_SUPPORTED`.
- **Not implemented yet:** automatic rollback when `tangod` does not come back healthy after an update;
  automatic Debian security patches (`unattended-upgrades`, only `trixie-security`, configurable).
  Today security patches are applied from the UI with the "security" scope. TangoOS versions are never
  installed automatically.
- State migrations: `tangod` keeps its state in JSON files (no SQLite); there is no migration framework yet.
- Debian major version upgrade (trixie → forky): guided procedure, only once TangoOS
  has validated it. Never automatic.

## Hardening the base Debian

- No graphical server, no recommended packages (`APT::Install-Recommends "0"`): done in the ISO image.
- SSH disabled by default in the `product` ISO (`ssh.service` disabled); the lab (`test`) variant enables
  root SSH with the development key. Planned: when enabled from the UI, key-only and without root.
- Time: `systemd-timesyncd` in the image (`chrony` was the plan; relevant for TOTP and TLS).
- Planned, not implemented: nftables by default (only the local subnet for 443, 8443, 3478, 445, 2049,
  3260), a `systemd-journald` size limit, and AppArmor profiles for `tangod` and the file-worker.

## Recovery mode

**Not implemented.** Today the installed system only has the rescue entries that Debian's `update-grub`
generates (Advanced options → "recovery mode"). The plan: a GRUB entry boots the **previous snapshot** or
the root in `systemd.unit=tango-rescue.target`: `tangod` starts in recovery mode and the data volumes are
mounted **read-only**. The same web desktop exposes Storage (inspection), Log, Terminal and "Restore
configuration". It never formats. It exports a diagnostic report without secrets.

## Configuration backup

**Not implemented.** The plan: `system.config.export` generates a `.tangobak`: the state in
`/var/lib/tango` + templates, encrypted with a password (age). Import validates the version and schema, and
shows a diff before applying.
