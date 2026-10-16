# ADR 0018 — Native app with Electron that opens the NAS's own desktop

- Status: Accepted (replaces the "Go + webview" choice of [06](../06-native-apps.md))
- Context: the native app (Windows, Linux, later macOS) has to look **exactly** like the web desktop. A parity check
  (Chromium vs WebKit, the engine of WebKitGTK and WKWebView) showed identical layout but no `backdrop-filter` in
  WebKit without accelerated compositing: windows let the one behind show through. That is common on Linux (VMs,
  some drivers) and the plan B (opaque fallback) would still not be identical.
- Decision:
  1. **Electron**: one Chromium everywhere, so the desktop renders exactly as in Chrome on every OS. Same language as
     the web (TypeScript).
  2. **The window loads the NAS's own desktop** (`https://<nas>/`, served by tangod) instead of a copy built into the
     app. It is literally the web page, and it always matches the NAS's version: updating TangoOS updates the
     interface without reinstalling the app. From outside the LAN the app opens the TangoConnect portal, which
     connects through signalhub and mounts the NAS's signed desktop (docs/16).
  3. **Launcher**: a local screen lists NAS devices found on the LAN (mDNS `_tangoos._tcp`, announced by avahi from the
     TangoOS package), the ones already added, and "add by address". The NAS uses a self-signed certificate: the first
     connection shows its SHA-256 fingerprint (the same one Settings shows) and the app **pins** it; afterwards a
     different certificate is refused with a clear message (trust on first use, like SSH).
  4. **Security of a window that shows remote content**: `contextIsolation`, `sandbox`, no Node in the page;
     navigation is limited to the NAS origin (and the portal for remote access), new windows open in the system
     browser, and the certificate check accepts only the pinned fingerprint for that host. The preload exposes a small
     `window.tangoNative` (version, OS notifications, open a share as a network drive) that the desktop can use when
     present.
  5. Native extras on top: tray icon, notifications, open shares with the OS (`\\host\share` on Windows,
     `smb://host/share` via `gio` on Linux), drag and drop as in the browser. Packaging with electron-builder:
     NSIS (Windows), AppImage/deb/rpm (Linux); updates later with electron-updater and signed releases.
- Consequences: a bigger download (~100 MB) and more memory than a system webview, accepted for pixel-identical
  rendering. File transfers use the browser's WebRTC like the web; a Go helper can be added later if large transfers
  need more speed. Playwright drives the Electron app directly (`_electron`) for end-to-end tests.
