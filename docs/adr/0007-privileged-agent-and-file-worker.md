# ADR 0007 — Privileged agent + per-UID file-worker

- Status: Accepted
- Context: tangod must not be root, and file access must respect the real permissions.
- Decision: `tango-agent` (root, Unix socket, `SO_PEERCRED`, typed ops, journal) performs system operations and launches one file-worker per user with that user's UID/GIDs and `openat2(RESOLVE_BENEATH)`.
- Consequences: the kernel enforces permissions/ACLs and the path traversal class of bugs is eliminated. More processes have to be managed.
