# 14 — Test plan (the 100 % criterion)

A feature is done when it has **all four layers** green and has been run on the
lab VM. The results of each run are recorded in `tests/RESULTS.md`
(date, commit/version, summary, failures).

| Layer | Tool | Where it runs | Command |
|---|---|---|---|
| Go unit tests | `go test` | Mac (+ `GOOS=linux go vet`); no CI workflow yet | `make check` |
| Web unit tests | Vitest | Mac | `cd web && npm test` |
| API E2E | `tests/e2e` (Go, real pion client) | Mac → VM 108 | `make e2e-api` |
| UI E2E | Playwright (Chromium) | Mac → VM 108 | `make e2e-ui` |
| Storage matrix | `tests/e2e/storage_matrix_test.go` | Mac → VM 108 (disks S*/M*/D3/D4) | `make e2e-storage` |
| Security | `tests/e2e/security_test.go` + review | Mac → VM 108 | `make e2e-security` |
| Installation | `system/iso` + VM 109 | Proxmox | `make iso ISO_VARIANT=test`, then boot it on VM 109 (there is no `make e2e-install` target yet) |
| TangoConnect | Playwright (`web/e2e/connect.spec.ts`) | Mac → VM 108 + local signalhub | `make e2e-connect` |

## Lab

- VM 108 `tangoos-dev` (172.17.0.44): 12 data disks — D1–D4 16 GB, S1–S6 2 GB, M1–M2 4 GB.
- `volume1` (RAID 1 D1+D2) and `volume2` (RAID 5) are **permanent fixtures**; the matrix only uses
  free disks and leaves everything as it found it.
- VM 109: installation tests from the ISO.

## Storage matrix (e2e-storage)

Each row: plan → confirmation → creation → verification (mdstat, findmnt, statfs, API) →
folder + write → `tangod` restart → (flagged row) VM reboot → deletion → disks free.

| # | Profile | Disks | Extra checks |
|---|---|---|---|
| 1 | Basic | S4 | Btrfs without md |
| 2 | RAID 1 | S4+S5 | resync, `[UU]` |
| 3 | RAID 5 | S4+S5+S6 | capacity = 2×min |
| 4 | RAID 6 | S3+S4+S5+S6 | capacity = 2×min |
| 5 | RAID 10 | S3+S4+S5+S6 | even number of disks required |
| 6 | Mixed RAID 5 | S6+M1+M2 | different-sizes note, capacity = 2×2 GB |
| 7 | Replacement | RAID 5 #3 with a forced failure on S5 → M1 | alert, rebuild, alert resolved |
| 8 | Expansion | RAID 5 #3 + M2 | reshape, `btrfs resize`, capacity grows |
| 9 | Persistence | RAID 1 #2 | **VM reboot**: md assembled, mounted, SMB active |
| 10 | Negatives | — | system disk, disk in use, expired/tampered token, misspelled name, odd RAID10, RAID6 with 3 |

## API E2E (per domain)

| Domain | Minimum cases |
|---|---|
| Session | login ok/err, lockout, resume after restart, logout, gating before login |
| System | `system.info`, 4 Hz metrics, `rtc.stats` |
| Folders | create/delete, SMB visible, rw/ro/none permissions verified with `smbclient` |
| Users | create, weak password rejected, password change, SMB login |
| Files | list, upload 1 GB (sha256), download, rename, move, trash/restore/empty, versions, thumbnail, per-user permissions, traversal/symlink |
| Snapshots | manual, scheduled (short test interval), retention, delete, restore version |
| Apps | Docker installed, install/stop/start/update/logs/uninstall, proxy without X-Frame-Options |
| Services (M4) | NFS mountable, WebDAV, SFTP chroot, iSCSI login |
| M4 | first boot, update with a root snapshot and rollback, local CA certificate, notifications (test SMTP, ntfy) |

## UI E2E (Playwright)

Walkthroughs on the real product (not a mock): login → desktop → widgets with real data →
Storage (create RAID 5 from the wizard with S4–S6, view the chassis, delete) → Settings (folder,
user, permissions) → Files (upload, quick look, trash, versions) → App Center (install
and open Uptime Kuma) → Monitor (WebRTC) → lock/sign out. Per-step screenshots in
`tests/e2e-ui/artifacts/` and visual regression of the desktop in light/dark.

## Security

No session → `UNAUTHENTICATED` on every non-public method; non-admin user → `PERMISSION_DENIED`
on admin methods; path traversal/symlink escape in Files; payload with extra
fields/unknown op to the agent; unauthorized peer on the agent socket; secrets absent from
logs (`journalctl | grep` for test passwords); HTTP security headers; login rate limit.

## Storage pools (ADR 0011) — `make e2e-storage` + Playwright

Uses only free lab disks: S3, S6 (2 GB), M1, M2 (4 GB), C1 and C2 (2 GB virtual SSDs, `ssd=1`) and KEY (1 GB with a FAT
partition, stands in for a USB key: the cloud kernel has no `usb-storage`).

| # | Case | Disks | Checks |
|---|---|---|---|
| 11 | RAID 5 pool with two volumes (Btrfs 1.5 GB + ext4 1 GB) | S3+S6+M1 | plan note about hybrid RAID, `NO_SPACE` for a third, folder + upload + tangod restart, `POOL_NOT_EMPTY`, delete volumes and pool, disks free |
| 12 | Hybrid-1 with 2 + 2 + 4 GB | S3+S6+M1 | tiers RAID 5 (4 GB) + reserved 2 GB, M1 with 2 GPT partitions `tg-<pool>-t*` |
| 13 | Encrypted volume with passphrase + key file | KEY | lock (unmounted, `/dev/mapper` closed), wrong passphrase → `WRONG_PASSPHRASE`, unlock with the key, with the passphrase, change passphrase (old one rejected), no passphrase in journald, agent journal or `/var/lib/tango*` |
| 14 | Expand hybrid with one more 4 GB disk | + M2 | plan 8 GB (RAID 5 × 4 + RAID 1), reshape, pool healthy, file intact (sha256) |
| 15 | Grow the encrypted volume online | — | LV + LUKS + Btrfs grow |
| 16 | SSD cache: read with 1 SSD, read/write with 2 (RAID 1) | C1, C1+C2 | HDD rejected, cache mode and disks in the volume, remove → SSDs free |
| 16b | Replace a disk of the hybrid pool (M2 → D3) | + D3 | too small SSD → `DEVICE_TOO_SMALL`, rebuild of both tiers, M2 free, file intact |
| 17 | VM reboot with the key plugged in (`TANGO_E2E_REBOOT=1`) | — | volume unlocked and mounted by itself, file intact, pool healthy |
| 18 | UI: wizard (Hybrid-1 with mixed disks), encrypted volume, lock/unlock, delete (`web/e2e/pools.spec.ts`) | S3+S6+M1 | screenshots `pools-*.png`; plus the demo scenarios `pools`, `hybrid`, `locked` |
| 28 | Cluster stage 2 (`make e2e-cluster2`, VMs 112–114): join with code + fingerprint, certify an old member, replica 3 on thin LV bricks, TLS on with the volume mounted, encryption checked with tcpdump/openssl, quotas (a folder with content and a subfolder opened before the limit: counted, stops at 20 MB, removing the limit lets writes through; on the distributed volume it stops before 45 MB with a 30 MB limit; remove-brick with quotas on), add/rebalance/remove a set, snapshot + restore, replace, NFS, revocation, TLS off | TANGO-G<n>, TANGO-P<n> | 14/14; UI `web/e2e/cluster2.spec.ts` (`cluster2-*.png`) |
| 29 | CA rotation (`make e2e-cluster2`): after removing n3, rotate with TLS on and a volume mounted | — | every node presents a certificate from the new CA (`openssl s_client` on 24007 and 24010), n3's old certificate is refused on 24010 and by the bricks, the volume is mounted again with the same data, fingerprints unchanged, pending list empty |

## ZFS (ADR 0013) — `go test -tags e2e -run TestZfs` + Playwright

Uses only free lab disks: S3, S6 (2 GB), M1, M2 (4 GB), D3, D4 (16 GB), C1 and C2 (2 GB virtual SSDs).

| # | Case | Disks | Checks |
|---|---|---|---|
| 19 | Enable ZFS from the API (`TANGO_E2E_ZFS_REINSTALL=1` purges first) | — | `ZFS_UNAVAILABLE` before, job succeeds, version 2.x, `modules-load.d`, contrib source |
| 20 | Mirror S3 + S6 | S3+S6 | invalid layouts/names, disk of volume1 → `DEVICE_NOT_ELIGIBLE`, tolerance 1, ≈ 2 GB, members marked, `lz4/sa/posix/atime off` |
| 21 | Volume + SMB folder + snapshot | — | folder is a dataset, upload, snapshot listed, file versions, `.zfs/snapshot` links + `.gmt`, `allinfo` over SMB shows `@GMT-` |
| 22 | zvol + iSCSI LUN | — | `iblock_0/<name>/udev_path`, zvol delete refused while used (`VOLUME_BUSY`), delete target then zvol |
| 23 | Replace S6 with M1 (resilver) | + M1 | pool ONLINE with M1, data intact; delete volume and pool via the API, disks free |
| 24 | RAIDZ1 M2 + D3 + D4 with L2ARC (C1) and SLOG (C2) | M2+D3+D4, C1, C2 | ≈ 8 GB (4 GB × 2), cache and log present |
| 25 | Encrypted volume | — | inherited `aes-256-gcm`, lock (key unavailable, unmounted), wrong passphrase, unlock, change passphrase, **VM reboot** (`TANGO_E2E_REBOOT=1`): pool imported, volume locked, unlock with the new one; no passphrase in journald or state |
| 26 | Scrub, export, import read-only, export, import read/write, destroy | — | scrub finished 0 errors, importable list, `readonly`, volume back locked, disks free |
| 27 | UI (`web/e2e/zfs.spec.ts`): demo enable/pool/import/wizard/volume; real NAS mirror S3 + S6 from the wizard, volume, destroy | S3+S6 | screenshots `zfs-*.png` |

