# 10 — Repo structure and conventions

## Monorepo

```
tangoos/
├─ go.mod                     # module github.com/lordbasex/go-link-tangoos
├─ cmd/
│  ├─ tangod/                 # server: HTTPS + /signal + embedded STUN + pion + RPC + modules
│  ├─ tango-agent/            # root, Unix socket, typed ops (storage, users, apps, M4 system ops)
│  ├─ tango-installer/        # graphical ISO installer (only in the live installer layer)
│  ├─ tangoctl/               # CLI: rpc, diagnostics, bench
│  ├─ tango-bundlesign/       # signs the desktop bundle manifest (Ed25519)
│  ├─ tango-aptrepo/          # builds the signed apt repository
│  └─ connect-web/            # static server for the TangoOS Connect portal (self-hosting)
│                             # (planned, not present yet: tango-desk/, native Win/Linux app)
├─ internal/
│  ├─ api/                    # RPC handlers per domain
│  ├─ auth/ audit/ jobs/ metrics/ monitor/ notify/ storage/ diskops/ snapsched/ smart/ fileworker/
│  ├─ appsys/ appproxy/ services/ updates/ ups/ wsd/ sysinfo/ certs/ tlscert/ webui/
│  ├─ rtc/                    # SettingEngine, ICE mux, embedded STUN, signaling
│  ├─ connect/                # TangoConnect (signalhub, link proof, bundle manifest)
│  ├─ installer/ aptrepo/     # installer engine and web UI; apt repo builder
│  └─ agentclient/            # typed agent client
├─ pkg/
│  ├─ rtcrpc/                 # RPC + events over DataChannel (importable, rtckit candidate)
│  └─ agentproto/             # agent protocol types (versioned)
├─ protocol/
│  ├─ README.md               # the tango/1 contract (methods, events, errors)
│  └─ vectors/*.json          # test vectors shared by Go/TS
│                             # (planned: schema/*.json + gen/ codegen to Go and TS)
├─ web/                       # npm workspaces (see 05)
│  ├─ apps/{desktop,connect}/
│  ├─ e2e/                    # Playwright
│  └─ packages/{client,shell,ui,theme-milonga,widgets,app-*}/
│                             # app-*: apps, files, log, monitor, settings, snapshots, storage, terminal
├─ system/
│  ├─ systemd/                # tangod, tango-agent (.service + .socket)
│  ├─ apps/                   # App Center catalog
│  └─ iso/                    # image-based ISO (debootstrap + squashfs + graphical installer);
│                             # netinst + preseed kept as `make iso-netinst` (README.md, ADR 0008)
├─ build/                     # nfpm.yaml, package scripts, keys/ (public keys, dev signing seed)
├─ tests/
│  ├─ e2e/                    # Go end-to-end tests against the lab NAS (over tangoctl / WebRTC)
│  └─ e2e-ui/                 # Playwright artifacts and results
└─ docs/
```

A single `go.mod` at the root (go-link uses `backend-device/`; here the backend is the product, so
it goes at the root).

## Pinned stack

| Layer | Choice | Source of the convention |
|---|---|---|
| Server language | Go 1.26.x (`go.mod`: `go 1.26.0`) | go-link-desk |
| WebRTC | `github.com/pion/webrtc/v4` (v4.2.x), `pion/turn/v5`, `pion/ice/v4` | go-link-desk |
| Signaling | WebSocket, signalhub JSON envelope, `gorilla/websocket` | signalhub |
| State | JSON files in `/var/lib/tango` (`users.json`, `storage.json`, `audit.jsonl`…); SQLite (`modernc.org/sqlite`, no cgo) was planned and is not used | new |
| Password hashing | argon2id (`golang.org/x/crypto/argon2`) | go-link-desk |
| Frontend | React 19, TypeScript, Vite, Vitest, Playwright, npm workspaces | go-link-desk |
| State | hooks + `useSyncExternalStore` (no library) | go-link-desk |
| Icons / font | Lucide (ISC) / Inter (OFL) | go-link-desk (`fyneview` uses Inter + Lucide) |
| Terminal | xterm.js | go-link-desk |
| Native | `webview/webview_go` + `fyne.io/systray` (planned; `tango-desk` does not exist yet) | new (see [06](06-native-apps.md)) |
| Packaging | nFPM (`.deb`); Ed25519-signed desktop bundle (`tango-bundlesign`); signed apt repo (`tango-aptrepo`). NSIS and `signsums` planned with `tango-desk` | go-link-desk |
| Distribution | minimal Debian 13 + signed apt repo; image-based ISO (debootstrap + squashfs, graphical installer), netinst + preseed kept as `make iso-netinst`; rollback by swapping the `@rootfs` Btrfs subvolume | new |
| CI | planned: GitHub Actions (`go test`, `vitest`, Playwright, govulncheck, gitleaks, QEMU harness). Today there is no workflow; checks run locally (`make check`, `make e2e-*`) | go-link-desk + new |

## Makefile (real targets)

```
make web                 # npm install/ci in web/ + build of @tango/desktop → internal/webui/dist (embed), signed
                         # with tango-bundlesign (dev key by default, TANGO_BUNDLE_KEY for releases)
make tangod              # static linux/amd64 binary (CGO_ENABLED=0, -trimpath) in dist/tangod
make tango-agent         # static linux/amd64 privileged agent in dist/tango-agent
make tango-installer     # graphical ISO installer in dist/tango-installer
make tangoctl            # tango/1 CLI over WebRTC: dist/tangoctl -url 'wss://<nas>/signal?v=1' -insecure -user admin rpc storage.disks.list
make dev                 # go run ./cmd/tangod --dev → http://127.0.0.1:8080 (no TLS, local only)
make check               # gofmt -l cmd internal pkg, go vet, go test
make deb                 # dist/tangoos_<VERSION>_amd64.deb with nFPM (go run …/nfpm@v2.48.0)
make deploy-vm           # scp + apt-get install of the .deb on the development VM (VM=172.17.0.44)
make release-vm          # like deploy-vm, with the desktop signed with the release key (RELEASE_KEY)
make iso                 # image-based ISO, built on the Proxmox host over SSH (ISO_VARIANT=product|test)
make iso-netinst         # phase 1 ISO: netinst + preseed
make vm-ssh              # root shell on the VM
make clean
make e2e-api | e2e-security | e2e-storage | e2e-ui | e2e-connect | e2e-all   # end to end (docs/14, tests/README.md)
make connect-portal-dev  # portal build that trusts the dev bundle key (for e2e-connect)
make connect-web-build | connect-web | connect-web-preview  # TangoOS Connect portal (docs/16)
make apt-repo | apt-publish  # signed apt repository in dist/apt and its upload
```

Usual cycle: `make web deploy-vm`. Still planned: `proto` (codegen), `vm` (create VM), `test-storage`
(QEMU harness), `desk-windows`, `desk-linux`.

## Conventions

- RPC methods: `domain.resource.verb`. Events: `domain.resource[.detail]`.
- Error codes: `SCREAMING_SNAKE`, stable, documented in `protocol/README.md` (planned: `protocol/schema/errors.json`).
- Commits: Conventional Commits. Every PR that touches `protocol/` updates the vectors and the TS types in `web/packages/client` (regenerated once codegen exists).
- All demo data is labeled in the UI with the "DEMO" badge.
- No change to `tango-agent` without a negative test (malformed payload and unknown op).
