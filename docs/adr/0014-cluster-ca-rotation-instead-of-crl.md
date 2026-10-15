# ADR 0014 — Rotate the cluster CA instead of publishing a CRL

- Status: Accepted
- Context: every cluster node keeps a copy of the cluster CA key (ADR 0012), so any node can add
  another one even if the first node is gone. A node that leaves the cluster takes that key with it.
  Removing it already takes its CN out of every `auth.ssl-allow`, out of every firewall and out of the
  pool. But with the CA key, it can sign itself a new certificate with the CN of a node that is still a
  member. A CRL does not help: GlusterFS reads one only from an OpenSSL hashed directory per volume, and
  the removed node could sign a CRL too. Only another CA, whose key the removed node never had, makes its
  material useless.
- Decision:
  1. **No CRL.** The cluster can **rotate its CA** (`cluster.ca.rotate`, a job you can start from any
     node). It creates a new CA and gives every node a new certificate for the key it already has, so
     the fingerprints you compare when joining do not change. Then it retires the old CA.
  2. **Three phases on every node, with both CAs trusted in between**, so the control channel between
     any two nodes never breaks halfway (each phase runs node by node, this node last):
     - `stage`: the node gets its new certificate, the new CA and its key. It trusts both CAs and still
       presents the old certificate.
     - `switch`: it presents the new certificate and signs with the new CA.
     - `finalize`: it trusts only the new CA and writes `/etc/ssl/glusterfs.*`.

     If `stage` fails on any node, the nodes already staged go back to the old CA (`abort`). From
     `switch` on, every node accepts both CAs, so the job can be **started again** to finish.
  3. **GlusterFS reloads in one controlled stop.** glusterd and the bricks read the certificates when
     they start. If management TLS or any TLS volume is on, the job runs the same stop as turning TLS
     on: unmount, stop the volumes, restart glusterd on every node, start the volumes and remount.
     Without TLS in GlusterFS, nothing stops.
  4. **Every node must be connected.** A node that is off would keep the old CA and drop out of the
     cluster. The job refuses to run, and the UI says which node to turn on or remove first.
  5. **Removals are tracked.** Removing a node records it on the node where it was removed, as
     "left with the CA key". The Cluster app shows a pending warning with a "Rotate the CA" button
     until the next rotation clears it.
- Consequences: rotating costs the same short downtime as turning TLS on, and you do it on purpose:
  it is offered, never automatic. Node keys are not rotated, so a node whose disk was stolen should
  also be removed and joined again (new key). Joins are refused while a rotation is running.
