# 03 — WebRTC protocol

Based on the go-link-desk conventions (`docs/protocol.md`): JSON envelopes `{type, …}`,
purpose-specific channels created by the host, binary chunks with an offset header, backpressure
with `bufferedAmountLowThreshold`, and ping/pong. The difference is that here **the host is the NAS and
there is no person on the other side to accept**: authorization is by username and password.

Protocol version: `tango/1`. The contract lives in `protocol/`. The plan is JSON Schema with the Go and TS
types **generated** from it, the same way as `keycodes.json` → `keymap` / `keycodes.gen.ts`; today the
contract is `protocol/README.md` plus shared vectors, and the types are handwritten (Go in `pkg/` and
`internal/`, TS in `web/packages/client/src/protocol*.ts`).

## 1. Bootstrap (the only thing that goes over HTTP)

```
GET  https://tango.local/             → index.html + bundle (embed.FS, immutable cache with hash)
GET  https://tango.local/sw.js        → Service Worker (§8)
WSS  https://tango.local/signal?v=1   → signaling
```

There are no business endpoints over HTTP. The signaling WebSocket closes as soon as the
`RTCPeerConnection` reaches `connected`, and is reopened only for an ICE restart.

## 2. Signaling (signalhub-compatible envelope)

```jsonc
// server → client, when the WS opens
{"type":"hello","v":1,"server":"tangod/0.1.0","nas_id":"tg_7f3a…",
 "ice_servers":[{"urls":["stun:tango.local:3478"]},
                {"urls":["turn:tango.local:3478?transport=tcp"],"username":"…","credential":"…"}],
 "dtls_fingerprint":"sha-256 AB:CD:…"}

// client → server
{"type":"signal","payload":{"kind":"hello_client","client":"web|desk-win|desk-linux","resume":"<optional>"}}

// the NAS always offers (same as go-link: the host makes the offer)
{"type":"signal","payload":{"kind":"offer","sdp":"…"}}
{"type":"signal","payload":{"kind":"answer","sdp":"…"}}
{"type":"signal","payload":{"kind":"candidate","candidate":{…}}}   // trickle ICE
```

- TURN credentials are ephemeral (HMAC, TURN REST API), with the same scheme as
  `signalhub/pkg/turncred`.
- Before login, signaling is rate-limited per IP (`signalhub/pkg/ratelimit`).
- **`dtls_fingerprint` in the `hello`**: the client checks that the DTLS certificate that appears
  in the SDP matches. Native apps also **pin** it the first time they
  are linked.

## 3. ICE on the LAN

| pion setting | Value | Why |
|---|---|---|
| `SettingEngine.SetICEUDPMux` | one port, `8443/udp` by default | a single firewall rule |
| `SetICEMulticastDNSMode` | `QueryOnly` | resolve the `xxxx.local` candidates from Chrome/Safari; the NAS publishes its real IPs (they are not secret on the LAN and the client does not depend on resolving mDNS) |
| `SetNAT1To1IPs` | only if configured | NAS behind NAT (remote) |
| ICE-TCP (`SetICETCPMux`) | `8443/tcp` enabled | networks that block UDP without TURN |
| Interfaces | excludes `docker*`, `veth*`, `br-*` | avoids useless candidates towards containers |
| Restart | after 3 s in `disconnected` or in `failed`, up to 3 attempts | same as go-link |

## 4. Channels

The **NAS** creates them as soon as it connects, with `negotiated:false`. Every JSON message is
≤ 64 KiB.

| Label | Ordering / reliability | Format | Use |
|---|---|---|---|
| `rpc` | ordered, reliable | JSON | requests/responses + subscriptions |
| `events` | ordered, reliable | JSON | pushed events (jobs, data changes, notifications) |
| `metrics` | **unordered, `maxRetransmits:0`** | binary | CPU/RAM/network/I/O at 2–4 Hz; losing a frame does not matter |
| `files.0…N` | ordered, reliable | binary + JSON control | transfers (pool of N = 4 by default) |
| `term` | ordered, reliable | binary, 4-byte `term_id` prefix | Terminal (same as go-link) |
| `ping` | unordered | JSON | liveness every 2 s, RTT for the status bar |

`rpc` and `events` are kept separate so that a burst of events (e.g. thousands of
`files.changed`) does not hold up the response to a click.

## 5. RPC (`rpc`)

```jsonc
// request
{"type":"req","id":42,"method":"storage.disks.list","params":{}}
// OK response
{"type":"res","id":42,"result":{"disks":[…]}}
// error: stable codes, never raw shell output
{"type":"err","id":42,"error":{"code":"DEVICE_NOT_ELIGIBLE","message":"The disk contains mounted partitions","details":{"wwn":"0x5000…"}}}
// cancel a long request
{"type":"cancel","id":42}
```

- `id`: increasing integer per connection. Default timeout: 15 s on the client (a different one can
  be set per method).
- Before login, the connection is **gated**: only `auth.*` and `system.info` are accepted.
  Anything else returns `UNAUTHENTICATED` and counts towards the rate limit.
- Every long operation returns `{job_id}` immediately. Progress arrives over `events`.
- On the Go side, a **handler registry** `method → func(ctx, Session, Params) (Result, error)`,
  like the `type → handler` registry in `controllers/control.go`, but with declarative authz:

```go
rpc.Handle("shares.create", shares.Create,
    rpc.Require(role.Admin), rpc.Audit("share.create"), rpc.Schema(proto.SharesCreateParams{}))
```

Naming convention: `domain.resource.verb` (`storage.volumes.create`,
`files.list`, `users.update`, `snapshots.restore`).

## 6. Events and subscriptions (`events`)

```jsonc
// the client subscribes via rpc
{"type":"req","id":7,"method":"events.subscribe","params":{"topics":["jobs.*","storage.disks","files.changed:/volume1/Familia"]}}
// the server pushes over `events`
{"type":"evt","topic":"jobs.progress","seq":1834,"data":{"job_id":"j_81","pct":62,"stage":"running"}}
{"type":"evt","topic":"storage.disks","seq":1835,"data":{"op":"changed","disk":{…}}}
```

- `seq` is a monotonic number per connection. On reconnect, the client sends `resume_from: seq`
  and the server replays from a ring buffer (5,000 events or 60 s). If the gap is larger,
  it sends `{"type":"evt","topic":"sync.reset"}` and the UI requests the state again.
- UI pattern: **`query + subscribe`**. A `useLive("storage.disks.list",
  "storage.disks")` hook requests the initial state and applies deltas. This replaces TanStack Query.
- File changes are detected with fanotify/inotify in the file-worker, with a 250 ms
  debounce and a subscription per open folder (not global).

## 7. File transfer (`files.N`)

Same as in go-link, but with a pool of channels and larger windows:

```
Upload
  rpc  files.upload.begin {path, size, mtime, overwrite:false} → {xfer_id, channel:"files.2", chunk:262144}
  bin  [8B offset BE][≤256 KiB data] …          (backpressure: 4 MiB window, lowThreshold = 2 MiB)
  rpc  files.upload.end {xfer_id, sha256?}      → the file-worker does fsync + atomic rename from .tango-part
Download (pull, like go-link)
  rpc  files.download.begin {path} → {xfer_id, size, channel}
  json {"type":"read","xfer_id":…,"offset":…,"length":…}    (up to 16 in flight)
  bin  [8B offset][data]
```

- **Chunk size:** 256 KiB between pion and pion (native app). In the browser it is negotiated
  based on `sctp.maxMessageSize`; Chrome accepts ≥ 256 KiB and so does Firefox, but we start
  with 64 KiB and measure.
- **An interrupted upload never overwrites the original:** it is written to `.tango-part` and renamed
  when it finishes. Resuming it with `files.upload.resume {xfer_id}` is planned and **not implemented**:
  today an interrupted upload is discarded and starts again.
- **Performance gate in M0 (mandatory):** measure browser↔pion and pion↔pion on 1 GbE and
  2.5 GbE, with 1/2/4/8 channels and 16/64/256 KiB chunks. Target: ≥ 80 % of the link in the native
  app and ≥ 300 Mb/s in Chrome. If Chrome does not get there, that is accepted: heavy use goes over SMB or
  through the native app, and it is documented.
- **M3 measurement (2026-10-14), pion↔pion with `tangoctl`, Mac ↔ VM 108 over LAN, 1 channel, 64 KiB frames,
  1 GiB with verified sha256:** upload **36.8 MB/s**, download **45.3 MB/s** (~300–360 Mb/s). The disk is
  a virtual RAID 1 on Proxmox LVM-thin. Pending: measure browser↔pion (not measured yet), 2.5 GbE, and
  256 KiB frames / several channels. The M0 gate above was therefore only partly run (pion↔pion).
- **What is implemented today** (protocol/README.md, files section) differs from the sketch above:
  `files.upload.begin {dir, name, size, overwrite}` → `{xfer, channel, chunk: 65536}`; frames are
  `[u32 xfer BE][u64 offset BE][data]` in both directions; the download is **pushed** by the NAS after
  `files.download.begin {path, offset?, length?}` (no `read` requests); fixed 64 KiB frames, up to 8
  transfers per session spread across 4 `files.N` channels, backpressure at 4 MiB of `bufferedAmount`.

## 8. Service Worker: URLs that are really a DataChannel

`<img>`, `<video>`, `<a download>` and PDF viewers need a URL. TangoOS registers
a SW that intercepts `https://tango.local/dc/*`:

```
<video src="/dc/stream/volume1/Videos/peli.mkv">
  → SW fetch → MessageChannel → main page → files.N read {offset,length} → Response(ReadableStream)
```

- Supports `Range` (you can seek in the video) and large streaming downloads without loading them
  into memory, using the StreamSaver technique.
- The SW **does not have the WebRTC connection**: it asks the tab that has it for the data. If there
  is none, it responds 503.
- Requires a secure context with a **trusted certificate** ([07 §3](07-security.md)).
  Without a SW (e.g. with a self-signed certificate that has not been accepted), downloads fall back to an
  in-memory `Blob` with a 512 MiB limit, and video previews are disabled.
- Thumbnails are generated by the NAS (with `vipsthumbnail` and `ffmpegthumbnailer` from the
  file-worker) and cached in the SW by `etag`.

## 9. Reconnection and multiple tabs

- If ICE drops: restart on the same PC. If the PC drops: new signaling with
  `resume` (session token, [07](07-security.md)) and re-subscription with `resume_from`.
- **One connection per tab**: it is the simple approach and what go-link does. To avoid opening 10 PCs with
  10 tabs, the second tab can use `BroadcastChannel` towards the first one. This will be evaluated
  in M2; it is not needed for the MVP.

### Current implementation

- The signaling WebSocket stays **tied to its PeerConnection**: while it is open, the NAS
  performs the ICE restarts (3 s in `disconnected`, immediately in `failed`, up to 3). If the WS has already
  closed when ICE fails, the session ends and the client opens a new PC and calls `auth.resume`.
- The embedded STUN (pion/turn without an `AuthHandler`) answers Binding requests and rejects every allocation:
  it is STUN only. Remote access (TangoConnect) uses the STUN/TURN servers with ephemeral credentials that
  signalhub hands out, not the embedded server.
- Not implemented: `resume_from` (events missed during a reconnection are not replayed) and sharing one
  connection between tabs with `BroadcastChannel` (each tab opens its own PC).

## 10. Tools

- `tangoctl rpc storage.disks.list` is a Go CLI with pion that logs in and calls methods.
  It is used for scripts and for end-to-end protocol tests.
- A **shared test vector** (`protocol/vectors/*.json`) runs in Go and in TS
  (Vitest), like `vectors/input.json` in go-link.
- "WebRTC Debug" panel in the Monitor app: ICE state, selected candidate pair, RTT,
  `bufferedAmount` per channel and throughput.
