# ADR 0005 — Own desktop with a theme engine

- Status: Accepted
- Context: Mac-OS-Desktop has no license and ships Apple assets. We also want a second theme in the future.
- Decision: our own window manager in React (`packages/shell`), with no brand styles. The look is defined in `theme-*` packages (tokens + slots + motion). Apps only use `packages/ui` and tokens.
- Consequences: more upfront work; a second theme does not require touching the apps.
