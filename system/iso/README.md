# TangoOS ISO installer

Two ways to build the ISO. Both install **minimal Debian 13 + the TangoOS `.deb`** (ADR 0008).

| | Phase 1: netinst + preseed | Phase 2: image-based (**recommended**) |
|---|---|---|
| Script | `build.sh` | `image/build-image.sh` |
| How it installs | debian-installer downloads Debian from `deb.debian.org` | copies a prebuilt Debian (squashfs) to the disk |
| Internet during install | yes | **no** |
| Measured time (VM, 2 vCPU, 2 GB) | 422 s (UEFI) | **83–89 s** unattended UEFI; graphical: 33–35 s of installation |
| Size | 621 MB | 533 MB (388 MB without the graphical installer; sha256 next to the ISO) |
| Interface | text (d-i) | **graphical by default** (local screen + another PC with a code); optional text mode |
| Questions | disk (if there is more than one) + confirm wipe | language/keyboard, disk + confirm wipe, name, network (DHCP/static IP) |
| Make | `make iso-netinst` | `make iso` |

Times = from `qm start` until `https://<ip>/` serves the TangoOS desktop (includes the
5 s of the GRUB menu and the reboot). Measured on 2026-10-14 on the development Proxmox (VM 109).
The previous version of the image (328 MB, without dependencies or apt lists) took 59 s BIOS /
68–76 s UEFI; adding Samba, NFS and the apt lists added ~60 MB and ~15–20 s.

ISOs generated on the Proxmox (`local:iso/`):

| File | What it is |
|---|---|
| `tangoos-0.1.0-dev-amd64.iso` | **product**: graphical installer by default (or text mode), DHCP, no SSH |
| `tangoos-0.1.0-dev-amd64-test.iso` | lab: default entry with no questions (disk `TANGO-SYS`, IP 172.17.0.46), root SSH with the development key (also in the live system) |
| `tangoos-0.1.0-dev-amd64-netinst-test.iso` | phase 1 (netinst + preseed), lab, IP 172.17.0.45 |

## Phase 2: image-based ISO (`image/`)

`build-image.sh` (on a Debian/Proxmox host, as root):

1. `debootstrap --variant=minbase trixie` with kernel, GRUB (BIOS + UEFI), Btrfs, systemd-networkd,
   Avahi, `live-boot` and `whiptail`. It is cached in `$WORK/rootfs-base`; rebuilds only change the `.deb`.
2. Configures inside the rootfs: hostname `tango`, `es_AR.UTF-8`, `latam` keyboard, Buenos Aires time zone,
   apt sources (trixie, trixie-updates, trixie-security), no recommends, root locked,
   `apt-get update` + installs `tangoos.deb` **with its dependencies** and the services it manages
   (`samba`, `nfs-kernel-server`; see `RUNTIME_PKGS`), enables `tangod` and `tango-agent.socket`,
   leaves NFS off, writes a minimal `smb.conf` with the TangoOS `include` and **keeps the
   apt lists** (only deletes caches).
3. Builds the **graphical installer layer** with overlayfs on top of that rootfs: `cage cog libpam-systemd
   fonts-dejavu-core`, the `tango-installer` binary (Go), `tango-kiosk`, the units, the live system's DHCP and
   a drop-in so that `tangod` does not start in installer mode. The layer is only the difference
   (`installer.squashfs`, ~152 MB) and **is never copied to the disk**.
4. Compresses the system into `live/filesystem.squashfs` (zstd), writes `live/filesystem.module`
   (layer order) and builds the hybrid BIOS/UEFI ISO with `grub-mkrescue` + its `.sha256`.

Boot menu (GRUB, 5 s):

| Entry | Parameter | What runs |
|---|---|---|
| **Instalar TangoOS** (default) | `tango.installer` | `tango-installer.service` (web :80) + `tango-kiosk.service` (cage + cog on tty1) |
| Instalar TangoOS en modo texto | `tango.installer=text` | `tango-installer-text.service` (whiptail on tty1) |
| Probar TangoOS sin instalar | — | the full system in RAM |
| Arrancar el TangoOS instalado | — | default if it finds `tangoos-boot` |
| Instalar TangoOS (laboratorio, desatendido) | `tango.installer=text tango.auto tango.disk=… tango.ip=…` | only in the `test` variant, where it is the default entry |

The graphical mode is shown on the machine's screen and from another PC at `http://<ip>/` with the
6-digit code shown on the screen (with no graphical display, tty1 shows the URL and the code). Design
and security details: docs/08 §Graphical installer. Both modes use the same engine, `tango-install`
(`--list-disks`; `--disk … --yes [--hostname] [--keyboard] [--ip] [--no-reboot] [--progress]`):

1. **Disk**: lists the disks (never the installation media). If there is more than one it asks which;
   it always asks to confirm the wipe, showing model, serial number and size (docs/04).
2. **Partitions** (GPT): `bios_grub` 1 MB · ESP 512 MB · `/boot` ext4 1 GB · **Btrfs** root with
   subvolumes `@rootfs`, `@var-log`, `@tango`, `@snapshots` (docs/08) and `compress=zstd:1`.
3. `unsquashfs` of the system to the disk, `fstab` by UUID, machine name, keyboard, DHCP network or static IP
   (systemd-networkd).
4. Removes `live-boot`, regenerates the initramfs, installs GRUB for BIOS **and** UEFI (also in the
   removable path `EFI/BOOT/BOOTX64.EFI`, so it boots even if the firmware loses the NVRAM entry).
5. Reboots. `tangod` starts on its own and the console shows `https://<name>.local` and `https://<ip>`
   (`/etc/issue`; there are no console users: root stays locked).

If the media is left in and the firmware boots from USB/CD first, the ISO's GRUB looks for the
`tangoos-boot` partition and, if it exists, boots the installed system by default (without this, on BIOS
it reinstalled in a loop). The "Probar TangoOS sin instalar" (try without installing) option brings up the full system in RAM.

Kernel parameters (lab only): `tango.auto` (does not ask if there is a single disk),
`tango.disk=TANGO-SYS` (with `tango.auto`: the disk by serial number or path even if there are several) and
`tango.ip=172.17.0.46/24,172.17.0.1,8.8.8.8` (static IP). The `test` variant adds them to its default
entry and copies `authorized_keys` for root SSH (installed and live); the `product` variant does not
ship with SSH enabled.

## Phase 1: netinst + preseed (`build.sh`)

Remasters `debian-13.4.0-amd64-netinst.iso` with `xorriso` without unpacking it:

- adds `preseed.cfg` to the text installer's `initrd.gz` (concatenated cpio);
- replaces the GRUB (UEFI) and isolinux (BIOS) menus with "Instalar TangoOS" with a 5 s timeout;
- copies the `.deb` to `/tangoos/` on the ISO; `preseed/late_command` installs it and enables `tangod`;
- reuses the original boot records (`-boot_image any replay`): hybrid BIOS/UEFI/USB.

Files: `preseed.cfg` (common), `preseed-product.cfg` (DHCP, no SSH, **asks to confirm** the wipe),
`preseed-test.cfg` (lab: no questions, static IP, SSH key).

Details that took effort:
- d-i does not accept `!` as the root password: the hash of a random password is set and `late_command`
  runs `passwd -l root`.
- With `console=ttyS0` the installer only offers English (the serial console does not display UTF-8) and asks
  for the language: the lab variant does not use a serial console.
- The `.deb`'s `postinstall` does not enable `tangod` inside a chroot (there is no running systemd):
  the installer runs `systemctl enable tangod.service` explicitly.

## Using the ISO

- **Proxmox**: the ISOs end up in `local:iso/`. q35 VM, OVMF or SeaBIOS, disk ≥ 16 GB, 2 GB of RAM.
- **USB**: it is hybrid → `sudo dd if=tangoos-0.1.0-dev-amd64.iso of=/dev/sdX bs=4M status=progress conv=fsync`
  (on macOS `/dev/rdiskN`). Careful: pick the right device; `dd` does not ask.

## Building

```bash
make iso                        # phase 2, product variant → /var/lib/vz/template/iso/tangoos-<ver>-amd64.iso on the Proxmox
make iso ISO_VARIANT=test       # lab
make iso-netinst                # phase 1
```

Dependencies on the build host (installed on the Proxmox for this work):
`xorriso debootstrap squashfs-tools mtools` (+ `grub-pc-bin grub-efi-amd64-bin`, which Proxmox already ships).
The first build runs debootstrap (~3 min) and caches it; subsequent ones take ~2–2.5 min (the graphical
installer layer is reinstalled with apt; the `.deb` files stay in `$WORK/apt-archives`). `make iso` also builds
`dist/tango-installer` and uploads it along with the scripts.

Known pending items:
- `grub-mkrescue` uses the host's GRUB (on the Proxmox it is `2.12-9+pmx2`); for reproducible builds
  it should be run with the rootfs's own GRUB.
- Secure Boot: the ISO is not signed (tested with OVMF without preloaded keys).
- Resolved: the `.deb`'s `postinstall` uses `deb-systemd-helper` and enables `tangod` also inside
  a chroot; the installers still enable it explicitly (harmless).
