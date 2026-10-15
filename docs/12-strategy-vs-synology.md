# 12 — Strategy: taking on Synology

> Goal: a Synology user looks at TangoOS and thinks *"this is DSM, but on my hardware,
> with my disks, nicer and with no locks"*.

## 1. Where Synology is hurting (facts, October 2026)

| Fact | What happened | Opportunity for TangoOS |
|---|---|---|
| **Disk lock-in** | In April 2025 it limited the 2025 Plus/Value/J models to its own branded or certified disks. There was a strong backlash and, according to the specialized press, sales dropped. It reversed this in **DSM 7.3 (October 8, 2025)**, but **only** for those 2025 models: business/enterprise models and earlier generations still have the compatibility list, and **M.2** pools **still require disks from the list** | "Any disk, any M.2, always". It is written down as a principle and is not negotiable |
| **Media cuts** | DSM 7.2.2 (August 2024) removed Video Station and server-side transcoding (HEVC). Synology recommends Plex/Jellyfin | Jellyfin with **hardware transcoding (Intel QSV)** set up in one click from the App Center |
| **Hardware** | New models with AMD CPUs without hardware transcoding; little RAM; high price for what they include | Runs on an N100/N150 mini-PC or an old PC, which cost quite a bit less and perform better |
| **Shift toward business** | Home and homelab users feel DSM prioritizes the business customer | That user is our initial audience ([01](01-vision-and-scope.md#audience)) |
| **Closed** | Volumes can be read with standard Linux, but DSM, its apps and its roadmap are closed | MIT, no telemetry, public roadmap |
| **Demand signal** | Xpenology exists (pirated DSM on generic hardware) and forums debate moving to TrueNAS or Unraid | People **want the DSM experience on their own hardware**: nobody fills that gap legally |

Sources: [TechSpot](https://www.techspot.com/news/109787-synology-reverses-course-non-certified-hdds-nas-systems.html),
[Neowin](https://www.neowin.net/news/synology-backtracks-on-policy-that-allegedly-turned-buyers-away-from-its-nas-devices/),
[NAS Compares (DSM 7.3)](https://nascompares.com/news/are-synology-nas-ok-to-buy-now/amp/),
[NAS Compares (DSM 7.2.2)](https://nascompares.com/news/synology-dsm-7-2-2-update-a-major-shift-in-multimedia-support/),
[WunderTech (2026 alternatives)](https://www.wundertech.net/what-is-the-best-synology-nas-alternative/).
The sales drop is reported by press sources; Synology has not confirmed it.

## 2. Where Synology is strong (do not underestimate)

- **Polish and stability:** DSM has "just worked" for 15 years.
- **Ecosystem:** Photos, Drive, Active Backup for Business, Surveillance Station, Hyper Backup, C2.
- Good **mobile apps**.
- **Integration with its hardware:** LEDs, beeps, fans, hot-swap, scheduled shutdown.
- **QuickConnect:** remote access with zero configuration.
- **Support** and brand trust.

**Rule:** we do not fight on every front at once. We fight where they are weak and we are
strong, and we match the bare minimum everywhere else.

## 3. The wedges (in order of impact)

### Wedge 1: Migration from Synology ("bring your disks")

DSM uses **mdadm + LVM + Btrfs**, the same stack as TangoOS ([04](04-storage.md)). Synology
itself documents how to read its disks from a generic Linux. So:

1. You take the disks out of the Synology and put them in the TangoOS machine.
2. Storage detects a **"Synology volume (SHR/RAID)"**, assembles the md arrays, activates the LVs and
   **mounts read-only**.
3. Wizard: "Import this volume?". It recreates the shared folders as TangoOS shares,
   without copying data when the format allows it and, otherwise, copying to a new volume.
4. Optional: import users and shared folders from the DSM configuration backup
   (`.dss`), if the format turns out to be readable. This needs research.

This is **the** marketing feature: nobody else offers it as a guided experience. It lands in
**M3 as read-only** and in M4 with import. Tests with real DSM 6 and 7 volumes
(SHR-1, SHR-2, RAID1, RAID5) created on a lab Synology.

### Wedge 2: Free hardware and disks

- Any x86-64, any disk, any NVMe for pools or cache.
- **Community hardware matrix:** each installation can send, optionally and with
  consent, an anonymous hardware report. It is published as "community tested".
- Recommended kits (2-bay N100 mini-PC, 4-bay case with an N305) with a build guide.

### Wedge 3: A better real-time experience

What DSM does not do, or does worse:

| TangoOS | DSM |
|---|---|
| Everything live (disks, jobs, changing files, connected users) over WebRTC | Panels with polling and refreshes |
| Native Windows/Linux app with **the same desktop** and transfers at network speed | Separate apps per function (Drive Client, Active Backup Agent…) |
| Global Spotlight: apps, settings, files and actions ("create a snapshot of Photos") | Separate search per app |
| Terminal built into the desktop, with auditing | Separate SSH |
| Per-file version timeline (snapshots) inside Files | Restore from Snapshot Replication |
| **"Doctor"** assistant: explains in human language why a disk or volume is in trouble and what to do | Technical messages |

### Wedge 4: P2P remote access (TangoConnect)

QuickConnect often goes through Synology's relays when there is no direct connection. TangoConnect
is WebRTC: it **tries P2P first** (hole punching) and only uses TURN as a last resort. It is the same
technology go-link-desk already runs in production. Free for P2P; the relay can be a paid
service to fund the project.

### Wedge 5: Media without cuts

"Media" app (packaged Jellyfin) with Intel iGPU detection and `/dev/dri` enabled
automatically in the container. Hardware transcoding without touching anything.

## 4. Target comparison table (v1.0)

| | Synology DSM | TrueNAS CE | Unraid | OMV | **TangoOS** |
|---|---|---|---|---|---|
| Your own hardware | ❌ | ✅ | ✅ | ✅ | ✅ |
| Any disk | partial | ✅ | ✅ | ✅ | ✅ |
| Open source | ❌ | ✅ | ❌ (paid) | ✅ | ✅ MIT |
| Desktop interface | ✅ | ❌ | ❌ | ❌ | ✅ **and in real time** |
| Easy for non-technical users | ✅ | ❌ | ~ | ❌ | ✅ (goal) |
| Import a Synology volume | — | ❌ | ❌ | ~ manual | ✅ guided |
| Remote access without opening ports | ✅ | ❌ | ✅ (plugin) | ❌ | ✅ P2P |
| Native desktop apps | partial | ❌ | ❌ | ❌ | ✅ |
| Mixed disks (SHR-style) | ✅ | ❌ | ✅ | ❌ | M6 |

The table is a product table, not a marketing one: only what is done and tested gets published.

## 5. Sustainability (to compete you have to last)

Open source MIT always. Possible revenue sources, without locking NAS features:

- TangoConnect relay and DNS/certificates (§07) as a service, with a free tier.
- Paid support for small and medium businesses.
- "TangoOS Ready" hardware kits with partners.
- Sponsors / GitHub Sponsors.

## 6. Impact on the roadmap

- **M2:** detect Synology volumes in the inventory, read-only and without mounting.
- **M3:** mount Synology volumes read-only + browse them in Files.
- **M4:** import wizard + tests with real DSM volumes.
- **M5:** Media app with QSV; TangoConnect.
- From M0: **the visual quality of the prototype is part of the strategy**. It is the first thing
  people compare with DSM.
