# 11 — Licencias y marca

> Esto es análisis técnico, no asesoramiento legal.

## Licencia del proyecto

**MIT**, igual que go-link-desk y signalhub. Política de dependencias: se permiten MIT, BSD,
ISC, Apache-2.0, MPL-2.0 (como librería) y OFL (fuentes). Se evitan GPL/AGPL **enlazadas**
en los binarios propios. Las herramientas del sistema (Samba GPLv3, mdadm GPLv2, kernel) se
invocan como procesos separados y se distribuyen como paquetes Debian con sus licencias:
eso es compatible con que el código de TangoOS sea MIT.

## Mac-OS-Desktop (mhmdmhd6)

Verificado el 2026-10-14 vía la API de GitHub:

- **No tiene archivo LICENSE** (`license: null`). El `package.json` dice `"license": "ISC"`,
  pero es el valor por defecto de `npm init` y no hay texto de licencia. **Sin licencia
  explícita, el código es "todos los derechos reservados".**
- Contiene **assets de Apple**: wallpapers de Monterey y Big Sur, `apple-white.png`,
  `apple_icon.png` y cursores. Eso no se puede redistribuir aunque el código tuviera licencia.

**Decisión:** se usa solo como **referencia visual**. No se copia código, CSS ni assets. Si en
algún momento se quiere reutilizar algo puntual, hay que pedir al autor una licencia
explícita (MIT) por escrito en un issue.

## Apple

- No se usan el logo de la manzana, wallpapers de macOS, íconos de apps de Apple, cursores
  ni la fuente **SF Pro** (su licencia solo permite usarla en software para plataformas Apple).
- El *estilo* (vidrio, Dock, semáforos, tipografía limpia) no se puede registrar como tal y lo
  imitan muchos proyectos. Aun así, el README dirá: *"TangoOS es un proyecto independiente,
  no afiliado ni respaldado por Apple Inc."*
- Nombres a **evitar** en la UI: Finder, Launchpad, Spotlight, Mission Control, Dock (genérico,
  aceptable), Time Machine (solo para nombrar la compatibilidad: "compatible con Time Machine"),
  macOS (solo descriptivo en docs: "inspirado en macOS"). En el código: `launcher`, `search`,
  `overview`.

## Synology

No se usan DSM, SHR, File Station, Hyper Backup, QuickConnect, Synology Drive ni sus íconos.
Decir "alternativa a Synology DSM" de forma descriptiva está bien; con su logo, no.

## Nombre: TangoOS

Verificado el 2026-10-14:

| Recurso | Estado |
|---|---|
| `tangoos.com` | ❌ registrado (2026-07-26, DropCatch) |
| `tangoos.org` | ✅ libre |
| `tangoos.io` | ✅ libre |
| `tangoos.net` | ✅ libre |
| `tango-os.org` | ✅ libre |
| GitHub org `tangoos` | ❌ tomado (org de 2014) |
| GitHub org `tango-os` | ✅ libre |
| Repos homónimos | `SquilliamF21/TangoOS` y `9xbt/TangoOS`: SO de juguete, 0 estrellas, sin conflicto práctico |

Riesgos a tener en cuenta: "Tango" ya lo usan Google (Project Tango, discontinuado), la app de
mensajería Tango y el *Tango Desktop Project* (íconos de escritorio Linux). Ninguno es un SO de
NAS, pero conviene buscar en las bases de marcas (USPTO, EUIPO, INPI Argentina) en la clase 9
antes de invertir en branding.

**Recomendación:** registrar ya `tangoos.org` + `tangoos.io` (y `tangoos.net` si se usa para
el DNS de certificados de [07 §3](07-seguridad.md)), y crear la org `tango-os` en GitHub.

## Assets propios a producir

- Logo TangoOS (SVG).
- ~12 íconos de app (*squircle*, SVG, claro/oscuro).
- 6 wallpapers (generados o CC0, con la fuente registrada en `design/ASSETS.md`).
- Sonidos de sistema (opcional, CC0).
