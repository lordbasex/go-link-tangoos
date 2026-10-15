# 17 — Cluster (GlusterFS)

Several TangoOS boxes joined with GlusterFS, managed from the **Cluster** app. Decision and trade-offs in
[ADR 0012](adr/0012-glusterfs-cluster.md); wire contract in [protocol §17](../protocol/README.md#17-cluster-glusterfs).

## 1. What a user can do

| Task | Where |
|---|---|
| Install GlusterFS on this NAS (firewall first) | Cluster → "Install GlusterFS" |
| Invite another TangoOS: open this side for 15 min and show a **join code** and this box's **fingerprint** | Cluster → Invite a node |
| Join a node: type its IP and code, **compare its fingerprint** with the invited screen, confirm | Cluster → Add a node |
| Certify a node that joined before TLS existed (same code + fingerprint flow, without `peer probe`) | Nodes → "Certify…" |
| Turn TLS on or off for the whole cluster (controlled stop, see §3.3) | Nodes → Encryption between nodes |
| Create a volume: replica 2/3/4, arbiter, distributed-replicate. Per node, a **folder** on a TangoOS volume or a **dedicated volume** (LV with XFS, optionally thin) on a storage pool | Cluster → Create volume |
| Presets: "Production replica 3" (the 17 options below) or GlusterFS defaults | Create volume / Options |
| Start, stop, delete; heal now | Volume view |
| Change options (allow-list only) | Volume → Options |
| Add bricks (one more replica set for space, or one more copy per set), remove a set (data migrates first, with progress), replace a brick, rebalance (with progress) | Volume → Bricks |
| Quotas per directory | Volume → Quotas |
| Snapshots: create, restore, delete (all bricks must be thin dedicated volumes) | Volume → Snapshots |
| Mount on this NAS at `/cluster/<volume>` and share it over SMB | Volume → Mount… |
| Export the mounted volume over NFS | Volume → NFS |
| See it as `gluster volume info` | Volume → terminal button |
| Nodes with state, latency and certificate; invitations; firewall allow-list | Nodes |
| Orphan bricks: folders and dedicated volumes no brick uses anymore | Overview → Orphan bricks |
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

### 3.1 Firewall

- `glusterd` has no authentication: anyone reaching TCP 24007 can `peer probe` a node into their pool.
  TangoOS loads `table inet tango_gluster` (nftables) **before** installing `glusterfs-server`:
  ports 24007, 24008, **24010** (TangoOS control channel) and 49152–60999 are rejected (TCP reset, so a
  stranger fails at once) except from loopback and the allow-list. The ruleset is regenerated when the
  agent starts, so an update that adds a port applies it without touching the allow-list.
- Allow-list = pool members (resolved to IPs by tangod; the agent has no network) + open invitations. Once a
  node is in the pool, every other member opens to it on its next sync (every 20 s).
- `tango-gluster-fw.service` has no `ExecStop` (stopping it does not open the ports) and is
  `RequiredBy=glusterd.service` (no ruleset, no glusterd). Updates use `reload`, never `restart`.

### 3.2 Joining: code and fingerprint

The tangod of every node listens on **TCP 24010** (HTTPS, behind the same firewall): the *control channel*.

1. **B invites A.** B opens its firewall to A for 15 minutes and shows a 10-character **join code**
   (`ABCDE-FGHJK`, 50 bits, no ambiguous letters) and the **fingerprint** of its node key (8 groups of 4 hex
   digits, the first 16 bytes of the SHA-256 of the public key).
2. **A prepares.** The user types B's IP and the code on A. A reads B's TLS certificate (self-signed at this
   point), sends `hello` with a MAC over the code, a nonce and **the fingerprint of the TLS certificate A sees**;
   B answers its certificate request (CSR) with a MAC over the same things. Somebody in the middle presents
   another certificate, so the MACs do not match and A stops before sending anything secret. Five wrong
   codes close B's invitation.
3. **The user compares** the fingerprint A shows with the one on B's screen, and confirms.
4. **A finishes.** The first time, A creates the **cluster CA** (ECDSA P-256, 20 years) and its own
   certificate. A signs B's request (CN = B's hostname, SAN = its names and IPs, server+client auth, 10 years)
   and sends B its certificate plus the CA and the CA key (any member can add the next node, even if the first
   one is gone). B checks that the certificate is for its key and name and signed by that CA, stores it in
   `/var/lib/tango/cluster/` (0700, tangod) and installs it for GlusterFS in `/etc/ssl/glusterfs.{pem,key,ca}`
   through the agent. Then A runs `peer probe`. B's invitation now shows the CA fingerprint it joined.

After joining, nodes talk over the channel with **mutual TLS**: a client certificate signed by the cluster CA
**and** a source address that belongs to a pool member. A removed node keeps its certificate but is no longer
in the pool or in anyone's firewall.

### 3.3 TLS between nodes

With TLS on, `glusterd` (management, `/var/lib/glusterd/secure-access`) and every brick (I/O,
`client.ssl`/`server.ssl`) use the node certificates; each volume has `auth.ssl-allow` = the CNs of the
cluster nodes, so a certificate from the CA that does not belong to a member is refused too. Turning it on (or
off) is a **controlled stop**, run from any node as a job:

1. Every node must answer on the control channel with its certificate (nodes that joined before TLS are
   *certified* first, §3.2 without `peer probe`).
2. Volumes are unmounted on every node (remembering where) and stopped.
3. Every node creates (or removes) `secure-access` and restarts `glusterd`; the job waits until all peers
   reconnect.
4. Every volume gets `client.ssl`, `server.ssl` and `auth.ssl-allow` (or loses them).
5. Volumes start again and are remounted where they were.

New volumes in a TLS cluster are created with TLS before they start. Verified on the lab cluster: a 440 KB
capture of brick traffic while writing a known marker has no plaintext, `openssl s_client` to 24007 shows
the cluster CA, and a client without a certificate is cut at the handshake.

**Revocation.** Removing a node takes its CN out of every `auth.ssl-allow`, out of every firewall (next sync)
and out of the pool, so its certificate opens nothing. There is no CRL yet (GlusterFS wants an OpenSSL hashed
directory per volume); rotating the CA is a pending item.

### 3.4 SMB groups with fixed GIDs

The SMB permission groups of a cluster share (`tg-<share>-rw/-ro`) get a **fixed GID derived from the name**
(FNV-1a into 60000–64999, next free on collision), the same on every node. The POSIX ACLs stored in GlusterFS
therefore mean the same everywhere, and NFS clients (`sec=sys`) can use the same group ID. User IDs are still
per node (TangoOS assigns them in creation order): create the users of a cluster share in the same order on
every node that publishes it, or publish it from one node.

## 4. Bricks

| Kind | Path | What it is |
|---|---|---|
| `dir` | `/volumeN/@gluster/<volume>` | a directory on a TangoOS Btrfs volume |
| `lv` | `/srv/gluster/<volume>/brick` | a dedicated LV `gb_<volume>` with XFS (`-i size=512`) in a storage pool, mounted by `srv-gluster-<volume>.mount` (`RequiredBy=glusterd`). With **thin**, the LV lives in the pool's thin pool `gb_thin`, which grows by size + 25 % for snapshots |
| `snap` | `/run/gluster/snaps/<id>/brickN/brick` | after **restoring a snapshot**, GlusterFS keeps the volume running on the snapshot LVs it mounts there |

Brick LVs and the thin pool have an underscore in their names, so the storage pool views do not list them as
TangoOS volumes. Bricks of a volume can mix kinds. Every form is one entry in `brickForms`
(`internal/gluster/ops2.go`): another kind (e.g. a ZFS dataset) is one more regular expression there plus its
preparation.

`glusterd` creates the brick directory but not its parents, so every node keeps `/volumeN/@gluster`
created. Dedicated volumes are created on their node when the cluster volume is created (or a brick is added
or replaced): on this node through the agent, on the others through the control channel; if anything fails,
the LVs created so far are removed.

**Orphans.** A `dir` brick is an orphan when its volume no longer exists; a dedicated LV is an orphan when no
brick of *this* node uses it (volume deleted, brick replaced, or volume restored from a snapshot). Removing a
dedicated orphan also unmounts and removes the snapshot LVs GlusterFS left from it (only once the volume is
gone).

## 5. Operational notes

- Debian's `glusterd.vol` ships `ping-timeout 0`: TangoOS sets 10 s on install, and its status reads use the CLI
  `--timeout` so the app never hangs.
- A node that just joined spends a few seconds in "Accepted peer request"; volume creation waits for
  "Peer in Cluster" on every node (`PEER_JOINING`).
- Quotas: GlusterFS 11 only *accounts* by default (`feature.simple-quota-pass-through=true`); TangoOS turns
  enforcement on when quotas are enabled. Limits apply a few seconds late (`features.hard-timeout`) and not to
  root. Verified on a folder brick without TLS (writes stop at the limit); **on the lab volume with TLS and XFS
  bricks GlusterFS did not account usage** (limit, list and remove work, enforcement does not) — see §6. With
  quotas on, glusterd sometimes cannot unmount its auxiliary quota mount and a later `remove-brick commit` fails:
  turn quotas off before removing bricks. Quota commands run outside the agent sandbox (auxiliary mount).
- Snapshots need every brick on a thin LV. Restoring needs the volume stopped (`VOLUME_STARTED`), consumes the
  snapshot and moves the volume onto the snapshot LVs (`snap` bricks). The restored root comes back without its
  permissions: mount it again with its SMB share to re-apply them. GlusterFS does not reliably remove bricks
  (`remove-brick commit`) from a restored volume: add/remove before restoring.
- Remove-brick runs in three steps (start → status until completed → commit); rebalance shows files moved,
  size and lookups per node.
- A FUSE mount that exists but does not answer (e.g. a client left from before a TLS change) is redone when
  mounting.
- NFS exports `/cluster/<volume>` with `fsid=<volume UUID>` (a FUSE mount has no stable device number) and
  `root_squash`; the FUSE mount is ordered before `nfs-server`.
- Deleting a volume leaves the data in each brick (orphans, above). A node with bricks cannot be detached.

## 6. Pending

- Quota accounting on volumes with TLS and XFS bricks (GlusterFS 11 reports 0 used there in the lab), and the
  auxiliary quota mount that blocks `remove-brick commit` while quotas are on.

- CA rotation and a CRL for removed nodes (today: `auth.ssl-allow` + firewall + pool).
- Geo-replication; scheduled snapshots of cluster volumes.
- ZFS datasets as bricks (hook in `brickForms`).
- User IDs shared across nodes for SMB/NFS (today only group IDs are fixed).
