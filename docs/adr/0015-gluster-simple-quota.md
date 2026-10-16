# ADR 0015 — Cluster quotas with simple-quota, per top-level folder

- Status: Accepted
- Context: GlusterFS 11 ships two quota implementations. The classic one (`gluster volume quota enable`:
  the marker translator, quotad and an auxiliary mount run by glusterd) accounted usage in the lab but never
  stopped a write, and its auxiliary mount made a later `remove-brick commit` fail. The new one,
  simple-quota, is always in the brick graph and is off by default (`feature.simple-quota-pass-through=true`).
  Turning pass-through off alone did nothing: simple-quota only applies limits to **namespaces**, folders
  marked with `trusted.glusterfs.namespace`, and the limit is the xattr `trusted.gfs.squota.limit`, not what
  `volume quota limit-usage` writes. A namespace can only be marked from a mount with a negative client pid.
  With that, writes stop at the limit with `EDQUOT` (verified on the lab with TLS and XFS bricks).
- Decision:
  1. **Quotas are simple-quota.** Turning them on sets `features.tag-namespaces=on` and
     `feature.simple-quota-pass-through=false` (and turns the classic quota off if a volume still has it).
     The agent manages limits from a short-lived mount with the quota helper's client pid
     (`--client-pid=-14`, `/run/tango/squota/<vol>`, one at a time per volume), run outside its sandbox.
  2. **Limits live in the volume option `user.tango.quotas`** (`/Docs=1073741824/Fotos=…`), the source of truth on
     every node. The xattrs are put again on every listing, so bricks added later (DHT does not copy them when it
     creates the folder there) get the namespace and the limit within a minute.
  3. **Only top-level folders.** A namespace inside another one takes its usage out of the parent, so limits
     go on `/<folder>` only. `/` is not a quota (it is the volume size).
  4. **Existing content is counted.** simple-quota only counts what is written after the folder becomes a
     namespace, and what the bricks already had open inside it keeps counting for the root until the brick
     restarts. When the folder is not empty, a job runs on every node with bricks of the volume, one node at
     a time: it restarts that node's bricks (SIGTERM, then `volume start force`), sums what the folder takes
     in each local brick and writes it as `trusted.gfs.squota.size`. On a replica it waits for heal before
     the next node. Then every brick reloads that usage (`glusterfs.quota.reset`). Every node with bricks
     must be connected. Turning quotas back on counts again, without restarts (nothing was counted while
     they were off).
  5. **Usage is saved and shared once a minute.** simple-quota keeps usage in memory and writes it to disk
     only when the quota helper asks for `statfs` (or when the brick stops cleanly). One node (the lowest
     UUID among the connected ones) lists the quotas every minute, which saves it. On a distributed volume
     each replica set only sees its own usage, so the same listing gives every brick the total
     (`glusterfs.quota.total-usage`); until then each set stops at the whole limit on its own.
  6. **Removing a limit** writes a limit that can never be reached in memory and 0 on disk: simple-quota
     ignores a 0 until the brick restarts.
- Consequences: limits stop writes immediately on replicated volumes and within about a minute on
  distributed ones (and on bricks added later); they never apply to root. Setting a limit on a folder in use restarts the bricks of the
  volume one node at a time (a replica keeps serving; with replica 2 the volume is read-only while the first
  brick restarts). Removing bricks works with quotas on. A crash loses at most a minute of accounting.
  simple-quota is marked experimental in GlusterFS 11; the e2e covers enforcement on a replica and on a
  distributed volume, counting existing content, and removing a limit.
