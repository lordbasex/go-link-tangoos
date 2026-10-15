# ADR 0008 — Minimal Debian 13 + TangoOS layer distributed via apt

- Status: Accepted (replaces the earlier "Two-phase distribution" version)
- Context: TangoOS is not a new distro: it is "the face" (interface and management) on top of a real minimal Debian 13, like DSM on top of Linux. The author already runs Debian in production (DebPBX).
- Decision: everything of our own is distributed as `.deb` packages (nFPM) in a signed apt repo. The end user installs a trixie debian-installer ISO with preseed (simple-cdd) plus that repo. Updates go through apt, with a Btrfs snapshot of root before each one and rollback via grub-btrfs. The immutable A/B image (mkosi + systemd-sysupdate) is ruled out.
- Consequences: a single distribution model from day 1; Debian keeps delivering security patches. We must take care not to overwrite Debian configs (`*.d/`, `dpkg-divert`). Rollback depends on root snapshots, not on A/B partitions.
- Update 2026-10-14: the end-user ISO becomes **image-based** (minimal Debian 13 prebuilt with debootstrap + squashfs + `tango-install`): it installs in ~1 min and without internet. The installed system is the same apt-managed Debian; netinst + preseed remains as an alternative (`system/iso/`).
