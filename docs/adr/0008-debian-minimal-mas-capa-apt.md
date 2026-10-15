# ADR 0008 — Debian 13 minimal + capa TangoOS distribuida por apt

- Estado: Aceptado (reemplaza la versión anterior "Distribución en dos fases")
- Contexto: TangoOS no es una distro nueva: es "la cara" (interfaz y gestión) sobre un Debian 13 minimal real, como DSM sobre Linux. El autor ya opera Debian en producción (DebPBX).
- Decisión: todo lo propio se distribuye como `.deb` (nFPM) en un repo apt firmado. El usuario final instala una ISO del debian-installer de trixie con preseed (simple-cdd) más ese repo. Las updates son por apt, con un snapshot Btrfs del root antes de cada una y rollback vía grub-btrfs. Se descarta la imagen inmutable A/B (mkosi + systemd-sysupdate).
- Consecuencias: un solo modelo de distribución desde el día 1; Debian sigue entregando los parches de seguridad. Hay que cuidar no pisar configs de Debian (`*.d/`, `dpkg-divert`). El rollback depende de los snapshots del root, no de particiones A/B.
- Actualización 2026-10-14: la ISO de usuario final pasa a ser **por imagen** (Debian 13 minimal preconstruido con debootstrap + squashfs + `tango-install`): instala en ~1 min y sin internet. El sistema instalado es el mismo Debian administrado por apt; netinst + preseed queda como alternativa (`system/iso/`).
