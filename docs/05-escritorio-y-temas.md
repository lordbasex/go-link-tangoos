# 05 — Escritorio y temas

## Vara de calidad

La referencia es <https://mhmdmhd6.github.io/Mac-OS-Desktop/>: barra superior translucida,
Dock con indicadores de apps abiertas, ventanas con semáforos, Launchpad, centro de control,
menú contextual y tema claro/oscuro. TangoOS tiene que **igualarla en lo visual y superarla en
lo funcional**. Esa demo es HTML/CSS/JS vanilla con posiciones fijas; nosotros necesitamos un
window manager real.

**No se copia nada de ese repo** (ver [11](11-licencias-y-marca.md)): se observa y se
reconstruye.

## Arquitectura del frontend

```
web/
├─ apps/desktop/            # entrada Vite: arma shell + tema + apps
├─ packages/protocol/       # tipos generados desde /protocol (no se edita a mano)
├─ packages/client/         # Transport (BrowserRTC | NativeBridge), RPC, eventos, useLive()
├─ packages/shell/          # window manager, Dock, menú, notificaciones, spotlight — SIN estilos de marca
├─ packages/ui/             # componentes base (Button, Table, Sidebar, Toolbar, Sheet, Toast…) con tokens
├─ packages/theme-milonga/  # tema por defecto, estética macOS
├─ packages/theme-*/        # segundo tema (futuro)
└─ packages/app-*/          # una app por paquete: archivos, almacenamiento, ajustes, monitor…
```

Stack: **React 19 + TypeScript + Vite + Vitest + Playwright**, npm workspaces, igual que
go-link-desk. Íconos de glifo: **Lucide** (ISC). Tipografía: **Inter** (OFL) con
`font-feature-settings` ajustados para acercarse a SF. No se usa librería de estado: un `store`
de ~80 líneas sobre `useSyncExternalStore` para el window manager, y `useLive()` para los
datos del servidor.

## El motor de temas (clave para el "segundo tema")

El shell define **qué** hay; el tema define **cómo se ve y cómo se mueve**.

```ts
// packages/shell/src/theme.ts
export interface Theme {
  id: string;                       // "milonga"
  tokens: { light: Tokens; dark: Tokens };   // colores, radios, sombras, blur, tipografía, duraciones
  slots: {                          // componentes reemplazables
    MenuBar: Component<MenuBarProps>;
    Dock: Component<DockProps>;              // un tema puede cambiarlo por una taskbar estilo Windows
    WindowFrame: Component<WindowFrameProps>;// barra de título, botones, sombras
    Launcher: Component<LauncherProps>;      // Launchpad / menú inicio
    NotificationCenter: Component<…>;
    Wallpaper: Component<…>;
  };
  motion: { open: Keyframes; minimize: "genie" | "scale" | "none"; … };
  layout: { windowControls: "left" | "right"; menuBar: "top" | "none"; dock: "bottom" | "left" | "taskbar" };
}
```

- Las **apps solo usan `packages/ui` y tokens** (`var(--tg-surface)`). Nunca importan nada de
  un tema. Así el segundo tema no toca las apps.
- Los tokens van con prefijo `--tg-*`, en dos juegos (claro/oscuro) más el color de acento
  elegido por el usuario.
- Test de contrato: el mismo Playwright corre con cada tema registrado y toma *screenshots*
  de referencia.

## Window manager (`packages/shell`)

| Función | MVP | Detalle |
|---|---|---|
| Abrir, enfocar, cerrar, minimizar, maximizar | ✅ | z-order por pila; foco con teclado (`⌘/Ctrl+\``) |
| Arrastrar / redimensionar | ✅ | Pointer Events + `transform: translate3d` y sin reflow durante el drag |
| Snap a mitades/cuartos | ✅ | arrastre al borde o atajo |
| Múltiples instancias por app | ✅ | p. ej. dos ventanas de Archivos |
| Persistencia de layout por usuario | ✅ | se guarda en el servidor (`desktop.layout.save`), así lo seguís en otra PC |
| Spotlight (`⌘K` / `Ctrl+Espacio`) | ✅ | busca apps, ajustes, archivos (vía RPC) y acciones |
| Menú superior por app | ✅ | cada app declara su menú en el manifiesto |
| Centro de notificaciones + toasts | ✅ | alimentado por `events` |
| Centro de control (desplegable) | ✅ | estado del NAS: volúmenes, red, temperatura, sesiones |
| Mission Control / escritorios virtuales | | M4 |
| Widgets en el escritorio | | M4 (uso de disco, jobs, red) |
| Arrastrar archivos del SO al escritorio/ventana | ✅ | subida directa (File System Access API cuando existe) |
| Móvil | | vista "monitor" simplificada, no el escritorio |

### Contrato de app

```ts
export const manifest: AppManifest = {
  id: "tango.storage",
  name: { es: "Almacenamiento", en: "Storage" },
  icon: () => import("./icon.svg"),
  roles: ["admin"],                         // si no tenés rol, no aparece en el Dock
  window: { width: 960, height: 640, minWidth: 720, minHeight: 480 },
  menu: [/* … */],
  singleInstance: true,
  load: () => import("./App"),              // code-splitting: cada app es un chunk
};
```

Esto mismo es la base del **Centro de apps**: una app de contenedor puede traer un manifiesto
y abrirse en una ventana (iframe aislado a un puerto local proxyado por DataChannel) en M5.

## Tema por defecto: "Milonga" (estética macOS)

- Barra superior de 28 px, `backdrop-filter: blur(30px) saturate(180%)`, con la fecha/hora,
  indicadores del NAS y el usuario.
- Dock con magnificación (interpolación de escala por distancia al cursor, solo
  `transform`), punto en las apps abiertas y separador para minimizadas/papelera.
- Ventanas con radio de 12 px, sombra en dos capas, controles a la izquierda y barra de
  título unificada con la toolbar (estilo macOS 11+).
- Animaciones: apertura con escala 0.96→1 en 180 ms `cubic-bezier(.2,.8,.2,1)`, minimizado
  tipo *genie* simplificado (clip-path + transform), y se respeta `prefers-reduced-motion`.
- **Wallpapers propios:** generados o fotografías CC0, 3 claros y 3 oscuros, con modo dinámico
  según la hora.
- **Íconos de app propios** en SVG con forma *squircle* y una paleta consistente. Hay que
  diseñarlos; no se usan íconos de Apple ni de Synology.

### Segundo tema (después de M3)

Candidato: **"Arrabal"**, un layout distinto para validar el motor: taskbar inferior, controles
a la derecha y una estética propia (no copia de Windows). Si el motor aguanta este tema sin
tocar las apps, el diseño de temas es correcto.

## Prototipo (lo siguiente a construir)

> Especificación completa en [13 — Prototipo del escritorio](13-prototipo-escritorio.md).

Un prototipo **navegable con datos demo rotulados**, en `web/` con el stack definitivo (no un
HTML suelto). Debe incluir:

1. Pantalla de login (wallpaper + avatar + reloj).
2. Escritorio: barra superior, Dock con magnificación, Launcher, Spotlight.
3. Window manager completo (drag, resize, snap, minimizar al Dock y foco).
4. Apps con UI real y datos demo: **Almacenamiento** (anillo de uso, lista de discos con
   SMART, asistente "Crear volumen" con el token de confirmación), **Archivos** (sidebar,
   grilla/lista, vista previa, drag&drop), **Ajustes** (usuarios y carpetas compartidas) y
   **Monitor** (gráficos en vivo simulados a 4 Hz).
5. Claro/oscuro + color de acento.
6. Un `MockTransport` que implementa la misma interfaz que el transporte WebRTC, así se
   cambia por el real sin tocar las apps.
