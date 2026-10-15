# `tango/1` protocol: the wire contract

Source of truth for the contract between the desktop (TS) and `tangod` (Go) until the
codegen from JSON Schema exists (docs/03, docs/10). The TS types live in
`web/packages/client/src/protocol.ts`; the Go ones in `pkg/rtcrpc` + `internal/proto`.
**Any change here is reflected on both sides in the same change.**

## 1. Signaling: `wss://<host>/signal?v=1`

One WebSocket per tab. Single-line JSON messages.

```jsonc
// S→C on open
{"type":"hello","v":1,"server":"tangod/0.1.0-dev","nas_id":"tg_…",
 "ice_servers":[{"urls":["stun:<page-host>:3478"]}],
 "methods":["auth.login","system.info", …],   // methods actually implemented
 "topics":["metrics","jobs.changed", …]}
// C→S
{"type":"signal","payload":{"kind":"hello_client","client":"web"}}
// S→C: the NAS always offers
{"type":"signal","payload":{"kind":"offer","sdp":"…"}}
// C→S
{"type":"signal","payload":{"kind":"answer","sdp":"…"}}
// both directions, trickle ICE; candidate:null = end of candidates
{"type":"signal","payload":{"kind":"candidate","candidate":{"candidate":"…","sdpMid":"0","sdpMLineIndex":0}}}
// S→C on error
{"type":"error","code":"RATE_LIMITED","message":"…"}
```

- The `hello` arrives before anything else. `ice_servers` uses the host the client
  came in on (`Host` header) for the embedded STUN.
- The WS may close once the PC is `connected`; the client reopens it for an ICE restart
  or a reconnection (new PC).
- Limit: 16 messages/s per connection, 64 KiB per message.

## 2. Channels (created by the NAS before the offer)

| Label | Options | Content |
|---|---|---|
| `rpc` | ordered | JSON: requests/responses |
| `events` | ordered | JSON: `{"type":"evt","topic":…,"seq":…,"data":…}` |
| `metrics` | `ordered:false, maxRetransmits:0` | JSON `MetricsFrame` at 4 Hz |

Until it authenticates, the session is **gated**: `events` and `metrics` emit nothing.

## 3. RPC (`rpc` channel)

```jsonc
{"type":"req","id":1,"method":"system.info","params":null}
{"type":"res","id":1,"result":{…}}
{"type":"err","id":1,"error":{"code":"UNAUTHENTICATED","message":"…"}}
```

- `id` is an increasing integer per connection. `params` may be `null`.
- `{"type":"cancel","id":1}` (C→S): the client abandoned the request (AbortSignal). The server
  may cancel the work or ignore it; the client no longer waits for a response for that `id`.
- On reconnect (new PC) the client calls `auth.resume` with its token **before** treating
  the connection as ready: the authenticated session is per PeerConnection.
- Stable errors: `UNAUTHENTICATED`, `INVALID_CREDENTIALS`, `RATE_LIMITED`, `METHOD_NOT_FOUND`,
  `INVALID_PARAMS`, `NOT_IMPLEMENTED`, `INTERNAL`, plus the domain ones (docs/03 §5).
- Methods allowed without a session: `auth.users`, `auth.login`, `auth.resume`, `system.info`.

## 4. Authentication (prototype)

```jsonc
auth.users  → [{"name":"admin","full":"Administrator","color":"#2F7CF6"}]   // for the login screen
auth.login  {"user":"admin","password":"…"} → {"user":User,"token":"<base64url 32B>"}
auth.resume {"token":"…"}                    → {"user":User}
auth.logout null                              → {}
```

- Passwords with argon2id. Tokens: the server stores only the SHA-256; they expire after 12 h of
  inactivity. Lockout: 5 failed attempts per user+IP → 5 min.
- First boot: if there are no users, `tangod` creates `admin` with a random password that
  it writes to `/var/lib/tango/initial-admin-password` (0600) and to the journal, Jenkins style.
  The first-boot wizard (M4) replaces it.
- TODO M1: `auth.resume` with "the host proves first" (HMAC with nonce, docs/07 §2).

## 5. Real methods in this phase

`auth.users`, `auth.login`, `auth.resume`, `auth.logout`, `system.info`, `storage.disks.list`,
`storage.volumes.list`, `sessions.list`, `users.list`, `groups.list`, `jobs.list`,
`notices.list`, `rtc.stats`.

The rest is handled by the `MockTransport` inside the client (**hybrid mode**) and the UI keeps
marking it as DEMO. As `tangod` implements them, they show up in `hello.methods` and the
client stops simulating them.

## 6. Types with unknown data

On real hardware some data does not always exist (temperature, hours, SMART on virtual
disks). It is sent as `null`, never as a made-up `0`:

- `Disk.tempC: number | null`, `Disk.powerOnHours: number | null`, `Disk.smart: "ok"|"warn"|"crit"|"unknown"`
- `Disk.bay: number | null`: without a hardware profile, bays are numbered by port
  order (`/dev/disk/by-path`), excluding the system disk.

## 7. Phase M2: real storage (operations that write)

Every destructive operation follows **plan → confirmation → revalidation → job** (docs/04).

```jsonc
storage.volumes.plan   {"name":"volume1","profile":"basic|raid1","disks":["<Disk.id>",…]}
  → {"plan_id":"p_…","expires":"<ISO>","confirm_token":"…",
     "summary":{"name":"volume1","profile":"raid1","fs":"btrfs","usable":17179869184,
                "disks":[{"id":"…","model":"…","serial":"…","size":…,"content":"empty|ext4 (unmounted)|…"}]}}
     // confirm_token = HMAC(process key, plan_id + ids + sizes + current signatures), expires in 5 min
storage.volumes.create {"plan_id":"p_…","confirm_token":"…","typed_name":"volume1"} → {"job":"j_…"}
     // errors: PLAN_EXPIRED, CONFIRMATION_MISMATCH, DEVICE_CHANGED, DEVICE_NOT_ELIGIBLE, VOLUME_EXISTS
storage.scrub.start    {"volume":"<Volume.id>"} → {"job":"j_…"}
shares.create          {"name":"Family","volume":"<Volume.id>","description":"…","smb":true} → {"job":"j_…"}
snapshots.list         {"share":"<Share.id>"?} → Snapshot[]
snapshots.create       {"share":"<Share.id>"} → {"job":"j_…"}
```

- **Real jobs:** `jobs.list` + the `jobs.changed` event (same `Job` type) on every progress or
  state change; states `queued → validating → running → succeeded | failed | interrupted`.
  A `notice` is also emitted when it finishes.
- **Eligible disk:** `use == "free"`, not the system disk, no mounted partitions, no
  holders, stable identity. If it has signatures (partition/FS), the plan shows them in `content`
  and creation wipes them only with the confirmation.
- **Layout:** md `/dev/md/<name>` (metadata 1.2, `--assume-clean` forbidden for RAID1),
  Btrfs `-m dup -d single` on top of the md (or directly on the disk for `basic`), mounted at
  `/<name>` through a persistent systemd `.mount` unit. Shared folders = subvolumes
  `/<name>/@shares/<Share>`; read-only snapshots at `/<name>/@snapshots/<share>/<ISO>`.
- **Samba:** `shares.create` with `smb:true` adds a block in `/etc/samba/tango.d/<share>.conf`
  (included from `smb.conf` with a single `include` line), validates it and reloads `smbd` over D-Bus.

### M2 implementation (clarifications)

- `Volume.disks` uses the same stable ids as `Disk.id` (by-id), never `sdX`.
- `Volume.lastScrub` is `""` until the first scrub; `Snapshot.size` and `Share.size` are `0`
  while there are no quotas (Btrfs simple quotas, docs/04): sizes are not made up.
- `snapshots.list` returns `kind:"manual"` (there is no scheduler yet) and `id = <share>@<name>`.
- New topics in `hello.topics`: `shares.changed` `{id}`, `snapshots.changed` `{share}`.
- Extra errors: `VOLUME_NOT_FOUND`, `VOLUME_BUSY`, `SHARE_EXISTS`, `SHARE_NOT_FOUND`,
  `DEVICE_NOT_FOUND`, `AGENT_UNAVAILABLE`.
- A plan lives in `tangod`'s memory: if it restarts, `storage.volumes.create` answers
  `PLAN_EXPIRED` and the UI asks for the plan again.

## 8. Phase M2.1: RAID levels, deletion, permissions

```jsonc
// Profiles: basic(1) · raid1(2) · raid5(≥3) · raid6(≥4) · raid10(≥4, even)
// usable: basic=size · raid1=min · raid5=(n−1)·min · raid6=(n−2)·min · raid10=(n/2)·min  (before Btrfs)
storage.volumes.plan   {"name","profile":"basic|raid1|raid5|raid6|raid10","disks":[…]}   // same as §7
storage.volumes.deletePlan {"volume":"<Volume.id>"} → {"plan_id","confirm_token","expires",
     "summary":{"name","shares":["Family",…],"snapshots":N,"used":bytes,"disks":[{id,model,serial}]}}
storage.volumes.delete {"plan_id","confirm_token","typed_name"} → {"job"}
     // unmounts, deletes the .mount unit and the volume's SMB folders, stops md and wipes superblocks/signatures
shares.delete          {"share":"<Share.id>","typed_name":"Family"} → {"job"}   // deletes subvolume + snapshots + SMB block
shares.permissions.get {"share"} → {"entries":[{"kind":"user|group","name","access":"rw|ro|none"}]}
shares.permissions.set {"share","entries":[…]} → {}
users.create           {"name","full","email","password","admin":bool,"groups":[…]} → User
users.setPassword      {"name","password"} → {}
```

- `Volume.profile` and `VolumeProfile` add `raid5 | raid6 | raid10`.
- TangoOS users = Unix system users (UID ≥ 2000, group `tango-users`) + a synced Samba
  account (`pdbedit` through the allow-list, ADR 0009). Folder permissions are applied with
  per-folder groups (`tg-<share>-rw`, `tg-<share>-ro`) + Samba's `valid users`/`read list`,
  and a POSIX ACL on the subvolume.
- Sessions: the token hashes are persisted in `/var/lib/tango/sessions.json` (0600) and
  survive a `tangod` restart.

## 9. Phase M3.5: App Center (Docker, ADR 0010)

```jsonc
apps.docker.status   null → {"installed":bool,"running":bool,"version":"27.x"|null}
apps.docker.install  null → {"job"}                     // apt-get install docker.io docker-compose (allow-list) + daemon.json (userns-remap)
apps.catalog         null → CatalogApp[]
apps.list            null → InstalledApp[]
apps.install         {"id","volume","options":{…}} → {"job"}
apps.start|stop|restart|update {"id"} → {"job"}
apps.uninstall       {"id","keepData":bool} → {"job"}
apps.logs            {"id","tail":200} → {"lines":["…"]}
// event: apps.changed {"id"}
```

```ts
interface CatalogApp { id: string; name: string; description: string; category: "Multimedia"|"Fotos"|"Hogar"|"Productividad"|"Red"|"Seguridad"|"Utilidades";
  icon: string /* data:image/svg+xml;base64,… (valid on the LAN and in the TangoConnect portal) */; version: string; webPort: number | null;
  options: { key: string; label: string; type: "string"|"password"|"port"|"bool"; default?: string|number|boolean; required?: boolean }[];
  notes?: string; requiresHostNetwork?: boolean; sizeHint: number /* approx. bytes of images */ }
interface InstalledApp { id: string; name: string; icon: string; version: string; volume: string; dataPath: string;
  state: "installing"|"running"|"stopped"|"updating"|"error"; url: string | null /* https://<host>:<proxyPort>/ */;
  cpu: number|null; mem: number|null; error?: string }
```

- Catalog embedded in `tangod` **and in `tango-agent`** (`system/apps/catalog/<id>/{app.json,compose.yaml,icon.svg}`;
  `app.json` rather than YAML to avoid adding dependencies). The agent only accepts an `id` from the catalog and options
  validated against it: never an arbitrary compose file or image. The variables (`DATA`, `PORT`, `TZ`, `HOST` + options) go into
  a `.env` (0600) that docker compose resolves; internal passwords (`generate: true`) are generated by `tangod`.
- `apps.install` / `update` / `start|stop|restart` / `uninstall` return `{job}`; progress arrives through
  `jobs.changed` (one event per `docker compose` line, `detail` = the line; the pull advances by completed layers).
- Errors: `DOCKER_NOT_INSTALLED`, `ALREADY_INSTALLED`, `APP_EXISTS`, `APP_NOT_INSTALLED`, `APP_BUSY`, `PORT_IN_USE`,
  `VOLUME_NOT_FOUND`, `VOLUME_NOT_MOUNTED`.
- Each app's data lives in `/<volume>/@apps/<id>/` (a subvolume, so it is included in snapshots).
- `apt-get` and `docker compose` (`-p tango-<id> … pull|up -d|stop|restart|down`, allow-list of subcommands) run as
  **transient systemd units** that the agent requests over D-Bus: outside its sandbox (`ProtectSystem`), without opening
  `ReadWritePaths` to `/usr` or `/etc/docker`. `daemon.json` is written by `tango-agent apps-helper write-daemon-json`
  in another transient unit (it does not overwrite an existing one). State, CPU/RAM and logs come from the Docker Engine API over
  `/run/docker.sock` (native Go, no docker CLI: Debian does not install it with `--no-install-recommends`).
- `userns-remap: default`: each container's root is a high uid on the host (`dockremap`); the agent gives that owner
  to the app's `data/` directory. Apps with `network_mode: host` (Home Assistant) are left out of the remapping
  (`userns_mode: host`), because Docker does not allow combining them.
- **App window:** `tangod` opens one HTTPS proxy per app on its own port (9100+),
  with the same certificate, which forwards to `127.0.0.1:<webPort>` and strips `X-Frame-Options`/`frame-ancestors`
  so the app can be shown inside a desktop window (iframe). An "Open in new tab" option is available.
  The desktop CSP allows `frame-src 'self' https://<same host>:*`. **MVP risk:** the proxy does not require the
  TangoOS session (each app has its own login); TODO M4: a gate with a cookie signed by `tangod`.

### M2.1 implementation (clarifications)

- `storage.volumes.plan` adds `summary.notes: string[]` (optional), e.g. "The disks have
  different sizes: 2.1 GB of each one is used".
- A freshly created RAID 5/6 (md recovers the last disk, `[UU_]`) and a replaced disk
  that is being rebuilt are reported as `state:"building"` with `progress`, not `degraded`.
- `deletePlan`/`delete` only for TangoOS volumes (mounted at `/<name>`) that are not
  `building` or `scrubbing` (`VOLUME_BUSY`). A creation plan cannot be used to delete
  or the other way around (the type is part of the HMAC). The agent deletes only if the plan's disks are
  exactly the real members of the array (or the disk of the basic volume).
- `shares.permissions.set`: `rw` wins over `ro`; an explicit `none` for a user removes the
  access they would have through a group. Valid groups: `administrators` and those in `User.groups`.
  New folder = `[{kind:"group",name:"administrators",access:"rw"}]`.
- `users.create` requires an administrator session; `users.setPassword` can be done by the
  user themselves or by an administrator, and it closes that user's other sessions.
- New errors: `PERMISSION_DENIED`, `USER_EXISTS`, `USER_NOT_FOUND`, `SYSTEM_USER`
  (a system user/group with that name already exists), and `CONFIRMATION_MISMATCH` also
  in `shares.delete`.
- On-disk permissions: owner `root:tg-<folder>-rw`, mode 770, access and default ACLs with
  named entries for `-rw` (rwx) and `-ro` (r-x); no setgid (the agent's unit has
  `RestrictSUIDSGID=yes`). Samba: `valid users`/`write list`/`read list` with the groups,
  `force group = tg-<folder>-rw`, `inherit acls = yes`.

## 10. Phase M3: real Files

**Virtual paths:** `/` lists the shared folders the user has access to;
`/<Folder>/sub/file` ↔ `/<volume>/@shares/<Folder>/sub/file`. Every operation runs in
a **file-worker with the user's UID/GIDs** (launched by the agent; `openat2` with
`RESOLVE_BENEATH|RESOLVE_NO_MAGICLINKS` anchored at the folder root), so the permissions
are the same as over SMB.

```jsonc
files.list     {"path"} → FileEntry[]            // `/` = shared folders (kind "dir")
files.search   {"q","path"?} → FileEntry[]        // by name, up to 200, within what is accessible
files.mkdir    {"path"} → FileEntry
files.rename   {"path","name"} → FileEntry
files.move     {"paths":[…],"dest"} → {"count"}
files.trash    {"paths":[…]} → {"count"}          // to /<Folder>/.tango-trash/ with origin metadata
files.trash.list  {"share"?} → TrashEntry[]       // {name, path(in trash), origin, deletedAt, size, kind}
files.restore  {"paths":[…]} → {"count"}          // paths in the trash
files.trash.empty {"share"} → {"count"}
files.versions {"path"} → {"snapshot":Snapshot,"size","mtime"}[]   // versions in @snapshots
files.restoreVersion {"path","snapshot"} → FileEntry               // reflink copy to "<name> (restaurado <date>)"
files.thumb    {"path","size":128|256} → {"mime":"image/jpeg","data":"<base64>"} | NOT_SUPPORTED
```

**Transfers over DataChannel** (`files.0` … `files.3`, ordered, binary; created by the NAS):

```
files.upload.begin  {"dir","name","size","overwrite":false} → {"xfer":<u32>,"channel":"files.N","chunk":65536}
  C→S frames: [u32 xfer BE][u64 offset BE][≤chunk bytes]       // backpressure: bufferedAmount ≤ 4 MiB
files.upload.end    {"xfer","sha256"?} → FileEntry              // fsync + atomic rename from ".<name>.tango-part"
files.download.begin {"path"} → {"xfer","channel","size","chunk"}
  S→C frames: same format; end = files.xfer event {"xfer","state":"done"|"error","error"?}
files.xfer.cancel   {"xfer"} → {}
// event: files.xfer {"xfer","state":"progress|done|error","bytes"} (progress every ~250 ms)
// event: files.changed {"dir"}
```

**Implementation clarifications (M3):**

- `files.upload.end` travels over `rpc` and the data over `files.N` (different SCTP streams): the NAS
  waits until it has received `size` bytes (it gives up after 30 s without progress: `XFER_INCOMPLETE`)
  before `fsync` + `rename`. A 0-byte upload sends no frames. `sha256` is optional; if it
  does not match: `CHECKSUM_MISMATCH` and the partial file is deleted. Without `overwrite`, if the destination exists:
  `PATH_EXISTS` (in `begin`). An upload with no data for 60 s is aborted and emits a `files.xfer` error.
- Download: the NAS starts sending frames as soon as it answers `begin`; since the response goes over another
  channel, the client **keeps the frames of an `xfer` it does not know yet**. The download ends
  when `size` bytes have been received; the `done` event is informational (it may arrive before the last frame).
- 64 KiB frames (`chunk`). Up to 8 transfers per session (`RATE_LIMITED`), spread across
  the 4 channels.
- Trash: `TrashEntry.path` = `/<Folder>/.tango-trash/<id>` (a 20-hex id); this is what
  `files.restore` receives. `files.trash.empty {share}` accepts the folder's name or id. The
  trash lives inside each folder (`.tango-trash`, hidden in the listing, and not published over SMB
  because it is hidden).
- `files.versions` returns only the snapshots in which the file exists; `files.restoreVersion
  {path, snapshot: "<folder>@<name>"}` copies (reflink) as "`<name> (restaurado AAAA-MM-DD HH.MM)<ext>`".
- `files.thumb`: JPEG/PNG/GIF; `NOT_SUPPORTED` for everything else. Cached in `/var/lib/tango/thumbs`.
- Stable errors: `PATH_NOT_FOUND`, `PERMISSION_DENIED`, `PATH_EXISTS`, `INVALID_NAME`,
  `PATH_ESCAPE`, `NO_SPACE`, `NOT_SUPPORTED`, `WORKER_UNAVAILABLE`, `XFER_NOT_FOUND`,
  `XFER_INCOMPLETE`, `CHECKSUM_MISMATCH`, `SNAPSHOT_NOT_FOUND`, `SHARE_NOT_FOUND`.
- File-worker: `tango-agent` re-executes itself as `tango-agent __fileworker` with
  `setresuid/setgroups` to the user (UID ≥ 2000) and hands the SEQPACKET socket to `tangod` over
  `SCM_RIGHTS` (op `files.worker`). The worker refuses to start if it keeps UID 0 or any
  capability. `tangod` closes idle workers (5 min) and relaunches them when a folder's
  permissions change (groups are taken at launch).

## 11. Phase M2.2: volume maintenance

```jsonc
// Stable IDs: Volume.id = UUID of the Btrfs filesystem (not "md127"). apps.json/shares.json are migrated.
storage.disks.replacePlan {"volume","failed":"<Disk.id>|null","replacement":"<Disk.id>"} → plan (as in §7, with "summary.rebuild":{"from","to","estimatedSec"})
storage.disks.replace     {"plan_id","confirm_token","typed_name"} → {"job"}     // mdadm --add (and --remove of the failed one) + rebuild with progress
storage.volumes.expandPlan {"volume","add":["<Disk.id>",…]} → plan                 // raid1→ more mirrors: no; raid5/6/10: --grow --raid-devices; basic→raid1 (convert) optional
storage.volumes.expand     {"plan_id","confirm_token","typed_name"} → {"job"}     // reshape + btrfs filesystem resize max
storage.disks.locate       {"disk"} → {}                                           // identification LED (if the hardware supports it; otherwise NOT_SUPPORTED)
snapshots.schedule.get     {"share"} → Schedule
snapshots.schedule.set     {"share","schedule":Schedule} → {}
snapshots.delete           {"id"} → {}
// Schedule: {"enabled":bool,"hourly":{"every":1|2|4|6|12,"keep":24},"daily":{"at":"03:00","keep":14},"weekly":{"day":0..6,"at":"03:30","keep":8}}
alerts.list                null → Alert[]   // {id, level, title, body, at, source:"md|btrfs|smart|space", resolved:bool}
// events: storage.changed, alerts.changed, notice (degraded, rebuild finished, space < 10 %)
```

- Continuous monitoring in `tangod`: it reads `/sys/block/md*/md/{degraded,sync_action,sync_completed}`,
  `/sys/fs/btrfs/<uuid>/devinfo/*/error_stats`, and free space every 30 s; it emits alerts and notices.
- Snapshot scheduler in `tangod` (no cron), with per-level retention, persisted in
  `/var/lib/tango/snapshot-schedules.json`; scheduled snapshots have `kind:"scheduled"`.

**Implementation (M2.2):**

- **IDs:** `Volume.id` = the Btrfs UUID (`/sys/fs/btrfs/<uuid>/devices/<md>`); an array without
  a visible Btrfs uses `md-<name>`. On startup, `tangod` migrates `apps.json` (the only file that
  stores volumes; `storage.json` uses mount points) by mount point or by the old ID form,
  idempotently, leaving `apps.json.bak-m22` the first time.
- **New fields:**
  - `Volume.task`: `"rebuild"|"reshape"|"resync"` (omitted when md is idle).
  - `Volume.taskSpeed`: bytes/s (`sync_speed` × 1024).
  - `Volume.taskEtaSec`: seconds, `(total − done) × 512 / taskSpeed` from `sync_completed`.
  - `Disk.rebuilding: true` on the member receiving the rebuild (neither `in_sync` nor `faulty`).
- **`replacePlan`:**
  - Without `failed`, it takes the member md marks as `faulty`. If there is none and the volume is not degraded → `INVALID_PARAMS`.
  - The replacement must be free, eligible and ≥ the smallest member; otherwise `DEVICE_TOO_SMALL`.
  - A `basic` volume → `NOT_SUPPORTED`.
  - With a `reshape`/`resync` or a scrub in progress → `VOLUME_BUSY`.
  - The agent (`md.replace`) marks the old disk as failed and removes it, wipes its signatures if it responds, revalidates the new disk's fingerprint, adds it and waits for the `recover`.
- **`expandPlan`:**
  - Only healthy, mounted RAID 5/6/10. RAID 1 and Basic → `NOT_SUPPORTED` with the explanation (a RAID 1 is grown by replacing its disks with larger ones).
  - `summary.notes` says from how much to how much it grows.
  - The agent (`md.grow`) does `--add` + `--grow --raid-devices=N` (with `--backup-file` in `/var/lib/tango-agent` only if mdadm asks for it), waits for the `reshape` and grows the Btrfs with `BTRFS_IOC_RESIZE "1:max"` (ioctl).
- **Plans:** the disk to replace and the final number of disks are signed into the token. A replacement plan cannot be used to expand or the other way around.
- **Scheduled snapshots:**
  - Name `<UTC time>.<level>`, for example `2026-10-14T15-00-00Z.hourly`.
  - `snapshots.list` returns `kind:"scheduled"` for those and `"manual"` for the rest.
  - `Schedule.<level>.keep = 0` turns that level off.
  - A folder without a policy uses the default one (every 1 h ×24, daily 03:00 ×14, weekly Sunday 03:30 ×8, enabled).
  - If `tangod` was off, it takes at most one per level (the one for the last missed time).
  - Retention deletes the oldest ones of each level with `snapshot.delete` (ioctl, only under `@snapshots/<folder>/`).
  - Manual ones are never pruned.
- **Alerts** (`alerts.json`, the last 50 resolved ones are kept):

  | id | level | resolves |
  |---|---|---|
  | `md:<vol>:degraded` | crit; becomes warn "rebuilding" during the rebuild | when the RAID is healthy again |
  | `md:<vol>:readonly` | crit | — |
  | `btrfs:<vol>:errors` | warn | — |
  | `space:<vol>:low` | warn < 10 %, crit < 5 % | ≥ 12 % free |
  | `smart:<disk>` | per SMART | — |

  Opening or resolving an alert emits a `notice`; if the volume no longer exists, the alert is resolved silently.
- **`storage.disks.locate`:** uses `/sys/class/enclosure`; without an enclosure with LEDs → `NOT_SUPPORTED`.
- **Tests:** `TANGOD_SNAPSCHED_UNIT=minute` makes the scheduler's "hour" last one minute.

## 12. Phase M4: product

```jsonc
// First boot (no session while setup.state == "pending"; afterwards it goes away)
setup.state      null → {"state":"pending|done","hostname","suggestedHostname"}
setup.complete   {"hostname","admin":{"name","full","password"},"timezone","certificate":"selfsigned|localca"} → {"user","token"}
// Updates (agent: apt through the allow-list; root snapshot first; rollback swaps @rootfs, see §12.2)
system.updates.check  null → {"tangoos":{"current","available"|null},"debian":[{"name","from","to","security":bool}],"checkedAt"}
system.updates.apply  {"scope":"all|security"} → {"job"}          // creates snapshot @rootfs-<date> first
system.rollback.list  null → [{"id","at","label"}]               // root snapshots
system.rollback.apply {"id","typed":"VOLVER"} → {"job"}           // replaces @rootfs with the snapshot + reboot (§12.2)
system.reboot / system.shutdown {"typed":"REINICIAR"|"APAGAR"} → {}
// Certificate
cert.info        null → {"mode":"selfsigned|localca|acme","subject","sans":[…],"notAfter","fingerprint"}
cert.localca     null → {"job"}      // generates a local CA (10 years) + server cert; reloads TLS without a restart
cert.caDownload  null → {"filename":"tangoos-ca.crt","pem":"…"}
// File services
services.list    null → [{"id":"smb|nfs|sftp|webdav|iscsi","enabled","running","port","detail"}]
services.set     {"id","enabled", "options":{…}} → {"job"}
nfs.exports.set  {"share","clients":[{"host":"192.168.1.0/24","access":"rw|ro"}]} → {}
iscsi.targets.list / iscsi.targets.create {"name","volume","sizeBytes","chap":{"user","secret"}?} / iscsi.targets.delete {"name","typed"}
// Notifications
notify.channels.get null → {"email":{…},"ntfy":{…},"telegram":{…},"levels":["crit","warn"]}
notify.channels.set {…} → {}
notify.test         {"channel":"email|ntfy|telegram"} → {}
// Log (audit + journal)
audit.list   {"q"?,"since"?,"limit":200} → AuditEvent[]   // {at, actor, ip, client, method, target, result, job?}
logs.query   {"unit"?: "tangod|tango-agent|smbd|docker|kernel", "q"?, "since"?, "limit"} → [{at, unit, prio, msg}]
// Terminal ("term" channel, binary, docs/03): term.open {"cols","rows"} → {"id"}; term.resize; term.close — admin only, audited
```

- **Audit:** every call that changes something (and every login) produces an `AuditEvent` in
  `/var/lib/tango/audit.jsonl` (rotated). It never includes passwords or tokens.
- **Secure resume:** `auth.resume` moves to "the host proves first" (docs/07 §2): the server sends
  `{"type":"challenge","nonce"}` and `HMAC-SHA256(SHA-256(token), "tango resume\n"+nonce)` before
  the client reveals the token.
- **App proxy with session:** the per-app proxy requires a `tango_app` cookie (HttpOnly, Secure,
  SameSite=None, 12 h) issued by `apps.ticket {"id"} → {"url"}` (a single-use URL that sets the cookie).

### 12.1 M4 frontend clarifications (for the backend)

- **`nfs.exports.get {"share"} → {"clients":[{host, access}]}`**: the NFS access editor needs it
  to show the current state (the contract only had `set`). `share` is the `Share.id`.
- **`services.changed {"id"?}`** (event): after `services.set`, `iscsi.targets.create|delete`
  and any state change of a service. The UI refreshes `services.list` and `iscsi.targets.list`.
- **`audit.event`** (event, same type as `AuditEvent`): the Log app adds it live at the top
  of the table. Only to administrator sessions.
- **`IscsiTarget`**: `{name, iqn, volume, sizeBytes, chap: bool, sessions: number}`. Errors `TARGET_EXISTS`,
  `NO_SPACE`; CHAP secret of 12–16 characters (LIO limit).
- **`setup.complete`**: if the password is weak → `WEAK_PASSWORD`; if it is already done → `SETUP_DONE`.
  The returned `token` is stored as the session (same as `auth.login`).
- **`term` channel**: frames `[u32 id BE][bytes]` in both directions. A frame from the NAS with **4 bytes and no
  data** means "terminal `id` ended" (shell exit). `term.open` requires admin
  (`PERMISSION_DENIED` otherwise) and creates a `term.open` `AuditEvent`.
- **`system.reboot`/`system.shutdown`**: answer `{}` *before* rebooting/shutting down (the UI switches to
  "Reconnecting…" and waits for the transport to drop and come back).
- **`notify.channels.get`**: passwords/tokens are returned empty if they did not change; in `set`,
  an empty secret field means "keep the current one".

### 12.2 M4 backend implementation (decisions)

- **Secure resume:** public method `auth.challenge {"tokenId","nonce"} → {"proof"}`.
  `tokenId = hex(SHA-256(SHA-256(token)))[:32]`, `proof = hex(HMAC-SHA256(SHA-256(token), "tango resume\n"+nonce))`
  with the **client's** nonce (16–128 characters). The client verifies it before sending `auth.resume`
  (`web/packages/client/src/hostproof.ts`, test vector shared with Go). Backward compatible: if
  `auth.challenge` is not in `hello.methods`, the classic resume is used.
- **Audit:** `Router.OnCall` hook → `audit.jsonl` (0600, 10 MB × 5). Everything that changes something,
  every login and every rejection (`denied`) is logged; parameters are reduced to `target` without sensitive keys.
  WebDAV and SFTP audit their writes and logins separately.
- **Agent:** whatever needs the network, mounting the top level of the system Btrfs, kernel modules or
  a real shell runs as a transient unit (`tango-agent m4-helper …`). Asynchronous ops
  (`updates.apply`, `rollback.apply`, `nfs.apply`, `iscsi.apply`) are tracked with `m4.job`; `tangod`
  queues them (one at a time) instead of failing with `BUSY`.
- **Updates:** before `apt-get`, `/.snapshots/rootfs-<date>` (read-only) is created + a copy of
  `/boot` (`rootfs-<date>.boot`). Rolling back mounts the top level, renames `@rootfs` to
  `@rootfs-reemplazado-<date>`, creates a writable `@rootfs` from the snapshot, restores `/boot` and reboots.
  On an installation without a Btrfs root (cloud image) `system.rollback.list` answers `NOT_SUPPORTED`.
- **Terminal:** the helper creates the PTY in the host's devpts (the agent has its own because of the sandbox),
  starts `bash -l` as root and returns the master over SCM_RIGHTS. Administrators only, audited
  (`term.open`), keystrokes are never logged.
- **SFTP:** our own Go server inside `tangod` (port 2222, no shell, sshd untouched), TangoOS
  passwords, files through the file-worker. **WebDAV:** `https://<nas>/dav/` (Basic over TLS), same file-worker;
  deleting sends to the trash. Limitation: `x/net/webdav` answers 404 (not 403) to a denied write.
- **NFS:** `/etc/exports.d/tango.exports` (`root_squash`, `sync`, `sec=sys`); `nfs-kernel-server` is
  installed when it is enabled. Permissions depend on matching UID/GID on the client (AUTH_SYS).
- **iSCSI:** LIO through configfs (Go), LUN in `/<vol>/@iscsi/<name>.img` without CoW and thin-provisioned,
  `generate_node_acls` + optional CHAP per TPG. The configuration is reapplied when `tangod` starts.
- **App proxy:** `apps.ticket` → `https://<host>:<port>/.tango/ticket?t=…` (single use, 60 s) → HMAC
  `tango_app` cookie (12 h, key in `app-gate.key`); without the cookie, 401. The TangoOS cookie never reaches the app.
- **First boot:** `setup.state` is `pending` until `setup-done` exists or a user has already logged
  in. `setup.complete` creates the admin (Unix + Samba if installed), removes the unused initial admin,
  and only then changes the hostname and time zone.

## 13. Phase v1.0: 2FA, UPS, streaming, browsable snapshots

```jsonc
// Two-step verification (TOTP RFC 6238: SHA-1, 6 digits, 30 s, ±1 window, no reuse)
auth.login        {"user","password"} → {"user","token"} | {"needs":"totp"}     // with 2FA: still no session
auth.totp         {"code"} → {"user","token"}                                    // public; 6 digits or recovery code "xxxxx-xxxxx"
                  // 5 wrong codes → TOTP_LOCKED and the password has to be entered again; no prior login → TOTP_NOT_PENDING
users.totp.begin  null → {"secret","uri","qr"}           // qr = data:image/svg+xml;base64,…; stays pending until confirmed
users.totp.confirm {"code"} → {"recoveryCodes":["xxxxx-xxxxx",…10]}   // shown only once (stored hashed)
users.totp.disable {"password"} → {}
users.totp.reset   {"name"} → {}                          // admin only: for someone who lost their phone
// User.twofa reflects whether it is active.

// UPS (NUT, TCP protocol 3493 spoken in Go)
ups.status        null → {"mode":"none|local|remote","configured","online","name","model","status":"OL|OB|LB|…",
                          "statusText","batteryPct"|null,"runtimeSec"|null,"loadPct"|null,"inputVoltage"|null,
                          "lastUpdate","onBatterySince"|null,"detected":[{"vendor","product","id"}],"error"?}
ups.config.get    null → {"mode","host","port","upsName","user","netServer","policy":{"action":"shutdown|notify","afterMinutes","belowPercent"}}
ups.config.set    {…same object…, "password"?} → {"job"}   // local: installs nut, writes /etc/nut/*, starts nut-server/driver
// ups.changed event (on every state change). Notices: "Power outage", "Power restored", "Low battery: shutting down".
// services.list adds {"id":"ups"}.

// Streaming / ranges and reading snapshots
files.download.begin {"path","offset"?,"length"?,"snapshot"?} → {"xfer","channel","size","chunk","offset","length"}
                      // size = total size; `length` bytes are sent starting at `offset` (frames with absolute offset)
files.list    {"path","snapshot"?} → FileEntry[]       // snapshot = Snapshot.id ("<folder>@<name>"): read-only
files.thumb   {"path","size","snapshot"?}
files.stat    {"path","snapshot"?} → FileEntry
```

- **Service Worker** (`/sw.js`, only in a secure context): intercepts `/dc/stream/<token>/<name>`
  and `/dc/download/<token>/<name>`. The token (opaque, random) is created by the page for a file
  (path + snapshot) and lives only in that tab's memory; the SW asks the page over a `MessageChannel`
  and the page fetches the bytes in 2 MiB pieces with `files.download.begin {offset,length}`
  (one ahead). `Range: bytes=a-b` → `206` + `Content-Range`; no Range → `200`;
  `/dc/download/` carries `Content-Disposition: attachment`. Unknown token → `404`, unsatisfiable
  range → `416`. If no SW is controlling the page, tangod answers `404` to `/dc/…` (never the
  SPA) and the apps use the Blob download as before.
- **App proxy:** the `tango_app` cookie is tied to the token of the session that requested the ticket
  (`app|sid|exp|signature`, `sid = TokenID`). If that session closes or expires, the cookie stops being valid.
- **External changes:** `files.changed` is also emitted for changes made over SMB/NFS/SFTP in
  folders opened in the last 60 s (inotify in the file-worker, batched every 250 ms, up to 64 folders per user).

### 13.1 Time Machine (SMB) and `shares.update`

```jsonc
Share += {"timeMachine": bool, "timeMachineMaxBytes": int|null}
shares.create {"name","volume","description","smb", "timeMachine"?, "timeMachineMaxBytes"?} → {"job"}
shares.update {"share", "description"?, "smb"?, "timeMachine"?, "timeMachineMaxBytes"?} → {"job"}   // admin
              // timeMachine requires smb; cap ≥ 50 GiB or null (no cap); cap without timeMachine → INVALID_PARAMS
              // publishes shares.changed and services.changed
// services.list: the smb detail adds " · Time Machine: N folder(s)"
```

- **Samba** (`/etc/samba/tango.conf`, `[global]`): `fruit:aapl = yes`, `fruit:model = MacSamba`,
  `fruit:metadata = stream`, `fruit:nfs_aces = no`, `fruit:posix_rename = yes`,
  `fruit:veto_appledouble = no`, `fruit:wipe_intentionally_left_blank_rfork = yes`,
  `fruit:delete_empty_adfiles = yes`, `multicast dns register = no` (Avahi announces, not smbd; the
  first time smbd is restarted instead of reloaded). Each folder: `vfs objects = fruit streams_xattr`;
  with Time Machine, `fruit:time machine = yes` and `fruit:time machine max size = <N>G` (GiB, rounded up).
- **Bonjour** (`/etc/avahi/services/tangoos-smb.service`, if the directory exists): `_smb._tcp` 445,
  `_device-info._tcp` `model=TimeCapsule8,119` and, if there are Time Machine folders, `_adisk._tcp` with
  `sys=waMa=0,adVF=0x100` and `dk<i>=adVN=<Folder>,adVF=0x82`.
- Recommended: one dedicated folder and one TangoOS user per Mac.

## 14. Closing v1.0: deleting users, previous versions, WS-Discovery, app passwords

```jsonc
users.delete {"name","typed_name","keepFiles"?: bool}   → {}            // admin
     // typed_name == name (otherwise CONFIRMATION_MISMATCH); not yourself (CANNOT_DELETE_SELF);
     // not the last administrator (LAST_ADMIN); USER_NOT_FOUND.
     // Deletes: the TangoOS user (and their 2FA, recovery codes and app passwords),
     // sessions, Samba account, Unix user (userdel, including their tg-* groups) and their
     // folder permission entries; reapplies the permissions of all folders.
     // keepFiles (default true): files keep their numeric UID. false: they become owned by
     // the administrator doing the deletion (chown inside /<vol>/@shares of each volume).
     // Publishes users.changed and shares.changed.
users.appPasswords.list   {"name"?} → [{"id","label","created","lastUsed"|null,"smb":bool}]   // own; another user: admin
users.appPasswords.create {"label","smb"?: bool} → {"id","label","password","smb"}           // only for yourself; the password is shown once
users.appPasswords.revoke {"id","name"?} → {}
```

- **App passwords:** 24 random characters (base32, in groups of 4); only their SHA-256 is
  stored. They work for WebDAV and SFTP. **With 2FA enabled, WebDAV and SFTP accept only app
  passwords** (the account password is not enough because those protocols cannot ask for the code).
  Without 2FA they accept both. At most 20 per user.
- **SMB and 2FA:** Samba cannot ask for a code. If an app password is created with `smb:true`, it
  becomes the user's SMB password (it replaces the previous one, only one at a time); revoking it
  leaves the SMB account with a random password (SMB stays disabled until another one is created or the
  password is changed). Without 2FA, the SMB password is still the account password.
- **Windows Previous Versions:** each shared folder adds `vfs_shadow_copy2` with
  `shadow:mountpoint = /<vol>/@shares/<Folder>`, `shadow:snapdir = /<vol>/@snapshots/<Folder>/.gmt`,
  `shadow:format = %Y-%m-%dT%H-%M-%SZ`, `shadow:localtime = no`, `shadow:sort = desc`. `.gmt` holds one
  symlink per snapshot with the exact name of the format (`2026-10-15T02-00-04Z →
  ../2026-10-15T02-00-04Z.hourly`): Samba lists names with a level suffix but does not open them. The
  agent maintains `.gmt` when creating/deleting snapshots and when applying permissions. TangoOS snapshots show up as
  `@GMT-…` in "Properties → Previous Versions". On startup, `tangod` regenerates the sections
  of existing folders when the format changes (`samba_rev` in `storage.json`).
- **WS-Discovery:** `tangod` answers on UDP 3702 (multicast 239.255.255.250, IPv4) to Probe/Resolve
  and announces Hello/Bye, and serves the metadata (WS-Transfer Get) at `http://<ip>:5357/<uuid>`. Type
  `wsdp:Device pub:Computer`, `pub:Computer = <hostname>/Workgroup:WORKGROUP`. Stable UUID derived
  from `nas-id`. It only runs if Samba is installed. Ports to open if there is a firewall: 3702/udp, 5357/tcp.

## 15. TangoConnect: remote access and public links (signalhub, `tangoos` app)

Reuses signalhub (`github.com/lordbasex/signalhub`, protocol v1, see its README) and its coturn.
Production: `wss://signal.go-link.org/ws?v=1` (requires `go-link-tangoos` in `ALLOWED_APPS`);
development: a local instance with no restriction. No accounts: codes + device tokens, like go-link.

### Roles
- **NAS** (`tangod`): a persistent outbound connection to signalhub (`register` with its own `device_id` +
  `device_secret`, stored 0600), renews TURN credentials before they expire, reconnects
  with backoff. Each remote browser = a new `RTCPeerConnection` with the same stack (`internal/rtc`):
  channels `rpc/events/metrics/files.N/term` + **`bundle`** (new).
- **Portal** (`https://tangoos.go-link.org`, static; same S3+CloudFront pipeline as desk.go-link.org; `tangoos.go-link.org` may become a CNAME in the future): landing page, "My NAS", desktop loader,
  public link page `/s/<deviceID>.<linkID>#<secret>`. It does not see content: everything travels over DTLS between browser and NAS.

### Linking a browser (like go-link: claim → token; then reach)
1. Settings → TangoConnect → "Link browser" shows a 9-digit code (signalhub pairing, 10 min).
2. The portal does `claim {code}` → session with the NAS → PC → on `rpc`: `connect.link {label}` → the NAS
   issues a `device_token` (32 B) and stores only its SHA-256 + label + date; the portal stores it (IndexedDB) together
   with the NAS's `device_id` and its DTLS fingerprint (pinning).
3. Subsequent times: `reach {device_id}` → PC → **the NAS proves first**:
   `connect.challenge {linkId, nonce}` → `{proof}` with
   `proof = base64url(HMAC-SHA256(SHA-256(device_token), "tangoconnect link proof\n"+nonce))`, **base64url
   without padding** (Go: `base64.RawURLEncoding` in `internal/connect/proof.go`; TS: `connectLinkProof()` in
   `web/packages/client/src/protocol-connect.ts` and `web/apps/connect/src/connect/proof.ts`; vector
   `protocol/vectors/connect-link-proof.json`) → the portal verifies it and only then
   sends `connect.resume {linkId, token}` → the connection becomes *linked* (still without a user).
4. Then the user does the normal TangoOS login (`auth.login` + 2FA). A linked browser without a login
   can only use `auth.*`, `system.info` and `bundle.*`.
- Methods (admin): `connect.status` → {enabled, connected, signal, deviceId, natType?, relayOnly}, `connect.enable {enabled}`,
  `connect.pair.start` → {code, expiresAt}, `connect.links.list` → [{id,label,createdAt,lastSeen,ip}], `connect.links.revoke {id}`.

### Desktop served by the NAS (`bundle` channel)
- `bundle.manifest` → {version, files:[{path,sha256,size}], signature} (Ed25519 signature of the manifest with the
  release key; the public key is embedded in the portal loader and in `tangod`).
- `bundle.get {path}` → binary frames over `bundle` (same framing as files: [u32 id][u64 off][data]).
- The loader verifies the signature and each sha256, builds blob: URLs and mounts the desktop with an `RtcTransport`
  on top of the already open PC. This way the remote UI is always the NAS's exact version.
- **Honest limit:** whoever controls the portal can serve a malicious loader; mitigation: a minimal,
  open source loader, a reproducible build with a published hash, a self-hostable portal and the native app.

### Public links (file sharing)
- `shares.links.create {path, expiresAt?, password?, maxDownloads?}` → `ShareLinkInfo {id, url, invite, owner, …}`.
  **signalhub rooms are not used** (they die when the NAS reconnects and there is one per session): the link points
  to the NAS and the secret travels in the fragment, which never reaches the portal's server or its logs:
  `url = https://tangoos.go-link.org/s/<deviceID>.<linkID>#<secret>` (`invite = "<deviceID>.<linkID>"`).
  `allowUpload` → `NOT_IMPLEMENTED` for now.
- `shares.links.list {all?}` (all is admin only) / `shares.links.revoke {id}`.
- Visitor: `reach {device_id}` on signalhub → PC → `guest.open {link, secret}` → a **guest** session limited to that
  path: `guest.list {path}` (if it is a folder, paths relative to the shared item), `guest.download.begin
  {path, offset?, length?}` over `files.N`; with a password: `guest.unlock {password}` (argon2id, 5 attempts).
  Download counter (counts when the download starts from 0), expiration and audit
  (`share.link.download`). Errors: `SHARE_LINK_NOT_FOUND`, `SHARE_LINK_EXPIRED`, `SHARE_LINK_LOCKED`, `FORBIDDEN`.

### DTLS fingerprint pinning (portal)
- When linking, `connect.link` returns `fingerprint`; the portal stores it with the link. On every `reach`, before
  `connect.challenge`, it compares the fingerprint in the NAS's SDP with the stored one: if it differs, it **aborts** (`FINGERPRINT_CHANGED`)
  without sending a proof or token, and offers "Forget and link again" (legitimate case: the NAS was reinstalled).

### Implementation (tangod 2026-10): what differs from or refines the above
- **Configuration:** `/var/lib/tango/connect.json` (0600): `enabled` (default **false**), `signalURL`
  (default `wss://signal.go-link.org/ws?v=1`), `portalURL` (default `https://tangoos.go-link.org`), the device
  identity (`deviceID` UUID + `deviceSecret`), links (only the token's SHA-256) and public links.
  `connect.enable {enabled, signalURL?, portalURL?}`. The remote DTLS certificate is fixed (`connect-dtls.pem`, 0600,
  10 years, renewed when it has <30 days left): its fingerprint (`connect.status.fingerprint`, `"SHA-256 AB:…"`) is the
  one the portal pins when linking and compares on every connection.
- **Remote PCs:** a separate pion API without the UDP mux (ephemeral ports → srflx/relay candidates with the TURN from signalhub's
  hello); session with `client: "remote"` (or `"guest"`), `origin=remote` for audit/rate limiting. A
  handshake that does not reach *linked*/login within 2 min is cut. At most 32 remote PCs.
- **`connect.hello`** (public, remote only): `{server, nas_id, name, deviceId, methods, topics, remote, state}` —
  replaces the WebSocket hello (the portal does not have it).
- **States and gate** (`Router.Gate`, runs before any other check):
  `paired` (arrived through claim) → `connect.link`; `unlinked` (arrived through reach) → `connect.challenge {linkId, nonce}` →
  `{proof}` and `connect.resume {linkId, token}` (5 attempts per session); `linked` → everything normal (login included);
  `guest` → only `guest.*`. `connect.link` returns `{linkId, token, deviceId, fingerprint}`.
  Proof test vector shared between Go/TS: `protocol/vectors/connect-link-proof.json`.
- **Errors:** `CONNECT_DISABLED`, `NOT_PAIRED`, `LINK_NOT_FOUND`, `LINK_INVALID`, `SHARE_LINK_NOT_FOUND`,
  `SHARE_LINK_EXPIRED`, `SHARE_LINK_LOCKED` (`guest.unlock` missing), `FORBIDDEN`.
- **Bundle:** `bundle.manifest` → `{version, files, signed, signature, keyId}` (`signed` = the exact signed bytes,
  `signature` Ed25519 base64, `""` in unsigned builds). `bundle.get {path}` → `{id, size, sha256, channel:"bundle", chunk}`
  and the frames arrive over the `bundle` channel. The manifest is signed at build time with `cmd/tango-bundlesign`
  (`make web`): key in `$TANGO_BUNDLE_KEY` or, in development, `build/keys/dev-ed25519` (gitignored, generated automatically).
  **Release:** the release seed lives outside the repo (CI secret / signing machine), never in the image;
  only the public key (`.tango-manifest.pub` + embedded in the portal loader) is distributed.
- **Public links — no rooms.** signalhub rooms die when the owner reconnects and there is 1 per session, so
  the NAS does **not** open a room per link: the visitor does `reach {device_id}` and identifies with `guest.open`.
  URL: `<portalURL>/s/<deviceID>.<linkID>#<secret>` — the secret goes in the **fragment** (it does not reach the
  portal's server or its logs). `shares.links.create` → `ShareLinkInfo {id, url, invite:"<deviceID>.<linkID>", code:"", owner,
  path, name, kind, size, createdAt, expiresAt?, hasPassword, maxDownloads?, downloads, expired}`; `allowUpload`
  → `NOT_IMPLEMENTED`. `shares.links.list {all?}` (all is admin only). Guest: `guest.open {link, secret}` → info;
  `guest.unlock {password}` (argon2id, 5 attempts); `guest.list {path}` with paths relative to the shared item;
  `guest.download.begin {path, offset?, length?}` (counts 1 download when it starts from 0; audits
  `share.link.download`). The guest reads with the link owner's permissions, never outside its path.
- **Web client:** `rtcFromSignal(join, {remote:{beforeAuth}})` in `@tango/client` builds an `RtcTransport` on top of
  signalhub signaling (the portal implements `SignalProvider`); `fetchBundleFile(path)` downloads and verifies a
  bundle file; `connectLinkProof()` and `parseShareURL()` in `protocol-connect.ts`.
- **Production:** signalhub needs `ALLOWED_APPS=go-link,go-link-tangoos` and `ALLOWED_ORIGINS` with `https://tangoos.go-link.org`.
  There is no need to raise `MAX_ROOMS_PER_SESSION` (rooms are not used).

## 16. Storage pools: LVM, hybrid RAID, LUKS2, SSD cache (ADR 0011)

```jsonc
// Pools (= LVM volume groups over md). Same plan → token → typed name flow as §7 for anything that erases disks.
storage.pools.list        null → Pool[]
storage.pools.plan        {"name","profile":"basic|raid1|raid5|raid6|raid10|hybrid1|hybrid2","disks":["<Disk.id>",…]} → PoolPlan
storage.pools.create      {"plan_id","confirm_token","typed_name"} → {"job"}
storage.pools.expandPlan  {"pool","add":["<Disk.id>",…]} → PoolPlan        // raid5/6/10: md --grow; hybrid: one more member per tier
storage.pools.expand      {"plan_id","confirm_token","typed_name"} → {"job"}
storage.pools.replacePlan {"pool","failed":"<Disk.id>|null","replacement":"<Disk.id>"} → PoolPlan (with summary.rebuild as §11)
storage.pools.replace     {"plan_id","confirm_token","typed_name"} → {"job"}
storage.pools.deletePlan  {"pool"} → DeletePlan                              // only pools without volumes: POOL_NOT_EMPTY
storage.pools.delete      {"plan_id","confirm_token","typed_name"} → {"job"}
// Volumes inside a pool (they only take free space of the pool: no disk is erased, no token)
storage.poolVolumes.create {"pool","name","size","fs":"btrfs|xfs|ext4","encryption":null|{"passphrase","keyDevice":"<fs UUID>|null"}} → {"job"}
storage.poolVolumes.resize {"volume","size"} → {"job"}                      // grow only (online)
// storage.volumes.deletePlan / delete (§8) also delete pool volumes (lvremove; the pool keeps its disks)
storage.volumes.unlock     {"volume","passphrase"?:string,"useKeyDevice"?:bool} → {}
storage.volumes.lock       {"volume"} → {}                                   // unmount + cryptsetup close
storage.volumes.passphrase {"volume","old","new"} → {}
storage.volumes.keyDevice  {"volume","passphrase","device":"<fs UUID>|null"} → {}  // null removes the key slot
storage.keyDevices.list    null → KeyDevice[]                                 // {uuid, fs, label, size, disk}
// SSD cache (lvmcache) of one volume
storage.cache.plan         {"volume","disks":["<Disk.id>",…],"mode":"read|readwrite"} → PoolPlan
storage.cache.create       {"plan_id","confirm_token","typed_name"} → {"job"}
storage.cache.remove       {"volume"} → {"job"}                               // flushes and frees the SSDs
// events: storage.changed {"what":"pools"|"volumes"|"disks"}
```

Types:

```ts
type PoolProfile = "basic" | "raid1" | "raid5" | "raid6" | "raid10" | "hybrid1" | "hybrid2";
interface Pool {
  id: string;            // LVM VG UUID
  name: string;
  profile: PoolProfile;
  state: "healthy" | "degraded" | "building" | "readonly";
  size: Bytes; allocated: Bytes; free: Bytes;
  disks: string[];       // data disks (Disk.id)
  cacheDisks: string[];
  tiers: PoolTier[];     // one for raid/basic, one per size tier for hybrid
  volumes: string[];     // Volume.id
  task?: "rebuild" | "reshape" | "resync"; progress?: number;
}
interface PoolTier { index: number; size: Bytes /* per member */; members: string[]; level: "basic"|"raid1"|"raid5"|"raid6"|"raid10"|"unused"; usable: Bytes; state?: string; progress?: number }
interface PoolPlan extends VolumePlan { summary: VolumePlan["summary"] & { profile: PoolProfile; tiers: PoolTier[]; unused: Bytes; rebuild?: {from, to, estimatedSec} } }
// Volume (§6) gains: kind?: "classic"|"pool"; pool?: Pool.id; fs: "btrfs"|"xfs"|"ext4";
//   encrypted?: boolean; locked?: boolean (state "locked"); keyDevice?: string|null;
//   cache?: { mode: "read"|"readwrite"; disks: string[]; size: Bytes; used: Bytes; dirty: Bytes;
//             readHits: number; readMisses: number; writeHits: number; writeMisses: number; hitRatio: number|null }
// Disk (§6) gains: pool?: Pool.id; role?: "data"|"cache".
```

Rules and errors:

- Pool names follow volume names (`^[a-z][a-z0-9]{0,23}$`) and are unique among pools and volumes.
  `POOL_EXISTS`, `POOL_NOT_FOUND`, `POOL_NOT_EMPTY`, `NO_SPACE` (size > free), `VOLUME_LOCKED`,
  `WRONG_PASSPHRASE`, `KEY_DEVICE_MISSING`, `CACHE_EXISTS`, `NOT_SSD`, `NOT_SUPPORTED` (shrinking, expanding
  basic/raid1 pools, resizing a cached volume: remove the cache first).
- Disk counts: basic 1; raid1 2; raid5 ≥ 3; raid6 ≥ 4; raid10 even ≥ 4; hybrid1 ≥ 2; hybrid2 ≥ 4.
- `size` is rounded down to 4 MiB extents; `"size": 0` means "all the free space".
- The passphrase (≥ 10 characters) is never logged, journaled or audited; `WRONG_PASSPHRASE` after 5 failed
  unlocks in 10 minutes becomes `RATE_LIMITED`.
- Cache: `read` takes 1 SSD (or 2 → RAID 1); `readwrite` needs exactly 2 SSDs (RAID 1). Disks with
  `kind:"hdd"` → `NOT_SSD`.

## 17. Cluster (GlusterFS)

Several TangoOS joined with GlusterFS ([docs/17](../docs/17-cluster.md), [ADR 0012](../docs/adr/0012-glusterfs-cluster.md)).
Every method is admin-only. Long operations return `{job}` and report through `jobs.*`.

| Method | Params | Result |
|---|---|---|
| `cluster.status` | — | `ClusterStatus` |
| `cluster.install` | — | `{job}`: nftables + firewall first, then `glusterfs-server`; also starts a stopped glusterd |
| `cluster.invite` | `{host}` | `{host, addrs[], expires}`: this node accepts `host` for 15 minutes |
| `cluster.invite.cancel` | `{host}` | `{}` |
| `cluster.peer.add` | `{host}` | `{}`: opens this side and runs `peer probe`; `PEER_UNREACHABLE` if the other side did not invite |
| `cluster.peer.remove` | `{host}` | `{}`: `peer detach` (fails while the node has bricks) |
| `cluster.volume.create` | `{name, replica, arbiter?, preset?, options?, bricks:[{host, tangoVolume}], start}` | `{job}` |
| `cluster.volume.action` | `{name, action: start\|stop\|delete}` | `{}` (`BUSY` if mounted on this node) |
| `cluster.volume.options` | `{name, set: {key: value}, reset: [key]}` | `{}` |
| `cluster.volume.heal` | `{name, full?}` | `{}` |
| `cluster.volume.bricks` | `{name, op: add\|remove\|replace, replica?, bricks?, phase?, from?, to?}` | `{}` |
| `cluster.volume.info` | `{name}` | `{text}`: same text as `gluster volume info <name>` |
| `cluster.volume.mount` | `{name, enabled, share?, comment?, rw?, ro?}` | `{job}`: `/cluster/<name>` (+ SMB share) |
| `cluster.brick.cleanup` | `{tangoVolume, name}` | `{}`: deletes `/<tangoVolume>/@gluster/<name>` if that volume no longer exists |

Topic `cluster.changed` carries a full `ClusterStatus` when anything changes (polled every 20 s by tangod).

```ts
interface ClusterStatus {
  installed: boolean; running: boolean; version?: string; error?: string;
  self: string; selfAddrs: string[];
  nodes: { uuid; host; hostnames?; connected; state; self; latencyMs /* -1 = no answer */ }[];
  volumes: ClusterVolume[]; orphans: { tango_volume; name; path }[];
  mounts: { name; path; mounted; share? }[];
  allowed: string[];                       // IPs the firewall lets reach GlusterFS
  invites: { host; addrs; expires }[];
  tangoVolumes: { name; mount; free }[];   // where this node can hold a brick
  presets: { id; name; desc; replica?; options }[];
  options: { key; type: "bool"|"int"|"enum"|"percent"; min?; max?; enum?; global?; desc }[];
}
interface ClusterVolume {
  name; id; status /* Created|Started|Stopped */; type /* Replicate|Distributed-Replicate|Distribute */;
  replica; arbiter; sets; options: Record<string,string>; optOrder: string[];
  bricks: { host; path; hostUuid; arbiter?; online; port?; pid?; sizeTotal?; sizeFree?; fsName?;
            healPending /* -1 = brick did not answer */; splitBrain; healStatus? }[];
  sizeTotal; sizeFree; healPending; splitBrain; online;
}
```

Agent ops (`gluster.*`, pkg/agentproto/gluster.go) take typed arguments and build the
`gluster --mode=script --xml` argv with internal/gluster: option keys come from an allow-list with typed values,
hosts are IPs or lowercase DNS names, brick paths are `/volumeN/@gluster/<volume>`, and `force` is never sent on
create. The agent has no network: tangod resolves names before `gluster.firewall`.

## 18. ZFS (ADR 0013)

Optional: OpenZFS is installed on demand (contrib + DKMS). Every method except `storage.zfs.status` and
`storage.zfs.scrubSchedule.get` is admin-only. Long operations return `{job}` and report through `jobs.*`.
Destructive ones use the plan → `confirm_token` → typed name flow of §7 (`PLAN_EXPIRED`, `CONFIRMATION_MISMATCH`,
`DEVICE_CHANGED`, `DEVICE_NOT_ELIGIBLE`).

```jsonc
storage.zfs.status            null → {enabled, installed, version?, enabling?: jobId, pools: ZfsPool[]}
storage.zfs.importable        null → [{name, guid, state, status?, disks: [Disk.id]}]   // reads every disk
storage.zfs.enable            null → {job}                                // ZFS_UNAVAILABLE elsewhere until done
storage.zfs.plan              {name, layout: "stripe"|"mirror"|"raidz1"|"raidz2"|"raidz3", disks} → ZfsPlan
storage.zfs.create            {plan_id, confirm_token, typed_name} → {job}
storage.zfs.addPlan           {pool, kind: "data"|"cache"|"log", disks} → ZfsPlan   // data = same shape as the pool
storage.zfs.add               {plan_id, confirm_token, typed_name} → {job}
storage.zfs.auxRemove         {pool, kind: "cache"|"log", vdev} → {job}
storage.zfs.replacePlan       {pool, old: vdev name | GUID | Disk.id, replacement: Disk.id} → ZfsPlan
storage.zfs.replace           {plan_id, confirm_token, typed_name} → {job}          // follows the resilver
storage.zfs.scrub             {pool, stop?} → {job}
storage.zfs.scrubSchedule.get {pool} → {enabled, days, last?}                       // default every 30 days
storage.zfs.scrubSchedule.set {pool, enabled, days: 1..365} → same
storage.zfs.import            {pool: name|guid, readonly} → {job}    // readonly: altroot /import/<pool>, nothing written
storage.zfs.export            {pool} → {job}                         // VOLUME_BUSY with apps or a zvol used by iSCSI
storage.zfs.destroyPlan       {pool} → DeletePlan                    // same VOLUME_BUSY rules
storage.zfs.destroy           {plan_id, confirm_token, typed_name} → {job}   // labels cleared, disks free
storage.zfs.volumes.create    {pool, name, quota?, reservation?, encryption: {passphrase} | null} → {job}
storage.zfs.volumes.set       {volume, quota, reservation} → {}     // 0 = none
storage.zfs.volumes.deletePlan {volume} → DeletePlan
storage.zfs.volumes.delete    {plan_id, confirm_token, typed_name} → {job}
storage.zfs.zvols.create      {pool, name, size, sparse} → {}       // /dev/zvol/<pool>/zvol/<name>
storage.zfs.zvols.delete      {pool, name} → {}                     // VOLUME_BUSY while an iSCSI target uses it

ZfsPool = {id: "zfs:<name>", name, guid, health: "ONLINE"|"DEGRADED"|…, state: "healthy"|"degraded"|"faulted"|"readonly",
  readonly, layout: ZfsLayout|"mixed", size, allocated, free, raw, frag, vdevs: ZfsVdev[], logs: ZfsVdev[], cache: ZfsLeaf[],
  scan: {function: "scrub"|"resilver", state: "scanning"|"finished"|"canceled", progress, start?, end?, errors} | null,
  errors?, volumes: [Volume.id], zvols: [{name, size, used, device}], encrypted: [Volume.id], disks: [Disk.id],
  other: [{name, mountpoint, mounted, used}]}                           // datasets outside the TangoOS layout
ZfsVdev = {name, type: "disk"|ZfsLayout, state, read, write, cksum, size, disks: ZfsLeaf[]}
ZfsLeaf = {id: Disk.id | "", name, guid, state, read, write, cksum, resilvering?}
ZfsPlan = {plan_id, expires, confirm_token, summary: {name, kind, layout, usable, raw, wasted, tolerance, disks, notes}}
```

- A ZFS volume is a `Volume` with `kind: "zfs"`, `fs: "zfs"`, `pool: "zfs:<name>"`, `profile` = the pool layout and
  `id: "zfs-<dataset guid>"`. Its folders, snapshots, Files versions, Previous Versions, permissions and apps work through
  the existing methods (§7, §8, §10, §11). `storage.scrub.start` on a ZFS volume scrubs its pool.
- Encrypted ZFS volumes use `storage.volumes.unlock|lock|passphrase` (§16); `useKeyDevice` → `NOT_SUPPORTED`.
- Disks: members of a ZFS pool come in `storage.disks.list` with `use: "member"`, `pool: "zfs:<name>"` and
  `role: "data"|"cache"|"log"`. A disk held open by the kernel (an imported pool) is never eligible.
- iSCSI (§12): `iscsi.targets.create` accepts `zvol: "/dev/zvol/<pool>/zvol/<name>"` instead of `volume`/`sizeBytes`
  (backstore `iblock`); `iscsi.targets.list` returns `zvol` for those targets.
- Agent ops (`pkg/agentproto/zfs.go`): `zfs.report {importable}`, `zfs.enable`, `zfs.pool.create|add|aux|replace|scrub|
  import|export|destroy`, `zfs.volume.create|set|delete|unlock|lock|passphrase`, `zfs.zvol.create|delete`. Reads use
  `zpool status -j -p`, `zpool list -j -p`, `zfs list -j -p` (OpenZFS 2.3 JSON; `zpool import` has no `-j` and is parsed
  as text). Everything that mounts runs in the transient unit; passphrases only go through stdin inside the agent.
