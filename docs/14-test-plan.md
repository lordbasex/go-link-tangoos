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
