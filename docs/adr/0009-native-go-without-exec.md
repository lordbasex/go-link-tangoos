# ADR 0009 — 100% native Go: syscalls, ioctl, netlink and D-Bus before `exec`

- Status: Accepted
- Context: the backend is 100% Go and the only piece of software installed is our `.deb`.
  There is no nginx, Node, Python, or any other web server. We want to avoid
  `exec.Command` and shell wrappers: they are fragile (they parse text, depend on the version and the
  locale), slow, and widen the attack surface.
- Decision: **priority rule** for every system operation:
  1. **Kernel directly**: syscalls, `ioctl`, sysfs/procfs, netlink (`golang.org/x/sys/unix`).
  2. **D-Bus** to services that are already running (systemd, logind; udisks no: not used).
  3. **Pure Go library** that implements the format or protocol.
  4. **`exec` as a last resort**: only allow-listed binaries, with arguments built in
     code (never a shell string), JSON output when the tool supports it
     (`--json`), and always inside `tango-agent`.
  Every use of `exec` is recorded in the table below and has a ticket to replace it.
- Consequences: more code of our own and integration tests in a VM. In return, typed errors,
  zero text parsing on critical paths, and a self-contained binary. HTTPS, signaling,
  STUN/TURN and WebRTC are already pure Go inside `tangod`.

## Map by area

| Area | Native in Go | How | Remaining `exec` |
|---|---|---|---|
| Web, signaling, STUN/TURN, WebRTC | ✅ | `net/http` + `embed.FS`, `pion/webrtc`, `pion/turn` | — |
| Disk inventory | ✅ | sysfs (`/sys/block/*`), `/dev/disk/by-id`, udev events over netlink (`NETLINK_KOBJECT_UEVENT`) | — |
| SMART | ✅ | `ioctl` SG_IO (ATA PASS-THROUGH) and NVMe admin (`NVME_IOCTL_ADMIN_CMD`); reference library `github.com/anatol/smart.go` | `smartctl --json` only as a *fallback* for unusual controllers |
| GPT partitioning | ✅ | `github.com/diskfs/go-diskfs` + `BLKRRPART` | — |
| md (RAID) status | ✅ | sysfs `/sys/block/mdX/md/*` (state, `sync_action`, `degraded`, progress) + `/proc/mdstat` | — |
| Create / assemble md | 🟡 phase 2 | ioctl `SET_ARRAY_INFO` / `ADD_NEW_DISK` / `RUN_ARRAY` + writing the v1.2 superblock in Go | `mdadm --create/--assemble/--manage` in the MVP (critical format: replaced once there are compatibility tests against mdadm) |
| Trigger md scrub/check | ✅ | write `check` to `/sys/block/mdX/md/sync_action` | — |
| Btrfs: subvolumes, snapshots, quotas, scrub, device stats, info | ✅ | `BTRFS_IOC_*` ioctls (reference lib `github.com/dennwc/btrfs`) | — |
| Btrfs: `mkfs` | ❌ | There is no reliable Go implementation of the format | `mkfs.btrfs` (allow-list) |
| Mount / unmount | ✅ | `unix.Mount` / `unix.Unmount`, `.mount` units via D-Bus so they persist | — |
| LUKS2 | 🟡 | open: `dm-crypt` via device-mapper ioctl | `cryptsetup luksFormat` (M4) |
| LVM (SHR-style hybrid, M6) | 🟡 | device-mapper via ioctl to read/activate | `lvm` to create (to be evaluated) |
| Services (Samba, NFS, etc.) | ✅ | systemd over **D-Bus** (`github.com/coreos/go-systemd/v22/dbus`): start/stop/reload/enable | — |
| Samba config | ✅ | generate `smb.conf.d/` from templates + `reload` over D-Bus | `testparm` to validate (evaluate our own validator) |
| Samba users | ❌ | the `passdb.tdb` database is TDB | `pdbedit` / `smbpasswd` (allow-list) |
| NFS exports | ✅ | write `/etc/exports.d/tango.exports` + reload over D-Bus | — |
| Network (IP, routes, links, VLAN, bonding) | ✅ | netlink (`github.com/vishvananda/netlink`) | — |
| Firewall | ✅ | nftables over netlink (`github.com/google/nftables`) | — |
| Hostname, time, NTP, time zone | ✅ | `org.freedesktop.hostname1` / `timedate1` over D-Bus | — |
| Unix users | 🟡 | atomic editing of `/etc/passwd`, `shadow` and `group` with a lock (`lckpwdf` semantics) | `useradd` in the MVP |
| Metrics (CPU, RAM, network, I/O, temperatures) | ✅ | procfs, sysfs, hwmon | — |
| System logs | ✅ | reading the journal (`sdjournal`) or the journald socket | — |
| Shut down / reboot | ✅ | `org.freedesktop.login1` over D-Bus | — |
| Packages / updates (apt) | ❌ | — | `apt-get` (allow-list, inside a job with a log) |
| Containers (M3.5) | ✅ | Docker Engine API over a Unix socket (local REST), via the agent | — |
| Root snapshot before the update | ✅ | Btrfs ioctl | — |

Legend: ✅ native from the MVP · 🟡 native in a later phase · ❌ `exec` with no immediate plan.

## External services that do run (and why)

Only those that **implement network protocols for clients** and that make no sense to rewrite:

- **Samba** (`smbd`): SMB3 for Windows and macOS. There is no production-ready SMB server in Go.
- **nfsd**: it lives in the kernel; we only configure it.
- **sshd** (optional, off by default).
- **avahi-daemon**: mDNS. To be evaluated: replacing it with mDNS in Go inside `tangod`
  (`github.com/pion/mdns` is already in the dependency tree because of WebRTC).
- **wsdd2**: WS-Discovery for Windows. To be evaluated: our own Go implementation (it is a small protocol).

The interface, management and real time: **only `tangod` + `tango-agent`**.
