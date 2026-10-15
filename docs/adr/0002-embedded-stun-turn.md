# ADR 0002 — STUN/TURN and signaling embedded in tangod

- Status: Accepted
- Context: the product is LAN-first. It must not depend on external servers (Google STUN, signalhub in the cloud) to work.
- Decision: `tangod` serves `/signal` (signalhub JSON envelope), a STUN/TURN with `pion/turn` on 3478 with ephemeral credentials, and ICE on a single UDP/TCP port (8443) with mDNS enabled.
- Consequences: it works without Internet. TangoConnect (remote) is added later as signaling federated to signalhub + coturn.
