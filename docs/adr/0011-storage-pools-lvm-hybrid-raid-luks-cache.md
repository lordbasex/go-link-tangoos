# ADR 0011 — Storage pools: LVM over md, hybrid RAID, LUKS2 and SSD cache

- Status: Accepted (extends ADR 0003; the classic md + Btrfs volume stays as the "classic" profile)
- Context:
  - With ADR 0003 one md array is one volume: the whole array is one Btrfs, disks of different sizes waste
    everything above the smallest one, and there is no place for several volumes, encryption or an SSD cache.
  - The model that commercial NAS systems converged on separates the **storage pool** (redundancy over the
    disks) from the **volumes** (filesystems that take space from the pool). It is plain Linux: md + LVM2,
    with dm-crypt and dm-cache in between.

## Decision

```
 disks ──► md RAID (one array)            ─┐
       └─► hybrid RAID: GPT partitions in    ├─► LVM PVs ─► VG = storage pool ─► LV = volume ─► [LUKS2] ─► Btrfs | XFS | ext4
           equal-size tiers, one md per tier ┘                                  └─ [lvmcache: SSD cache]
```

1. **Storage pool = one LVM volume group** named like the pool and tagged `tangoos` and `tg.profile=<profile>`.
   Profiles: `basic` (one disk, PV straight on the disk), `raid1`, `raid5`, `raid6`, `raid10` (one md array with
   whole disks, as in ADR 0003) and **hybrid RAID**: `hybrid1` (survives one failed disk) and `hybrid2` (two).
   md arrays of a pool are named `<pool>_md<N>`; their PVs carry the tag `tg.role=data`.
2. **Hybrid RAID (disks of different sizes).** Usable sizes are sorted and every distinct size is a tier
   boundary (sizes within 1 % are merged into the smaller one). Every disk gets one GPT partition per tier it
   reaches, all disks the same layout from 1 MiB. Tier *k* is an md array over the partitions *k* of all the disks
   that reach it:
   - `hybrid1`: ≥ 3 partitions → RAID 5, 2 → RAID 1, 1 → not used yet (kept for a future disk).
   - `hybrid2`: ≥ 4 partitions → RAID 6, fewer → not used yet.
   Example `hybrid1` with 2 + 2 + 4 GB: tier 1 = RAID 5 of three 2 GB partitions (4 GB usable), tier 2 = the
   upper 2 GB of the 4 GB disk, unused until another disk of ≥ 4 GB arrives; then it becomes a RAID 1 and the
   pool gains 2 GB. The algorithm is pure Go (`internal/diskops/hybrid.go`), with unit tests, and the wizard
   shows its tiers. The GPT is written in Go (`internal/diskops/gpt.go`), no `sgdisk`.
   **Growing:** a new disk must be at least as large as the largest one (exactly: its partitions have to fit
   the existing tiers). Each tier then gains one member: 1→2 creates a RAID 1, 2→3 turns the RAID 1 into a
   RAID 5 (`--grow --level=5`, then `--raid-devices=3`), ≥ 3 adds a member and reshapes; afterwards `pvresize`
   or `vgextend`. Volumes do not grow by themselves: the pool shows the new free space.
3. **Volumes = logical volumes** of the pool (`/dev/<pool>/<volume>`), mounted at `/<volume>` with the same
   `.mount` units as ADR 0003 (with `Type=` of the filesystem). Btrfs by default (shares, snapshots, quotas);
   XFS or ext4 for special uses (for example bricks of a distributed filesystem). Only Btrfs volumes can hold
   shared folders. Volume names are unique across classic and pool volumes. Volumes can grow online
   (`lvextend` + Btrfs resize ioctl / `xfs_growfs` / `resize2fs`); shrinking is not offered.
   LVs are only allocated on the pool's data PVs (passed explicitly), never on cache SSDs.
4. **Encryption per volume (LUKS2), not per pool.** The LV is formatted with `cryptsetup luksFormat --type luks2
   --pbkdf argon2id` and opened as `/dev/mapper/tgc_<volume>`. Per volume because a hybrid pool has several md
   arrays (one LUKS container each, same secret, every growth would need one more) and because it lets a pool
   hold encrypted and plain volumes, as the commercial NAS systems do since they moved encryption to volumes.
   - Unlock with the passphrase, or with a **key file on a removable device** (any vfat/exFAT/ext4 filesystem,
     identified by its filesystem UUID): `tangoos-keys/<LUKS UUID>.key`, 4 KiB random, added as a second LUKS key
     slot. If the device is plugged in at boot, `tangod` unlocks the volume by itself.
   - A locked volume has its `.mount` unit installed but not enabled; unlocking starts it.
   - The passphrase travels inside the encrypted DataChannel to `tangod` and over the agent's Unix socket; the
     agent hands it to `cryptsetup` through stdin (or an inherited pipe for a second secret). It is never in
     argv, logs, the agent journal (job arguments are journaled redacted) or the audit log.
   - Mounting the key device is done by systemd (transient `.mount` unit over D-Bus, like the volumes): the
     agent's seccomp filter has no `mount(2)`.
5. **SSD cache = lvmcache with a cache volume (`--cachevol`)** attached to one volume:
   - `read` (dm-cache `writethrough`): 1 SSD, or 2 in RAID 1.
   - `readwrite` (`writeback`): only with 2 SSDs in RAID 1, with an explicit warning (dirty blocks live only on the
     SSDs until they are flushed).
   - The SSD (or its md RAID 1, `<pool>_cache<N>`) becomes a PV of the pool tagged `tg.role=cache`.
     Removing the cache flushes it (`lvconvert --splitcache`) and releases the SSDs.
   - Hit ratio and dirty blocks come from `lvs` (`cache_read_hits`, …) and are shown in the volume and in the
     storage widget.
6. **Who does what.** Every write goes through typed ops of `tango-agent` (allow-list, strict validation,
   revalidation with fingerprints as in ADR 0003). `lvm`, `cryptsetup`, `mkfs.xfs`, `mkfs.ext4` join `mdadm` in the
   short list of binaries the agent may run (ADR 0009: exec as a last resort, arguments built in code).
   `tangod` cannot run LVM tools (no root, no exec): it asks the agent for a read-only `pool.report` (cached a
   few seconds) and joins it with sysfs (md state, mounts).
7. **Coexistence.** Existing md + Btrfs volumes keep working untouched and are shown as "classic". New volumes
   are created inside a pool by default; the classic path stays in the protocol.

## Consequences

- One more layer (LVM) between md and the filesystem: one more thing to activate at boot (udev does it: md
  assembles, then LVM activates the VG, then systemd mounts by filesystem UUID).
- Packages: `lvm2`, `cryptsetup-bin`, `thin-provisioning-tools` (`cache_check`, needed to activate cached LVs)
  become dependencies; `xfsprogs` is recommended.
- Btrfs still cannot repair a bad block from the md mirror (ADR 0003), with or without LVM.
- Hybrid pools are larger and slower to rebuild than a single array (several md arrays share the disks);
  md serializes their resyncs.
- No ZFS here: it would replace md, LVM and Btrfs at once; it stays a separate, optional package (roadmap).
