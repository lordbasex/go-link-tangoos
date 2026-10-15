# 12 — Strategy: taking on commercial NAS systems

> Goal: someone who uses a commercial NAS looks at TangoOS and thinks *"this is the experience I know,
> but on my hardware, with my disks, nicer and with no locks"*.

## 1. Where commercial NAS systems are hurting

| Pattern | What happens | Opportunity for TangoOS |
|---|---|---|
| **Disk lock-in** | Vendors have limited new models to their own branded or certified disks, and some still require disks from a compatibility list for M.2 pools | "Any disk, any M.2, always". It is written down as a principle and is not negotiable |
| **Feature cuts** | Built-in media features such as server-side transcoding have been removed in updates, pushing users to third-party apps | Jellyfin with **hardware transcoding (Intel QSV)** set up in one click from the App Center |
| **Hardware** | New models with CPUs without hardware transcoding, little RAM and a high price for what they include | Runs on an N100/N150 mini-PC or an old PC, which cost quite a bit less and perform better |
| **Shift toward business** | Home and homelab users feel the products prioritize the business customer | That user is our initial audience ([01](01-vision-and-scope.md#audience)) |
| **Closed** | Volumes can often be read with standard Linux, but the operating system, its apps and its roadmap are closed | MIT, no telemetry, public roadmap |
| **Demand signal** | People run unofficial copies of commercial NAS software on generic hardware, and forums debate which open alternative to move to | People **want a polished NAS experience on their own hardware**: nobody fills that gap well in the open |

## 2. Where commercial NAS systems are strong (do not underestimate)

- **Polish and stability:** many years of "it just works".
- **Ecosystem:** photos, file sync, backup, surveillance and cloud services, all integrated.
- Good **mobile apps**.
- **Integration with their hardware:** LEDs, beeps, fans, hot-swap, scheduled shutdown.
- **Zero-configuration remote access.**
- **Support** and brand trust.

**Rule:** we do not fight on every front at once. We fight where they are weak and we are
strong, and we match the bare minimum everywhere else.

## 3. The wedges (in order of impact)

### Wedge 1: Migration from another NAS ("bring your disks")

Most commercial NAS systems use **mdadm + LVM + Btrfs or ext4**, the same building blocks as TangoOS
([04](04-storage.md)), and their disks can be read from a generic Linux. So:

1. You take the disks out of the old NAS and put them in the TangoOS machine.
2. Storage detects a **"volume from another NAS"**, assembles the md arrays, activates the LVs and
   **mounts read-only**.
3. Wizard: "Import this volume?". It recreates the shared folders as TangoOS shares,
   without copying data when the format allows it and, otherwise, copying to a new volume.
4. Optional: import users and shared folders from the old system's configuration backup,
   if the format turns out to be readable. This needs research.

This is **the** marketing feature: nobody else offers it as a guided experience. It lands in
**M3 as read-only** and in M4 with import. Tests with real volumes (hybrid RAID with 1 and 2 disks
of redundancy, RAID1, RAID5) created on lab hardware.

### Wedge 2: Free hardware and disks

- Any x86-64, any disk, any NVMe for pools or cache.
- **Community hardware matrix:** each installation can send, optionally and with
  consent, an anonymous hardware report. It is published as "community tested".
- Recommended kits (2-bay N100 mini-PC, 4-bay case with an N305) with a build guide.

### Wedge 3: A better real-time experience

| TangoOS | Typical commercial NAS |
|---|---|
| Everything live (disks, jobs, changing files, connected users) over WebRTC | Panels with polling and refreshes |
| Native Windows/Linux app with **the same desktop** and transfers at network speed | Separate apps per function |
| Global Spotlight: apps, settings, files and actions ("create a snapshot of Photos") | Separate search per app |
| Terminal built into the desktop, with auditing | Separate SSH |
| Per-file version timeline (snapshots) inside Files | Restore from a separate snapshot app |
| **"Doctor"** assistant: explains in human language why a disk or volume is in trouble and what to do | Technical messages |

### Wedge 4: P2P remote access (TangoConnect)

Vendor remote-access services often go through the vendor's relays when there is no direct
connection. TangoConnect is WebRTC: it **tries P2P first** (hole punching) and only uses TURN as a last
resort. It is the same technology go-link-desk already runs in production. Free for P2P; the relay can
be a paid service to fund the project.

### Wedge 5: Media without cuts

"Media" app (packaged Jellyfin) with Intel iGPU detection and `/dev/dri` enabled
automatically in the container. Hardware transcoding without touching anything.

## 4. Target capabilities (v1.0)

| | Typical commercial NAS | **TangoOS** |
|---|---|---|
| Your own hardware | ❌ | ✅ |
| Any disk | partial | ✅ |
| Open source | ❌ | ✅ MIT |
| Desktop interface | ✅ | ✅ **and in real time** |
| Easy for non-technical users | ✅ | ✅ (goal) |
| Import a volume from another NAS | — | ✅ guided |
| Remote access without opening ports | ✅ (vendor relay) | ✅ P2P |
| Native desktop apps | partial | ✅ |
| Mixed disk sizes (hybrid RAID) | ✅ | in progress |

The table is a product table, not a marketing one: only what is done and tested gets published.

## 5. Sustainability (to compete you have to last)

Open source MIT always. Possible revenue sources, without locking NAS features:

- TangoConnect relay and DNS/certificates (§07) as a service, with a free tier.
- Paid support for small and medium businesses.
- "TangoOS Ready" hardware kits with partners.
- Sponsors / GitHub Sponsors.

## 6. Impact on the roadmap

- **M2:** detect volumes from other NAS systems in the inventory, read-only and without mounting.
- **M3:** mount them read-only + browse them in Files.
- **M4:** import wizard + tests with real volumes.
- **M5:** Media app with QSV; TangoConnect.
- From M0: **the visual quality of the prototype is part of the strategy**. It is the first thing
  people compare.
