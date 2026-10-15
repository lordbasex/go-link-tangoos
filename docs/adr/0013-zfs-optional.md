# ADR 0013 — ZFS as an optional alternative to md + LVM + Btrfs

- Status: Accepted
- Context: users coming from other open NAS systems ask for ZFS (end-to-end checksums with
  self-healing, RAIDZ without a write hole, native encryption, L2ARC/SLOG, `zfs send`). OpenZFS is
  CDDL-licensed, which is incompatible with the kernel's GPL: Debian ships it in **contrib** as
  `zfs-dkms` (source compiled on each machine) + `zfsutils-linux`.
- Decision:
  1. **Not in the base package or the ISO.** `tangoos` never depends on ZFS. Storage offers an
     "Enable ZFS" button that explains the license and runs the agent op `zfs.enable` in a transient
     unit: add a `contrib` source derived from the existing Debian sources
     (`/etc/apt/sources.list.d/tangoos-contrib.sources`), install `linux-headers-$(uname -r)` and the
     flavor meta-package (`linux-headers-amd64` / `-cloud-amd64`, so DKMS rebuilds for every new
     kernel), then `zfs-dkms zfsutils-linux zfs-zed`, `modprobe zfs` and
     `/etc/modules-load.d/tangoos-zfs.conf`. Progress is estimated by time while DKMS compiles
     (~4–5 min on the lab VM).
  2. **No `tangoos-zfs` meta-package (for now).** It would not remove any step: contrib has to be
     enabled before any of its packages resolve, the headers must match the running kernel flavor
     (computed at runtime), and the DKMS progress and the license explanation belong in the UI. A
     meta-package can be added later if a channel needs it; the op stays the single path.
  3. **Same TangoOS layout on ZFS.** A pool is created with `ashift=12 autotrim=on` and a root dataset
     `compression=lz4 atime=off xattr=sa acltype=posixacl dnodesize=auto canmount=off mountpoint=none`.
     A TangoOS volume is the dataset `<pool>/<vol>` mounted at `/<vol>`; `<vol>/shares` and
     `<vol>/apps` are unmounted parents whose children inherit `/<vol>/@shares/<Folder>` and
     `/<vol>/@apps/<app>`. So Samba, permissions (POSIX ACLs), Files, apps and the snapshot scheduler
     see exactly the same paths as on Btrfs.
  4. **Snapshots** are ZFS snapshots of the folder dataset, named like Btrfs ones
     (`2026-10-15T02-00-04Z.hourly`). `/<vol>/@snapshots/<Folder>/<name>` is a symlink to
     `../../@shares/<Folder>/.zfs/snapshot/<name>`, so the snapshot list, retention, Files "versions"
     and Windows "Previous Versions" (`vfs_shadow_copy2` via `.gmt`) work unchanged.
  5. **Encryption is per volume** (as with LUKS, ADR 0011): `encryption=aes-256-gcm
     keyformat=passphrase`. The passphrase never leaves the agent: `zfs create -u` / `load-key` /
     `change-key` read it from stdin inside the agent; the transient unit only mounts. Children
     inherit the key. A locked volume is not mounted at boot; `storage.volumes.unlock|lock|passphrase`
     are shared with LUKS volumes.
  6. **Mount namespace.** The agent runs in its own mount namespace (ProtectSystem), so everything that
     mounts or unmounts (`zpool create/import/export/destroy`, `zfs create/mount/destroy`) runs in a
     transient unit (`tango-agent m4-helper zfs-*`) with re-validated typed arguments; reads
     (`zpool status/list -j`, `zfs list -j`) run in the agent. No arbitrary argv: `internal/zfs` builds
     every command from an allow-list.
  7. **Block volumes (zvol)** `<pool>/zvol/<name>` back iSCSI LUNs with an `iblock` backstore
     (replacing the "LUN on an LV" item: a zvol is the ZFS equivalent).
  8. **Disks in use**: a disk that the kernel holds open exclusively (an imported pool, md, dm) is not
     eligible (`diskops.Env.Eligible` tries `O_EXCL`), and the inventory marks pool members with
     `pool: "zfs:<name>"` and role `data | cache | log`.
- Consequences: ZFS and md/LVM/Btrfs coexist on the same NAS. The ISO stays small and GPL-clean.
  Kernel upgrades depend on DKMS rebuilding the module (the flavor headers meta-package makes that
  automatic). `zpool destroy/export` hang while LIO holds a zvol, so the API refuses them until the
  iSCSI target is deleted.
