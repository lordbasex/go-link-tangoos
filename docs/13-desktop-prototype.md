# 13 — Desktop prototype (specification)

Details the "Prototype" section of [05](05-desktop-and-themes.md). This is the document used
to build it.

## 1. Goal

A **navigable desktop, on the final stack and with labeled demo data**, that demonstrates:

1. Visual quality on par with Mac-OS-Desktop and above DSM: it is the first thing people compare ([12](12-strategy-vs-synology.md)).
2. A real window manager (not fixed positions).
3. The "real-time" feel: metrics that move, jobs with progress, notifications that arrive.
4. That the apps talk to a `Transport` interface: later `MockTransport` is swapped for WebRTC without touching the apps.
5. That the theme engine works: light/dark + accent color, live.

**Not included:** real backend, WebRTC, real login, i18n (only `es`) or mobile. Today the UI is still Spanish-only: strings are
written inline and there is no i18n framework or translation keys.

## 2. Stack and structure

React 19 + TypeScript + Vite + Vitest + Playwright, npm workspaces. No state library.
Lucide + Inter. CSS Modules + `--tg-*` custom properties.

```
web/
├─ apps/desktop/                 # main.tsx: registers the theme, the apps and the transport
├─ packages/client/              # Transport, RpcClient, useLive(), MockTransport + scenarios
├─ packages/shell/               # WM, MenuBar, Dock, Launcher, Spotlight, Notifs, ControlCenter, ContextMenu
├─ packages/ui/                  # Button, IconButton, Segmented, Toggle, Slider, Table, Sidebar,
│                                # Toolbar, Sheet, Popover, Toast, Badge(DEMO), Ring, Sparkline, Wizard
├─ packages/theme-milonga/       # tokens.css (light/dark), slots, motion, wallpapers, app icons
└─ packages/app-{storage,files,settings,monitor}/
```

What exists today goes beyond this plan:

```
web/
├─ apps/desktop/                 # the desktop (served by tangod and by the TangoConnect portal)
├─ apps/connect/                 # TangoOS Connect portal ([16](16-tangoconnect.md))
├─ e2e/                          # Playwright: desktop.spec.ts, connect.spec.ts
└─ packages/
   ├─ client/ shell/ ui/ theme-milonga/ widgets/      # widgets: desktop widgets (clock, CPU, network, storage…)
   └─ app-{storage,files,settings,monitor,snapshots,log,terminal,apps}/
```

There is no `packages/protocol`: the protocol types live in `packages/client/src/protocol*.ts`
(handwritten).

## 3. Screens

### 3.1 Boot

Wallpaper background with a strong blur, the TangoOS logo centered and a thin progress bar. It lasts
≤ 1.2 s in the prototype: loading the shell is real and the rest is simulated.

### 3.2 Login

```
┌──────────────────────────────────────────────────────────────┐
│                         Wed Oct 14                           │
│                          22:41            (large clock, 300) │
│                                                              │
│                         ( avatar )                           │
│                          Federico                            │
│                    [ ••••••••     → ]                        │
│                    Switch user · DEMO                        │
│                                                              │
│  tango.local · 3 disks OK                        ⏻  ⟳        │
└──────────────────────────────────────────────────────────────┘
```

- Wrong password ("error" in demo mode): the card does a horizontal *shake* of 3
  cycles in 300 ms. Any other password logs in.
- On login, the clock and the card fade out and the desktop comes in with a 1.02→1 scale.

### 3.3 Desktop

```
┌ MenuBar 28px ────────────────────────────────────────────────────────────────────────┐
│ ◆ Files  File  Edit  View  Go  Window  Help                ⟳2  ⛁  ⇅  🔔  ⊞  👤  Wed 22:41 │
└──────────────────────────────────────────────────────────────────────────────────────┘
   (wallpaper)        ┌──────── window ─────────┐
                      │ ● ● ●   Files      ⌕ ⇪  │
                      │ side │ content          │
                      └─────────────────────────┘
                 ┌───────────── Dock ─────────────┐
                 │ ▦  📁 ⛁ ⚙ 📈 ≡ >_ │ ⬇ 🗑      │
                 └────────────────────────────────┘
```

- **◆ TangoOS menu** (our own logo, never the apple): About this NAS, Settings, Updates,
  Lock, Log out, Restart, Shut down. Restart and Shut down are disabled in demo.
- **Status icons, from left to right:**
  - ⟳ Activity: running jobs, with their count and a progress popover.
  - ⛁ Volume health: green, amber or red, always paired with a distinct icon.
  - ⇅ Transfers.
  - 🔔 Notifications.
  - ⊞ Control center.
  - User.
  - Date and time.
- **Desktop context menu:** Change wallpaper…, Arrange windows, Show desktop, Appearance settings.

### 3.4 Lock

`Alt+L` or the ◆ menu: goes back to the login screen, but keeps the windows.

## 4. Shell components

### 4.1 MenuBar

| Property | Value |
|---|---|
| Height | 28 px |
| Background | `--tg-menubar-bg` + `backdrop-filter: blur(30px) saturate(180%)` |
| Typography | 13 px/500. The active app's name is 600 |
| Menus | Each app defines them in its manifest. They open on click and, with one open, on hover (as in macOS). Keyboard: ←/→ between menus, ↑/↓ inside, Enter, Esc |
| Item | 22 px tall, padding 0 10 px, radius 4 px, hover `--tg-accent` with white text, shortcut right-aligned in `--tg-text-2` |

### 4.2 Dock

| Property | Value |
|---|---|
| Base icon | 52 px. Container with 6 px padding and 18 px radius |
| Magnification | `s = 1 + (sMax − 1) · (cos(min(d/R, 1)·π) + 1) / 2` with `sMax = 1.6`, `R = 140 px` and `d` = horizontal distance from the cursor to the icon center. Applied with `transform: scale()` and `translateY` so it grows upward; neighbors move apart with a `margin` animated via the container's `transform`, no layout |
| Launch | 2-hop bounce (`translateY` −14 px, 600 ms) until the window opens |
| Indicator | 4 px dot under open apps |
| Tooltip | name above, 150 ms delay, blurred pill |
| Zones | Launcher · pinned apps · separator · minimized (thumbnail) · Downloads · Trash |
| Context menu | Open / Show all windows / Keep in Dock / Close |
| Preferences | position (bottom/left), size, magnification on/off, auto-hide |

### 4.3 Window (Milonga theme WindowFrame)

| Property | Value |
|---|---|
| Radius | 12 px; contents are clipped with `overflow: clip` |
| Title bar | unified with the toolbar: 52 px with a toolbar, 32 px without one |
| Traffic lights | 12 px, 8 px apart, 14 px from the edge. They show the ×, −, ⤢ glyphs only when hovering over the group. Inactive: gray `--tg-control-inactive` |
| Shadows | active: `0 0 0 .5px var(--tg-window-ring), 0 24px 70px rgba(0,0,0,.45)`; inactive: `…, 0 10px 30px rgba(0,0,0,.25)` |
| Inactive | title and toolbar at 60 % opacity; content does not change |
| Move | dragged from the title bar. `pointer capture` + `translate3d`; final position on release |
| Resize | 8 edges/corners with a 6 px hit zone, respecting `minWidth/minHeight` |
| Double click on the title | maximizes within the usable area (without covering the MenuBar or the Dock) |
| Snap | drag to the left/right edge → half; to a corner → quarter; to the top → maximize. Preview with a translucent `--tg-accent` rectangle at 20 % |
| Bounds | the title bar can never end up off screen |
| Cascade | each new window opens +28 px in x/y from the previous one of the same app |

### 4.4 Launcher

Full-screen overlay with a blurred wallpaper. Grid of 88 px icons with their names,
search at the top with autofocus, and pagination with dots and swipe/wheel. It enters with a
1.08→1 scale and 0→1 opacity in 220 ms. Esc or a click on empty space closes it.

### 4.5 Spotlight (`Ctrl/⌘ + K`)

```
┌──────────────────────────────────────────────┐
│ ⌕  snapshot photos                            │
├──────────────────────────────────────────────┤
│ ACTIONS                                       │
│ ▸ Create snapshot of "Photos"         ⏎       │
│ APPS                                          │
│   Snapshots                                   │
│ FILES (demo)                                  │
│   /Photos/2026/vacation.jpg                   │
│ SETTINGS                                      │
│   Schedule snapshots                          │
└──────────────────────────────────────────────┘
```

- 640 px wide, 20 % from the top edge. Local fuzzy search (apps, settings, actions)
  plus `files.search` (mock) with a 150 ms debounce.
- Actions are commands that the apps register (`registerCommand`).

### 4.6 Notifications and toasts

- Toast at the top right (below the MenuBar), 360 px, icon + title + text + optional
  action. It closes by itself after 6 s, except critical ones.
- Notification center: panel that slides in from the right (380 px), grouped by app, with
  "Clear all".
- Severities `info`, `ok`, `warn`, `crit`: always icon + text, never color alone.

### 4.7 Control center (⊞)

320 px popover with cards:

- Theme (Light / Dark / Auto) and accent color.
- Volumes: usage as a mini ring and status.
- Network: IP, 1 GbE link and live traffic.
- CPU and disk temperature.
- Active sessions (3).
- "Do not disturb" button.

### 4.8 Context menu

8 px radius, blur, 24 px items, separators, submenus with a 200 ms delay and an "intent
triangle" so they do not close when moving diagonally. Full keyboard support.

## 5. Milonga theme tokens

Our own palette (not Apple's system colors). Everything lives in `theme-milonga/tokens.css`.

| Token | Light | Dark |
|---|---|---|
| `--tg-font` | `"Inter", system-ui, sans-serif` | = |
| `--tg-font-size` | 13 px (body), 11 px (secondary), 15/20/28 px (titles) | = |
| `--tg-text-1` | `#1C1C1F` | `#F4F4F6` |
| `--tg-text-2` | `rgba(28,28,31,.62)` | `rgba(244,244,246,.62)` |
| `--tg-text-3` | `rgba(28,28,31,.38)` | `rgba(244,244,246,.38)` |
| `--tg-window-bg` | `rgba(248,248,250,.82)` | `rgba(32,32,36,.78)` |
| `--tg-sidebar-bg` | `rgba(236,236,240,.70)` | `rgba(44,44,50,.62)` |
| `--tg-content-bg` | `#FFFFFF` | `#1E1E22` |
| `--tg-menubar-bg` | `rgba(246,246,248,.55)` | `rgba(24,24,28,.45)` |
| `--tg-dock-bg` | `rgba(246,246,248,.45)` | `rgba(40,40,46,.45)` |
| `--tg-separator` | `rgba(0,0,0,.10)` | `rgba(255,255,255,.10)` |
| `--tg-window-ring` | `rgba(0,0,0,.18)` | `rgba(255,255,255,.12)` |
| `--tg-blur` | `30px` | `30px` |
| `--tg-accent` | chosen by the user (below) | the same, +6 % lightness |
| `--tg-ok / warn / crit / info` | `#1FA463 / #D98A00 / #E5484D / #2F7CF6` | `#3DD68C / #F5B43C / #FF6B6E / #5B9BFF` |
| Radii | window 12 · popover 10 · control 6 · Dock 18 · card 10 | = |
| Traffic lights | `#FF5F57 / #FEBC2E / #28C840` | = |

Accent colors: Tango `#2F7CF6` (default), Violet `#8B5CF6`, Pink `#EC4899`, Red
`#EF4444`, Orange `#F97316`, Yellow `#EAB308`, Green `#22C55E` and Graphite `#8E8E96`.

## 6. Motion

| Event | Duration | Curve | Detail |
|---|---|---|---|
| Open window | 200 ms | `cubic-bezier(.2,.8,.2,1)` | scale .96→1, opacity 0→1, from the Dock icon (`transform-origin`) |
| Close window | 150 ms | `ease-in` | scale 1→.96, opacity 1→0 |
| Minimize | 320 ms | `cubic-bezier(.4,0,.2,1)` | simplified *genie*: trapezoidal `clip-path` + `translate` and `scale` toward the Dock thumbnail |
| Maximize / snap | 220 ms | `cubic-bezier(.2,.8,.2,1)` | interpolates the rect with `transform` and applies the size at the end |
| Popover / menu | 120 ms | `ease-out` | opacity + `translateY(-4px)`→0 |
| Dock (magnification) | per frame | critically damped spring (`k=300, c=30`) | no CSS transitions: `requestAnimationFrame` |
| Toast | 240 ms in / 180 ms out | `cubic-bezier(.2,.8,.2,1)` | from the right |

With `prefers-reduced-motion`, everything becomes a 100 ms fade and magnification is disabled.

## 7. Prototype apps

### 7.1 Storage (`tango.storage`)

```
┌ ● ● ●  Storage ─────────────────────────────────────────────────────┐
│ Overview     │  ┌ Bays ───────────────────────────────────────────┐ │
│ Volumes      │  │ [▮ 1 ●] [▮ 2 ●] [▮ 3 ●] [  4 empty ]    DEMO    │ │
│ Disks        │  └────────────────────────────────────────────────┘ │
│ Snapshots    │  ┌ volume1 · RAID1 · Btrfs ───┐ ┌ Health ────────┐ │
│ iSCSI (soon) │  │   ◯ 62 %  4.9 / 7.9 TB     │ │ ✓ All good      │ │
│              │  │   ▁▂▃▅ 30-day growth       │ │ Last scrub:     │ │
│              │  └────────────────────────────┘ │ 12 days ago     │ │
│              │  [ + Create volume ]            └─────────────────┘ │
└──────────────────────────────────────────────────────────────────────┘
```

- **Bays:** drawing of the bays with one LED per disk. Click → disk details.
- **Disks:** table with bay, model, serial, capacity, temperature, hours, SMART status and usage
  (volume or "unused"). Selecting one opens a side inspector with the SMART attributes.
- **"Create volume" wizard** (modal Sheet, 4 steps):
  1. *Disks:* only eligible ones; ineligible ones appear disabled with the reason ("System disk", "Mounted", "Member of md0").
  2. *Profile:* Basic / RAID1 (and RAID5/6 labeled "soon"), with a bar of usable capacity vs. protection.
  3. *Options:* name, encryption (disabled, "soon").
  4. *Review:* list of what gets erased on each disk (model + serial + detected content) and a field "type `volume2` to confirm". The **Create** button is enabled only if it matches.
  - On create: a job with live progress in the window and in the MenuBar ⟳, and a notification when done.
- **`degraded` scenario:** volume1 in amber and one bay in red. The **Doctor** card appears:
  "The disk in bay 2 stopped responding. Your data is complete on disk 1, but
  without redundancy. Replace it with one of ≥ 4 TB." + [How to replace].

### 7.2 Files (`tango.files`)

```
┌ ● ● ●  ‹ ›  Photos › 2026 › Vacation           ⊞ ≡ ▥   ⌕ search   ⇪ ┐
│ FAVORITES     │  🖼 beach.jpg   🖼 dinner.jpg  🎞 video.mp4   📁 raw   │
│  Home         │                                                    │
│  Downloads    │                                                    │
│ SHARED        │                                                    │
│  Family       │                                                    │
│  Photos  ←    │                                                    │
│  Backups      │                                                    │
│ ─────────     │                                                    │
│  Trash        │  4 items · 312 MB                          DEMO    │
└───────────────┴────────────────────────────────────────────────────┘
```

- Views: icons / list (sortable columns) / columns (Miller style).
- Selection: click, Shift (range), Ctrl/⌘ (add) and a selection rectangle.
- Inline rename with Enter or F2; Delete → Trash (with an "Undo" toast).
- **Quick Look** with the space bar: image, video (mock), text and PDF.
- **Drag files from the OS** → transfers panel (⇅) with speed and ETA
  simulated according to the scenario.
- **Versions** (right click → "Previous versions…"): timeline with the snapshots and
  restore with a preview.
- Demo thumbnails are locally generated images (canvas), not external photos.

### 7.3 Settings (`tango.settings`)

Sidebar with a search box at the top and a user card. Sections:

- General
- Appearance: **functional**: theme, accent, wallpaper, Dock size, magnification.
- Network
- Users and groups: table + creation sheet.
- Shared folders: table + per-user/group permissions (read/write/no access).
- File services (SMB/NFS/SFTP)
- Security: active sessions, 2FA.
- Notifications
- Updates: list of Debian and TangoOS packages, separately.
- Hardware and power

In demo, only **Appearance** persists changes (in `localStorage`). The rest shows the UI with
demo data and a "Demo: not applied" toast.

### 7.4 Monitor (`tango.monitor`)

- Tabs: Overview · CPU · Memory · Network · Disks · Connections · **WebRTC**.
- Live area charts, with a 60 s window at 4 Hz (`MockTransport` emits on the
  `metrics` "channel"). Pauses on hover, with a tooltip showing the value.
- The **WebRTC** tab (mock) shows the ICE state, the candidate pair, the RTT and, per channel
  (`rpc`, `events`, `metrics`, `files.0…3`), the `bufferedAmount` and the throughput. It is there to
  show that the product is "WebRTC-native".

### 7.5 In the Dock but not implemented (original plan)

In the prototype plan, Log, Terminal, Snapshots and App Center opened a window with a designed empty
state ("Coming in M1"). **Today they are implemented** as real apps: `tango.log` (`app-log`: audit and
journal), `tango.terminal` (`app-terminal`: xterm.js), `tango.snapshots` (`app-snapshots`) and
`tango.apps` (`app-apps`: catalog, installed apps, Docker).

## 8. MockTransport and scenarios

```ts
export interface Transport {
  call<M extends Method>(method: M, params: Params<M>, opts?: { signal?: AbortSignal }): Promise<Result<M>>;
  subscribe(topics: string[], onEvent: (e: Evt) => void): () => void;
  state: Observable<"connecting" | "connected" | "reconnecting" | "closed">;
}
```

- `MockTransport` implements the same contract as `RtcTransport`, using the protocol types
  (planned as `packages/protocol` with codegen; today handwritten in `packages/client/src/protocol*.ts`).
- **Simulated latency** of 30–120 ms per call and jitter on events.
- **Scenarios** via the `?scenario=` query string:

| Scenario | What it shows |
|---|---|
| `healthy` (default) | 3 disks, volume1 RAID1 at 62 %, all green |
| `degraded` | a failed disk, degraded volume, critical notification, Doctor |
| `busy` | scrub in progress + 2 transfers + a snapshot being created |
| `empty` | freshly installed NAS: no volumes, first-volume wizard highlighted |
| `migrate` | disks from a Synology detected → "Synology volume found (read-only)" card ([12](12-strategy-vs-synology.md)) |

- A scenario picker (development only) in ◆ → "Demo".
- **DEMO badge** visible on every view with mock data, without exception.

## 9. Keyboard shortcuts

In the browser, `Ctrl+W/T/N/Tab` and `⌘W/Q` cannot be captured. That is why the web uses `Alt`
(Option on Mac); the native app can use the classic shortcuts.

| Action | Web | Native app |
|---|---|---|
| Spotlight | `Ctrl/⌘+K` | `Ctrl/⌘+Space` |
| Close window | `Alt+W` | `Ctrl/⌘+W` |
| New app window | `Alt+N` | `Ctrl/⌘+N` |
| Cycle windows | ``Alt+` `` | ``Ctrl/⌘+` `` |
| Minimize | `Alt+M` | `Ctrl/⌘+M` |
| Snap left/right | `Alt+←/→` | `Ctrl+Alt+←/→` |
| Launcher | `Alt+Space` | `F4` |
| Lock | `Alt+L` | `Ctrl/⌘+L` |
| Quick Look | `Space` | `Space` |

## 10. Accessibility

- All interaction can be done with the keyboard, with a visible focus (2 px `--tg-accent` ring).
- ARIA roles: `menubar`/`menu`/`menuitem`, `dialog` for windows (`aria-labelledby` = title), `toolbar`, `tree` in the sidebar.
- AA contrast in both themes. Transparency is compensated with an opaque *fallback* when
  `prefers-reduced-transparency` is active or `backdrop-filter` is not supported.
- States never depend on color alone.

## 11. Responsive

| Width | Behavior |
|---|---|
| ≥ 1280 px | full |
| 1024–1279 | 44 px Dock, new windows at 80 % of the area |
| 768–1023 (tablet) | windows open maximized, no resizing; snap disabled |
| < 768 | simplified "Monitor" view (status, alerts, jobs) with a notice: "The full desktop is available on larger screens" |

## 12. Performance budgets

- Initial JS (shell + theme) ≤ 220 KB gzip. Each app is a lazy chunk.
- Dragging, resizing and Dock magnification at 60 fps with 8 open windows, on an
  8th-gen i5 with an iGPU. Measured with the Performance panel; no *long tasks* > 50 ms.
- `backdrop-filter` only on the MenuBar, Dock, popovers and the window sidebar, not on the whole
  window body. Inactive windows lose the blur (`--tg-blur: 0`) if an FPS drop
  is detected.
- Time-to-desktop ≤ 1.5 s on LAN (with the bundle cached: ≤ 400 ms).

## 13. Assets to produce

| Asset | Quantity | Notes |
|---|---|---|
| TangoOS logo | 1 (+ monochrome) | SVG; used in ◆, the login and the boot screen |
| App icons | 10: Files, Storage, Settings, Monitor, Log, Terminal, Snapshots, App Center, Launcher, Trash/Downloads | *squircle* 1024 → SVG, same lighting and palette, dark version when needed |
| Wallpapers | 6 (3 light / 3 dark) + 1 dynamic | our own or CC0, ≤ 400 KB in AVIF/WebP, 2560×1600; source recorded in `design/ASSETS.md` |
| Empty-state illustrations | 4 | line style, using tokens |
| Default avatar | 1 | initials over an accent gradient |

## 14. Acceptance criteria

- [ ] Login → desktop → open the 4 apps → move, resize, snap, minimize to the Dock, restore and close, all without glitches.
- [ ] Several Files windows at once; focus and z-order are correct.
- [ ] The "Create volume" wizard ends with an animated job and a notification; the Create button is not enabled without typing the name.
- [ ] All 5 scenarios load and look different.
- [ ] Light/dark and accent change live without reloading; the choice persists.
- [ ] Spotlight finds apps, settings and the "Create snapshot" action.
- [ ] Everything works with the keyboard alone.
- [ ] The DEMO badge is present on every view with mock data.
- [ ] The performance budgets are met (§12).
- [ ] Playwright: one e2e walkthrough + reference *screenshots* per scenario and theme.
- [ ] Side-by-side visual review with Mac-OS-Desktop and DSM 7: list of differences resolved or justified.

## 15. Build plan

| Step | Deliverable | Estimate |
|---|---|---|
| 1 | Workspaces, Vite, tokens, base `packages/ui`, MenuBar + wallpaper | 1 day |
| 2 | Window manager (store, drag, resize, z-order, maximize, snap) + store unit tests | 2 days |
| 3 | Dock with magnification, *genie* minimize, Launcher | 1.5 days |
| 4 | `MockTransport` + scenarios + `useLive()` + notifications/toasts + Activity | 1 day |
| 5 | Storage app + wizard | 2 days |
| 6 | Files app (views, selection, Quick Look, transfers, versions) | 2.5 days |
| 7 | Settings (functional Appearance) + Monitor (live charts + WebRTC) | 1.5 days |
| 8 | Spotlight, Control center, context menu, shortcuts, accessibility | 1.5 days |
| 9 | Login, boot, lock, responsive, motion polish, Playwright | 1.5 days |
| | **Total** | **~14 days** |

In parallel: design of the icons, the logo and the wallpapers (§13). Meanwhile, labeled
*placeholders* are used.
