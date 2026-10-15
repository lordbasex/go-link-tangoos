# 11 — Licenses and brand

> This is technical analysis, not legal advice.

## Project license

**MIT**, same as go-link-desk and signalhub. Dependency policy: MIT, BSD, ISC, Apache-2.0,
MPL-2.0 (as a library) and OFL (fonts) are allowed. GPL/AGPL **linked** into our own binaries
is avoided. System tools (Samba GPLv3, mdadm GPLv2, the kernel) are invoked as separate
processes and shipped as Debian packages with their own licenses: that is compatible with
the TangoOS code being MIT.

## Mac-OS-Desktop (mhmdmhd6)

Checked on 2026-10-14 via the GitHub API:

- **It has no LICENSE file** (`license: null`). Its `package.json` says `"license": "ISC"`,
  but that is the `npm init` default and there is no license text. **Without an explicit
  license, the code is "all rights reserved".**
- It contains **Apple assets**: Monterey and Big Sur wallpapers, `apple-white.png`,
  `apple_icon.png` and cursors. Those cannot be redistributed even if the code had a license.

**Decision:** it is used only as a **visual reference**. No code, CSS or assets are copied. If
at some point we want to reuse a specific piece, we must ask the author for an explicit
license (MIT) in writing in an issue.

## Apple

- We do not use the apple logo, macOS wallpapers, Apple app icons, cursors
  or the **SF Pro** font (its license only allows it in software for Apple platforms).
- The *style* (glass, Dock, traffic lights, clean typography) cannot be registered as such and
  many projects imitate it. Even so, the README will say: *"TangoOS is an independent project,
  not affiliated with or endorsed by Apple Inc."*
- Names to **avoid** in the UI: Finder, Launchpad, Spotlight, Mission Control, Dock (generic,
  acceptable), Time Machine (only to name the compatibility: "Time Machine compatible"),
  macOS (only descriptively in docs: "inspired by macOS"). In the code: `launcher`, `search`,
  `overview`.
- **Current state of the code (to fix):** the code does not follow this naming yet. The window overview
  is `web/packages/shell/src/MissionControl.tsx` (`missionLayout.ts`, overlay id `"mission"`, CSS
  `.tg-mission*`), and search is `Spotlight` (overlay id `"spotlight"`). The visible labels are already
  neutral ("Mostrar todas las ventanas" / "Show all windows", "Todas las ventanas", "Buscar" / "Search"),
  but the Spotlight action keywords include "mission control". Pending: rename to `overview` / `search` in
  the code and review every user-visible string and keyword. Mentioning the Mac Finder to explain where the
  NAS appears on the network (first-boot wizard) is descriptive and acceptable.

## Other NAS vendors

The project does not name competing products or vendors in its documentation, interface or marketing,
and does not use their product names, terms or icons. We describe them generically ("commercial NAS
systems").

## Name: TangoOS

Checked on 2026-10-14:

| Resource | Status |
|---|---|
| `tangoos.com` | ❌ registered (2026-07-26, DropCatch) |
| `tangoos.org` | ✅ available |
| `tangoos.io` | ✅ available |
| `tangoos.net` | ✅ available |
| `tango-os.org` | ✅ available |
| GitHub org `tangoos` | ❌ taken (org from 2014) |
| GitHub org `tango-os` | ✅ available |
| Repos with the same name | `SquilliamF21/TangoOS` and `9xbt/TangoOS`: toy OSes, 0 stars, no practical conflict |

Risks to keep in mind: "Tango" is already used by Google (Project Tango, discontinued), the
Tango messaging app and the *Tango Desktop Project* (Linux desktop icons). None of them is a
NAS OS, but it is worth searching the trademark databases (USPTO, EUIPO, INPI Argentina) in
class 9 before investing in branding.

**Recommendation:** register `tangoos.org` + `tangoos.io` now (and `tangoos.net` if it is used for
the certificate DNS of [07 §3](07-security.md)), and create the `tango-os` org on GitHub.

## Our own assets to produce

- TangoOS logo (SVG).
- ~12 app icons (*squircle*, SVG, light/dark).
- 6 wallpapers (generated or CC0, with the source recorded in `design/ASSETS.md`).
- System sounds (optional, CC0).
