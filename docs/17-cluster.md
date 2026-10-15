# 17 — Cluster (GlusterFS)

Several TangoOS boxes joined with GlusterFS, managed from the **Cluster** app. Decision and trade-offs in
[ADR 0011](adr/0011-glusterfs-cluster.md); wire contract in [protocol §16](../protocol/README.md#16-cluster-glusterfs).

## 1. What a user can do

| Task | Where |
|---|---|
| Install GlusterFS on this NAS (firewall first) | Cluster → "Install GlusterFS" |
| Invite another TangoOS (open this side for 15 min) | Cluster → Invite a node |
| Join a node (`peer probe`) | Cluster → Add a node |
| Create a volume: replica 2/3/4, arbiter, distributed-replicate; brick per node on a TangoOS volume | Cluster → Create volume |
| Presets: "Production replica 3" (the 17 options below) or GlusterFS defaults | Create volume / Options |
| Start, stop, delete; heal now | Volume view |
| Change options (allow-list only) | Volume → Options |
| Add, remove, replace bricks | `cluster.volume.bricks` (API; UI pending) |
| Mount on this NAS at `/cluster/<volume>` and share it over SMB | Volume → Mount… |
| See it as `gluster volume info` | Volume → terminal button |
| Nodes with state and latency, invitations, firewall allow-list | Nodes |
| Orphan bricks (data left by a deleted volume) | Overview → Orphan bricks |
| Alerts: split-brain, heal pending for ~3 min, bricks or nodes offline | Notifications + optional "Cluster" widget |

## 2. Preset "Production replica 3"

Exactly the options of a production replica-3 cluster:

```
cluster.heal-timeout: 10            performance.write-behind: off
cluster.shd-wait-qlength: 512       diagnostics.client-log-level: WARNING
cluster.shd-max-threads: 2          diagnostics.brick-log-level: WARNING
performance.open-behind: off        network.ping-timeout: 10
performance.quick-read: off         cluster.server-quorum-type: server
performance.read-ahead: off         cluster.quorum-type: auto
cluster.granular-entry-heal: on     storage.fips-mode-rchecksum: on
transport.address-family: inet      performance.client-io-threads: on
cluster.server-quorum-ratio: 51%    (cluster-wide: applied with `volume set all`)
```

Server quorum (51 %) stops the bricks of a node that lost the majority; client quorum `auto` only writes
when most copies answer; with replica 3 one node can be down and the volume keeps reading and writing
without split-brain. Client caches are off so every node sees changes immediately.

## 3. Security

- `glusterd` has no authentication: anyone reaching TCP 24007 can `peer probe` a node into their pool.
  TangoOS loads `table inet tango_gluster` (nftables) **before** installing `glusterfs-server`:
  ports 24007, 24008 and 49152–60999 are rejected (TCP reset, so a stranger fails at once) except from
  loopback and the allow-list.
- Allow-list = pool members (resolved to IPs by tangod; the agent has no network) + open invitations.
  Joining needs both sides: B invites A, then A adds B. A's own side opens when A adds B. Once a node is in
  the pool, every other member opens to it on its next sync (every 20 s).
- `tango-gluster-fw.service` has no `ExecStop` (stopping it does not open the ports) and is
  `RequiredBy=glusterd.service` (no ruleset, no glusterd). Updates use `reload`, never `restart`: a restart
  would restart glusterd too.
- Options go through an allow-list with typed values; `auth.allow`, `storage.owner-*` and anything not
  listed are rejected by the agent even if tangod is compromised. Hosts are IPs or lowercase DNS names (never
  something starting with `-`); brick paths must be `/volumeN/@gluster/<volume>`.

## 4. Bricks and the LVM hook

Today a brick is `/volumeN/@gluster/<volume>` on a TangoOS Btrfs volume. `glusterd` creates the brick
directory but not its parents, so every node keeps `/volumeN/@gluster` created (the agent does it while
reading the status). `@gluster` is a plain directory: TangoOS snapshots are of `@shares` and do not include
it.

When the LVM pool exists, production-style bricks will be a dedicated LV with XFS mounted at
`/srv/gluster/<volume>`: extend `gluster.BrickPath`/`ValidBrick` (internal/gluster) and the brick picker; the
rest of the flow (create, status, heal, orphans) does not change.

## 5. Operational notes

- Debian's `glusterd.vol` ships `ping-timeout 0`: after a hard power-off the peer stays "Connected" and
  `volume status` waits 120 s for it. TangoOS sets 10 s on install, and its status reads use the CLI
  `--timeout` so the app never hangs.
- Deleting a volume leaves the data in each brick; each node lists them as orphan bricks and removes them on
  request (only if the volume no longer exists in the cluster).
- A node with bricks cannot be detached (GlusterFS refuses): replace or remove its bricks first.

## 6. Pending

- TLS between nodes (management `secure-access` and I/O `client.ssl`/`server.ssl`): needs a CA shared by
  the cluster. Plan: the first node's local CA signs the others' certificates during the invitation.
- SMB publishing assumes one node serves a given cluster volume (permission group IDs are per node).
- UI for add/remove/replace brick and quota; NFS export of a cluster volume.
- Geo-replication and snapshots of cluster volumes (GlusterFS snapshots need thin LVM bricks).
