# Test results

Criteria and matrix: [`docs/14-test-plan.md`](../docs/14-test-plan.md) · how to run: [`README.md`](README.md).

## Full run — 2026-10-15

| Item | Value |
|---|---|
| Version | `0.1.0-dev` (final build deployed to VM 108 at 00:28 with this run's fixes) |
| NAS | VM 108 `tangoos-dev` (172.17.0.44), Debian 13.7, kernel 6.12, 12 virtual data disks |
| Client | Development Mac, real WebRTC client (`internal/rtc`, pion) and Chromium (Playwright 1.64.1) |
| Result | **All green** (including the destructive suites, approved by the user) |

| Layer / suite | Command | Result | Duration |
|---|---|---|---|
| Go unit tests (+ Linux `go vet`, gofmt) | `make check` | **157/157** in 28 packages | ~40 s |
| Web unit tests (Vitest) | `cd web && npm test` | **70/70** in 9 files | ~1 s |
| Security | `make e2e-security` | **7/7** | 4.6 s |
| API E2E | `make e2e-api` | **20/20** (+ 2 SFTP subtests) | 2 min 32 s |
| UI E2E | `make e2e-ui E2E_DESTRUCTIVE=1` | **1/1** walkthrough, 19 screenshots | 44 s |
| Storage matrix | `make e2e-storage` | **8/8** rows | 4 min 27 s |
| VM reboot (row 9) | `make e2e-storage E2E_REBOOT=1` | **1/1** | 2 min 28 s |
| Installation from the ISO | `make iso` + VM 109 (no e2e target yet) | not run in this battery: measured in the installer phase (59–80 s, `docs/08`) and used by M4 on VM 109 | — |

### API E2E (20)

| Test | What it checks |
|---|---|
| `TestSessionLoginAndGating` | public vs. session-only methods, bad credentials, logout invalidates the token |
| `TestSessionLockout` | lockout on the 5th failed attempt (nonexistent user, never a real one) |
| `TestSessionResumeAfterRestart` | the token is still valid after `systemctl restart tangod` |
| `TestSystemInfoMetricsRTC` | `system.info`, ≥ 8 metrics frames in 3 s, ICE connected, `rpc/events/metrics/files.*` channels, 1 system disk |
| `TestSharesUsersPermissions` | new folder in `smbclient -L`; rw/ro/none checked with `smbclient`; weak password rejected; password change applied to SMB; invalid names |
| `TestFiles` | **1 GiB round trip with sha256 (51.6 MB/s upload, 47.0 MB/s download)**; mkdir/rename/move/trash/restore/empty; search; thumbnail; traversal; absolute and relative symlinks that escape; per-user permissions (none/ro) |
| `TestSnapshots` | manual snapshot, versions, restore version (content verified), schedule round trip + validation, delete |
| `TestAppsLifecycle` | Docker ready, catalog ≥ 8, syncthing: install → proxy 401 without cookie → ticket → 200 without X-Frame-Options → single-use ticket → stop/start/restart/update → logs → uninstall without data |
| `TestAppsCookieRevokedOnLogout` | the proxy cookie stops working when the session that issued it logs out |
| `TestServicesWebDAVSFTPNFS` | WebDAV PUT/GET/401; SFTP (Go server, traversal and bad key); NFS exported to loopback and mounted (read as the owning user), invalid host rejected |
| `TestServicesISCSI` | short CHAP rejected; 256 MB LUN with CHAP visible to open-iscsi (`LIO`); bad CHAP does not get in; delete with a mistyped name rejected |
| `TestM4AuditLogsTerminal` | audit with a failed login and `users.setPassword` without passwords; `logs.query`; root terminal (`id -u`=0) |
| `TestM4CertAndUpdates` | `cert.info`; `updates.check`; rollback on VM 108 → `NOT_SUPPORTED` (ext4 root); reboot with a mistyped word rejected |
| `TestM4Notifications` | ntfy (with Bearer) and SMTP reach a receiver on loopback; the token does not come back in clear text; `notify.json` 0600; config restored |
| `TestUsersDelete` | `CONFIRMATION_MISMATCH`, admin cannot delete themselves, real deletion (Unix + Samba + login), `USER_NOT_FOUND` |
| `TestTOTPAndAppPasswords` | TOTP begin/confirm, two-step login, bad code, single-use recovery code, `TOTP_NOT_PENDING`; with 2FA, WebDAV/SFTP reject the account password and accept the app password; revoke; cannot see other users' passwords; disable 2FA |
| `TestPreviousVersionsBrowseRangesWatcher` | browse a snapshot (`files.list/stat` with `snapshot`), 4 KiB range from the snapshot, out-of-size range rejected, **`allinfo` over SMB shows `@GMT-`**, **`files.changed` after writing over SMB** |
| `TestTimeMachineShare` | `TimeMachine` folder with `fruit:time machine = yes` in `testparm` and `_adisk._tcp`/`adVN=TimeMachine` in Avahi |
| `TestWSDiscovery` | `tangoctl wsd-probe` from the Mac finds the NAS |
| `TestUPSFake` | fake remote UPS (`fakeupsd` on loopback): `ups.status` online with 87 % battery; config restored |

### Security (7)

`TestSecureResume` (the NAS proves the token before the client does), `TestSecurityUnauthenticated` (the 97 non-public methods → `UNAUTHENTICATED`), `TestSecurityNonAdmin` (31 administration methods → `PERMISSION_DENIED` for a regular user), `TestSecurityRouterValidation` (unknown method, extra fields), `TestSecurityAgentSocket` (`nobody` cannot talk to the agent; unknown op, extra fields and invalid version rejected), `TestSecurityHTTPHeaders` (CSP without `unsafe-eval`, HSTS, nosniff, Referrer-Policy, :80 → HTTPS, nonexistent asset 404), `TestSecuritySecretsNotLogged` (none of the run's passwords in the journal or in `audit.jsonl`).

### Storage matrix (disks S3, S6 of 2 GB and M1, M2 of 4 GB)

| Row | Profile and disks | Created and healthy in | Result |
|---|---|---|---|
| 1 | Basic · S3 | 2 s | ✅ + folder, file, `tangod` restart, deletion, disk free |
| 2 | RAID 1 · S3+S6 | 14 s | ✅ same |
| 3+6 | Mixed RAID 5 · S3+S6+M1 | 28 s | ✅ capacity 2×2 GB and note about different sizes |
| 4 | RAID 6 · S3+S6+M1+M2 | 16 s | ✅ |
| 5 | RAID 10 · S3+S6+M1+M2 | 23 s | ✅ |
| 7 | Replacement · RAID 5 with `mdadm --fail` on S6 → M2 | 26 s + rebuild | ✅ alert active → replacement (disk in use rejected) → healthy, alert resolved, S6 free |
| 8 | Expansion · RAID 5 + M2 | 18 s + reshape | ✅ 4.29 GB → 6.43 GB |
| 9 | Persistence · RAID 1 S3+S6 + **VM reboot** | 14 s | ✅ volume1, volume2 and the new one healthy; file present; SMB with Familia; apps running |
| 10 | Negatives | — | ✅ system disk, member disk, odd RAID 10, RAID 6 with 3, Basic with 2, repeated/invalid name, tampered token, nonexistent plan, expanding RAID 1 |

In every row: mistyped name → `CONFIRMATION_MISMATCH`, single-use plan, `mdadm --detail` and `findmnt` checked on the NAS, and after deletion no md, mount or signatures remain (`wipefs -n` empty).

### UI E2E — screenshots (`tests/e2e-ui/artifacts/`)

`01-login` · `02-escritorio` · `03-almacenamiento-resumen` · `04-almacenamiento-discos` · `05-asistente-discos` · `06-asistente-perfil` · `07-asistente-revision` · `08-asistente-listo` · `09-volumen-e2eui` · `10-borrar-volumen` · `11-ajustes-carpetas` · `12-ajustes-usuarios` · `13-archivos-familia` · `14-apps-instaladas` · `15-monitor-webrtc` · `16-escritorio-oscuro` · `17-escritorio-claro` · `18-spotlight` · `19-sesion-cerrada`. No JavaScript errors on the page.

## TangoConnect — 2026-10-15 (`make e2e-connect`)

Real browser (Playwright 1.64.1, Chromium) · local portal (`web/apps/connect`, built with the dev public key) ·
development signalhub on the Mac (`ws://172.17.0.5:8090`) · NAS = VM 108. `signal.go-link.org` was never used.

| Step | Result |
|---|---|
| Pair with the 9-digit code (`connect.pair.start` → portal `/nas/vincular`) | ✅ |
| Desktop served by the NAS (Ed25519 manifest + per-file sha256, served from `/nas-bundle/<id>/`), **without development mode** | ✅ |
| Login as admin inside the remote desktop | ✅ |
| Storage: 12-bay rack, real volume1 RAID 1 + volume2 RAID 5 | ✅ |
| Files → "Share link…" on `Fotos/Atardecer en el Río.jpg` | ✅ |
| Public link in a new context (`guest.open`) → download → **sha256 equal to the NAS's** | ✅ |
| Reload of `/nas/<id>`: `reach` + the NAS proves first + resume | ✅ |
| Revoke the browser's pairing → blocked ("this browser is no longer paired") | ✅ |

3 consecutive green runs (23.6 s · 32.2 s · 57.1 s). Screenshots: `tests/e2e-ui/artifacts/connect-01…10-*.png`.

**Real bugs found and fixed by this test**
1. **The NAS desktop never mounted in the portal** (`Failed to fetch dynamically imported module`): the Service
   Worker had scope `/nas-bundle/`, but a SW only intercepts requests from pages it **controls**. Now the scope is `/`
   (it only answers `/nas-bundle/`) and the loader waits for `controllerchange` (`web/apps/connect/src/bundle/loader.ts`, `public/bundle-sw.js`).
2. **The portal closed the connection when handing off the page**: the React cleanup of `/nas/<id>` called `s.close()` when
   unmounting the portal → the NAS desktop was left at "No connection to the NAS" (`pages/Remote.tsx`, `handedOff`).
3. **Double pairing step** (`connect.link` / `guest.open` twice per connection → intermittent `NOT_PAIRED` when
   pairing and "this connection cannot use guest.open" on links): `RtcTransport` ran `onRpcOpen` if the channel
   arrived already open **and** again on the `open` event. It is now idempotent, with a regression test
   (`packages/client/src/rtc/RtcTransport.ts`, `connect.test.ts`).
4. **Public link to a file without a "Download" button**: the NAS reports the real type (`image`, `video`…) and the page
   compared it with `"file"` (`pages/Share.tsx`).

**Integration changes**: `apps/desktop` with `base: "./"` and transport `window.__TANGO_TRANSPORT__` (no
`installDCStream` in the portal); development mode **only** as a fallback if the bundle is missing or does not mount (invalid signature/hash/
manifest never falls back); **mandatory DTLS pin** (different fingerprint → the connection is cut before the proof
and the token, with "Forget and pair again"); `protocol/README.md` §15 aligned (links via `reach` + `guest.open`
with the secret in the fragment, DTLS pin section); `cmd/connect-web/dist` kept out of the repo (`.keep` + `.gitignore`).

## ISO graphical installer — 2026-10-15 (VM 109, Proxmox, OVMF, 2 vCPU, 2 GB, disks 16 GB `TANGO-SYS` + 4 GB `LAB-D1`)

| Test | Result |
|---|---|
| `go test ./internal/installer` (code only on the local screen, PIN + cookie from the LAN, lockout after 5 errors, POST without header → 403, disk/name/keyboard/IP validation, fake engine: progress, failure and retry, a single installation, reboot, extra fields → 400, CSP) | ✅ 9 tests |
| Product ISO (533 MB): boot → graphical installer on screen | ✅ ~41 s from `qm start`, in Spanish, with the desktop's wallpaper and style |
| Installation **from another PC** (Playwright on the Mac → `http://172.17.0.8/`, wrong code rejected, correct one accepted) | ✅ 34.4 s; the local screen followed the progress on its own |
| Installation **on the machine's own screen** (clicks via QMP on cage + cog, text with the VM's keyboard) | ✅ ~35 s; name `kiosco`, latam keyboard |
| Graphical installation with a **static IP** (172.17.0.47/24, DNS 1.1.1.1) | ✅ 33.5 s; `20-wired.network`, `/etc/hosts` and name correct |
| Reboot from the interface → installed system serves HTTPS | ✅ 17–26 s; port 80 is already `tangod` (redirects) |
| Installed system with no installer leftovers (cog, cage, `tango-installer`, units, `tango-kiosk` user, `live-boot`) | ✅ checked over SSH (graphical and text installation) |
| Interactive **text mode** (second entry, whiptail) | ✅ installation 47 s with the keys; HTTPS 21 s after rebooting |
| **Unattended lab** (`ISO_VARIANT=test`, 2 disks, `tango.disk=TANGO-SYS`) | ✅ 83–89 s from `qm start` to HTTPS (previously 90 s) |
| **No graphical display** (`vga: none`) | ✅ tty1 shows `http://<ip>/` and the code; installation from another PC is still available |

Bugs found along the way and fixed: WPE (cog) does not generate `click` in the kiosk (the page
synthesizes it); its native `<select>` keeps the input (replaced by custom controls); cog
started in English and with leftovers of the first 1024×768 frame (language from the server, full
repaint); `/etc/issue` always showed `tango.local` (now `\n.local`); the lab ISO was not
unattended with more than one disk (`tango.disk`); the ISOs' `.sha256` files were not regenerated.

## Script installation and APT repository — 2026-10-15 (`install.sh`, `cmd/tango-aptrepo`)

Test repository built with `tango-aptrepo` (signed with the real `TangoOS Archive` key,
`B34312438C2849B683A053199C06A62DC9C4DB37`) and served over HTTP with a request log; nothing against production.

| Test | Where | Result |
|---|---|---|
| `go test ./internal/aptrepo` (.deb control, immutable pool, epoch, Packages/Release, old indexes deleted, `install.sh` key = `build/keys`) | Mac | ok |
| `shellcheck -s sh install.sh`, `publish-apt.sh`, `deploy-connect-web.sh` | Mac | clean |
| `cat install.sh \| sh` on a clean `debian:trixie` (no systemd, `TANGOOS_FORCE=1`) | Docker | installs `tangoos` 0.1.0-dev; user `tango`, group `tango-agent` |
| apt only requests `InRelease`, `Packages.gz` and the `.deb` (server log) | Docker | ok: nothing that CloudFront would answer with `index.html` |
| Higher version published (0.1.1-test) → `apt-get -s upgrade` sees it and installs it | Docker | ok |
| Tampered `InRelease` | Docker | rejected by `sqv` ("Message has been manipulated"); restored: no warnings |
| Guards: Debian 12, `:443` in use (with and without `TANGOOS_FORCE`), user without root or sudo | Docker | aborts with a clear message / warns and continues / aborts |
| `curl … \| sh` on a clean Debian 13 cloud VM (real systemd, temporary VM 111, 2 vCPU, 2 GB) | Proxmox | **1 min 18 s**; tangod, agent, smbd and avahi active, NFS disabled, `https://<ip>/` 200, `http` → 308 |
| Admin login over WebRTC (`tangoctl … system.info`) with the initial password | VM 111 | ok |
| `system.updates.check` sees 0.1.1-test; `system.updates.apply {scope: all}` installs it and tangod comes back | VM 111 | ok (ext4 root: no `@rootfs` snapshot, no error) |

VM 111 was deleted when done. Finding fixed: the postinstall uses `adduser`/`addgroup` and the
package did not declare it (`adduser` is not essential in Debian): added to `Depends` in `build/nfpm.yaml`.

## Product bugs found and fixed in this battery

| # | Severity | Bug | Fix | Test that covers it |
|---|---|---|---|---|
| 1 | **High (security)** | 10 methods without an administrator check: a regular user could plan and **create volumes (wipes disks)**, start a scrub, create folders and snapshots, install/uninstall/stop/update apps and see their logs | `requireAdmin` in `internal/api/storage.go` and `internal/api/apps.go` | `TestSecurityNonAdmin` |
| 2 | Medium | `users.setPassword` for **any** user deleted `/var/lib/tango/initial-admin-password` | only when changing `admin`'s (`internal/auth/auth.go`) | `internal/auth/initialpw_test.go` |
| 3 | Low | `system.rollback.apply` with a nonexistent id created a job that failed later (it did not reboot anything) | validates the point before the job (`internal/api/m4.go`) | `TestM4CertAndUpdates` |
| 4 | **High (availability)** | no SMART cache: every `storage.disks.list` queried the agent for all 13 disks (1221 `disk.smart` in 5 min) and exhausted its 20/s quota → real operations such as `storage.volumes.plan` failed with `RATE_LIMITED` | `SmartCache` (1 min; 10 min without SMART; 15 s on error) in `internal/api/smartcache.go` | `TestSmartCache`, matrix |
| 5 | **High** | deleting a volume (or folder) recently used from Files failed: "umount: target is busy". The `files.changed` watcher kept a long-poll with fds in the folder, the worker waited for that long-poll when closing, and the supervisor relaunched a worker on the volume | the worker exits at most 2 s after EOF (`internal/fileworker/worker_linux.go`); `ForgetUnder` + `InvalidateWorkers` before deleting (`internal/api/storage_m21.go`, `files_watch.go`); unmounting retries for ~10 s (`internal/diskops/systemd_linux.go`) | `TestForgetUnder`, matrix rows 1–8 |
| 6 | Low (test) | `internal/jobs` `TestInterruptedOnReload` flaky (writing `jobs.json` racing with TempDir removal) | the test waits for the job to finish | 50 consecutive runs without failure |

In addition, `DownloadRange` (range downloads and downloads from snapshots) was added to the Go client, used by the tests and by the future native app.

## Unfixed findings (noted)

- On VM 108, rollback answers `NOT_SUPPORTED` because the root is ext4 (cloud image); the full flow with Btrfs was tested on VM 109 installed from the ISO (phase M4).
- The local CA was not tested on VM 108 so as not to change the certificate the user had already accepted; it was tested on VM 109.
- Snapshot scheduling with a short interval has no knob in production; its execution and retention were validated in M2.2 with a debug mode (here: round trip + validation).
- The Storage widget shows the first volume in the list; with a newly created volume it may show that one instead of `volume1` (cosmetic).

## Lab state at the end

`volume1` RAID 1 (D1+D2) and `volume2` RAID 5 (S1, S2, S4, S5) healthy · folders Familia, Fotos and TimeMachine · users `admin` and `sofia` (`e2etester` deleted with `users.delete`) · apps Jellyfin and Uptime Kuma running · no iSCSI targets · Familia's trash empty · free disks S3, S6, M1, M2, D3, D4 with no signatures. `open-iscsi` was left installed on the VM (used by the iSCSI suite). The NFS export of Familia to 172.17.0.46 predates this run (M4 test with VM 109).

## Storage pools: LVM2, hybrid RAID, LUKS2, SSD cache (ADR 0011) — 2026-10-15

Branch `feat/storage-pools`, VM 108 with Debian 13 (kernel 6.12 cloud), `lvm2` 2.03.31, `cryptsetup` 2.7.5. Free lab disks
only; three virtual disks added for this suite (Proxmox `qm set 108`): C1 and C2 (2 GB, `ssd=1`) and KEY (1 GB, FAT
partition "LLAVE", stands in for a USB key because the cloud kernel has no `usb-storage`).

| Test | Where | Result |
|---|---|---|
| `go test ./internal/diskops` (hybrid tiers: 2+2+4, 2+4+4, 2+2+4+4, 1+10, 2+4+8+8, Hybrid-2 cases, 1 % merge, growth; GPT round trip 512/4096, rejects; names, tags, XFS/ext4/LUKS/FAT UUIDs) | Mac | ok |
| `go test ./cmd/tango-agent` (validation of every new op, passphrase redacted for the journal) | Mac | ok |
| `go test ./internal/storage` (pool volumes, locked state, cache hit ratio, pool disks, stale md node not listed) | Mac | ok |
| `go test ./internal/api` (pool plan hybrid/RAID, names, `NO_SPACE`, passphrase not in any state file, 5 failed unlocks → `RATE_LIMITED`, auto-unlock once per key, cache rules, `POOL_NOT_EMPTY`) | Mac | ok |
| `npm test` (same tier cases in TS + demo flows) | Mac | 108/108 |
| Agent ops by hand (`pool.create` hybrid, `lv.create` Btrfs and encrypted XFS + key, lock/unlock with key and passphrase, passphrase change, read cache 1 SSD, read/write cache 2 SSDs, grow cached + encrypted volume, expand hybrid, replace disk, delete) | VM 108 | ok after the fixes below |
| `TestPoolRAID5TwoVolumes` (row 11) | VM 108 | **pass** (44 s) |
| `TestPoolHybridLUKSCache` with `TANGO_E2E_REBOOT=1` (rows 12–17) | VM 108 | **pass** (4 min 17 s, VM reboot included: unlocked by itself with the key plugged in) |
| `web/e2e/pools.spec.ts` (row 18: demo + real wizard, encrypted volume, lock/unlock, delete) | VM 108 | **pass** (45 s), screenshots `tests/e2e-ui/artifacts/pools-*.png` |

Findings fixed while testing:

1. LVM only tags PVs that are already in a VG: tags are set after `vgcreate`/`vgextend`.
2. An undone creation left md superblocks inside the future partitions; writing the same GPT again made udev reassemble the
   old array (`Array name … is in use`). Now the partition zones are zeroed before writing the table, and undo zeroes the
   superblocks.
3. `cryptsetup resize` asked for the passphrase again (volume key in the kernel keyring of the process that opened it):
   volumes are opened with `--disable-keyring`.
4. `lvconvert --type cache` failed: LVM runs `modprobe dm-cache` and the agent has `ProtectKernelModules=yes`. The package now
   ships `modules-load.d/tangoos.conf` (and the postinstall loads them once).
5. An LV with two segments showed up twice in `pool.report`: rows are merged by VG/LV.
6. After wiping a disk that had just left an md, the kernel kept showing its old partitions: `BLKRRPART` is retried.
7. A failed `mdadm --create` leaves an empty md node: the inventory no longer lists it as a volume.

Lab state at the end: `volume1` (D1+D2) and `volume2` (S1, S2, S4, S5) healthy and mounted; no pools left; S3, S6, M1, M2,
D3, D4, C1, C2 free; KEY with its FAT partition (holds stale key files of deleted test volumes, harmless). VM 108 now has
`lvm2`, `cryptsetup-bin`, `thin-provisioning-tools`, `xfsprogs` and `dosfstools` installed.
