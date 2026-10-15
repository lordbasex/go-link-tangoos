# 03 — Protocolo WebRTC

Basado en las convenciones de go-link-desk (`docs/protocol.md`): sobres JSON `{type, …}`,
canales por propósito creados por el host, chunks binarios con cabecera de offset, backpressure
con `bufferedAmountLowThreshold` y ping/pong. La diferencia es que acá **el host es el NAS y no
hay una persona del otro lado que acepte**: la autorización es por usuario y contraseña.

Versión del protocolo: `tango/1`. El contrato vive en `protocol/` (JSON Schema). Los tipos Go
y TS se **generan** desde ahí, igual que `keycodes.json` → `keymap` / `keycodes.gen.ts`.

## 1. Bootstrap (lo único que va por HTTP)

```
GET  https://tango.local/             → index.html + bundle (embed.FS, cache inmutable con hash)
GET  https://tango.local/sw.js        → Service Worker (§8)
WSS  https://tango.local/signal?v=1   → señalización
```

No hay endpoints de negocio por HTTP. El WebSocket de señalización se cierra en cuanto la
`RTCPeerConnection` pasa a `connected`, y se reabre solo para un ICE restart.

## 2. Señalización (sobre compatible con signalhub)

```jsonc
// servidor → cliente, al abrir el WS
{"type":"hello","v":1,"server":"tangod/0.1.0","nas_id":"tg_7f3a…",
 "ice_servers":[{"urls":["stun:tango.local:3478"]},
                {"urls":["turn:tango.local:3478?transport=tcp"],"username":"…","credential":"…"}],
 "dtls_fingerprint":"sha-256 AB:CD:…"}

// cliente → servidor
{"type":"signal","payload":{"kind":"hello_client","client":"web|desk-win|desk-linux","resume":"<opcional>"}}

// el NAS siempre ofrece (igual que en go-link: el host hace la offer)
{"type":"signal","payload":{"kind":"offer","sdp":"…"}}
{"type":"signal","payload":{"kind":"answer","sdp":"…"}}
{"type":"signal","payload":{"kind":"candidate","candidate":{…}}}   // trickle ICE
```

- Las credenciales TURN son efímeras (HMAC, TURN REST API), con el mismo esquema que
  `signalhub/pkg/turncred`.
- Antes del login la señalización tiene rate-limit por IP (`signalhub/pkg/ratelimit`).
- **`dtls_fingerprint` en el `hello`**: el cliente verifica que el certificado DTLS que aparece
  en la SDP coincida. Las apps nativas además lo **fijan** (pinning) la primera vez que se
  vinculan.

## 3. ICE en LAN

| Ajuste pion | Valor | Por qué |
|---|---|---|
| `SettingEngine.SetICEUDPMux` | un puerto, por defecto `8443/udp` | una sola regla de firewall |
| `SetICEMulticastDNSMode` | `QueryOnly` | resolver los candidatos `xxxx.local` de Chrome/Safari; el NAS publica sus IPs reales (no son secreto en la LAN y el cliente no depende de resolver mDNS) |
| `SetNAT1To1IPs` | solo si se configura | NAS detrás de NAT (remoto) |
| ICE-TCP (`SetICETCPMux`) | `8443/tcp` activado | redes que bloquean UDP sin TURN |
| Interfaces | excluye `docker*`, `veth*`, `br-*` | evita candidatos inútiles hacia contenedores |
| Restart | a los 3 s en `disconnected` o en `failed`, hasta 3 intentos | igual que go-link |

## 4. Canales

Los crea el **NAS** apenas se conecta, con `negotiated:false`. Todos los mensajes JSON pesan
≤ 64 KiB.

| Label | Orden / confiabilidad | Formato | Uso |
|---|---|---|---|
| `rpc` | ordenado, confiable | JSON | requests/responses + suscripciones |
| `events` | ordenado, confiable | JSON | eventos empujados (jobs, cambios de datos, notificaciones) |
| `metrics` | **desordenado, `maxRetransmits:0`** | binario | CPU/RAM/red/I/O a 2–4 Hz; si se pierde un frame no importa |
| `files.0…N` | ordenado, confiable | binario + control JSON | transferencias (pool de N = 4 por defecto) |
| `term` | ordenado, confiable | binario, prefijo `term_id` de 4 bytes | Terminal (igual que go-link) |
| `ping` | desordenado | JSON | liveness cada 2 s, RTT para la barra de estado |

`rpc` y `events` van separados para que una ráfaga de eventos (p. ej. miles de
`files.changed`) no frene la respuesta a un clic.

## 5. RPC (`rpc`)

```jsonc
// request
{"type":"req","id":42,"method":"storage.disks.list","params":{}}
// respuesta OK
{"type":"res","id":42,"result":{"disks":[…]}}
// error: códigos estables, nunca salida cruda de shell
{"type":"err","id":42,"error":{"code":"DEVICE_NOT_ELIGIBLE","message":"El disco contiene particiones montadas","details":{"wwn":"0x5000…"}}}
// cancelar un request largo
{"type":"cancel","id":42}
```

- `id`: entero creciente por conexión. Timeout por defecto: 15 s en el cliente (por método
  se puede definir otro).
- Antes del login, la conexión está **gated**: solo se aceptan `auth.*` y `system.info`.
  Cualquier otra cosa devuelve `UNAUTHENTICATED` y se cuenta para el rate-limit.
- Toda operación larga devuelve `{job_id}` de inmediato. El progreso llega por `events`.
- Del lado Go, un **registro de handlers** `method → func(ctx, Session, Params) (Result, error)`,
  como el registro `type → handler` de `controllers/control.go`, pero con authz declarativa:

```go
rpc.Handle("shares.create", shares.Create,
    rpc.Require(role.Admin), rpc.Audit("share.create"), rpc.Schema(proto.SharesCreateParams{}))
```

Convención de nombres: `dominio.recurso.verbo` (`storage.volumes.create`,
`files.list`, `users.update`, `snapshots.restore`).

## 6. Eventos y suscripciones (`events`)

```jsonc
// el cliente se suscribe vía rpc
{"type":"req","id":7,"method":"events.subscribe","params":{"topics":["jobs.*","storage.disks","files.changed:/volume1/Familia"]}}
// el servidor empuja por `events`
{"type":"evt","topic":"jobs.progress","seq":1834,"data":{"job_id":"j_81","pct":62,"stage":"running"}}
{"type":"evt","topic":"storage.disks","seq":1835,"data":{"op":"changed","disk":{…}}}
```

- `seq` es un número monótono por conexión. Al reconectar, el cliente manda `resume_from: seq`
  y el servidor reenvía desde un ring buffer (5 000 eventos o 60 s). Si el hueco es mayor,
  manda `{"type":"evt","topic":"sync.reset"}` y la UI vuelve a pedir el estado.
- Patrón en la UI: **`query + subscribe`**. Un hook `useLive("storage.disks.list",
  "storage.disks")` pide el estado inicial y aplica deltas. Esto reemplaza a TanStack Query.
- El cambio de archivos se detecta con fanotify/inotify en el file-worker, con debounce de
  250 ms y suscripción por carpeta abierta (no global).

## 7. Transferencia de archivos (`files.N`)

Igual que en go-link, pero con un pool de canales y ventanas más grandes:

```
Subida
  rpc  files.upload.begin {path, size, mtime, overwrite:false} → {xfer_id, channel:"files.2", chunk:262144}
  bin  [8B offset BE][≤256 KiB datos] …         (backpressure: ventana 4 MiB, lowThreshold = 2 MiB)
  rpc  files.upload.end {xfer_id, sha256?}      → el file-worker hace fsync + rename atómico desde .tango-part
Descarga (pull, como go-link)
  rpc  files.download.begin {path} → {xfer_id, size, channel}
  json {"type":"read","xfer_id":…,"offset":…,"length":…}    (hasta 16 en vuelo)
  bin  [8B offset][datos]
```

- **Tamaño de chunk:** 256 KiB entre pion y pion (app nativa). En el navegador se negocia
  según `sctp.maxMessageSize`; Chrome acepta ≥ 256 KiB y Firefox también, pero arrancamos
  con 64 KiB y medimos.
- **Una subida interrumpida nunca pisa el original:** se escribe en `.tango-part` y se hace
  rename al terminar. Se puede reanudar con `files.upload.resume {xfer_id}`.
- **Gate de rendimiento en M0 (obligatorio):** medir navegador↔pion y pion↔pion en 1 GbE y
  2.5 GbE, con 1/2/4/8 canales y chunks de 16/64/256 KiB. Objetivo: ≥ 80 % del enlace en la app
  nativa y ≥ 300 Mb/s en Chrome. Si Chrome no llega, se acepta: el uso pesado va por SMB o
  por la app nativa, y se documenta.
- **Medición M3 (2026-10-14), pion↔pion con `tangoctl`, Mac ↔ VM 108 por LAN, 1 canal, frames de 64 KiB,
  1 GiB con sha256 verificado:** subida **36,8 MB/s**, bajada **45,3 MB/s** (~300–360 Mb/s). El disco es
  un RAID 1 virtual sobre LVM-thin del Proxmox. Pendiente: medir con el navegador y con frames de 256 KiB / varios canales.

## 8. Service Worker: URLs que en realidad son DataChannel

`<img>`, `<video>`, `<a download>` y los visores de PDF necesitan una URL. TangoOS registra
un SW que intercepta `https://tango.local/dc/*`:

```
<video src="/dc/stream/volume1/Videos/peli.mkv">
  → SW fetch → MessageChannel → página principal → files.N read {offset,length} → Response(ReadableStream)
```

- Soporta `Range` (se puede saltar en el video) y descargas grandes en streaming sin cargarlas
  en memoria, con la técnica de StreamSaver.
- El SW **no tiene la conexión WebRTC**: le pide los datos a la pestaña que la tiene. Si no
  hay ninguna, responde 503.
- Requiere un contexto seguro con **certificado de confianza** ([07 §3](07-seguridad.md)).
  Sin SW (p. ej. con un certificado autofirmado sin aceptar), las descargas caen a un
  `Blob` en memoria con un límite de 512 MiB, y las vistas previas de video se desactivan.
- Las miniaturas las genera el NAS (con `vipsthumbnail` y `ffmpegthumbnailer` desde el
  file-worker) y se cachean en el SW por `etag`.

## 9. Reconexión y multi-pestaña

- Si cae el ICE: restart sobre la misma PC. Si cae la PC: nueva señalización con
  `resume` (token de sesión, [07](07-seguridad.md)) y re-suscripción con `resume_from`.
- **Una conexión por pestaña**: es lo simple y lo que hace go-link. Para no abrir 10 PCs con
  10 pestañas, la segunda pestaña puede usar `BroadcastChannel` hacia la primera. Se evalúa
  en M2, no es necesario para el MVP.

### Implementación actual (M0)

- El WebSocket de señalización queda **atado a su PeerConnection**: mientras está abierto, el NAS
  hace los ICE restart (3 s en `disconnected`, inmediato en `failed`, hasta 3). Si el WS ya se
  cerró cuando ICE falla, la sesión termina y el cliente abre una PC nueva y llama `auth.resume`.
- El STUN embebido (pion/turn sin `AuthHandler`) responde Binding y rechaza toda allocation:
  es solo STUN. TURN con credenciales HMAC efímeras llega con TangoConnect.

## 10. Herramientas

- `tangoctl rpc storage.disks.list` es una CLI Go con pion que se loguea y llama métodos.
  Sirve para scripts y para tests e2e del protocolo.
- Un **vector de pruebas compartido** (`protocol/vectors/*.json`) corre en Go y en TS
  (Vitest), como `vectors/input.json` en go-link.
- Panel "Debug WebRTC" en la app Monitor: estado ICE, par de candidatos elegido, RTT,
  `bufferedAmount` por canal y throughput.
