# 04 — Storage

## Decision: mdadm + Btrfs (the commercial NAS model)

Commercial NAS systems do not use Btrfs RAID. They use `mdadm` for redundancy, optionally LVM, and
**single-device** Btrfs on top, to get snapshots, subvolumes, checksums and compression.
TangoOS does the same:

```
 disk A ─┐
 disk B ─┼─ /dev/md/tango-vol1 (RAID1/5/6/10, mdadm, metadata 1.2)
 disk C ─┘        │
              [LVM + optional LUKS2: storage pools, ADR 0011 and below]
                   │
              Btrfs (single, metadata DUP) ── /volume1
                   ├─ @shares/Familia      (subvolume = shared folder)
                   ├─ @shares/Fotos
                   ├─ @apps/…              (container data)
                   └─ @snapshots/…         (read-only, scheduled)
```

**Why not Btrfs RAID5/6:** the *write hole* problem and the risks on power loss are still
documented upstream. Btrfs RAID1/RAID1c3 are reliable, but mdadm offers a full range of
profiles with a single tool and a well-known recovery story.

**Honest limitation:** with single Btrfs on top of md, the checksum *detects* corruption
but cannot *repair* it from another copy (md does not expose which mirror is good). Some commercial
NAS systems patch this in the kernel; we do not. Mitigations: scheduled md (`check`) and Btrfs
scrubs, clear alerts, and the "native Btrfs RAID1" mode as an advanced option for
2-disk volumes (ADR 0004). Today: the scrub (md `check` + Btrfs scrub) is started by hand ("Verify now"),
there is no scheduled scrub yet, and native Btrfs RAID1 is not implemented.

## Supported profiles by milestone

| Profile | MVP (M2) | Later |
|---|---|---|
| Basic (1 disk) | ✅ | |
| RAID1 (2 disks) | ✅ | |
| RAID5 / RAID6 / RAID10 | | ✅ done in M2.1 (see below) |
| Storage pools (LVM2): several volumes per pool, Btrfs/XFS/ext4, online grow | | ✅ done (ADR 0011, "Storage pools" below) |
| Hybrid RAID, disks of different sizes (`hybrid1` / `hybrid2`) | | ✅ done: GPT partitions in equal-size tiers, one md per tier, joined with LVM; grows one disk at a time |
| SSD cache | | ✅ done: lvmcache (`dm-cache`) per volume, read (writethrough, 1–2 SSDs) or read/write (writeback, 2 SSDs in RAID 1) |
| Volume encryption (LUKS2) | | ✅ done: per volume in a pool, passphrase or key file on a removable device, unlocked by itself at boot when the key is plugged in |
| iSCSI (LUN on a Btrfs file with `nodatacow`, or a block volume) | | M4. ✅ LUN on a Btrfs file (LIO over configfs); ✅ LUN on a ZFS block volume (zvol, `iblock`) |
| ZFS (pool as an alternative to md+LVM+Btrfs) | | ✅ done (ADR 0013): enabled on demand from Storage (contrib + DKMS, CDDL explained); stripe/mirror/RAIDZ1–3, volumes as datasets with quotas and native encryption, L2ARC/SLOG, scheduled scrub, replace with resilver, add vdev, import (read-only first)/export/destroy, zvols for iSCSI |

## Disk lifecycle

`detected → inspected → eligible → confirmed → in operation → verified`

- **Identity:** WWN → serial+model → `by-id`. If there is no stable identity, the disk is
  **not eligible** and can only be read.
- **Not eligible if:** it is the system disk, has something mounted, is a member of an active md,
  has an unknown signature that the user has not explicitly acknowledged, or is USB (USB disks
  are only used as backup destinations).

## Confirmation token (destructive operations)

1. `storage.volumes.plan {disks:[wwn…], profile:"raid1"}` → returns the plan (what gets erased,
   resulting capacity) and a `confirm_token` = HMAC(session key, op + wwns + sizes +
   current signatures + 5-minute expiry).
2. The UI shows the model, serial, size and detected contents of each disk, and asks the user
   to type the **volume name**.
3. `storage.volumes.create {plan_id, confirm_token}`. The agent **recomputes** the signatures and
   aborts if anything changed (`DEVICE_CHANGED`).

## Snapshots

- Read-only, in `@snapshots/<share>/<ISO8601>`. Policies (hourly, daily or weekly +
  retention) are stored in `/var/lib/tango/snapshot-schedules.json` and run by a `tangod` scheduler
  (`internal/snapsched`) through the agent.
- **Restoring a file:** the file-worker mounts the snapshot read-only and copies with
  reflink (instant). **Restoring an entire shared folder:** a snapshot of the
  current state is created and the subvolume is replaced.
- Previous versions can be seen from Windows (`vfs_shadow_copy2` in Samba) and from the Files
  app ("Previous versions"). Samba reads `/<vol>/@snapshots/<Folder>/.gmt`: one symlink per
  snapshot with the exact name of the format (scheduled ones carry a level suffix and Samba
  would not open them). The agent keeps it up to date when creating/deleting snapshots and when applying permissions.
- Replication (M5): incremental `btrfs send | receive` over a DataChannel between two TangoOS systems.

## Quotas

**Not implemented yet** (planned design). **Simple quotas** are used (`btrfs quota enable --simple`, kernel ≥ 6.7; Debian 13 ships
6.12), because classic qgroups degrade performance with many snapshots. There are quotas
per shared folder and per user (via a home subvolume).

## Health

- SMART with `smartctl --json`, polled every 30 min and when an I/O error is detected.
- md status (`/proc/mdstat` + `mdadm --monitor` events) and Btrfs errors
  (`btrfs device stats`) are pushed as events.
- Monthly scheduled scrub (md `check` + `btrfs scrub`), with live progress. **Today the scrub is
  manual only** (`storage.scrub.start`); the schedule is pending.
- Today: SMART is read through the agent with a 1-minute cache, and `tangod` evaluates md, Btrfs,
  space and SMART alerts every 10 s (`internal/monitor`).

## Test battery: QEMU virtual disks

Failure scenarios covered, including md-specific ones:

- empty disk / with ext4 / with existing Btrfs / with mounted partitions
- the system disk is picked by mistake
- the disk is removed before confirming and during the operation
- power loss during array creation and during a rebuild
- degrade RAID1 → replace disk → rebuild → verify the data checksums
- snapshot → delete file → restore → compare sha256
- the state in `/var/lib/tango` is lost but the filesystem survives → reconciliation recovers the shares

The harness uses `qemu-system-x86_64` with *throwaway* `qcow2` disks and `virtio-scsi` (to
get stable serials), and runs in CI. **Planned:** today there is no QEMU harness or CI; the storage
matrix runs against the lab VM with `make e2e-storage` (`tests/e2e/storage_matrix_test.go`, docs/14).

## M2 implementation (what the code does today)

- **Who does what:** `tangod` builds the plan (the agent's `disk.inspect`: fingerprint = SHA-256 of
  size + first 128 KiB + last MiB; contents detected by signatures in Go) and signs the
  token. `tango-agent` revalidates right before writing: whole disk by by-id, not the
  system disk (compared against `/proc/1/mountinfo`), no mounts or holders, opened with
  `O_EXCL` and with the same fingerprint. Only then does it wipe signatures (in Go: first/last MiB and
  the Btrfs superblock copies).
- **RAID 1:** `mdadm --create … --metadata=1.2 --homehost=any --bitmap=internal --run` (without
  `--assume-clean`): the name ends up as `any:<volume>` and the array is assembled as
  `/dev/md/<volume>` on any host, including in the initramfs (which does not know the hostname)
  and on another NAS the disks are moved to. The `ARRAY` line is added to `mdadm.conf`.
- **Btrfs:** `mkfs.btrfs -m dup -d single`; the UUID is read from the superblock in Go.
- **Mounting:** a `/etc/systemd/system/<volume>.mount` unit (by `UUID=`) enabled and
  started over D-Bus; it is in `multi-user.target` (a broken data disk does not prevent
  booting) and **without** `After=local-fs.target` (it created an ordering cycle and systemd dropped
  the mount from the boot).
- **Subvolumes, snapshots and scrub:** Btrfs ioctls from the agent. The scrub first runs an md
  `check` (sysfs) and then `BTRFS_IOC_SCRUB`, with live progress.
- **Samba:** one section per folder in `/etc/samba/tango.d/`, a `tango.conf` index, a single
  `include` line in `smb.conf`; it is validated with `testparm` (reverted if it fails) and
  `smbd` is reloaded over D-Bus.
- **Tested in the VM** (TANGO-D1 + D2): create RAID 1, SMB folder, read-only snapshot,
  scrub with no errors, and reboot with automatic assembly and mounting.
- **Pending at the time of M2:** user permissions on folders, sizes via quotas, snapshot scheduler,
  deleting volumes/folders, RAID 5/6/10, disk replacement and rebuild. All of these except quotas were
  done later (M2.1 and M2.2, below).

## M2.1 status (implemented and tested in VM 108)

- **Profiles:** Basic, RAID 1, RAID 5 (≥3), RAID 6 (≥4), RAID 10 (≥4, even), with mdadm
  `--metadata=1.2 --homehost=any --bitmap=internal`, never `--assume-clean`. With disks of
  different sizes md uses the size of the smallest one and the plan warns about it in `notes`.
- **Deleting a volume** (plan + token + typed name): unpublishes its Samba folders,
  stops and deletes the `.mount` unit, `mdadm --stop`, `--zero-superblock` on each member,
  signature wipe in Go, and removes the line from `mdadm.conf`. Before touching anything the agent
  checks that the disks are exactly the members of the array.
- **Deleting a folder:** snapshots and subvolume with `BTRFS_IOC_SNAP_DESTROY`, and its
  Samba section.
- **Users and permissions:** a Unix user without login (UID ≥ 2000, `tango-users`) + a Samba account
  per user; `tg-<folder>-rw/-ro` groups; POSIX ACL written as an xattr (without `setfacl`).
  `useradd`/`groupadd`/`gpasswd`/`smbpasswd` through an allow-list (ADR 0009: editing
  `/etc/shadow` by hand in Go is more risk than benefit). That is why the agent's unit moved
  to `ProtectSystem=yes` (`/etc` writable) and adds `CAP_FOWNER`/`CAP_FSETID`.
- Pending at the time of M2.1: guided disk replacement/rebuild, expansion and the snapshot scheduler
  (all done in M2.2, below), quotas (still pending) and the execute bit on files created over SMB (the
  share now sets `create mask = 0660`, `directory mask = 0770`).

## Maintenance (M2.2, implemented)

- **Replacing a disk:** Storage → failed disk → "Replace". Uses plan + token + typed name, same as create (protocol/README.md §11). md rebuilds in the background; the volume shows `task: rebuild` with speed and time remaining, and the redundancy alert clears by itself when it finishes.
- **Expanding:** RAID 5/6/10 only. Disks are added and md reorganizes (`reshape`, it can take hours); when it finishes, Btrfs grows by itself. A RAID 1 is grown by replacing its disks with larger ones.
- **Scheduled snapshots:** each folder has hourly ×24, daily ×14 and weekly ×8 by default. Manual ones are never deleted by retention.
- **Alerts:** no redundancy, read-only, Btrfs errors, low space and SMART. They resolve by themselves when the problem goes away.

## Not implemented yet

Quotas on Btrfs volumes, native Btrfs RAID1 (ADR 0004), scheduled scrub of md/Btrfs volumes and replication
(`btrfs send` / `zfs send`). Hybrid RAID, SSD cache and LUKS2 are in "Storage pools"; ZFS in "ZFS" below.

## Storage pools (ADR 0011): what the code does (implemented and tested in VM 108)

- **Model:** disks → md (one array, or one per tier in hybrid RAID) → LVM PV → VG = storage pool → LV = volume →
  [LUKS2] → Btrfs (default), XFS or ext4. The classic md + Btrfs volumes keep working and are listed as such;
  from the UI, "Create volume" now creates inside a pool (the classic one is in the window menu).
- **Names and tags:** the VG has the pool name and the tags `tangoos`, `tg.profile=<profile>`; its md arrays are
  `<pool>_md<N>` (tier N) and `<pool>_cache<N>`; PVs are tagged `tg.role=data|cache`; LVs carry `tg.fs=`, `tg.enc=luks2`,
  `tg.key=<FS UUID of the key device>`, `tg.slot=<LUKS slot>`, `tg.cache=read|readwrite`. Everything is on disk:
  losing `/var/lib/tango` loses nothing.
- **Hybrid RAID:** `internal/diskops/hybrid.go` computes the tiers (same rules in `web/packages/client/src/protocol-pools.ts`
  for the wizard), `gpt.go` writes the GPT in Go (partition names `tg-<pool>-t<N>`, type Linux RAID). Before writing the
  table, the zones of the future partitions are zeroed: otherwise udev reassembles an old md the moment the kernel publishes a
  partition (found in the lab). Growing: the new disk gets partitions for every tier; 1 → 2 members creates a RAID 1, 2 → 3
  converts RAID 1 → RAID 5 (`--grow --level=5`) and reshapes, ≥ 3 adds and reshapes; then `pvresize` or `vgextend`.
  Replacing a disk fails/removes its partition in each md, partitions the new one the same way and adds it.
- **Volumes:** `lvcreate` only on the data PVs (never on the cache SSDs), `mkfs`, `.mount` unit by filesystem UUID with
  its `Type=`; Btrfs volumes get `@shares`, `@snapshots`, `@apps`. Grow online: `lvextend` + `cryptsetup resize` +
  Btrfs resize ioctl / `xfs_growfs` / `resize2fs`. Shrinking is not offered. Deleting a pool volume (`storage.volumes.delete`)
  unpublishes its folders, unmounts, closes LUKS, drops the cache and does `lvremove`: the disks stay in the pool.
- **LUKS2:** `luksFormat --type luks2 --pbkdf argon2id`, opened as `/dev/mapper/tgc_<volume>` with `--disable-keyring`
  (the volume key stays in the device-mapper table, readable only by root, so `cryptsetup resize` does not ask for the
  passphrase again). Secrets go through stdin or an inherited pipe (`/dev/fd/3`), never argv or a temporary file; the agent
  journals `lv.create` with the passphrase redacted; tangod never stores it and the audit log skips `passphrase` keys.
  5 failed unlocks in 10 minutes → `RATE_LIMITED`. A locked volume keeps its `.mount` unit installed but not enabled.
- **Key device:** any vfat, exFAT or ext4 filesystem outside the system disk, RAID and LVM, not mounted
  (`storage.keyDevices.list`). The key is 4 KiB from `crypto/rand` in `tangoos-keys/<LUKS UUID>.key`, added as another
  LUKS slot. The agent mounts the device with a transient systemd `.mount` (`/run/tangoos-key`) only while it reads or
  writes the file: its seccomp filter has no `mount(2)`. `tangod` tries once per volume and key, every 10 s, to unlock
  locked volumes whose key device is present (boot with the key plugged in → unlocked by itself).
- **SSD cache:** `lvcreate` a cache volume on the SSD PV (or on the RAID 1 of two SSDs) and `lvconvert --type cache
  --cachevol … --cachemode writethrough|writeback`. Removing it is `lvconvert --splitcache` (flushes dirty blocks) +
  `lvremove` + `vgreduce` + `pvremove` + stop the md + wipe. Hit ratio and dirty blocks come from `lvs` (`cache_*` fields)
  and are shown in the volume and the Storage widget. Growing a cached volume works with LVM 2.03.31 (tested).
- **What tangod sees:** `pool.report` from the agent (LVM is exec + root), cached 3 s and invalidated after each operation,
  joined with sysfs for md state. md arrays that belong to a pool are not listed as volumes; pool disks have `pool` and
  `role`. Monitoring raises one redundancy alert per pool (`md:<pool id>:degraded`) and the space alerts per volume.
- **Packages:** `lvm2`, `cryptsetup-bin`, `thin-provisioning-tools` (`cache_check`) are dependencies, `xfsprogs` recommended;
  `/usr/lib/modules-load.d/tangoos.conf` loads `dm_crypt`, `dm_cache`, `dm_cache_smq` at boot (the agent runs with
  `ProtectKernelModules=yes`, so LVM cannot `modprobe` them; found in the lab).
- **iSCSI on an LV:** a target can live in a logical volume `il_<name>` of a storage pool (thick, or thin in the pool's
  `il_thin` thin pool), exported with an `iblock` backstore; the LV is created with the target and removed with it.
- **Limits today:** hybrid growth requires the new
  disk to be at least as large as the largest one; one SSD cache per volume; the key file stays on the device when the key
  slot is removed (it no longer opens anything).

## ZFS (ADR 0013): what the code does (implemented and tested in VM 108)

- **Enable:** Storage → Pools ZFS → "Enable ZFS" (`storage.zfs.enable`). The agent adds a `contrib` source derived from
  the Debian sources, installs `linux-headers-$(uname -r)` + the flavor meta-package, `zfs-dkms zfsutils-linux zfs-zed`,
  loads the module and writes `/etc/modules-load.d/tangoos-zfs.conf`. Lab VM (4 vCPU): 47 s of headers + 228 s of
  install/DKMS. OpenZFS 2.3.9 (Debian 13).
- **Layout:** pool root `compression=lz4 atime=off xattr=sa acltype=posixacl dnodesize=auto`, not mounted; volume =
  `<pool>/<vol>` at `/<vol>`; folders = `<pool>/<vol>/shares/<Folder>` at `/<vol>/@shares/<Folder>`; apps under
  `<vol>/apps`; block volumes `<pool>/zvol/<name>` (`/dev/zvol/<pool>/zvol/<name>`).
- **Pools:** stripe, mirror (≥ 2), RAIDZ1 (≥ 3), RAIDZ2 (≥ 4), RAIDZ3 (≥ 5); same plan → token → typed-name flow; disks
  are wiped including the first and last 17 MiB (an old md superblock at 1 MiB reappeared in `sdX1` and udev
  assembled it: found in the lab). Add a vdev with the same shape; L2ARC cache (1–2 SSD) and SLOG (1 or 2 mirrored);
  replace a disk (by name or GUID when it is gone) with resilver progress; scrub now or every N days (default 30);
  alerts for DEGRADED / FAULTED and checksum errors.
- **Import:** `storage.zfs.importable` lists pools on the disks; "view read-only" imports with `readonly=on` under
  `/import/<pool>` (altroot); "import" mounts the TangoOS-layout volumes. Export and destroy refuse while a volume has apps
  or a zvol backs an iSCSI target (LIO keeps it open and `zpool destroy` would hang).
- **Snapshots and Previous Versions:** ZFS snapshots of the folder dataset + symlinks in `/<vol>/@snapshots/<Folder>`
  (see ADR 0013); the scheduler, retention, Files versions and `vfs_shadow_copy2` are unchanged.
- **Encryption:** per volume, `aes-256-gcm`, passphrase through stdin in the agent; lock = unmount + `unload-key`; after a
  reboot the volume stays locked until unlocked (tested with a real reboot); 5 wrong tries → 10 min lockout.
- **USB key (ADR 0016):** the stick holds an X25519 key and the volume the passphrase sealed for it (`tangoos:keyseal`,
  `tangoos:keydev`). With the stick plugged in at boot the volume unlocks by itself (the same loop as LUKS); the
  passphrase still opens it anywhere. Changing the passphrase re-seals it; a lost stick is revoked by removing the key
  and then changing the passphrase.
- **Replication (ADR 0016):** Storage → Replication. A task sends a volume to another ZFS pool of this NAS or to another
  TangoOS linked with a code and its fingerprint (TLS on TCP 24020, only while that NAS has "Receive replicas" on). Every
  dataset travels raw and separately: encrypted volumes stay encrypted, a cut transfer resumes, the first run copies the
  snapshots' history and the next ones only the changes. Replicas live unmounted in `<pool>/tgrepl/<origin>/<vol>` and
  can be converted into a normal volume (disaster recovery).
- **GlusterFS bricks on ZFS:** a ZFS volume named `volumeN` qualifies as a brick origin as is (docs/17 §4): the brick
  directory inherits `xattr=sa` and `acltype=posixacl` from the pool.
- **Limits today:** replicas mirror the source's snapshots (no separate retention on the target); dRAID and
  special/dedup vdevs are not offered; adding a vdev requires the pool's shape (mixed pools are shown but not grown).

