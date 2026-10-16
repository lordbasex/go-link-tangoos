# 06 — Native apps (Windows and Linux; macOS later)

**Status:** the choice below (Go + system webview) was **replaced by Electron** in
[ADR 0018](adr/0018-native-app-electron.md): a WebKit parity check showed that the glass effect (`backdrop-filter`)
is not drawn without accelerated compositing, so only Chromium everywhere guarantees the same look. The app lives in
`web/apps/native`. The analysis below is kept for history.


## What exists (ADR 0018)

`web/apps/native` is an Electron app ("TangoOS" on the desktop):

- **Launcher**: NAS devices found on the LAN (mDNS `_tangoos._tcp`, announced by avahi from the TangoOS package), the
  saved ones, "add by address", and "from outside (TangoConnect)". The first connection shows the NAS's certificate
  fingerprint (the same one Settings shows) and pins it; a different certificate is refused with a clear error page.
  Language switch (Español / English).
- **NAS window**: loads `https://<nas>/`, the NAS's own desktop, in its own session (separate storage per NAS),
  sandboxed, navigation limited to that NAS (other links open in the system browser). It is the web page itself:
  compared with Chrome on the same NAS, 0.10 % of the pixels differ, all of them live data (clock, network, uptime).
- **Tray** with the saved NAS devices; closing windows keeps the app in the tray. `window.tangoNative` exposes
  `openShare(name)` (opens `\\nas\share` or `smb://nas/share`) and `launcher()`.
- Commands: `make native-run` (build and open), `make native-dist` (AppImage, deb, rpm and NSIS installers built in
  the `electronuserland/builder` image), `make e2e-native NATIVE_NAS=<ip>` (Playwright drives the app against a real
  NAS: launcher, fingerprint, login, and a forged pin refused).

## Requirement

"The native apps and the web have to be the same": the same desktop with the macOS theme.

## Options evaluated

| Option | Same desktop | WebRTC | Consistency with go-link | Verdict |
|---|---|---|---|---|
| **Fyne** (like go-link-desk) | ❌ the whole shell and all apps would have to be rewritten in Fyne | pion | ✅ | Ruled out for the main window. It duplicates all the UI work |
| Electron | ✅ | Chromium | ❌ ~150 MB, another runtime | Ruled out |
| Tauri | ✅ | OS WebView | ❌ Rust | Ruled out |
| Wails v3 | ✅ | OS WebView | ~ Go, but adds a framework | Valid alternative |
| **Go + `webview/webview_go` + pion in Go + bridge** | ✅ | **pion** (does not depend on the webview) | ✅ pure Go + cgo, like go-link | **Chosen** |

## Design: `tango-desk`

```
┌──────────────────────── tango-desk (Go) ─────────────────────────┐
│  WebView2 (Windows) / WebKitGTK 4.1 (Linux)                       │
│   └─ same bundle web/apps/desktop (embedded with embed.FS)        │
│        └─ packages/client → NativeBridgeTransport                 │
│              ⇅  window.tango.send / onMessage (webview bind)      │
│  Go: signalclient + pion/webrtc v4 → DataChannels → NAS           │
│  Go: mDNS discovery (_tango._tcp), DTLS pinning, keyring          │
│  Go: tray (fyne.io/systray), native notifications                 │
│  Go: direct local disk ⇄ NAS transfers (not going through JS)     │
└──────────────────────────────────────────────────────────────────┘
```

**Why WebRTC runs in Go and not in the webview:**

1. WebKitGTK almost never ships with WebRTC enabled in distros (it depends on GStreamer
   webrtc and build flags). On Linux it would be a lottery.
2. pion↔pion allows 256 KiB chunks and large windows, so it reaches network
   speed. The browser does not.
3. Files are read and written **directly from Go** to the local disk; JS only sees the
   progress. That way GBs do not go through the bridge.

The bridge carries only `rpc` and `events` messages (JSON) and transfer
metadata. `packages/client` defines a `Transport` interface; apps do not know whether they run
in the browser or in the native app.

## Extra features of the native app

| Feature | Milestone |
|---|---|
| Discover NAS devices on the LAN (mDNS) and pair (DTLS fingerprint pinning + device token) | M3 |
| Remember credentials in the OS keyring (Windows Credential Manager / Secret Service) | M3 |
| Mount shared folders as a drive (launches the native SMB mount: `net use` / `gio mount`) | M3 |
| Native notifications for NAS events (degraded disk, job finished) | M3 |
| Drag from Explorer/Nautilus to a window → direct upload from Go | M3 |
| "Drive"-style sync client (local folder ⇄ share) | M6 |
| Open a NAS file with a local app (temporary download + re-upload on save) | M5 |

## Packaging (same as go-link-desk)

- Windows: NSIS installer, signed `tango-desk.exe`. WebView2 Evergreen already ships with
  Windows 10/11; if it is missing, the installer downloads it.
- Linux: `.deb` / `.rpm` / Arch with **nFPM**. Depends on `libwebkit2gtk-4.1-0`.
- Updates: `SHA256SUMS` signed with **Ed25519** (`build/signsums`), with the same verifier.
- Cross builds in Docker (`build/{linux,windows}.Dockerfile`).
- macOS (later): same design with WKWebView. Signing + notarization like go-link.

## Reuse from go-link-desk

Can be imported directly (your own MIT modules):

- `github.com/lordbasex/go-link-desk/backend-device/pkg/signalclient`
- `github.com/lordbasex/signalhub/pkg/{turncred,ratelimit,ids}`

Has to be copied and adapted, because it lives in `internal/`: the `SettingEngine`
configuration from `stream_service.go`, the ICE restart logic, `files.ts` (upload/download with
backpressure) and `signal-client.ts`. **Recommendation:** extract those pieces into a shared
module (`github.com/lordbasex/rtckit`) used by both go-link and TangoOS, instead of
maintaining two copies.
