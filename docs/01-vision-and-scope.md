# 01 — Vision and scope

## Mission

A NAS that feels like a polished desktop operating system: you open `https://tango.local`
(or the native app) and you are on a desktop with a Dock, windows and a top menu bar, where
everything updates in real time. Open source (MIT), no telemetry, no dependence on a proprietary cloud.

## Product principles

1. **Data first.** No destructive operation without a confirmation tied to the disk; nothing gets
   formatted "just in case".
2. **Real time by default.** There is no "refresh" button. Disks, jobs, connected users and
   metrics are pushed from the server.
3. **One desktop, many clients.** The same bundle runs in the browser, in the Windows app
   and in the Linux app.
4. **Real state or explicitly demo.** The UI never makes up values.
5. **Everything is auditable.** Every administrative action leaves an event with user, origin and result.
6. **Product-grade visual quality.** The bar is Mac-OS-Desktop / DSM: 60 fps animations,
   careful typography, useful empty states.

## Parity map with Synology DSM

The names are provisional and shown here in English; the UI itself is Spanish-only today (there is no
i18n framework yet). See [11](11-licenses-and-brand.md)
to avoid brand names.

| DSM equivalent | TangoOS app | MVP | Later |
|---|---|---|---|
| Storage Manager | **Storage** | disks, SMART, mdadm+Btrfs volume (single/RAID1), scrub | RAID5/6/10, SHR-style hybrid, SSD cache, online expansion |
| File Station | **Files** | browse, upload/download, rename, move, trash, preview | share by link, indexed search, compression |
| Control Panel | **Settings** | users, groups, shared folders, permissions, network, time, SMB | LDAP/AD, quotas, UPS, notifications, hardware/power |
| Snapshot Replication | **Snapshots** | scheduled snapshots, retention, file restore | `btrfs send` replication to another TangoOS |
| Hyper Backup | **Backup** | — | rsync/restic to USB, another NAS or S3 |
| Resource Monitor | **Monitor** | live CPU, RAM, network, disk I/O | history, per process/service |
| Log Center | **Log** | audit + filterable system logs | syslog forwarding |
| Package Center + Container Manager | **App Center** | — | catalog of container apps (Compose) |
| Security Advisor | **Security** | active sessions, 2FA, IP blocking | configuration scanner |
| QuickConnect | **TangoConnect** | — | remote access via signalhub + coturn |
| Synology Drive / Photos | — | — | App Center apps |
| iSCSI Manager | **iSCSI** (in Storage) | — | targets/LUNs with LIO, CHAP |
| Terminal (SSH) | **Terminal** | admin terminal over the `term` channel (xterm.js) | — |
| Notifications | **Notification Center** | live system events | email / ntfy / Telegram |

MVP network services: **SMB3** (with Time Machine support via `vfs_fruit`),
**NFS** (v4.2, `nfs-kernel-server`), **discovery** (Avahi mDNS + native WS-Discovery in
`tangod` so it shows up in Windows) and **SSH/SFTP** (SFTP with a per-user chroot). In M4: **iSCSI** (LIO /
`targetcli-fb`, LUN = file or zvol/LV), **WebDAV** and the rsync daemon.

## Audience

1. First: technical / homelab users who use DSM, TrueNAS or OMV today and want something nicer and more modern.
2. Later: small and medium businesses (AD/LDAP and backup are a requirement).

## Non-goals (for now)

- Universal ARM board support: x86-64 first; ARM64 with **one** reference board in M5.
- ZFS in the MVP. It comes in M6 as an option (`tangoos-zfs`, with `zfs-dkms` from `contrib`), because the CDDL license prevents putting it in the kernel and DKMS complicates kernel updates.
- Virtual machines, Kubernetes, clustering.
- Our own file system or SMB server.
- Telemetry.

## MVP success metric

Someone installs TangoOS on a mini-PC with 2 disks, creates a RAID1 volume, creates a shared
folder, mounts it from macOS and Windows, uploads files from the web desktop, deletes one
and recovers it from a snapshot. All without opening a terminal, and seeing the progress live
at every step.
