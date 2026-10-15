# Pruebas de TangoOS

El criterio y la matriz están en [`docs/14-plan-de-pruebas.md`](../docs/14-plan-de-pruebas.md).
Los resultados de cada corrida completa quedan en [`RESULTS.md`](RESULTS.md).

| Capa | Dónde | Comando |
|---|---|---|
| Unitarias Go (+ vet Linux, gofmt) | `cmd/ internal/ pkg/` | `make check` |
| Unitarias web (Vitest) | `web/packages/*` | `cd web && npm test` |
| Seguridad | `tests/e2e/security_test.go` | `make e2e-security` |
| E2E de API | `tests/e2e/*_test.go` | `make e2e-api` |
| Matriz de almacenamiento | `tests/e2e/storage_matrix_test.go` | `make e2e-storage` (`E2E_REBOOT=1` reinicia la VM) |
| E2E de UI (Playwright 1.64.1) | `web/e2e/desktop.spec.ts` | `make e2e-ui` (`E2E_DESTRUCTIVE=1` crea y borra un RAID 5) |
| Todo, en orden | — | `make e2e-all` |

## Requisitos

- La VM de laboratorio 108 (`172.17.0.44`) con TangoOS instalado (`make deploy-vm`) y la clave
  SSH root del laboratorio (`SSH_KEY`, por defecto `~/.ssh/id_ed25519`; la tuya va en `local.mk`).
- Go del `go.mod`, Node LTS. Playwright descarga su Chromium la primera vez.
- Las pruebas usan un cliente WebRTC real (`internal/rtc`), no mocks. La contraseña de `admin` se
  lee por SSH de `/var/lib/tango/initial-admin-password` y nunca se imprime.

## Qué toca cada suite

| Suite | Efectos en el NAS | Limpieza |
|---|---|---|
| `e2e-security` | crea/resetea el usuario `e2etester`; intentos de login fallidos con usuarios inexistentes | ninguna necesaria |
| `e2e-api` | carpetas `E2E-*` en volume1, app `syncthing` (instala y desinstala), LUN iSCSI `e2e-lun`, receptor de notificaciones en loopback, `open-iscsi` en la VM | borra todo al terminar (`t.Cleanup`) |
| `e2e-storage` | volúmenes `e2em*` sobre **S3, S6, M1, M2**; falla forzada de un disco (mdadm --fail); reinicio de la VM | borra cada volumen y verifica discos libres |
| `e2e-ui` | sube y manda a la papelera un archivo en Familia; con `E2E_DESTRUCTIVE=1` crea y borra `e2eui` (RAID 5 en S3, S6, M1) | sí |

Fixtures permanentes que ninguna suite modifica: `volume1` (RAID 1 en D1+D2) con Familia,
`volume2` (RAID 5 en S1, S2, S4, S5) con Fotos, usuarios `admin` y `sofia`, apps Jellyfin y Uptime Kuma.

## Variables

| Variable | Uso |
|---|---|
| `TANGO_HOST` | host del NAS (por defecto 172.17.0.44) |
| `TANGO_SSH_KEY` | clave SSH root del laboratorio |
| `TANGO_E2E_DESTRUCTIVE=1` | habilita la matriz y el RAID desde la UI |
| `TANGO_E2E_REBOOT=1` | habilita la fila 9 (reinicio de la VM) |
| `TANGO_E2E_BIGFILE_MB` | tamaño del archivo grande de Archivos (por defecto 1024) |

Artefactos: logs en `tests/e2e-logs/`, capturas de la UI en `tests/e2e-ui/artifacts/`,
traces/videos de fallos en `tests/e2e-ui/test-results/`.

## TangoConnect (`make e2e-connect`)

Recorre el acceso remoto con un navegador real (Playwright): vincular con el código → escritorio que
entrega el NAS (firmado, verificado por el portal) → login → Almacenamiento (rack de 12 bahías) → enlace
público de una foto desde Archivos → descarga en otro contexto con sha256 igual al del NAS → recarga
(`reach`, el NAS prueba primero) → revocar el vínculo (queda bloqueado). Capturas en
`tests/e2e-ui/artifacts/connect-*.png`. **Nunca usa el signal de producción.**

Preparación (una vez por corrida):

1. Signalhub de desarrollo en la Mac, aceptando el origen del portal local:
   `cd ../signaling && ADDR=0.0.0.0:8090 ALLOWED_ORIGINS=http://127.0.0.1:5174 go run ./cmd/signal`
2. Habilitar TangoConnect en el NAS hacia ese signal (y que los enlaces apunten al portal local):
   `tests/tangoctl-admin.sh rpc connect.enable '{"enabled":true,"signalURL":"ws://172.17.0.5:8090/ws?v=1","portalURL":"http://localhost:5174"}'`
3. Portal armado con la clave pública dev y servido en :5174:
   `make connect-portal-dev && (cd web/apps/connect && npx vite preview --port 5174 --strictPort --host 127.0.0.1)`
4. `make e2e-connect`

Al terminar: `tests/tangoctl-admin.sh rpc connect.enable '{"enabled":false,"signalURL":"wss://signal.go-link.org/ws?v=1","portalURL":"https://tangoos.go-link.org"}'`
y apagar el signalhub y el preview. `tests/tangoctl-admin.sh` y `tests/vm-ssh.sh` leen la contraseña / corren
comandos por SSH sin imprimir secretos.
