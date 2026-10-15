# ADR 0004 — Btrfs RAID1 nativo como opción avanzada

- Estado: Propuesto
- Contexto: en volúmenes de 2–3 discos, Btrfs RAID1/RAID1c3 sí autorrepara con checksums.
- Decisión: ofrecerlo como perfil "avanzado" en M4, con tests de degradado y reemplazo propios.
- Consecuencias: dos caminos de código en Almacenamiento; la UI debe explicar la diferencia.
