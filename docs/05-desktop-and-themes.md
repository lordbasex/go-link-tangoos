# 05 — Desktop and themes

## Quality bar

The reference is <https://mhmdmhd6.github.io/Mac-OS-Desktop/>: a translucent top bar,
a Dock with indicators for open apps, windows with traffic lights, Launchpad, control center,
context menu and light/dark theme. TangoOS has to **match it visually and surpass it
functionally**. That demo is vanilla HTML/CSS/JS with fixed positions; we need a
real window manager.

**Nothing is copied from that repo** (see [11](11-licenses-and-brand.md)): we observe it and
rebuild.

## Frontend architecture

```
web/
├─ apps/desktop/            # Vite entry: assembles shell + theme + apps
├─ packages/protocol/       # types generated from /protocol (planned; today handwritten in packages/client)
├─ packages/client/         # Transport (BrowserRTC | NativeBridge), RPC, events, useLive()
├─ packages/shell/          # window manager, Dock, menu, notifications, spotlight — NO brand styles
├─ packages/ui/             # base components (Button, Table, Sidebar, Toolbar, Sheet, Toast…) with tokens
├─ packages/theme-milonga/  # default theme, macOS look
├─ packages/theme-*/        # second theme (future)
└─ packages/app-*/          # one app per package: files, storage, settings, monitor…
```

Stack: **React 19 + TypeScript + Vite + Vitest + Playwright**, npm workspaces, same as
go-link-desk. Glyph icons: **Lucide** (ISC). Typeface: **Inter** (OFL) with
`font-feature-settings` tuned to get closer to SF. No state library is used: a ~80-line `store`
on top of `useSyncExternalStore` for the window manager, and `useLive()` for
server data.

## The theme engine (key for the "second theme")

The shell defines **what** is there; the theme defines **how it looks and how it moves**.

```ts
// packages/shell/src/theme.ts
export interface Theme {
  id: string;                       // "milonga"
  tokens: { light: Tokens; dark: Tokens };   // colors, radii, shadows, blur, typography, durations
  slots: {                          // replaceable components
    MenuBar: Component<MenuBarProps>;
    Dock: Component<DockProps>;              // a theme can swap it for a Windows-style taskbar
    WindowFrame: Component<WindowFrameProps>;// title bar, buttons, shadows
    Launcher: Component<LauncherProps>;      // Launchpad / start menu
    NotificationCenter: Component<…>;
    Wallpaper: Component<…>;
  };
  motion: { open: Keyframes; minimize: "genie" | "scale" | "none"; … };
  layout: { windowControls: "left" | "right"; menuBar: "top" | "none"; dock: "bottom" | "left" | "taskbar" };
}
```

- **Apps only use `packages/ui` and tokens** (`var(--tg-surface)`). They never import anything from
  a theme. That way the second theme does not touch the apps.
- Tokens use the `--tg-*` prefix, in two sets (light/dark) plus the accent color
  chosen by the user.
- Contract test: the same Playwright suite runs with every registered theme and takes reference
  *screenshots*.

## Window manager (`packages/shell`)

| Feature | MVP | Detail |
|---|---|---|
| Open, focus, close, minimize, maximize | ✅ | stack-based z-order; keyboard focus (`⌘/Ctrl+\``) |
| Drag / resize | ✅ | Pointer Events + `transform: translate3d` and no reflow during the drag |
| Snap to halves/quarters | ✅ | drag to the edge or shortcut |
| Multiple instances per app | ✅ | e.g. two Files windows |
| Per-user layout persistence | ✅ | saved on the server (`desktop.layout.save`), so you pick it up on another PC |
| Spotlight (`⌘K` / `Ctrl+Space`) | ✅ | searches apps, settings, files (via RPC) and actions |
| Per-app top menu | ✅ | each app declares its menu in the manifest |
| Notification center + toasts | ✅ | fed by `events` |
| Control center (drop-down) | ✅ | NAS status: volumes, network, temperature, sessions |
| Mission Control / virtual desktops | | M4 |
| Desktop widgets | | M4 (disk usage, jobs, network) |
| Drag files from the OS to the desktop/window | ✅ | direct upload (File System Access API when available) |
| Mobile | | simplified "monitor" view, not the desktop |

### App contract

```ts
export const manifest: AppManifest = {
  id: "tango.storage",
  name: { es: "Almacenamiento", en: "Storage" },
  icon: () => import("./icon.svg"),
  roles: ["admin"],                         // if you don't have the role, it doesn't show in the Dock
  window: { width: 960, height: 640, minWidth: 720, minHeight: 480 },
  menu: [/* … */],
  singleInstance: true,
  load: () => import("./App"),              // code-splitting: each app is a chunk
};
```

Today the manifest is simpler (`web/apps/desktop/src/main.tsx`): `name` is a plain Spanish string
(`"Almacenamiento"`), since the UI is Spanish-only with no i18n framework yet, and the app is loaded with
`component: lazy(() => import("@tango/app-storage"))`.

This same contract is the basis of the **App Center**: a container app can ship a manifest
and open in a window (an isolated iframe to a local port proxied over a DataChannel) in M5.

## Default theme: "Milonga" (macOS look)

- 28 px top bar, `backdrop-filter: blur(30px) saturate(180%)`, with date/time,
  NAS indicators and the user.
- Dock with magnification (scale interpolated by distance to the cursor, only
  `transform`), a dot on open apps and a separator for minimized windows/trash.
- Windows with a 12 px radius, a two-layer shadow, controls on the left and a title
  bar unified with the toolbar (macOS 11+ style).
- Animations: opening with a 0.96→1 scale over 180 ms `cubic-bezier(.2,.8,.2,1)`, a simplified
  *genie* minimize (clip-path + transform), and `prefers-reduced-motion` is respected.
- **Our own wallpapers:** generated or CC0 photographs, 3 light and 3 dark, with a dynamic mode
  based on the time of day.
- **Our own app icons** in SVG with a *squircle* shape and a consistent palette. They have to be
  designed; no Apple or Synology icons are used.

### Second theme (after M3)

Candidate: **"Arrabal"**, a different layout to validate the engine: bottom taskbar, controls
on the right and a look of its own (not a copy of Windows). If the engine handles this theme without
touching the apps, the theme design is correct.

## Prototype (the next thing to build)

> Full specification in [13 — Desktop prototype](13-desktop-prototype.md).

A **navigable prototype with labeled demo data**, in `web/` with the final stack (not a
standalone HTML file). It must include:

1. Login screen (wallpaper + avatar + clock).
2. Desktop: top bar, Dock with magnification, Launcher, Spotlight.
3. Full window manager (drag, resize, snap, minimize to the Dock and focus).
4. Apps with real UI and demo data: **Storage** (usage ring, disk list with
   SMART, "Create volume" wizard with the confirmation token), **Files** (sidebar,
   grid/list, preview, drag&drop), **Settings** (users and shared folders) and
   **Monitor** (simulated live charts at 4 Hz).
5. Light/dark + accent color.
6. A `MockTransport` that implements the same interface as the WebRTC transport, so it can be
   swapped for the real one without touching the apps.
