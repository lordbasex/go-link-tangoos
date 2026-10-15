# ADR 0005 — Escritorio propio con motor de temas

- Estado: Aceptado
- Contexto: Mac-OS-Desktop no tiene licencia y trae assets de Apple. Además se quiere un segundo tema a futuro.
- Decisión: window manager propio en React (`packages/shell`), sin estilos de marca. El aspecto se define en paquetes `theme-*` (tokens + slots + motion). Las apps solo usan `packages/ui` y tokens.
- Consecuencias: más trabajo inicial; un segundo tema no requiere tocar las apps.
