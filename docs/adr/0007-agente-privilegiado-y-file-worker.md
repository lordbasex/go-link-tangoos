# ADR 0007 — Agente privilegiado + file-worker por UID

- Estado: Aceptado
- Contexto: tangod no debe ser root, y el acceso a archivos debe respetar los permisos reales.
- Decisión: `tango-agent` (root, socket Unix, `SO_PEERCRED`, ops tipadas, journal) hace las operaciones de sistema y lanza un file-worker por usuario con su UID/GIDs y `openat2(RESOLVE_BENEATH)`.
- Consecuencias: el kernel aplica permisos/ACL y se elimina la clase de path traversal. Hay que gestionar más procesos.
