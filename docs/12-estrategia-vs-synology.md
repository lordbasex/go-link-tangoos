# 12 — Estrategia: mojarle la oreja a Synology

> Objetivo: que un usuario de Synology mire TangoOS y piense *"esto es DSM, pero en mi hardware,
> con mis discos, más lindo y sin candados"*.

## 1. Dónde está golpeado Synology (hechos, octubre 2026)

| Hecho | Qué pasó | Oportunidad para TangoOS |
|---|---|---|
| **Bloqueo de discos** | En abril de 2025 limitó los modelos Plus/Value/J 2025 a discos de su marca o certificados. Hubo reacción fuerte y, según la prensa especializada, cayeron las ventas. Lo revirtió en **DSM 7.3 (8 de octubre de 2025)**, pero **solo** para esos modelos 2025: los modelos business/enterprise y los de generaciones anteriores siguen con la lista de compatibilidad, y los pools en **M.2 todavía exigen discos de la lista** | "Cualquier disco, cualquier M.2, siempre". Está escrito como principio y no se negocia |
| **Recorte multimedia** | DSM 7.2.2 (agosto de 2024) eliminó Video Station y el transcoding en el servidor (HEVC). Synology recomienda Plex/Jellyfin | Jellyfin con **transcoding por hardware (Intel QSV)** configurado en un clic desde el Centro de apps |
| **Hardware** | Modelos nuevos con CPUs AMD sin transcoding por hardware; poca RAM; precio alto por lo que traen | Corre en un mini-PC N100/N150 o en una PC vieja, que cuestan bastante menos y rinden más |
| **Giro hacia la empresa** | Los usuarios home y homelab sienten que DSM prioriza al cliente business | Ese usuario es nuestro público inicial ([01](01-vision-y-alcance.md#público)) |
| **Cerrado** | Los volúmenes se leen con Linux estándar, pero DSM, sus apps y su roadmap son cerrados | MIT, sin telemetría, roadmap público |
| **Señal de demanda** | Existe Xpenology (DSM pirateado sobre hardware genérico) y en foros se debate migrar a TrueNAS o Unraid | La gente **quiere la experiencia DSM en su hardware**: ese hueco no lo llena nadie de forma legal |

Fuentes: [TechSpot](https://www.techspot.com/news/109787-synology-reverses-course-non-certified-hdds-nas-systems.html),
[Neowin](https://www.neowin.net/news/synology-backtracks-on-policy-that-allegedly-turned-buyers-away-from-its-nas-devices/),
[NAS Compares (DSM 7.3)](https://nascompares.com/news/are-synology-nas-ok-to-buy-now/amp/),
[NAS Compares (DSM 7.2.2)](https://nascompares.com/news/synology-dsm-7-2-2-update-a-major-shift-in-multimedia-support/),
[WunderTech (alternativas 2026)](https://www.wundertech.net/what-is-the-best-synology-nas-alternative/).
La caída de ventas la informan fuentes de la prensa; Synology no la confirmó.

## 2. Dónde Synology es fuerte (no subestimar)

- **Pulido y estabilidad:** DSM "simplemente anda" hace 15 años.
- **Ecosistema:** Photos, Drive, Active Backup for Business, Surveillance Station, Hyper Backup, C2.
- **Apps móviles** buenas.
- **Integración con su hardware:** LEDs, beeps, ventiladores, hot-swap, apagado programado.
- **QuickConnect:** acceso remoto sin configurar nada.
- **Soporte** y confianza de marca.

**Regla:** no peleamos todo a la vez. Peleamos donde ellos son débiles y nosotros somos
fuertes, e igualamos lo mínimo indispensable en lo demás.

## 3. Las cuñas (por orden de impacto)

### Cuña 1: Migración desde Synology ("traé tus discos")

DSM usa **mdadm + LVM + Btrfs**, el mismo stack que TangoOS ([04](04-almacenamiento.md)). La
propia Synology documenta cómo leer sus discos desde un Linux genérico. Entonces:

1. Sacás los discos del Synology y los ponés en el equipo con TangoOS.
2. Almacenamiento detecta **"Volumen Synology (SHR/RAID)"**, ensambla los md, activa los LV y
   **monta en solo lectura**.
3. Asistente: "¿Importar este volumen?". Recrea las carpetas compartidas como shares de TangoOS,
   sin copiar datos cuando el formato lo permite y, si no, copiando a un volumen nuevo.
4. Opcional: importar usuarios y carpetas compartidas desde el backup de configuración de DSM
   (`.dss`), si el formato resulta legible. Hay que investigarlo.

Es **el** feature de marketing: nadie más lo ofrece como experiencia guiada. Entra en
**M3 en solo lectura** y en M4 con importación. Tests con volúmenes reales de DSM 6 y 7
(SHR-1, SHR-2, RAID1, RAID5) creados en un Synology de laboratorio.

### Cuña 2: Hardware y discos libres

- Cualquier x86-64, cualquier disco, cualquier NVMe para pools o caché.
- **Matriz de hardware comunitaria:** cada instalación puede enviar, opcionalmente y con
  consentimiento, un informe anónimo de hardware. Se publica como "probado por la comunidad".
- Kits recomendados (mini-PC N100 de 2 bahías, gabinete de 4 bahías con N305) con guía de armado.

### Cuña 3: Experiencia superior en tiempo real

Lo que DSM no hace o hace peor:

| TangoOS | DSM |
|---|---|
| Todo en vivo (discos, jobs, archivos que cambian, usuarios conectados) sobre WebRTC | Paneles con sondeo y refrescos |
| App nativa Windows/Linux con **el mismo escritorio** y transferencias a velocidad de red | Apps separadas por función (Drive Client, Active Backup Agent…) |
| Spotlight global: apps, ajustes, archivos y acciones ("crear snapshot de Fotos") | Búsqueda separada por app |
| Terminal integrada en el escritorio, con auditoría | SSH aparte |
| Línea de tiempo de versiones por archivo (snapshots) dentro de Archivos | Restaurar desde Snapshot Replication |
| Asistente **"Doctor"**: explica en lenguaje humano por qué un disco o volumen está mal y qué hacer | Mensajes técnicos |

### Cuña 4: Acceso remoto P2P (TangoConnect)

QuickConnect suele pasar por los relays de Synology cuando no hay conexión directa. TangoConnect
es WebRTC: **intenta P2P primero** (hole punching) y solo usa TURN como último recurso. Es la misma
tecnología que go-link-desk ya opera en producción. Gratis en P2P; el relay puede ser un servicio
pago para financiar el proyecto.

### Cuña 5: Media sin recortes

App "Media" (Jellyfin empaquetado) con detección de iGPU Intel y `/dev/dri` habilitado
automáticamente en el contenedor. Transcoding por hardware sin tocar nada.

## 4. Tabla comparativa objetivo (v1.0)

| | Synology DSM | TrueNAS CE | Unraid | OMV | **TangoOS** |
|---|---|---|---|---|---|
| Hardware propio | ❌ | ✅ | ✅ | ✅ | ✅ |
| Cualquier disco | parcial | ✅ | ✅ | ✅ | ✅ |
| Open source | ❌ | ✅ | ❌ (pago) | ✅ | ✅ MIT |
| Interfaz de escritorio | ✅ | ❌ | ❌ | ❌ | ✅ **y en tiempo real** |
| Fácil para no técnicos | ✅ | ❌ | ~ | ❌ | ✅ (objetivo) |
| Importar volumen Synology | — | ❌ | ❌ | ~ manual | ✅ guiado |
| Acceso remoto sin abrir puertos | ✅ | ❌ | ✅ (plugin) | ❌ | ✅ P2P |
| Apps nativas de escritorio | parcial | ❌ | ❌ | ❌ | ✅ |
| Discos mixtos (tipo SHR) | ✅ | ❌ | ✅ | ❌ | M6 |

La tabla es de producto, no de marketing: se publica solo lo que esté hecho y probado.

## 5. Sostenibilidad (para competir hay que durar)

Open source MIT siempre. Posibles fuentes de ingreso, sin cerrar funciones del NAS:

- Relay de TangoConnect y DNS/certificados (§07) como servicio, con un tier gratis.
- Soporte pago para pymes.
- Kits de hardware "TangoOS Ready" con partners.
- Sponsors / GitHub Sponsors.

## 6. Impacto en el roadmap

- **M2:** detectar volúmenes Synology en el inventario, solo lectura y sin montar.
- **M3:** montar volúmenes Synology en solo lectura + navegarlos en Archivos.
- **M4:** asistente de importación + tests con volúmenes DSM reales.
- **M5:** app Media con QSV; TangoConnect.
- Desde M0: **la calidad visual del prototipo es parte de la estrategia**. Es lo primero que se
  compara con DSM.
