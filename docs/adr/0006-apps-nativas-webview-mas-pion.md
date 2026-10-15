# ADR 0006 — Apps nativas: webview + pion en Go

- Estado: Aceptado
- Contexto: las apps nativas deben mostrar el mismo escritorio que la web. Fyne obligaría a reescribir toda la UI. WebKitGTK no garantiza WebRTC.
- Decisión: `tango-desk` en Go embebe el mismo bundle en WebView2/WebKitGTK. WebRTC y las transferencias corren en Go (pion), y la UI habla por un bridge (`NativeBridgeTransport`).
- Consecuencias: el mismo código de UI en todas partes y transferencias rápidas. Dependencia de cgo para el webview. Alternativa de reserva: Wails v3.
