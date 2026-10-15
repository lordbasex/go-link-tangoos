# TangoOS

NAS open source (MIT) sobre Debian 13, con escritorio web estilo macOS y administración en tiempo real por WebRTC.
Parte del ecosistema [go-link](https://go-link.org). Probá la demo en **[tangoos.go-link.org/demo](https://tangoos.go-link.org/demo)**.

> *Open-source NAS on Debian 13 with a macOS-style web desktop, managed in real time over WebRTC.
> Install from the ISO or with `curl -fsSL https://tangoos.go-link.org/install.sh | sh`.*

![Escritorio de TangoOS](web/apps/connect/public/img/desktop-dark.png)

> **Estado: v0.1 (pre-release), en pruebas.** Administra discos reales: volúmenes Btrfs (Básico y RAID 1/5/6/10
> por mdadm), carpetas compartidas con instantáneas, SMB con Time Machine y «Versiones anteriores» de Windows,
> descubrimiento en Windows (WS-Discovery) y macOS (Bonjour), NFS, SFTP, WebDAV, iSCSI, usuarios con verificación
> en dos pasos (TOTP) y contraseñas de app, UPS por NUT, centro de apps (Docker), transferencias por WebRTC y
> acceso remoto con TangoConnect. Lo que falta está en [docs/09 → Próximos](docs/09-roadmap.md#próximos-post-v10).

## Instalar

**Desde la ISO** (recomendado: equipo dedicado). Bajá `tangoos-<versión>-amd64.iso` de
[Releases](https://github.com/lordbasex/go-link-tangoos/releases), grabala en un USB y arrancá. El instalador es
gráfico; también se maneja desde otra PC de la red (`http://<ip>`, con el código que aparece en pantalla) y tiene un
modo texto en el menú de arranque. Instala en menos de un minuto y deja el sistema en Btrfs con vuelta atrás de
actualizaciones.

**Sobre un Debian 13 que ya tenés** (amd64, con systemd, puertos 80 y 443 libres):

```bash
curl -fsSL https://tangoos.go-link.org/install.sh | sh
```

Agrega el repositorio APT firmado de TangoOS e instala el paquete `tangoos`; las actualizaciones llegan desde el
propio escritorio. Esta vía no tiene la vuelta atrás del sistema que da la ISO.

En los dos casos, abrí `https://<ip-del-nas>/` y creá la cuenta de administrador.

## Desarrollo

```bash
cd web && npm install && npm run dev   # escritorio con datos simulados en http://localhost:5173/
make check                              # gofmt, go vet, go test
make web deploy-vm                      # bundle → embed en tangod → .deb → apt install en la VM de pruebas
```

Parámetros del escritorio simulado: `?skip` (directo al escritorio), `?scenario=healthy|degraded|busy|empty|migrate`.
En el login demo cualquier contraseña entra; `error` falla.

Los ajustes de tu laboratorio (`VM`, `SSH_KEY`, `PVE`…) van en `local.mk`, que no se versiona. Las pruebas E2E y el
plan completo están en [`tests/`](tests/README.md) y [docs/14](docs/14-plan-de-pruebas.md).

## Estructura

```
cmd/          tangod (servidor), tango-agent (operaciones privilegiadas), tangoctl, tango-installer, connect-web…
internal/     api, rtc, storage, fileworker, appsys, connect, installer, aptrepo…
pkg/          rtcrpc, agentproto (contratos entre procesos)
web/          npm workspaces: shell, ui, client, widgets, app-*; apps/desktop y apps/connect (portal)
system/       systemd, ISO, catálogo de apps
protocol/     contrato del protocolo WebRTC
docs/         diseño, ADRs, roadmap
```

## Licencia

MIT, ver [LICENSE](LICENSE).
