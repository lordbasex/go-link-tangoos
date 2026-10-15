# 13 — Prototipo del escritorio (especificación)

Detalla la sección "Prototipo" de [05](05-escritorio-y-temas.md). Es el documento que se usa
para construirlo.

## 1. Objetivo

Un escritorio **navegable, con el stack definitivo y datos demo rotulados**, que demuestre:

1. Calidad visual a la par de Mac-OS-Desktop y por encima de DSM: es lo primero que se compara ([12](12-estrategia-vs-synology.md)).
2. Un window manager real (no posiciones fijas).
3. La sensación de "tiempo real": métricas que se mueven, jobs con progreso, notificaciones que llegan.
4. Que las apps hablan contra una interfaz `Transport`: después se cambia `MockTransport` por WebRTC sin tocar las apps.
5. Que el motor de temas funciona: claro/oscuro + color de acento en vivo.

**No incluye:** backend real, WebRTC, login real, i18n completo (solo `es`, con las claves preparadas) ni móvil.

## 2. Stack y estructura

React 19 + TypeScript + Vite + Vitest + Playwright, npm workspaces. Sin librería de estado.
Lucide + Inter. CSS Modules + custom properties `--tg-*`.

```
web/
├─ apps/desktop/                 # main.tsx: registra el tema, las apps y el transporte
├─ packages/client/              # Transport, RpcClient, useLive(), MockTransport + escenarios
├─ packages/shell/               # WM, MenuBar, Dock, Launcher, Spotlight, Notifs, ControlCenter, ContextMenu
├─ packages/ui/                  # Button, IconButton, Segmented, Toggle, Slider, Table, Sidebar,
│                                # Toolbar, Sheet, Popover, Toast, Badge(DEMO), Ring, Sparkline, Wizard
├─ packages/theme-milonga/       # tokens.css (claro/oscuro), slots, motion, wallpapers, íconos de app
└─ packages/app-{storage,files,settings,monitor}/
```

## 3. Pantallas

### 3.1 Arranque

Fondo del wallpaper con blur fuerte, logo TangoOS centrado y una barra de progreso fina. Dura
≤ 1,2 s en el prototipo: la carga del shell es real y el resto es simulado.

### 3.2 Login

```
┌──────────────────────────────────────────────────────────────┐
│                         mié 14 oct                           │
│                          22:41            (reloj grande, 300) │
│                                                              │
│                         ( avatar )                           │
│                          Federico                            │
│                    [ ••••••••     → ]                        │
│                  Cambiar usuario · DEMO                      │
│                                                              │
│  tango.local · 3 discos OK                       ⏻  ⟳        │
└──────────────────────────────────────────────────────────────┘
```

- Contraseña incorrecta ("error" en el modo demo): la tarjeta hace un *shake* horizontal de 3
  ciclos en 300 ms. Cualquier otra contraseña entra.
- Al entrar, el reloj y la tarjeta se desvanecen y el escritorio entra con escala 1.02→1.

### 3.3 Escritorio

```
┌ MenuBar 28px ────────────────────────────────────────────────────────────────────────┐
│ ◆ Archivos  Archivo  Edición  Ver  Ir  Ventana  Ayuda      ⟳2  ⛁  ⇅  🔔  ⊞  👤  mié 22:41 │
└──────────────────────────────────────────────────────────────────────────────────────┘
   (wallpaper)        ┌──────── ventana ────────┐
                      │ ● ● ●   Archivos   ⌕ ⇪  │
                      │ side │ contenido        │
                      └─────────────────────────┘
                 ┌───────────── Dock ─────────────┐
                 │ ▦  📁 ⛁ ⚙ 📈 ≡ >_ │ ⬇ 🗑      │
                 └────────────────────────────────┘
```

- **◆ menú TangoOS** (logo propio, nunca la manzana): Acerca de este NAS, Ajustes, Actualizaciones,
  Bloquear, Cerrar sesión, Reiniciar, Apagar. Reiniciar y Apagar están deshabilitados en demo.
- **Iconos de estado, de izquierda a derecha:**
  - ⟳ Actividad: jobs en curso, con su número y un popover de progreso.
  - ⛁ Salud de los volúmenes: verde, ámbar o rojo, siempre acompañado de un ícono distinto.
  - ⇅ Transferencias.
  - 🔔 Notificaciones.
  - ⊞ Centro de control.
  - Usuario.
  - Fecha y hora.
- **Menú contextual del escritorio:** Cambiar fondo…, Ordenar ventanas, Mostrar escritorio, Ajustes de apariencia.

### 3.4 Bloqueo

`Alt+L` o el menú ◆: vuelve a la pantalla de login, pero conserva las ventanas.

## 4. Componentes del shell

### 4.1 MenuBar

| Propiedad | Valor |
|---|---|
| Alto | 28 px |
| Fondo | `--tg-menubar-bg` + `backdrop-filter: blur(30px) saturate(180%)` |
| Tipografía | 13 px/500. El nombre de la app activa va en 600 |
| Menús | Los define cada app en su manifiesto. Se abren al clic y, con uno abierto, al pasar por encima (como en macOS). Teclado: ←/→ entre menús, ↑/↓ dentro, Enter, Esc |
| Item | 22 px de alto, padding 0 10 px, radio 4 px, hover `--tg-accent` con texto blanco, atajo alineado a la derecha en `--tg-text-2` |

### 4.2 Dock

| Propiedad | Valor |
|---|---|
| Ícono base | 52 px. Contenedor con padding 6 px y radio 18 px |
| Magnificación | `s = 1 + (sMax − 1) · (cos(min(d/R, 1)·π) + 1) / 2` con `sMax = 1.6`, `R = 140 px` y `d` = distancia horizontal del cursor al centro del ícono. Se aplica con `transform: scale()` y `translateY` para que crezca hacia arriba; los vecinos se separan con `margin` animado vía `transform` del contenedor, sin layout |
| Lanzar | rebote de 2 saltos (`translateY` −14 px, 600 ms) hasta que la ventana se abre |
| Indicador | punto de 4 px debajo de las apps abiertas |
| Tooltip | nombre arriba, 150 ms de retardo, píldora con blur |
| Zonas | Launcher · apps fijadas · separador · minimizadas (miniatura) · Descargas · Papelera |
| Menú contextual | Abrir / Mostrar todas las ventanas / Mantener en el Dock / Cerrar |
| Preferencias | posición (abajo/izquierda), tamaño, magnificación on/off, ocultar automáticamente |

### 4.3 Ventana (WindowFrame del tema Milonga)

| Propiedad | Valor |
|---|---|
| Radio | 12 px; los contenidos recortan con `overflow: clip` |
| Barra de título | unificada con la toolbar: 52 px con toolbar, 32 px sin ella |
| Semáforos | 12 px, separados 8 px, a 14 px del borde. Muestran los glifos ×, −, ⤢ solo al pasar el mouse sobre el grupo. Inactiva: gris `--tg-control-inactive` |
| Sombras | activa: `0 0 0 .5px var(--tg-window-ring), 0 24px 70px rgba(0,0,0,.45)`; inactiva: `…, 0 10px 30px rgba(0,0,0,.25)` |
| Inactiva | título y toolbar al 60 % de opacidad; el contenido no cambia |
| Mover | se arrastra desde la barra de título. `pointer capture` + `translate3d`; posición final al soltar |
| Redimensionar | 8 bordes/esquinas de 6 px de zona activa, respetando `minWidth/minHeight` |
| Doble clic en el título | maximiza dentro del área útil (sin tapar la MenuBar ni el Dock) |
| Snap | arrastrar al borde izquierdo/derecho → mitad; a una esquina → cuarto; arriba → maximizar. Previsualización con un rectángulo translúcido `--tg-accent` al 20 % |
| Límites | la barra de título nunca puede quedar fuera de pantalla |
| Cascada | cada ventana nueva se abre +28 px en x/y respecto de la anterior de la misma app |

### 4.4 Launcher

Overlay a pantalla completa con blur del wallpaper. Grilla de íconos de 88 px con su nombre,
búsqueda arriba con foco automático y paginación con puntos y swipe/rueda. Entra con escala
1.08→1 y opacidad 0→1 en 220 ms. Esc o clic en el vacío lo cierra.

### 4.5 Spotlight (`Ctrl/⌘ + K`)

```
┌──────────────────────────────────────────────┐
│ ⌕  instantánea fotos                          │
├──────────────────────────────────────────────┤
│ ACCIONES                                      │
│ ▸ Crear instantánea de «Fotos»        ⏎       │
│ APPS                                          │
│   Instantáneas                                │
│ ARCHIVOS (demo)                               │
│   /Fotos/2026/vacaciones.jpg                  │
│ AJUSTES                                       │
│   Programar instantáneas                      │
└──────────────────────────────────────────────┘
```

- 640 px de ancho, a un 20 % del borde superior. Búsqueda difusa local (apps, ajustes, acciones)
  más `files.search` (mock) con debounce de 150 ms.
- Las acciones son comandos que registran las apps (`registerCommand`).

### 4.6 Notificaciones y toasts

- Toast arriba a la derecha (debajo de la MenuBar), 360 px, ícono + título + texto + acción
  opcional. Se cierra solo a los 6 s, salvo los críticos.
- Centro de notificaciones: panel que entra desde la derecha (380 px), agrupado por app, con
  "Borrar todo".
- Severidades `info`, `ok`, `warn`, `crit`: siempre ícono + texto, nunca solo color.

### 4.7 Centro de control (⊞)

Popover de 320 px con tarjetas:

- Tema (Claro / Oscuro / Auto) y color de acento.
- Volúmenes: uso en mini-anillo y estado.
- Red: IP, enlace 1 GbE y tráfico en vivo.
- Temperatura de CPU y de discos.
- Sesiones activas (3).
- Botón "No molestar".

### 4.8 Menú contextual

Radio 8 px, blur, items de 24 px, separadores, submenús con un retardo de 200 ms y "triángulo
de intención" para no cerrarlos al mover en diagonal. Teclado completo.

## 5. Tokens del tema Milonga

Paleta propia (no son los colores del sistema de Apple). Todo en `theme-milonga/tokens.css`.

| Token | Claro | Oscuro |
|---|---|---|
| `--tg-font` | `"Inter", system-ui, sans-serif` | = |
| `--tg-font-size` | 13 px (cuerpo), 11 px (secundario), 15/20/28 px (títulos) | = |
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
| `--tg-accent` | elegido por el usuario (abajo) | el mismo, +6 % de luminosidad |
| `--tg-ok / warn / crit / info` | `#1FA463 / #D98A00 / #E5484D / #2F7CF6` | `#3DD68C / #F5B43C / #FF6B6E / #5B9BFF` |
| Radios | ventana 12 · popover 10 · control 6 · Dock 18 · tarjeta 10 | = |
| Semáforos | `#FF5F57 / #FEBC2E / #28C840` | = |

Colores de acento: Tango `#2F7CF6` (por defecto), Violeta `#8B5CF6`, Rosa `#EC4899`, Rojo
`#EF4444`, Naranja `#F97316`, Amarillo `#EAB308`, Verde `#22C55E` y Grafito `#8E8E96`.

## 6. Movimiento

| Evento | Duración | Curva | Detalle |
|---|---|---|---|
| Abrir ventana | 200 ms | `cubic-bezier(.2,.8,.2,1)` | escala .96→1, opacidad 0→1, desde el ícono del Dock (`transform-origin`) |
| Cerrar ventana | 150 ms | `ease-in` | escala 1→.96, opacidad 1→0 |
| Minimizar | 320 ms | `cubic-bezier(.4,0,.2,1)` | *genie* simplificado: `clip-path` trapezoidal + `translate` y `scale` hacia la miniatura del Dock |
| Maximizar / snap | 220 ms | `cubic-bezier(.2,.8,.2,1)` | interpola el rect con `transform` y aplica el tamaño al final |
| Popover / menú | 120 ms | `ease-out` | opacidad + `translateY(-4px)`→0 |
| Dock (magnificación) | por frame | resorte crítico (`k=300, c=30`) | sin CSS transitions: `requestAnimationFrame` |
| Toast | 240 ms entra / 180 ms sale | `cubic-bezier(.2,.8,.2,1)` | desde la derecha |

Con `prefers-reduced-motion`, todo pasa a ser un fundido de 100 ms y la magnificación se desactiva.

## 7. Apps del prototipo

### 7.1 Almacenamiento (`tango.storage`)

```
┌ ● ● ●  Almacenamiento ──────────────────────────────────────────────┐
│ Resumen      │  ┌ Bahías ─────────────────────────────────────────┐ │
│ Volúmenes    │  │ [▮ 1 ●] [▮ 2 ●] [▮ 3 ●] [  4 vacía ]   DEMO    │ │
│ Discos       │  └────────────────────────────────────────────────┘ │
│ Instantáneas │  ┌ volume1 · RAID1 · Btrfs ───┐ ┌ Salud ─────────┐ │
│ iSCSI (pronto)│  │   ◯ 62 %  4,9 / 7,9 TB    │ │ ✓ Todo en orden │ │
│              │  │   ▁▂▃▅ crecimiento 30 días │ │ Último scrub:   │ │
│              │  └────────────────────────────┘ │ hace 12 días    │ │
│              │  [ + Crear volumen ]            └─────────────────┘ │
└──────────────────────────────────────────────────────────────────────┘
```

- **Bahías:** dibujo de las bahías con un LED por disco. Clic → detalle del disco.
- **Discos:** tabla con bahía, modelo, serie, capacidad, temperatura, horas, estado SMART y uso
  (volumen o "sin usar"). Al seleccionar uno se abre un inspector lateral con los atributos SMART.
- **Asistente "Crear volumen"** (Sheet modal, 4 pasos):
  1. *Discos:* solo los elegibles; los no elegibles aparecen deshabilitados con el motivo ("Disco del sistema", "Montado", "Miembro de md0").
  2. *Perfil:* Básico / RAID1 (y RAID5/6 con el rótulo "pronto"), con una barra de capacidad útil vs. protección.
  3. *Opciones:* nombre, cifrado (deshabilitado, "pronto").
  4. *Revisión:* lista de lo que se borra en cada disco (modelo + serie + contenido detectado) y un campo "escribí `volume2` para confirmar". El botón **Crear** se habilita solo si coincide.
  - Al crear: job con progreso en vivo en la ventana y en ⟳ de la MenuBar, notificación al terminar.
- **Escenario `degraded`:** volume1 en ámbar y una bahía en rojo. Aparece la tarjeta **Doctor**:
  "El disco de la bahía 2 dejó de responder. Tus datos están completos en el disco 1, pero
  sin redundancia. Reemplazalo por uno de ≥ 4 TB." + [Cómo reemplazar].

### 7.2 Archivos (`tango.files`)

```
┌ ● ● ●  ‹ ›  Fotos › 2026 › Vacaciones          ⊞ ≡ ▥   ⌕ buscar   ⇪ ┐
│ FAVORITOS     │  🖼 playa.jpg   🖼 cena.jpg   🎞 video.mp4   📁 raw   │
│  Inicio       │                                                    │
│  Descargas    │                                                    │
│ COMPARTIDAS   │                                                    │
│  Familia      │                                                    │
│  Fotos   ←    │                                                    │
│  Backups      │                                                    │
│ ─────────     │                                                    │
│  Papelera     │  4 ítems · 312 MB                          DEMO    │
└───────────────┴────────────────────────────────────────────────────┘
```

- Vistas: íconos / lista (columnas ordenables) / columnas (estilo Miller).
- Selección: clic, Shift (rango), Ctrl/⌘ (suma) y rectángulo de selección.
- Renombrar en línea con Enter o F2; Supr → Papelera (con toast "Deshacer").
- **Vista rápida** con la barra espaciadora: imagen, video (mock), texto y PDF.
- **Arrastrar archivos desde el SO** → panel de transferencias (⇅) con la velocidad y el ETA
  simulados según el escenario.
- **Versiones** (clic derecho → "Versiones anteriores…"): línea de tiempo con los snapshots y
  restaurar con vista previa.
- Las miniaturas en demo son imágenes generadas localmente (canvas), no fotos externas.

### 7.3 Ajustes (`tango.settings`)

Sidebar con buscador arriba y una tarjeta del usuario. Secciones:

- General
- Apariencia: **funcional**: tema, acento, wallpaper, tamaño del Dock, magnificación.
- Red
- Usuarios y grupos: tabla + hoja de alta.
- Carpetas compartidas: tabla + permisos por usuario/grupo (lectura/escritura/sin acceso).
- Servicios de archivos (SMB/NFS/SFTP)
- Seguridad: sesiones activas, 2FA.
- Notificaciones
- Actualizaciones: lista de paquetes Debian y TangoOS separados.
- Hardware y energía

En demo, solo **Apariencia** persiste los cambios (en `localStorage`). El resto muestra la UI con
datos demo y un toast "Demo: no se aplicó".

### 7.4 Monitor (`tango.monitor`)

- Pestañas: Resumen · CPU · Memoria · Red · Discos · Conexiones · **WebRTC**.
- Gráficos de área en vivo, con una ventana de 60 s a 4 Hz (`MockTransport` emite por el
  "canal" `metrics`). Pausa al pasar el mouse y tooltip con el valor.
- La pestaña **WebRTC** (mock) muestra el estado ICE, el par de candidatos, el RTT y, por canal
  (`rpc`, `events`, `metrics`, `files.0…3`), el `bufferedAmount` y el throughput. Sirve para que
  se vea que el producto es "WebRTC-native".

### 7.5 En el Dock pero sin implementar

Registro, Terminal, Instantáneas y Centro de apps abren una ventana con un estado vacío
diseñado ("Llega en M1") con ilustración propia. No son ventanas rotas.

## 8. MockTransport y escenarios

```ts
export interface Transport {
  call<M extends Method>(method: M, params: Params<M>, opts?: { signal?: AbortSignal }): Promise<Result<M>>;
  subscribe(topics: string[], onEvent: (e: Evt) => void): () => void;
  state: Observable<"connecting" | "connected" | "reconnecting" | "closed">;
}
```

- `MockTransport` implementa el mismo contrato que `RtcTransport`, usando los tipos de
  `packages/protocol` (en el prototipo, escritos a mano y marcados como TODO de codegen).
- **Latencia simulada** de 30–120 ms por llamada y jitter en los eventos.
- **Escenarios** por query string `?scenario=`:

| Escenario | Qué muestra |
|---|---|
| `healthy` (por defecto) | 3 discos, volume1 RAID1 al 62 %, todo verde |
| `degraded` | un disco caído, volumen degradado, notificación crítica, Doctor |
| `busy` | scrub en curso + 2 transferencias + creación de un snapshot |
| `empty` | NAS recién instalado: sin volúmenes, asistente de primer volumen destacado |
| `migrate` | discos de un Synology detectados → tarjeta "Volumen Synology encontrado (solo lectura)" ([12](12-estrategia-vs-synology.md)) |

- Un selector de escenario (solo en desarrollo) en ◆ → "Demo".
- **Insignia DEMO** visible en cada vista con datos mock, sin excepción.

## 9. Atajos de teclado

En el navegador no se pueden capturar `Ctrl+W/T/N/Tab` ni `⌘W/Q`. Por eso la web usa `Alt`
(Option en Mac); la app nativa sí puede usar los atajos clásicos.

| Acción | Web | App nativa |
|---|---|---|
| Spotlight | `Ctrl/⌘+K` | `Ctrl/⌘+Espacio` |
| Cerrar ventana | `Alt+W` | `Ctrl/⌘+W` |
| Nueva ventana de la app | `Alt+N` | `Ctrl/⌘+N` |
| Ciclar ventanas | ``Alt+` `` | ``Ctrl/⌘+` `` |
| Minimizar | `Alt+M` | `Ctrl/⌘+M` |
| Snap izquierda/derecha | `Alt+←/→` | `Ctrl+Alt+←/→` |
| Launcher | `Alt+Espacio` | `F4` |
| Bloquear | `Alt+L` | `Ctrl/⌘+L` |
| Vista rápida | `Espacio` | `Espacio` |

## 10. Accesibilidad

- Toda la interacción se puede hacer con teclado, con foco visible (anillo de 2 px `--tg-accent`).
- Roles ARIA: `menubar`/`menu`/`menuitem`, `dialog` para las ventanas (`aria-labelledby` = título), `toolbar`, `tree` en la sidebar.
- Contraste AA en ambos temas. Las transparencias se compensan con un *fallback* opaco cuando
  `prefers-reduced-transparency` está activo o `backdrop-filter` no está soportado.
- Los estados nunca dependen solo del color.

## 11. Responsive

| Ancho | Comportamiento |
|---|---|
| ≥ 1280 px | completo |
| 1024–1279 | Dock de 44 px, ventanas nuevas al 80 % del área |
| 768–1023 (tablet) | las ventanas se abren maximizadas, sin redimensionar; snap desactivado |
| < 768 | vista "Monitor" simplificada (estado, alertas, jobs) con un aviso: "El escritorio completo está disponible en pantallas más grandes" |

## 12. Presupuestos de rendimiento

- JS inicial (shell + tema) ≤ 220 KB gzip. Cada app es un chunk diferido.
- Arrastrar, redimensionar y magnificar el Dock a 60 fps con 8 ventanas abiertas, en un
  i5 de 8.ª generación con iGPU. Medido con el Performance panel; sin *long tasks* > 50 ms.
- `backdrop-filter` solo en MenuBar, Dock, popovers y la sidebar de las ventanas, no en todo el
  cuerpo de la ventana. Las ventanas inactivas pierden el blur (`--tg-blur: 0`) si se detecta
  una caída de FPS.
- Time-to-desktop ≤ 1,5 s en LAN (con el bundle cacheado: ≤ 400 ms).

## 13. Assets a producir

| Asset | Cantidad | Notas |
|---|---|---|
| Logo TangoOS | 1 (+ monocromo) | SVG; se usa en ◆, el login y el arranque |
| Íconos de app | 10: Archivos, Almacenamiento, Ajustes, Monitor, Registro, Terminal, Instantáneas, Centro de apps, Launcher, Papelera/Descargas | *squircle* 1024 → SVG, misma luz y paleta, versión oscura cuando haga falta |
| Wallpapers | 6 (3 claros / 3 oscuros) + 1 dinámico | propios o CC0, ≤ 400 KB en AVIF/WebP, 2560×1600; fuente registrada en `design/ASSETS.md` |
| Ilustraciones de estado vacío | 4 | estilo lineal, en tokens |
| Avatar por defecto | 1 | iniciales sobre degradé del acento |

## 14. Criterios de aceptación

- [ ] Login → escritorio → abrir las 4 apps → mover, redimensionar, snap, minimizar al Dock, restaurar y cerrar, todo sin glitches.
- [ ] Varias ventanas de Archivos a la vez; el foco y el z-order son correctos.
- [ ] El asistente "Crear volumen" termina con un job animado y una notificación; el botón Crear no se habilita sin escribir el nombre.
- [ ] Los 5 escenarios se cargan y se ven distintos.
- [ ] Claro/oscuro y acento cambian en vivo sin recargar; la elección persiste.
- [ ] Spotlight encuentra apps, ajustes y la acción "Crear instantánea".
- [ ] Todo funciona solo con el teclado.
- [ ] La insignia DEMO está presente en todas las vistas con datos mock.
- [ ] Se cumplen los presupuestos de rendimiento (§12).
- [ ] Playwright: un recorrido e2e + *screenshots* de referencia por escenario y tema.
- [ ] Revisión visual lado a lado con Mac-OS-Desktop y DSM 7: lista de diferencias resuelta o justificada.

## 15. Plan de construcción

| Paso | Entregable | Estimado |
|---|---|---|
| 1 | Workspaces, Vite, tokens, `packages/ui` base, MenuBar + wallpaper | 1 día |
| 2 | Window manager (store, drag, resize, z-order, maximizar, snap) + tests de unidad del store | 2 días |
| 3 | Dock con magnificación, minimizar *genie*, Launcher | 1,5 días |
| 4 | `MockTransport` + escenarios + `useLive()` + notificaciones/toasts + Actividad | 1 día |
| 5 | App Almacenamiento + asistente | 2 días |
| 6 | App Archivos (vistas, selección, Vista rápida, transferencias, versiones) | 2,5 días |
| 7 | Ajustes (Apariencia funcional) + Monitor (gráficos en vivo + WebRTC) | 1,5 días |
| 8 | Spotlight, Centro de control, menú contextual, atajos, accesibilidad | 1,5 días |
| 9 | Login, arranque, bloqueo, responsive, pulido de motion, Playwright | 1,5 días |
| | **Total** | **~14 días** |

En paralelo: diseño de los íconos, el logo y los wallpapers (§13). Mientras tanto se usan
*placeholders* rotulados.
