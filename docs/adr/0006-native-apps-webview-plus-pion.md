# ADR 0006 — Native apps: webview + pion in Go

- Status: Accepted
- Context: the native apps must show the same desktop as the web. Fyne would force rewriting the whole UI. WebKitGTK does not guarantee WebRTC.
- Decision: `tango-desk` in Go embeds the same bundle in WebView2/WebKitGTK. WebRTC and transfers run in Go (pion), and the UI talks through a bridge (`NativeBridgeTransport`).
- Consequences: the same UI code everywhere and fast transfers. A cgo dependency for the webview. Fallback alternative: Wails v3.
