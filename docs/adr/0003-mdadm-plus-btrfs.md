# ADR 0003 — mdadm + single-device Btrfs

- Status: Accepted (rules out Btrfs native RAID)
- Context: Btrfs native RAID5/6 is not reliable. Synology uses md + Btrfs.
- Decision: mdadm provides redundancy (RAID1/5/6/10); Btrfs runs single (metadata DUP) on top, with subvolumes = shares.
- Consequences: Btrfs detects corruption but does not self-heal from the md mirror. This is mitigated with scrubs and alerts. The SHR-style hybrid is built later with md over slices + LVM.
