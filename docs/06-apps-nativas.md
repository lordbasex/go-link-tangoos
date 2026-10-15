# 06 — Apps nativas (Windows y Linux; macOS después)

## Requisito

"Las apps nativas y la web tienen que ser lo mismo": el mismo escritorio con tema macOS.

## Opciones evaluadas

| Opción | Mismo escritorio | WebRTC | Coherencia con go-link | Veredicto |
|---|---|---|---|---|
| **Fyne** (como go-link-desk) | ❌ habría que reescribir todo el shell y las apps en Fyne | pion | ✅ | Descartada para la ventana principal. Duplica todo el trabajo de UI |
| Electron | ✅ | Chromium | ❌ ~150 MB, otro runtime | Descartada |
| Tauri | ✅ | WebView del SO | ❌ Rust | Descartada |
| Wails v3 | ✅ | WebView del SO | ~ Go, pero agrega un framework | Alternativa válida |
| **Go + `webview/webview_go` + pion en Go + bridge** | ✅ | **pion** (no depende del webview) | ✅ Go puro + cgo, como go-link | **Elegida** |

## Diseño: `tango-desk`

```
┌──────────────────────── tango-desk (Go) ─────────────────────────┐
│  WebView2 (Windows) / WebKitGTK 4.1 (Linux)                       │
│   └─ mismo bundle web/apps/desktop (embebido con embed.FS)        │
│        └─ packages/client → NativeBridgeTransport                 │
│              ⇅  window.tango.send / onMessage (bind de webview)   │
│  Go: signalclient + pion/webrtc v4 → DataChannels → NAS           │
│  Go: descubrimiento mDNS (_tango._tcp), pinning DTLS, keyring     │
│  Go: bandeja (fyne.io/systray), notificaciones nativas            │
│  Go: transferencias directas disco local ⇄ NAS (sin pasar por JS) │
└──────────────────────────────────────────────────────────────────┘
```

**Por qué el WebRTC va en Go y no en el webview:**

1. WebKitGTK casi nunca trae WebRTC habilitado en las distros (depende de GStreamer
   webrtc y flags de compilación). En Linux sería una lotería.
2. pion↔pion permite chunks de 256 KiB y ventanas grandes, así que se llega a velocidad de
   red. El navegador no.
3. Los archivos se leen y escriben **directo desde Go** al disco local; JS solo ve el
   progreso. Así no pasan GB por el bridge.

El bridge transporta solo mensajes de `rpc` y `events` (JSON) y los metadatos de
transferencia. `packages/client` define una interfaz `Transport`; las apps no saben si corren
en el navegador o en la app nativa.

## Funciones extra de la app nativa

| Función | Hito |
|---|---|
| Descubrir NAS en la LAN (mDNS) y vincular (pinning de fingerprint DTLS + token de dispositivo) | M3 |
| Recordar credenciales en el keyring del SO (Windows Credential Manager / Secret Service) | M3 |
| Montar carpetas compartidas como unidad (lanza el mount SMB nativo: `net use` / `gio mount`) | M3 |
| Notificaciones nativas de eventos del NAS (disco degradado, job terminado) | M3 |
| Arrastrar desde el Explorador/Nautilus a una ventana → subida directa desde Go | M3 |
| Cliente de sincronización tipo "Drive" (carpeta local ⇄ share) | M6 |
| Abrir archivo del NAS con app local (descarga temporal + re-subida al guardar) | M5 |

## Empaquetado (igual que go-link-desk)

- Windows: instalador NSIS, `tango-desk.exe` firmado. WebView2 Evergreen ya viene con
  Windows 10/11; si falta, el instalador lo descarga.
- Linux: `.deb` / `.rpm` / Arch con **nFPM**. Depende de `libwebkit2gtk-4.1-0`.
- Updates: `SHA256SUMS` firmado con **Ed25519** (`build/signsums`), con el mismo verificador.
- Builds cruzados en Docker (`build/{linux,windows}.Dockerfile`).
- macOS (después): mismo diseño con WKWebView. Firma + notarización como go-link.

## Reutilización de go-link-desk

Se puede importar directo (módulos MIT tuyos):

- `github.com/lordbasex/go-link-desk/backend-device/pkg/signalclient`
- `github.com/lordbasex/signalhub/pkg/{turncred,ratelimit,ids}`

Hay que copiar y adaptar, porque está en `internal/`: la configuración del `SettingEngine`
de `stream_service.go`, la lógica de ICE restart, `files.ts` (upload/download con
backpressure) y `signal-client.ts`. **Recomendación:** extraer esas piezas a un módulo
común (`github.com/lordbasex/rtckit`) que usen tanto go-link como TangoOS, en vez de
mantener dos copias.
