# TangoOS — Documentación de diseño

> Sistema operativo NAS open source (MIT) para competir con Synology DSM: su calidad de producto, en tu hardware y con tus discos,
> escritorio web con estética macOS y **todo el tráfico de administración sobre WebRTC**.

Estado: **M0–M3.5 implementados y M4 (v1.0) en pruebas** sobre VM. TangoOS administra discos reales; el avance hito por hito está en [09](09-roadmap.md) y los pendientes en [09 → Próximos](09-roadmap.md#próximos-post-v10).

## Índice

| # | Documento | Qué resuelve |
|---|---|---|
| 01 | [Visión y alcance](01-vision-y-alcance.md) | Paridad con DSM, MVP, no-objetivos |
| 02 | [Arquitectura](02-arquitectura.md) | Procesos, capas, flujo de una operación |
| 03 | [Protocolo WebRTC](03-protocolo-webrtc.md) | Señalización, STUN embebido, canales, RPC, eventos, archivos |
| 04 | [Almacenamiento](04-almacenamiento.md) | mdadm + Btrfs (modelo Synology), snapshots, shares |
| 05 | [Escritorio y temas](05-escritorio-y-temas.md) | Window manager, Dock, sistema de temas, vara de calidad |
| 06 | [Apps nativas](06-apps-nativas.md) | Windows / Linux (y luego macOS) con el mismo escritorio |
| 07 | [Seguridad](07-seguridad.md) | Modelo de amenazas, auth, agente privilegiado |
| 08 | [Sistema base, instalador y updates](08-sistema-base-instalador-updates.md) | Debian 13 minimal + repo apt, ISO, updates con rollback |
| 09 | [Roadmap](09-roadmap.md) | Hitos M0–M6 con gates |
| 10 | [Estructura del repo y convenciones](10-estructura-repo-y-convenciones.md) | Monorepo, stack fijado, build |
| 11 | [Licencias y marca](11-licencias-y-marca.md) | MIT, Mac-OS-Desktop, Apple, Synology, nombre |
| 12 | [Estrategia vs Synology](12-estrategia-vs-synology.md) | Dónde está débil Synology, nuestras cuñas, migración desde DSM |
| 13 | [Prototipo del escritorio](13-prototipo-escritorio.md) | Especificación completa: pantallas, shell, tokens, motion, apps, mock, aceptación |
| ADR | [adr/](adr/) | Decisiones registradas |

## Decisiones clave (resumen)

1. **WebRTC en lugar de REST.** Un único `RTCPeerConnection` cifrado (DTLS) por cliente, con
   DataChannels por propósito: `rpc`, `events`, `files`, `term`, `metrics`. HTTP queda
   reducido a: servir el bundle estático + un WebSocket de señalización. Ver [03](03-protocolo-webrtc.md).
2. **El propio `tangod` es servidor de señalización y STUN** (pion/turn embebido, UDP 3478) y
   expone ICE en un único puerto UDP multiplexado. Es LAN-first; el acceso remoto llega después
   reutilizando la infraestructura de go-link (signalhub + coturn).
3. **Mismo stack que go-link-desk:** Go 1.26 + `pion/webrtc/v4`, React 19 + TypeScript + Vite en
   npm workspaces, sin librería de estado, sobres JSON `{type,…}`, nFPM, sumas firmadas Ed25519.
4. **Almacenamiento como Synology:** RAID con `mdadm` + Btrfs de un solo dispositivo encima
   (no RAID5/6 nativo de Btrfs). Shares = subvolúmenes.
5. **Escritorio propio desde cero.** Mac-OS-Desktop es la *referencia visual*; no se copia código
   ni assets (no tiene licencia y trae arte de Apple). El tema por defecto imita la estética macOS;
   el motor de temas permite un segundo tema propio sin tocar las apps.
6. **Apps nativas = el mismo escritorio dentro de un webview**, con el WebRTC en Go (pion) del
   lado nativo y un *bridge*. Así se evita depender del WebRTC de WebKitGTK y se ganan
   transferencias más rápidas.
7. **Debian 13 minimal + "nuestra cara".** TangoOS no es una distro nueva: es la capa de
   gestión + interfaz, distribuida como `.deb` en un repo apt firmado, sobre un Debian real.
   Ver [08](08-sistema-base-instalador-updates.md).
8. **100% Go nativo.** No hay otro servidor para la web: solo `tangod` y `tango-agent`. Se
   habla con el kernel (ioctl, sysfs, netlink) y con systemd por D-Bus; `exec` es la excepción
   documentada ([ADR 0009](adr/0009-go-nativo-sin-exec.md)).
9. **Agente privilegiado separado** (`tango-agent`, root, socket Unix, operaciones tipadas en
   allow-list). `tangod` corre sin privilegios.
