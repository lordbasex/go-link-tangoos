# ADR 0003 — mdadm + Btrfs de un solo dispositivo

- Estado: Aceptado (descarta el RAID nativo de Btrfs)
- Contexto: el RAID5/6 nativo de Btrfs no es confiable. Synology usa md + Btrfs.
- Decisión: la redundancia la da mdadm (RAID1/5/6/10); Btrfs va single (metadata DUP) encima, con subvolúmenes = shares.
- Consecuencias: Btrfs detecta la corrupción pero no la autorrepara desde el espejo md. Se mitiga con scrubs y alertas. El híbrido tipo SHR se construye luego con md por rebanadas + LVM.
