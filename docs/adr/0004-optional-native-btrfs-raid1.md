# ADR 0004 — Native Btrfs RAID1 as an advanced option

- Status: Proposed
- Context: on 2–3 disk volumes, Btrfs RAID1/RAID1c3 does self-heal using checksums.
- Decision: offer it as an "advanced" profile in M4, with its own degraded-mode and replacement tests.
- Consequences: two code paths in Storage; the UI must explain the difference.
