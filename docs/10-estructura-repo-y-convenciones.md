# 10 — Estructura del repo y convenciones

## Monorepo

```
tangoos/
├─ go.mod                     # module github.com/lordbasex/go-link-tangoos
├─ cmd/
│  ├─ tangod/                 # servidor: HTTPS + /signal + STUN/TURN + pion + RPC + módulos
│  ├─ tango-agent/            # root, socket Unix, ops tipadas
│  ├─ tango-desk/             # app nativa Win/Linux (webview + pion)
│  └─ tangoctl/               # CLI: rpc, diagnóstico, bench
├─ internal/
│  ├─ auth/ users/ shares/ files/ storage/ snapshots/ jobs/ audit/ metrics/ apps/ desktop/
│  ├─ rtc/                    # SettingEngine, ICE mux, STUN/TURN embebido, señalización
│  ├─ agentclient/            # cliente tipado del agente
│  └─ db/                     # SQLite + migraciones
├─ pkg/
│  ├─ rtcrpc/                 # RPC + eventos sobre DataChannel (importable, candidato a rtckit)
│  └─ agentproto/             # tipos del protocolo del agente (versionado)
├─ protocol/
│  ├─ schema/*.json           # JSON Schema por método y por evento (fuente de verdad)
│  ├─ vectors/*.json          # vectores de prueba compartidos Go/TS
│  └─ gen/                    # generador → internal/proto (Go) y web/packages/protocol (TS)
├─ web/                       # npm workspaces (ver 05)
│  ├─ apps/desktop/
│  └─ packages/{protocol,client,shell,ui,theme-milonga,app-*}/
├─ system/
│  ├─ systemd/ samba/ nftables/ udev/ avahi/   # plantillas
│  ├─ iso/                    # simple-cdd + preseed (ISO de instalación)
│  └─ branding/               # GRUB, Plymouth, motd, os-release
├─ build/                     # Dockerfiles cross, nfpm.yaml, NSIS, signsums
├─ tests/
│  ├─ storage/                # harness QEMU + escenarios
│  └─ e2e/                    # Playwright (web) + tangoctl (protocolo)
└─ docs/
```

Un solo `go.mod` en la raíz (go-link usa `backend-device/`; acá el backend es el producto, así
que va en la raíz).

## Stack fijado

| Capa | Elección | Fuente de la convención |
|---|---|---|
| Lenguaje servidor | Go 1.26.x (`go.mod` declara 1.25 hasta actualizar el toolchain local a 1.26) | go-link-desk |
| WebRTC | `github.com/pion/webrtc/v4` (v4.2.x), `pion/turn/v4` | go-link-desk |
| Señalización | WebSocket, sobre JSON de signalhub, `gorilla/websocket` | signalhub |
| DB | SQLite (`modernc.org/sqlite`, sin cgo) | nuevo |
| Hash de contraseñas | argon2id (`golang.org/x/crypto/argon2`) | go-link-desk |
| Frontend | React 19, TypeScript, Vite, Vitest, Playwright, npm workspaces | go-link-desk |
| Estado | hooks + `useSyncExternalStore` (sin librería) | go-link-desk |
| Íconos / fuente | Lucide (ISC) / Inter (OFL) | go-link-desk (`fyneview` usa Inter + Lucide) |
| Terminal | xterm.js | go-link-desk |
| Nativo | `webview/webview_go` + `fyne.io/systray` | nuevo (ver [06](06-apps-nativas.md)) |
| Paquetes | nFPM, NSIS, `signsums` Ed25519 | go-link-desk |
| Distribución | Debian 13 minimal + repo apt firmado; ISO con debian-installer + preseed (simple-cdd); rollback con snapshots Btrfs + grub-btrfs | nuevo |
| CI | GitHub Actions: `go test`, `vitest`, Playwright, govulncheck, gitleaks, harness QEMU | go-link-desk + nuevo |

## Makefile (objetivos reales)

```
make web         # npm install/ci en web/ + build de @tango/desktop → copia a internal/webui/dist (embed)
make tangod      # binario estático linux/amd64 (CGO_ENABLED=0, -trimpath) en dist/tangod
make dev         # go run ./cmd/tangod --dev → http://127.0.0.1:8080 (sin TLS, solo local)
make check       # gofmt -l cmd internal pkg, go vet, go test
make tangoctl    # CLI tango/1 sobre WebRTC: dist/tangoctl -url 'wss://<nas>/signal?v=1' -insecure -user admin rpc storage.disks.list
make deb         # dist/tangoos_<VERSION>_amd64.deb con nFPM (go run …/nfpm@v2.48.0)
make deploy-vm   # scp + apt-get install del .deb en la VM de desarrollo (VM=172.17.0.44)
make vm-ssh      # shell root en la VM
make clean
```

Ciclo habitual: `make web deploy-vm`. Pendientes de M1+: `proto`, `vm` (crear VM), `test-storage`,
`apt-repo`, `iso`, `desk-windows`, `desk-linux`.

## Convenciones

- Métodos RPC: `dominio.recurso.verbo`. Eventos: `dominio.recurso[.detalle]`.
- Códigos de error: `SCREAMING_SNAKE`, estables, documentados en `protocol/schema/errors.json`.
- Commits: Conventional Commits. Cada PR que toca `protocol/` incluye los vectores y los tipos regenerados.
- Todo dato demo se rotula en la UI con la insignia "DEMO".
- Ningún cambio en `tango-agent` sin test negativo (payload malformado y op desconocida).
