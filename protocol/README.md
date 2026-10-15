# Protocolo `tango/1`: contrato de cable

Fuente de verdad del contrato entre el escritorio (TS) y `tangod` (Go) mientras no exista el
codegen desde JSON Schema (docs/03, docs/10). Los tipos TS viven en
`web/packages/client/src/protocol.ts`; los Go en `pkg/rtcrpc` + `internal/proto`.
**Cualquier cambio acá se refleja en los dos lados en el mismo cambio.**

## 1. Señalización: `wss://<host>/signal?v=1`

Un WebSocket por pestaña. Mensajes JSON de una línea.

```jsonc
// S→C al abrir
{"type":"hello","v":1,"server":"tangod/0.1.0-dev","nas_id":"tg_…",
 "ice_servers":[{"urls":["stun:<host-de-la-página>:3478"]}],
 "methods":["auth.login","system.info", …],   // métodos implementados de verdad
 "topics":["metrics","jobs.changed", …]}
// C→S
{"type":"signal","payload":{"kind":"hello_client","client":"web"}}
// S→C: el NAS siempre ofrece
{"type":"signal","payload":{"kind":"offer","sdp":"…"}}
// C→S
{"type":"signal","payload":{"kind":"answer","sdp":"…"}}
// ambos sentidos, trickle ICE; candidate:null = fin de candidatos
{"type":"signal","payload":{"kind":"candidate","candidate":{"candidate":"…","sdpMid":"0","sdpMLineIndex":0}}}
// S→C ante error
{"type":"error","code":"RATE_LIMITED","message":"…"}
```

- El `hello` llega antes de cualquier otra cosa. `ice_servers` usa el host con el que el
  cliente llegó (header `Host`) para el STUN embebido.
- El WS puede cerrarse cuando la PC está `connected`; el cliente lo reabre para un ICE restart
  o una reconexión (nueva PC).
- Límite: 16 mensajes/s por conexión, 64 KiB por mensaje.

## 2. Canales (los crea el NAS antes de la offer)

| Label | Opciones | Contenido |
|---|---|---|
| `rpc` | ordered | JSON: requests/responses |
| `events` | ordered | JSON: `{"type":"evt","topic":…,"seq":…,"data":…}` |
| `metrics` | `ordered:false, maxRetransmits:0` | JSON `MetricsFrame` a 4 Hz |

Hasta autenticarse la sesión está **gated**: `events` y `metrics` no emiten nada.

## 3. RPC (canal `rpc`)

```jsonc
{"type":"req","id":1,"method":"system.info","params":null}
{"type":"res","id":1,"result":{…}}
{"type":"err","id":1,"error":{"code":"UNAUTHENTICATED","message":"…"}}
```

- `id` entero creciente por conexión. `params` puede ser `null`.
- `{"type":"cancel","id":1}` (C→S): el cliente abandonó el pedido (AbortSignal). El servidor
  puede cancelar el trabajo o ignorarlo; el cliente ya no espera respuesta para ese `id`.
- Al reconectar (nueva PC) el cliente llama `auth.resume` con su token **antes** de considerar
  la conexión lista: la sesión autenticada es por PeerConnection.
- Errores estables: `UNAUTHENTICATED`, `INVALID_CREDENTIALS`, `RATE_LIMITED`, `METHOD_NOT_FOUND`,
  `INVALID_PARAMS`, `NOT_IMPLEMENTED`, `INTERNAL`, más los de dominio (docs/03 §5).
- Métodos permitidos sin sesión: `auth.users`, `auth.login`, `auth.resume`, `system.info`.

## 4. Autenticación (prototipo)

```jsonc
auth.users  → [{"name":"admin","full":"Administrador","color":"#2F7CF6"}]   // para la pantalla de login
auth.login  {"user":"admin","password":"…"} → {"user":User,"token":"<base64url 32B>"}
auth.resume {"token":"…"}                    → {"user":User}
auth.logout null                              → {}
```

- Contraseñas con argon2id. Tokens: el servidor guarda solo SHA-256, expiran a las 12 h de
  inactividad. Lockout: 5 intentos fallidos por usuario+IP → 5 min.
- Primer arranque: si no hay usuarios, `tangod` crea `admin` con una contraseña aleatoria que
  escribe en `/var/lib/tango/initial-admin-password` (0600) y en el journal, al estilo Jenkins.
  El asistente de primer arranque (M4) lo reemplaza.
- TODO M1: `auth.resume` con "el host prueba primero" (HMAC con nonce, docs/07 §2).

## 5. Métodos reales de esta fase

`auth.users`, `auth.login`, `auth.resume`, `auth.logout`, `system.info`, `storage.disks.list`,
`storage.volumes.list`, `sessions.list`, `users.list`, `groups.list`, `jobs.list`,
`notices.list`, `rtc.stats`.

El resto lo atiende el `MockTransport` dentro del cliente (**modo híbrido**) y la UI lo sigue
marcando como DEMO. A medida que `tangod` los implementa, aparecen en `hello.methods` y el
cliente deja de simularlos.

## 6. Tipos con datos desconocidos

En hardware real hay datos que no siempre existen (temperatura, horas, SMART en discos
virtuales). Se envían como `null`, nunca como `0` inventado:

- `Disk.tempC: number | null`, `Disk.powerOnHours: number | null`, `Disk.smart: "ok"|"warn"|"crit"|"unknown"`
- `Disk.bay: number | null`: sin perfil de hardware, las bahías se numeran por orden de
  puerto (`/dev/disk/by-path`), excluyendo el disco del sistema.

## 7. Fase M2: almacenamiento real (operaciones que escriben)

Toda operación destructiva sigue **plan → confirmación → revalidación → job** (docs/04).

```jsonc
storage.volumes.plan   {"name":"volume1","profile":"basic|raid1","disks":["<Disk.id>",…]}
  → {"plan_id":"p_…","expires":"<ISO>","confirm_token":"…",
     "summary":{"name":"volume1","profile":"raid1","fs":"btrfs","usable":17179869184,
                "disks":[{"id":"…","model":"…","serial":"…","size":…,"content":"vacío|ext4 (sin montar)|…"}]}}
     // confirm_token = HMAC(clave del proceso, plan_id + ids + tamaños + firmas actuales), vence en 5 min
storage.volumes.create {"plan_id":"p_…","confirm_token":"…","typed_name":"volume1"} → {"job":"j_…"}
     // errores: PLAN_EXPIRED, CONFIRMATION_MISMATCH, DEVICE_CHANGED, DEVICE_NOT_ELIGIBLE, VOLUME_EXISTS
storage.scrub.start    {"volume":"<Volume.id>"} → {"job":"j_…"}
shares.create          {"name":"Familia","volume":"<Volume.id>","description":"…","smb":true} → {"job":"j_…"}
snapshots.list         {"share":"<Share.id>"?} → Snapshot[]
snapshots.create       {"share":"<Share.id>"} → {"job":"j_…"}
```

- **Jobs reales:** `jobs.list` + evento `jobs.changed` (mismo tipo `Job`) en cada cambio de
  progreso o estado; estados `queued → validating → running → succeeded | failed | interrupted`.
  Al terminar se emite también un `notice`.
- **Disco elegible:** `use == "free"`, no es el del sistema, sin particiones montadas, sin
  holders, identidad estable. Si tiene firmas (partición/FS), el plan las muestra en `content`
  y la creación las borra solo con la confirmación.
- **Layout:** md `/dev/md/<name>` (metadata 1.2, `--assume-clean` prohibido para RAID1),
  Btrfs `-m dup -d single` sobre el md (o directo sobre el disco en `basic`), montado en
  `/<name>` vía unidad systemd `.mount` persistente. Carpetas compartidas = subvolúmenes
  `/<name>/@shares/<Share>`; instantáneas read-only en `/<name>/@snapshots/<share>/<ISO>`.
- **Samba:** `shares.create` con `smb:true` agrega un bloque en `/etc/samba/tango.d/<share>.conf`
  (incluido desde `smb.conf` con una sola línea `include`), valida y recarga `smbd` por D-Bus.

### Implementación M2 (aclaraciones)

- `Volume.disks` usa los mismos ids estables que `Disk.id` (by-id), nunca `sdX`.
- `Volume.lastScrub` es `""` hasta el primer scrub; `Snapshot.size` y `Share.size` son `0`
  mientras no haya cuotas (Btrfs simple quotas, docs/04): no se inventan tamaños.
- `snapshots.list` devuelve `kind:"manual"` (todavía no hay programador) y `id = <share>@<nombre>`.
- Topics nuevos en `hello.topics`: `shares.changed` `{id}`, `snapshots.changed` `{share}`.
- Errores extra: `VOLUME_NOT_FOUND`, `VOLUME_BUSY`, `SHARE_EXISTS`, `SHARE_NOT_FOUND`,
  `DEVICE_NOT_FOUND`, `AGENT_UNAVAILABLE`.
- Un plan vive en memoria de `tangod`: si se reinicia, `storage.volumes.create` responde
  `PLAN_EXPIRED` y la UI vuelve a pedir el plan.

## 8. Fase M2.1: niveles RAID, borrado, permisos

```jsonc
// Perfiles: basic(1) · raid1(2) · raid5(≥3) · raid6(≥4) · raid10(≥4, par)
// usable: basic=size · raid1=min · raid5=(n−1)·min · raid6=(n−2)·min · raid10=(n/2)·min  (antes de Btrfs)
storage.volumes.plan   {"name","profile":"basic|raid1|raid5|raid6|raid10","disks":[…]}   // igual que §7
storage.volumes.deletePlan {"volume":"<Volume.id>"} → {"plan_id","confirm_token","expires",
     "summary":{"name","shares":["Familia",…],"snapshots":N,"used":bytes,"disks":[{id,model,serial}]}}
storage.volumes.delete {"plan_id","confirm_token","typed_name"} → {"job"}
     // desmonta, borra la unidad .mount y las carpetas SMB del volumen, detiene md y limpia superbloques/firmas
shares.delete          {"share":"<Share.id>","typed_name":"Familia"} → {"job"}   // borra subvolumen + instantáneas + bloque SMB
shares.permissions.get {"share"} → {"entries":[{"kind":"user|group","name","access":"rw|ro|none"}]}
shares.permissions.set {"share","entries":[…]} → {}
users.create           {"name","full","email","password","admin":bool,"groups":[…]} → User
users.setPassword      {"name","password"} → {}
```

- `Volume.profile` y `VolumeProfile` suman `raid5 | raid6 | raid10`.
- Usuarios TangoOS = usuarios Unix del sistema (UID ≥ 2000, grupo `tango-users`) + cuenta Samba
  sincronizada (`pdbedit` por allow-list, ADR 0009). Los permisos de carpeta se aplican con
  grupos por carpeta (`tg-<share>-rw`, `tg-<share>-ro`) + `valid users`/`read list` de Samba,
  y POSIX ACL en el subvolumen.
- Sesiones: el hash de los tokens se persiste en `/var/lib/tango/sessions.json` (0600) y
  sobreviven al reinicio de `tangod`.

## 9. Fase M3.5: Centro de apps (Docker, ADR 0010)

```jsonc
apps.docker.status   null → {"installed":bool,"running":bool,"version":"27.x"|null}
apps.docker.install  null → {"job"}                     // apt-get install docker.io docker-compose (allow-list) + daemon.json (userns-remap)
apps.catalog         null → CatalogApp[]
apps.list            null → InstalledApp[]
apps.install         {"id","volume","options":{…}} → {"job"}
apps.start|stop|restart|update {"id"} → {"job"}
apps.uninstall       {"id","keepData":bool} → {"job"}
apps.logs            {"id","tail":200} → {"lines":["…"]}
// evento: apps.changed {"id"}
```

```ts
interface CatalogApp { id: string; name: string; description: string; category: "Multimedia"|"Fotos"|"Hogar"|"Productividad"|"Red"|"Seguridad"|"Utilidades";
  icon: string /* data:image/svg+xml;base64,… (válido en la LAN y en el portal TangoConnect) */; version: string; webPort: number | null;
  options: { key: string; label: string; type: "string"|"password"|"port"|"bool"; default?: string|number|boolean; required?: boolean }[];
  notes?: string; requiresHostNetwork?: boolean; sizeHint: number /* bytes aprox. de imágenes */ }
interface InstalledApp { id: string; name: string; icon: string; version: string; volume: string; dataPath: string;
  state: "installing"|"running"|"stopped"|"updating"|"error"; url: string | null /* https://<host>:<proxyPort>/ */;
  cpu: number|null; mem: number|null; error?: string }
```

- Catálogo embebido en `tangod` **y en `tango-agent`** (`system/apps/catalog/<id>/{app.json,compose.yaml,icon.svg}`;
  `app.json` y no YAML para no sumar dependencias). El agente solo acepta un `id` del catálogo y opciones validadas
  contra él: nunca un compose ni una imagen arbitraria. Las variables (`DATA`, `PORT`, `TZ`, `HOST` + opciones) van en
  un `.env` (0600) que resuelve docker compose; las contraseñas internas (`generate: true`) las genera `tangod`.
- `apps.install` / `update` / `start|stop|restart` / `uninstall` devuelven `{job}`; el progreso llega por
  `jobs.changed` (un evento por línea de `docker compose`, `detail` = la línea; el pull avanza por capas completas).
- Errores: `DOCKER_NOT_INSTALLED`, `ALREADY_INSTALLED`, `APP_EXISTS`, `APP_NOT_INSTALLED`, `APP_BUSY`, `PORT_IN_USE`,
  `VOLUME_NOT_FOUND`, `VOLUME_NOT_MOUNTED`.
- Datos de cada app en `/<volume>/@apps/<id>/` (subvolumen, entra en las instantáneas).
- `apt-get` y `docker compose` (`-p tango-<id> … pull|up -d|stop|restart|down`, allow-list de subcomandos) corren como
  **unidades transitorias de systemd** que pide el agente por D-Bus: fuera de su sandbox (`ProtectSystem`), sin abrir
  `ReadWritePaths` a `/usr` ni `/etc/docker`. `daemon.json` lo escribe `tango-agent apps-helper write-daemon-json`
  en otra unidad transitoria (no pisa uno existente). Estado, CPU/RAM y logs salen de la Docker Engine API por
  `/run/docker.sock` (Go nativo, sin CLI de docker: Debian no lo instala con `--no-install-recommends`).
- `userns-remap: default`: el root de cada contenedor es un uid alto del host (`dockremap`); el agente le da ese dueño
  al directorio `data/` de la app. Las apps con `network_mode: host` (Home Assistant) salen del remapeo
  (`userns_mode: host`), porque Docker no permite combinarlos.
- **Ventana de la app:** `tangod` abre un proxy HTTPS por app en un puerto propio (9100+),
  con el mismo certificado, que reenvía a `127.0.0.1:<webPort>` y quita `X-Frame-Options`/`frame-ancestors`
  para que la app se vea dentro de una ventana del escritorio (iframe). Opción "Abrir en pestaña nueva".
  La CSP del escritorio permite `frame-src 'self' https://<mismo host>:*`. **Riesgo MVP:** el proxy no exige la
  sesión de TangoOS (cada app tiene su login); TODO M4: compuerta con cookie firmada por `tangod`.

### Implementación M2.1 (aclaraciones)

- `storage.volumes.plan` suma `summary.notes: string[]` (opcional), p. ej. «Los discos tienen
  distinto tamaño: se usa 2,1 GB de cada uno».
- Un RAID 5/6 recién creado (md recupera el último disco, `[UU_]`) y un disco reemplazado
  que se reconstruye se informan como `state:"building"` con `progress`, no `degraded`.
- `deletePlan`/`delete` solo para volúmenes de TangoOS (montados en `/<nombre>`) que no
  estén `building` ni `scrubbing` (`VOLUME_BUSY`). Un plan de creación no sirve para borrar
  ni al revés (el tipo entra en el HMAC). El agente borra solo si los discos del plan son
  exactamente los miembros reales del array (o el disco del volumen básico).
- `shares.permissions.set`: `rw` gana sobre `ro`; `none` explícito para un usuario le quita el
  acceso que tendría por un grupo. Grupos válidos: `administrators` y los de `User.groups`.
  Carpeta nueva = `[{kind:"group",name:"administrators",access:"rw"}]`.
- `users.create` exige sesión de administrador; `users.setPassword` lo puede hacer el propio
  usuario o un administrador, y cierra las otras sesiones de ese usuario.
- Errores nuevos: `PERMISSION_DENIED`, `USER_EXISTS`, `USER_NOT_FOUND`, `SYSTEM_USER`
  (ya existe un usuario/grupo del sistema con ese nombre), `CONFIRMATION_MISMATCH` también
  en `shares.delete`.
- Permisos en disco: dueño `root:tg-<carpeta>-rw`, modo 770, ACL de acceso y por defecto con
  entradas nombradas para `-rw` (rwx) y `-ro` (r-x); sin setgid (la unidad del agente tiene
  `RestrictSUIDSGID=yes`). Samba: `valid users`/`write list`/`read list` con los grupos,
  `force group = tg-<carpeta>-rw`, `inherit acls = yes`.

## 10. Fase M3: Archivos real

**Rutas virtuales:** `/` lista las carpetas compartidas a las que el usuario tiene acceso;
`/<Carpeta>/sub/archivo` ↔ `/<volumen>/@shares/<Carpeta>/sub/archivo`. Toda operación corre en
un **file-worker con el UID/GIDs del usuario** (lo lanza el agente; `openat2` con
`RESOLVE_BENEATH|RESOLVE_NO_MAGICLINKS` anclado en la raíz de la carpeta), así que los permisos
son los mismos que por SMB.

```jsonc
files.list     {"path"} → FileEntry[]            // `/` = carpetas compartidas (kind "dir")
files.search   {"q","path"?} → FileEntry[]        // por nombre, hasta 200, dentro de lo accesible
files.mkdir    {"path"} → FileEntry
files.rename   {"path","name"} → FileEntry
files.move     {"paths":[…],"dest"} → {"count"}
files.trash    {"paths":[…]} → {"count"}          // a /<Carpeta>/.tango-trash/ con metadatos de origen
files.trash.list  {"share"?} → TrashEntry[]       // {name, path(en papelera), origin, deletedAt, size, kind}
files.restore  {"paths":[…]} → {"count"}          // paths en papelera
files.trash.empty {"share"} → {"count"}
files.versions {"path"} → {"snapshot":Snapshot,"size","mtime"}[]   // versiones en @snapshots
files.restoreVersion {"path","snapshot"} → FileEntry               // copia reflink a "<nombre> (restaurado <fecha>)"
files.thumb    {"path","size":128|256} → {"mime":"image/jpeg","data":"<base64>"} | NOT_SUPPORTED
```

**Transferencias por DataChannel** (`files.0` … `files.3`, ordenados, binarios; los crea el NAS):

```
files.upload.begin  {"dir","name","size","overwrite":false} → {"xfer":<u32>,"channel":"files.N","chunk":65536}
  C→S frames: [u32 xfer BE][u64 offset BE][≤chunk bytes]       // backpressure: bufferedAmount ≤ 4 MiB
files.upload.end    {"xfer","sha256"?} → FileEntry              // fsync + rename atómico desde ".<name>.tango-part"
files.download.begin {"path"} → {"xfer","channel","size","chunk"}
  S→C frames: mismo formato; fin = evento files.xfer {"xfer","state":"done"|"error","error"?}
files.xfer.cancel   {"xfer"} → {}
// evento: files.xfer {"xfer","state":"progress|done|error","bytes"} (progreso cada ~250 ms)
// evento: files.changed {"dir"}
```

**Aclaraciones de la implementación (M3):**

- `files.upload.end` viaja por `rpc` y los datos por `files.N` (streams SCTP distintos): el NAS
  espera hasta haber recibido `size` bytes (corta a los 30 s sin progreso: `XFER_INCOMPLETE`)
  antes de `fsync` + `rename`. Una subida de 0 bytes no manda frames. `sha256` es opcional; si
  no coincide: `CHECKSUM_MISMATCH` y el parcial se borra. Sin `overwrite`, si el destino existe:
  `PATH_EXISTS` (en `begin`). Una subida sin datos por 60 s se aborta y emite `files.xfer` error.
- Descarga: el NAS empieza a mandar frames apenas responde `begin`; como la respuesta va por otro
  canal, el cliente **guarda los frames de un `xfer` que todavía no conoce**. La descarga termina
  al recibir `size` bytes; el evento `done` es informativo (puede llegar antes que el último frame).
- Frames de 64 KiB (`chunk`). Hasta 8 transferencias por sesión (`RATE_LIMITED`), repartidas en
  los 4 canales.
- Papelera: `TrashEntry.path` = `/<Carpeta>/.tango-trash/<id>` (id de 20 hex); es lo que recibe
  `files.restore`. `files.trash.empty {share}` acepta el nombre o el id de la carpeta. La
  papelera vive dentro de cada carpeta (`.tango-trash`, oculta en el listado y en SMB no se
  publica por ser oculta).
- `files.versions` devuelve solo las instantáneas donde el archivo existe; `files.restoreVersion
  {path, snapshot: "<carpeta>@<nombre>"}` copia (reflink) como "`<nombre> (restaurado AAAA-MM-DD HH.MM)<ext>`".
- `files.thumb`: JPEG/PNG/GIF; para el resto `NOT_SUPPORTED`. Cacheado en `/var/lib/tango/thumbs`.
- Errores estables: `PATH_NOT_FOUND`, `PERMISSION_DENIED`, `PATH_EXISTS`, `INVALID_NAME`,
  `PATH_ESCAPE`, `NO_SPACE`, `NOT_SUPPORTED`, `WORKER_UNAVAILABLE`, `XFER_NOT_FOUND`,
  `XFER_INCOMPLETE`, `CHECKSUM_MISMATCH`, `SNAPSHOT_NOT_FOUND`, `SHARE_NOT_FOUND`.
- File-worker: `tango-agent` se re-ejecuta como `tango-agent __fileworker` con
  `setresuid/setgroups` al usuario (UID ≥ 2000) y le pasa a `tangod` el socket SEQPACKET por
  `SCM_RIGHTS` (op `files.worker`). El worker se niega a arrancar si conserva UID 0 o alguna
  capacidad. `tangod` cierra los workers inactivos (5 min) y los relanza cuando cambian los
  permisos de una carpeta (los grupos se toman al lanzar).

## 11. Fase M2.2: mantenimiento de volúmenes

```jsonc
// IDs estables: Volume.id = UUID del filesystem Btrfs (no "md127"). Se migran apps.json/shares.json.
storage.disks.replacePlan {"volume","failed":"<Disk.id>|null","replacement":"<Disk.id>"} → plan (como §7, con "summary.rebuild":{"from","to","estimatedSec"})
storage.disks.replace     {"plan_id","confirm_token","typed_name"} → {"job"}     // mdadm --add (y --remove del fallado) + rebuild con progreso
storage.volumes.expandPlan {"volume","add":["<Disk.id>",…]} → plan                 // raid1→ más espejos no; raid5/6/10: --grow --raid-devices; basic→raid1 (convertir) opcional
storage.volumes.expand     {"plan_id","confirm_token","typed_name"} → {"job"}     // reshape + btrfs filesystem resize max
storage.disks.locate       {"disk"} → {}                                           // LED de identificación (si el hardware lo soporta; si no NOT_SUPPORTED)
snapshots.schedule.get     {"share"} → Schedule
snapshots.schedule.set     {"share","schedule":Schedule} → {}
snapshots.delete           {"id"} → {}
// Schedule: {"enabled":bool,"hourly":{"every":1|2|4|6|12,"keep":24},"daily":{"at":"03:00","keep":14},"weekly":{"day":0..6,"at":"03:30","keep":8}}
alerts.list                null → Alert[]   // {id, level, title, body, at, source:"md|btrfs|smart|space", resolved:bool}
// eventos: storage.changed, alerts.changed, notice (degradado, rebuild terminado, espacio < 10 %)
```

- Monitoreo continuo en `tangod`: lee `/sys/block/md*/md/{degraded,sync_action,sync_completed}`,
  `/sys/fs/btrfs/<uuid>/devinfo/*/error_stats`, y espacio libre cada 30 s; emite alertas y avisos.
- Scheduler de instantáneas en `tangod` (sin cron), con retención por nivel, persistido en
  `/var/lib/tango/snapshot-schedules.json`; las instantáneas programadas tienen `kind:"scheduled"`.

**Implementación (M2.2):**

- **IDs:** `Volume.id` = UUID del Btrfs (`/sys/fs/btrfs/<uuid>/devices/<md>`); un array sin
  Btrfs visible usa `md-<nombre>`. Al arrancar, `tangod` migra `apps.json` (el único archivo que
  guarda volúmenes; `storage.json` usa puntos de montaje) por punto de montaje o por la forma vieja
  del ID, de forma idempotente, dejando `apps.json.bak-m22` la primera vez.
- **Campos nuevos:**
  - `Volume.task`: `"rebuild"|"reshape"|"resync"` (se omite si md está quieto).
  - `Volume.taskSpeed`: bytes/s (`sync_speed` × 1024).
  - `Volume.taskEtaSec`: segundos, `(total − hechos) × 512 / taskSpeed` de `sync_completed`.
  - `Disk.rebuilding: true` en el miembro que recibe la reconstrucción (ni `in_sync` ni `faulty`).
- **`replacePlan`:**
  - Sin `failed`, toma el miembro que md marca como `faulty`. Si no hay ninguno y el volumen no está degradado → `INVALID_PARAMS`.
  - El reemplazo tiene que ser libre, elegible y ≥ el miembro más chico; si no, `DEVICE_TOO_SMALL`.
  - Un volumen `basic` → `NOT_SUPPORTED`.
  - Con un `reshape`/`resync` o un scrub en curso → `VOLUME_BUSY`.
  - El agente (`md.replace`) marca como fallado y saca el disco viejo, le borra las firmas si responde, revalida la huella del nuevo, lo agrega y espera el `recover`.
- **`expandPlan`:**
  - Solo RAID 5/6/10 sanos y montados. RAID 1 y Básico → `NOT_SUPPORTED` con la explicación (un RAID 1 se agranda reemplazando sus discos por otros más grandes).
  - `summary.notes` dice de cuánto a cuánto pasa.
  - El agente (`md.grow`) hace `--add` + `--grow --raid-devices=N` (con `--backup-file` en `/var/lib/tango-agent` solo si mdadm lo pide), espera el `reshape` y agranda el Btrfs con `BTRFS_IOC_RESIZE "1:max"` (ioctl).
- **Planes:** el disco a reemplazar y la cantidad final de discos van firmados en el token. Un plan de reemplazo no sirve para ampliar ni al revés.
- **Instantáneas programadas:**
  - Nombre `<hora UTC>.<nivel>`, por ejemplo `2026-10-14T15-00-00Z.hourly`.
  - `snapshots.list` devuelve `kind:"scheduled"` para esas y `"manual"` para el resto.
  - `Schedule.<nivel>.keep = 0` apaga ese nivel.
  - Una carpeta sin política usa la por defecto (cada 1 h ×24, diario 03:00 ×14, semanal domingo 03:30 ×8, activada).
  - Si `tangod` estuvo apagado, hace a lo sumo una por nivel (la del último horario pasado).
  - La retención borra las más viejas de cada nivel con `snapshot.delete` (ioctl, solo bajo `@snapshots/<carpeta>/`).
  - Las manuales nunca se podan.
- **Alertas** (`alerts.json`, se guardan las 50 últimas resueltas):

  | id | nivel | se resuelve |
  |---|---|---|
  | `md:<vol>:degraded` | crit; pasa a warn "reconstruyéndose" durante el rebuild | cuando el RAID vuelve a sano |
  | `md:<vol>:readonly` | crit | — |
  | `btrfs:<vol>:errors` | warn | — |
  | `space:<vol>:low` | warn < 10 %, crit < 5 % | ≥ 12 % libre |
  | `smart:<disk>` | según SMART | — |

  Abrir o resolver una alerta emite un `notice`; si el volumen ya no existe, la alerta se resuelve en silencio.
- **`storage.disks.locate`:** usa `/sys/class/enclosure`; sin gabinete con LEDs → `NOT_SUPPORTED`.
- **Pruebas:** `TANGOD_SNAPSCHED_UNIT=minute` hace que la "hora" del programador dure un minuto.

## 12. Fase M4: producto

```jsonc
// Primer arranque (sin sesión mientras setup.state == "pending"; luego desaparece)
setup.state      null → {"state":"pending|done","hostname","suggestedHostname"}
setup.complete   {"hostname","admin":{"name","full","password"},"timezone","certificate":"selfsigned|localca"} → {"user","token"}
// Actualizaciones (agente: apt por allow-list; instantánea del root antes; GRUB/grub-btrfs para volver)
system.updates.check  null → {"tangoos":{"current","available"|null},"debian":[{"name","from","to","security":bool}],"checkedAt"}
system.updates.apply  {"scope":"all|security"} → {"job"}          // crea snapshot @rootfs-<fecha> antes
system.rollback.list  null → [{"id","at","label"}]               // instantáneas del root
system.rollback.apply {"id","typed":"VOLVER"} → {"job"}           // marca default subvol + reinicio programado
system.reboot / system.shutdown {"typed":"REINICIAR"|"APAGAR"} → {}
// Certificado
cert.info        null → {"mode":"selfsigned|localca|acme","subject","sans":[…],"notAfter","fingerprint"}
cert.localca     null → {"job"}      // genera CA local (10 años) + cert del servidor; recarga TLS sin reinicio
cert.caDownload  null → {"filename":"tangoos-ca.crt","pem":"…"}
// Servicios de archivos
services.list    null → [{"id":"smb|nfs|sftp|webdav|iscsi","enabled","running","port","detail"}]
services.set     {"id","enabled", "options":{…}} → {"job"}
nfs.exports.set  {"share","clients":[{"host":"192.168.1.0/24","access":"rw|ro"}]} → {}
iscsi.targets.list / iscsi.targets.create {"name","volume","sizeBytes","chap":{"user","secret"}?} / iscsi.targets.delete {"name","typed"}
// Notificaciones
notify.channels.get null → {"email":{…},"ntfy":{…},"telegram":{…},"levels":["crit","warn"]}
notify.channels.set {…} → {}
notify.test         {"channel":"email|ntfy|telegram"} → {}
// Registro (auditoría + journal)
audit.list   {"q"?,"since"?,"limit":200} → AuditEvent[]   // {at, actor, ip, client, method, target, result, job?}
logs.query   {"unit"?: "tangod|tango-agent|smbd|docker|kernel", "q"?, "since"?, "limit"} → [{at, unit, prio, msg}]
// Terminal (canal "term", binario, docs/03): term.open {"cols","rows"} → {"id"}; term.resize; term.close — solo admin, auditado
```

- **Auditoría:** toda llamada que modifica algo (y todo login) genera un `AuditEvent` en
  `/var/lib/tango/audit.jsonl` (rotado). Nunca incluye contraseñas ni tokens.
- **Resume seguro:** `auth.resume` pasa a "el host prueba primero" (docs/07 §2): el servidor envía
  `{"type":"challenge","nonce"}` y `HMAC-SHA256(SHA-256(token), "tango resume\n"+nonce)` antes
  de que el cliente revele el token.
- **Proxy de apps con sesión:** el proxy por app exige una cookie `tango_app` (HttpOnly, Secure,
  SameSite=None, 12 h) emitida por `apps.ticket {"id"} → {"url"}` (URL de un solo uso que setea la cookie).

### 12.1 Aclaraciones del frontend M4 (para el backend)

- **`nfs.exports.get {"share"} → {"clients":[{host, access}]}`**: el editor de acceso NFS lo necesita
  para mostrar lo actual (el contrato solo tenía `set`). `share` es el `Share.id`.
- **`services.changed {"id"?}`** (evento): después de `services.set`, `iscsi.targets.create|delete`
  y cualquier cambio de estado de un servicio. La UI refresca `services.list` e `iscsi.targets.list`.
- **`audit.event`** (evento, mismo tipo que `AuditEvent`): la app Registro lo agrega en vivo arriba
  de la tabla. Solo a sesiones de administradores.
- **`IscsiTarget`**: `{name, iqn, volume, sizeBytes, chap: bool, sessions: number}`. Error `TARGET_EXISTS`,
  `NO_SPACE`; secreto CHAP de 12–16 caracteres (límite de LIO).
- **`setup.complete`**: si la contraseña es débil → `WEAK_PASSWORD`; si ya está hecho → `SETUP_DONE`.
  El `token` devuelto se guarda como sesión (igual que `auth.login`).
- **Canal `term`**: frames `[u32 id BE][bytes]` en ambos sentidos. Un frame del NAS con **4 bytes y sin
  datos** significa "la terminal `id` terminó" (exit del shell). `term.open` requiere admin
  (`PERMISSION_DENIED` si no) y crea un `AuditEvent` `term.open`.
- **`system.reboot`/`system.shutdown`**: responder `{}` *antes* de reiniciar/apagar (la UI pasa a
  "Reconectando…" y espera que el transporte caiga y vuelva).
- **`notify.channels.get`**: las contraseñas/tokens se devuelven vacíos si no cambiaron; en `set`,
  un campo secreto vacío significa "conservar el actual".

### 12.2 Implementación del backend M4 (decisiones)

- **Resume seguro:** método público `auth.challenge {"tokenId","nonce"} → {"proof"}`.
  `tokenId = hex(SHA-256(SHA-256(token)))[:32]`, `proof = hex(HMAC-SHA256(SHA-256(token), "tango resume\n"+nonce))`
  con el nonce **del cliente** (16–128 caracteres). El cliente lo verifica antes de mandar `auth.resume`
  (`web/packages/client/src/hostproof.ts`, vector compartido con Go). Compatible hacia atrás: si
  `auth.challenge` no está en `hello.methods`, se usa el resume clásico.
- **Auditoría:** hook `Router.OnCall` → `audit.jsonl` (0600, 10 MB × 5). Se registra todo lo que modifica,
  todo login y todo rechazo (`denied`); los parámetros se reducen a `target` sin claves sensibles.
  WebDAV y SFTP auditan sus escrituras y logins aparte.
- **Agente:** lo que necesita red, montar el nivel superior del Btrfs del sistema, módulos del kernel o
  un shell real corre como unidad transitoria (`tango-agent m4-helper …`). Las ops asíncronas
  (`updates.apply`, `rollback.apply`, `nfs.apply`, `iscsi.apply`) se siguen con `m4.job`; `tangod` las
  encola (una a la vez) en vez de fallar con `BUSY`.
- **Actualizaciones:** antes de `apt-get` se crea `/.snapshots/rootfs-<fecha>` (read-only) + copia de
  `/boot` (`rootfs-<fecha>.boot`). La vuelta atrás monta el nivel superior, renombra `@rootfs` a
  `@rootfs-reemplazado-<fecha>`, crea `@rootfs` escribible desde el punto, restaura `/boot` y reinicia.
  En una instalación sin raíz Btrfs (imagen cloud) `system.rollback.list` responde `NOT_SUPPORTED`.
- **Terminal:** el helper crea el PTY en el devpts del host (el agente tiene uno propio por el sandbox),
  arranca `bash -l` como root y devuelve el master por SCM_RIGHTS. Solo administradores, auditado
  (`term.open`), nunca se registran las teclas.
- **SFTP:** servidor propio en Go en `tangod` (puerto 2222, sin shell, sin tocar sshd), contraseñas de
  TangoOS, archivos vía file-worker. **WebDAV:** `https://<nas>/dav/` (Basic sobre TLS), mismo file-worker;
  borrar manda a la papelera. Limitación: `x/net/webdav` responde 404 (no 403) a una escritura denegada.
- **NFS:** `/etc/exports.d/tango.exports` (`root_squash`, `sync`, `sec=sys`); `nfs-kernel-server` se
  instala al activarlo. Los permisos dependen de UID/GID iguales en el cliente (AUTH_SYS).
- **iSCSI:** LIO por configfs (Go), LUN en `/<vol>/@iscsi/<nombre>.img` sin CoW y con provisión fina,
  `generate_node_acls` + CHAP opcional por TPG. La configuración se reaplica al arrancar `tangod`.
- **Proxy de apps:** `apps.ticket` → `https://<host>:<puerto>/.tango/ticket?t=…` (un uso, 60 s) → cookie
  `tango_app` HMAC (12 h, clave en `app-gate.key`); sin cookie, 401. La cookie de TangoOS no llega a la app.
- **Primer arranque:** `setup.state` es `pending` hasta que existe `setup-done` o un usuario ya inició
  sesión. `setup.complete` crea el admin (Unix + Samba si está instalado), quita el admin inicial sin uso,
  y recién después cambia hostname y zona horaria.

## 13. Fase v1.0: 2FA, UPS, streaming, instantáneas navegables

```jsonc
// Verificación en dos pasos (TOTP RFC 6238: SHA-1, 6 dígitos, 30 s, ventana ±1, sin reuso)
auth.login        {"user","password"} → {"user","token"} | {"needs":"totp"}     // con 2FA: todavía sin sesión
auth.totp         {"code"} → {"user","token"}                                    // público; 6 dígitos o código de recuperación "xxxxx-xxxxx"
                  // 5 códigos mal → TOTP_LOCKED y hay que volver a poner la contraseña; sin login previo → TOTP_NOT_PENDING
users.totp.begin  null → {"secret","uri","qr"}           // qr = data:image/svg+xml;base64,…; queda pendiente hasta confirmar
users.totp.confirm {"code"} → {"recoveryCodes":["xxxxx-xxxxx",…10]}   // se muestran una sola vez (se guardan hasheados)
users.totp.disable {"password"} → {}
users.totp.reset   {"name"} → {}                          // solo admin: para quien perdió el teléfono
// User.twofa refleja si está activa.

// UPS (NUT, protocolo TCP 3493 hablado en Go)
ups.status        null → {"mode":"none|local|remote","configured","online","name","model","status":"OL|OB|LB|…",
                          "statusText","batteryPct"|null,"runtimeSec"|null,"loadPct"|null,"inputVoltage"|null,
                          "lastUpdate","onBatterySince"|null,"detected":[{"vendor","product","id"}],"error"?}
ups.config.get    null → {"mode","host","port","upsName","user","netServer","policy":{"action":"shutdown|notify","afterMinutes","belowPercent"}}
ups.config.set    {…mismo objeto…, "password"?} → {"job"}   // local: instala nut, escribe /etc/nut/*, arranca nut-server/driver
// evento ups.changed (cada cambio de estado). Avisos: "Corte de luz", "Volvió la luz", "Batería baja: apagando".
// services.list suma {"id":"ups"}.

// Streaming / rangos y lectura de instantáneas
files.download.begin {"path","offset"?,"length"?,"snapshot"?} → {"xfer","channel","size","chunk","offset","length"}
                      // size = tamaño total; se mandan `length` bytes desde `offset` (frames con offset absoluto)
files.list    {"path","snapshot"?} → FileEntry[]       // snapshot = Snapshot.id ("<carpeta>@<nombre>"): solo lectura
files.thumb   {"path","size","snapshot"?}
files.stat    {"path","snapshot"?} → FileEntry
```

- **Service Worker** (`/sw.js`, solo en contexto seguro): intercepta `/dc/stream/<token>/<nombre>`
  y `/dc/download/<token>/<nombre>`. El token (opaco, aleatorio) lo crea la página para un archivo
  (ruta + snapshot) y vive solo en memoria de esa pestaña; el SW le pregunta por `MessageChannel`
  y la página baja los bytes en pedazos de 2 MiB con `files.download.begin {offset,length}`
  (uno adelantado). `Range: bytes=a-b` → `206` + `Content-Range`; sin Range → `200`;
  `/dc/download/` lleva `Content-Disposition: attachment`. Token desconocido → `404`, rango
  imposible → `416`. Si no hay SW controlando la página, tangod responde `404` a `/dc/…` (nunca el
  SPA) y las apps usan la descarga a Blob como antes.
- **Proxy de apps:** la cookie `tango_app` queda atada al token de la sesión que pidió el ticket
  (`app|sid|exp|firma`, `sid = TokenID`). Si esa sesión cierra o vence, la cookie deja de valer.
- **Cambios externos:** `files.changed` también se emite por cambios hechos por SMB/NFS/SFTP en
  carpetas abiertas en los últimos 60 s (inotify en el file-worker, agrupado cada 250 ms, hasta 64 carpetas por usuario).

### 13.1 Time Machine (SMB) y `shares.update`

```jsonc
Share += {"timeMachine": bool, "timeMachineMaxBytes": int|null}
shares.create {"name","volume","description","smb", "timeMachine"?, "timeMachineMaxBytes"?} → {"job"}
shares.update {"share", "description"?, "smb"?, "timeMachine"?, "timeMachineMaxBytes"?} → {"job"}   // admin
              // timeMachine exige smb; tope ≥ 50 GiB o null (sin tope); tope sin timeMachine → INVALID_PARAMS
              // publica shares.changed y services.changed
// services.list: el detalle de smb suma " · Time Machine: N carpeta(s)"
```

- **Samba** (`/etc/samba/tango.conf`, `[global]`): `fruit:aapl = yes`, `fruit:model = MacSamba`,
  `fruit:metadata = stream`, `fruit:nfs_aces = no`, `fruit:posix_rename = yes`,
  `fruit:veto_appledouble = no`, `fruit:wipe_intentionally_left_blank_rfork = yes`,
  `fruit:delete_empty_adfiles = yes`, `multicast dns register = no` (anuncia Avahi, no smbd; la
  primera vez se reinicia smbd en vez de recargarlo). Cada carpeta: `vfs objects = fruit streams_xattr`;
  con Time Machine, `fruit:time machine = yes` y `fruit:time machine max size = <N>G` (GiB, hacia arriba).
- **Bonjour** (`/etc/avahi/services/tangoos-smb.service`, si existe el directorio): `_smb._tcp` 445,
  `_device-info._tcp` `model=TimeCapsule8,119` y, si hay carpetas Time Machine, `_adisk._tcp` con
  `sys=waMa=0,adVF=0x100` y `dk<i>=adVN=<Carpeta>,adVF=0x82`.
- Recomendado: una carpeta dedicada y un usuario de TangoOS por Mac.

## 14. Cierre v1.0: borrar usuarios, versiones anteriores, WS-Discovery, contraseñas de app

```jsonc
users.delete {"name","typed_name","keepFiles"?: bool}   → {}            // admin
     // typed_name == name (si no CONFIRMATION_MISMATCH); no a uno mismo (CANNOT_DELETE_SELF);
     // no al último administrador (LAST_ADMIN); USER_NOT_FOUND.
     // Borra: usuario de TangoOS (y su 2FA, códigos de recuperación y contraseñas de app),
     // sesiones, cuenta Samba, usuario Unix (userdel, sus grupos tg-* incluidos) y sus entradas
     // de permisos de carpetas; vuelve a aplicar los permisos de todas las carpetas.
     // keepFiles (por defecto true): los archivos quedan con su UID numérico. false: pasan a ser
     // del administrador que borra (chown dentro de /<vol>/@shares de cada volumen).
     // Publica users.changed y shares.changed.
users.appPasswords.list   {"name"?} → [{"id","label","created","lastUsed"|null,"smb":bool}]   // propio; otro usuario: admin
users.appPasswords.create {"label","smb"?: bool} → {"id","label","password","smb"}           // solo para uno mismo; la contraseña se ve una vez
users.appPasswords.revoke {"id","name"?} → {}
```

- **Contraseñas de app:** 24 caracteres aleatorios (base32, en grupos de 4), solo se guarda su
  SHA-256. Sirven para WebDAV y SFTP. **Con 2FA activado, WebDAV y SFTP aceptan solo contraseñas
  de app** (la contraseña de la cuenta no basta porque esos protocolos no pueden pedir el código).
  Sin 2FA aceptan ambas. Máximo 20 por usuario.
- **SMB y 2FA:** Samba no puede pedir un código. Si se crea una contraseña de app con `smb:true`, esa
  pasa a ser la contraseña SMB del usuario (reemplaza la anterior, solo una a la vez); revocarla
  deja la cuenta SMB con una contraseña aleatoria (SMB queda deshabilitado hasta crear otra o cambiar
  la contraseña). Sin 2FA, la contraseña SMB sigue siendo la de la cuenta.
- **Versiones anteriores de Windows:** cada carpeta compartida suma `vfs_shadow_copy2` con
  `shadow:mountpoint = /<vol>/@shares/<Carpeta>`, `shadow:snapdir = /<vol>/@snapshots/<Carpeta>/.gmt`,
  `shadow:format = %Y-%m-%dT%H-%M-%SZ`, `shadow:localtime = no`, `shadow:sort = desc`. `.gmt` tiene un
  symlink por instantánea con el nombre exacto del formato (`2026-10-15T02-00-04Z →
  ../2026-10-15T02-00-04Z.hourly`): Samba lista los nombres con sufijo de nivel pero no los abre. El
  agente mantiene `.gmt` al crear/borrar instantáneas y al aplicar permisos. Las instantáneas de TangoOS aparecen como
  `@GMT-…` en «Propiedades → Versiones anteriores». `tangod` regenera al arrancar las secciones
  de las carpetas existentes cuando cambia el formato (`samba_rev` en `storage.json`).
- **WS-Discovery:** `tangod` responde en UDP 3702 (multicast 239.255.255.250, IPv4) a Probe/Resolve
  y anuncia Hello/Bye, y sirve los metadatos (WS-Transfer Get) en `http://<ip>:5357/<uuid>`. Tipo
  `wsdp:Device pub:Computer`, `pub:Computer = <hostname>/Workgroup:WORKGROUP`. UUID estable derivado
  de `nas-id`. Solo corre si Samba está instalado. Puertos a abrir si hay firewall: 3702/udp, 5357/tcp.

## 15. TangoConnect: acceso remoto y enlaces públicos (signalhub, app `tangoos`)

Reusa signalhub (`github.com/lordbasex/signalhub`, protocolo v1, ver su README) y su coturn.
Producción: `wss://signal.go-link.org/ws?v=1` (requiere `tangoos` en `ALLOWED_APPS`);
desarrollo: instancia local sin restricción. Sin cuentas: códigos + tokens de dispositivo, como go-link.

### Roles
- **NAS** (`tangod`): conexión saliente persistente a signalhub (`register` con `device_id` +
  `device_secret` propios, guardados 0600), renueva credenciales TURN antes de vencer, reconecta
  con backoff. Cada navegador remoto = una `RTCPeerConnection` nueva con el mismo stack (`internal/rtc`):
  canales `rpc/events/metrics/files.N/term` + **`bundle`** (nuevo).
- **Portal** (`https://tangoos.go-link.org`, estático; mismo pipeline S3+CloudFront que desk.go-link.org; `tangoos.go-link.org` podrá ser un CNAME a futuro): landing, "Mis NAS", cargador del escritorio,
  página de enlace público `/s/<deviceID>.<linkID>#<secreto>`. No ve contenido: todo viaja por DTLS entre navegador y NAS.

### Vincular un navegador (como go-link: claim → token; luego reach)
1. Ajustes → TangoConnect → "Vincular navegador" muestra un código de 9 dígitos (pairing de signalhub, 10 min).
2. El portal hace `claim {code}` → sesión con el NAS → PC → en `rpc`: `connect.link {label}` → el NAS
   emite `device_token` (32 B), guarda solo su SHA-256 + label + fecha; el portal lo guarda (IndexedDB) junto
   al `device_id` del NAS y su fingerprint DTLS (pinning).
3. Siguientes veces: `reach {device_id}` → PC → **el NAS prueba primero**:
   `HMAC-SHA256(SHA-256(device_token), "tangoconnect link proof\n"+nonce)` → el portal verifica y recién ahí
   envía `connect.resume {device_token}` → la conexión queda *linked* (aún sin usuario).
4. Después el usuario hace el login normal de TangoOS (`auth.login` + 2FA). Un navegador vinculado sin login
   solo puede `auth.*`, `system.info` y `bundle.*`.
- Métodos (admin): `connect.status` → {enabled, connected, signal, deviceId, natType?, relayOnly}, `connect.enable {enabled}`,
  `connect.pair.start` → {code, expiresAt}, `connect.links.list` → [{id,label,createdAt,lastSeen,ip}], `connect.links.revoke {id}`.

### Escritorio servido por el NAS (canal `bundle`)
- `bundle.manifest` → {version, files:[{path,sha256,size}], signature} (firma Ed25519 del manifiesto con la
  clave de release; la pública va embebida en el cargador del portal y en `tangod`).
- `bundle.get {path}` → frames binarios por `bundle` (mismo framing que files: [u32 id][u64 off][data]).
- El cargador verifica la firma y cada sha256, arma blob: URLs y monta el escritorio con un `RtcTransport`
  sobre la PC ya abierta. Así la UI remota es siempre la versión exacta del NAS.
- **Límite honesto:** si alguien controla el portal puede servir un cargador malicioso; mitigación: cargador
  mínimo, open source, build reproducible con hash publicado, portal self-hosteable y la app nativa.

### Enlaces públicos (compartir archivos)
- `shares.links.create {path, expiresAt?, password?, maxDownloads?}` → `ShareLinkInfo {id, url, invite, owner, …}`.
  **No se usan salas de signalhub** (mueren cuando el NAS se reconecta y hay una por sesión): el enlace apunta
  al NAS y el secreto viaja en el fragmento, que nunca llega al servidor del portal ni a sus logs:
  `url = https://tangoos.go-link.org/s/<deviceID>.<linkID>#<secreto>` (`invite = "<deviceID>.<linkID>"`).
  `allowUpload` → `NOT_IMPLEMENTED` por ahora.
- `shares.links.list {all?}` (all solo admin) / `shares.links.revoke {id}`.
- Visitante: `reach {device_id}` en signalhub → PC → `guest.open {link, secret}` → sesión **guest** limitada a ese
  path: `guest.list {path}` (si es carpeta, rutas relativas al elemento compartido), `guest.download.begin
  {path, offset?, length?}` por `files.N`; con contraseña: `guest.unlock {password}` (argon2id, 5 intentos).
  Contador de descargas (cuenta cuando la descarga empieza desde 0), vencimiento y auditoría
  (`share.link.download`). Errores: `SHARE_LINK_NOT_FOUND`, `SHARE_LINK_EXPIRED`, `SHARE_LINK_LOCKED`, `FORBIDDEN`.

### Pin de la huella DTLS (portal)
- Al vincular, `connect.link` devuelve `fingerprint`; el portal la guarda con el vínculo. En cada `reach`, antes de
  `connect.challenge`, compara la huella de la SDP del NAS con la guardada: si difiere, **corta** (`FINGERPRINT_CHANGED`)
  sin enviar prueba ni token, y ofrece "Olvidar y volver a vincular" (caso legítimo: reinstalación del NAS).

### Implementación (tangod 2026-10) — lo que difiere o precisa lo de arriba
- **Configuración:** `/var/lib/tango/connect.json` (0600): `enabled` (default **false**), `signalURL`
  (default `wss://signal.go-link.org/ws?v=1`), `portalURL` (default `https://tangoos.go-link.org`), identidad
  del dispositivo (`deviceID` UUID + `deviceSecret`), vínculos (solo SHA-256 del token) y enlaces públicos.
  `connect.enable {enabled, signalURL?, portalURL?}`. El certificado DTLS remoto es fijo (`connect-dtls.pem`, 0600,
  10 años, se renueva si le quedan <30 días): su huella (`connect.status.fingerprint`, `"SHA-256 AB:…"`) es la
  que el portal fija al vincular y compara en cada conexión.
- **PCs remotas:** API de pion propia sin mux UDP (puertos efímeros → candidatos srflx/relay con el TURN del hello
  de signalhub); sesión con `client: "remote"` (o `"guest"`), `origin=remote` para auditoría/rate-limit. Un
  handshake que no llega a *linked*/login en 2 min se corta. Máx. 32 PCs remotas.
- **`connect.hello`** (público, solo remoto): `{server, nas_id, name, deviceId, methods, topics, remote, state}` —
  reemplaza al hello del WebSocket (el portal no lo tiene).
- **Estados y gate** (`Router.Gate`, corre antes que cualquier otra verificación):
  `paired` (llegó por claim) → `connect.link`; `unlinked` (llegó por reach) → `connect.challenge {linkId, nonce}` →
  `{proof}` y `connect.resume {linkId, token}` (5 intentos por sesión); `linked` → todo lo normal (login incluido);
  `guest` → solo `guest.*`. `connect.link` devuelve `{linkId, token, deviceId, fingerprint}`.
  Vector de la prueba compartido Go/TS: `protocol/vectors/connect-link-proof.json`.
- **Errores:** `CONNECT_DISABLED`, `NOT_PAIRED`, `LINK_NOT_FOUND`, `LINK_INVALID`, `SHARE_LINK_NOT_FOUND`,
  `SHARE_LINK_EXPIRED`, `SHARE_LINK_LOCKED` (falta `guest.unlock`), `FORBIDDEN`.
- **Bundle:** `bundle.manifest` → `{version, files, signed, signature, keyId}` (`signed` = bytes exactos firmados,
  `signature` Ed25519 base64, `""` en builds sin firmar). `bundle.get {path}` → `{id, size, sha256, channel:"bundle", chunk}`
  y los frames llegan por el canal `bundle`. El manifiesto se firma en build con `cmd/tango-bundlesign`
  (`make web`): clave en `$TANGO_BUNDLE_KEY` o, en desarrollo, `build/keys/dev-ed25519` (gitignored, se genera sola).
  **Release:** la semilla de release vive fuera del repo (secreto de CI / máquina de firma), nunca en la imagen;
  solo la pública (`.tango-manifest.pub` + embebida en el cargador del portal) se distribuye.
- **Enlaces públicos — sin salas.** Las salas de signalhub mueren al reconectar el dueño y hay 1 por sesión, así que
  el NAS **no** abre una sala por enlace: el visitante hace `reach {device_id}` y se identifica con `guest.open`.
  URL: `<portalURL>/s/<deviceID>.<linkID>#<secreto>` — el secreto va en el **fragmento** (no llega al servidor del
  portal ni a sus logs). `shares.links.create` → `ShareLinkInfo {id, url, invite:"<deviceID>.<linkID>", code:"", owner,
  path, name, kind, size, createdAt, expiresAt?, hasPassword, maxDownloads?, downloads, expired}`; `allowUpload`
  → `NOT_IMPLEMENTED`. `shares.links.list {all?}` (all solo admin). Invitado: `guest.open {link, secret}` → info;
  `guest.unlock {password}` (argon2id, 5 intentos); `guest.list {path}` con rutas relativas al elemento compartido;
  `guest.download.begin {path, offset?, length?}` (cuenta 1 descarga cuando empieza desde 0; audita
  `share.link.download`). El invitado lee con los permisos del dueño del enlace, nunca fuera de su path.
- **Cliente web:** `rtcFromSignal(join, {remote:{beforeAuth}})` en `@tango/client` arma un `RtcTransport` sobre la
  señalización de signalhub (el portal implementa `SignalProvider`); `fetchBundleFile(path)` baja y verifica un
  archivo del bundle; `connectLinkProof()` y `parseShareURL()` en `protocol-connect.ts`.
- **Producción:** signalhub necesita `ALLOWED_APPS=go-link,tangoos` y `ALLOWED_ORIGINS` con `https://tangoos.go-link.org`.
  No hace falta subir `MAX_ROOMS_PER_SESSION` (no se usan salas).
