# TangoOS tests

The criteria and the matrix are in [`docs/14-test-plan.md`](../docs/14-test-plan.md).
The results of each full run are recorded in [`RESULTS.md`](RESULTS.md).

| Layer | Where | Command |
|---|---|---|
| Go unit tests (+ Linux vet, gofmt) | `cmd/ internal/ pkg/` | `make check` |
| Web unit tests (Vitest) | `web/packages/*` | `cd web && npm test` |
| Security | `tests/e2e/security_test.go` | `make e2e-security` |
| API E2E | `tests/e2e/*_test.go` | `make e2e-api` |
| Storage matrix | `tests/e2e/storage_matrix_test.go` | `make e2e-storage` (`E2E_REBOOT=1` reboots the VM) |
| UI E2E (Playwright 1.64.1) | `web/e2e/desktop.spec.ts` | `make e2e-ui` (`E2E_DESTRUCTIVE=1` creates and deletes a RAID 5) |
| Everything, in order | — | `make e2e-all` |

## Requirements

- Lab VM 108 (`172.17.0.44`) with TangoOS installed (`make deploy-vm`) and the lab's root SSH
  key (`SSH_KEY`, default `~/.ssh/id_ed25519`; yours goes in `local.mk`).
- Go from `go.mod`, Node LTS. Playwright downloads its Chromium the first time.
- The tests use a real WebRTC client (`internal/rtc`), not mocks. The `admin` password is
  read over SSH from `/var/lib/tango/initial-admin-password` and is never printed.

## What each suite touches

| Suite | Effects on the NAS | Cleanup |
|---|---|---|
| `e2e-security` | creates/resets the `e2etester` user; failed login attempts with nonexistent users | none needed |
| `e2e-api` | `E2E-*` folders on volume1, `syncthing` app (installs and uninstalls), iSCSI LUN `e2e-lun`, notification receiver on loopback, `open-iscsi` on the VM | deletes everything when done (`t.Cleanup`) |
| `e2e-storage` | `e2em*` volumes on **S3, S6, M1, M2**; forced failure of a disk (mdadm --fail); VM reboot | deletes each volume and checks the disks are free |
| `e2e-ui` | uploads a file to Familia and moves it to the trash; with `E2E_DESTRUCTIVE=1` creates and deletes `e2eui` (RAID 5 on S3, S6, M1) | yes |

Permanent fixtures that no suite modifies: `volume1` (RAID 1 on D1+D2) with Familia,
`volume2` (RAID 5 on S1, S2, S4, S5) with Fotos, users `admin` and `sofia`, apps Jellyfin and Uptime Kuma.

## Variables

| Variable | Use |
|---|---|
| `TANGO_HOST` | NAS host (default 172.17.0.44) |
| `TANGO_SSH_KEY` | the lab's root SSH key |
| `TANGO_E2E_DESTRUCTIVE=1` | enables the matrix and the RAID from the UI |
| `TANGO_E2E_REBOOT=1` | enables row 9 (VM reboot) |
| `TANGO_E2E_BIGFILE_MB` | size of the large file for Files (default 1024) |

Artifacts: logs in `tests/e2e-logs/`, UI screenshots in `tests/e2e-ui/artifacts/`,
failure traces/videos in `tests/e2e-ui/test-results/`.

## TangoConnect (`make e2e-connect`)

Walks through remote access with a real browser (Playwright): pair with the code → desktop served
by the NAS (signed, verified by the portal) → login → Storage (12-bay rack) → public link
to a photo from Files → download in another context with a sha256 equal to the NAS's → reload
(`reach`, the NAS proves first) → revoke the pairing (it ends up blocked). Screenshots in
`tests/e2e-ui/artifacts/connect-*.png`. **It never uses the production signal server.**

Setup (once per run):

1. Development signalhub on the Mac, accepting the local portal's origin:
   `cd ../signaling && ADDR=0.0.0.0:8090 ALLOWED_ORIGINS=http://127.0.0.1:5174 go run ./cmd/signal`
2. Enable TangoConnect on the NAS pointing at that signal server (and make links point to the local portal):
   `tests/tangoctl-admin.sh rpc connect.enable '{"enabled":true,"signalURL":"ws://172.17.0.5:8090/ws?v=1","portalURL":"http://localhost:5174"}'`
3. Portal built with the dev public key and served on :5174:
   `make connect-portal-dev && (cd web/apps/connect && npx vite preview --port 5174 --strictPort --host 127.0.0.1)`
4. `make e2e-connect`

When done: `tests/tangoctl-admin.sh rpc connect.enable '{"enabled":false,"signalURL":"wss://signal.go-link.org/ws?v=1","portalURL":"https://tangoos.go-link.org"}'`
and stop the signalhub and the preview. `tests/tangoctl-admin.sh` and `tests/vm-ssh.sh` read the password / run
commands over SSH without printing secrets.
