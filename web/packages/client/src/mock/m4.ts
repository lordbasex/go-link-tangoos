// Fase M4 simulada (protocol/README.md §12). Datos DEMO. `?setup` en la URL arranca con el
// asistente de primer arranque pendiente.
import { RpcError } from "../protocol";
import type { Job, Method, Notice, Params, Result, Scenario, Share, Topic, Topics, Volume } from "../protocol";
import type { AuditEvent, CertInfo, IscsiTarget, LogLine, NfsClient, NotifyChannels, RollbackPoint, ServiceInfo, UpdatesInfo } from "../protocol-m4";
import { passwordStrength, validHostname } from "../protocol-m4";
import * as data from "./data";
import { t } from "../i18n";

export interface MockM4Ctx {
  scenario: () => Scenario;
  emit: <T extends Topic>(topic: T, d: Topics[T]) => void;
  startJob: (type: string, title: string, detail: string, progress: number, step: number, rate?: string, onDone?: () => void) => Job & { onTick?: (p: number) => void };
  notify: (level: Notice["level"], app: string, title: string, body: string) => void;
  vols: () => Volume[];
  shares: () => Share[];
}

const iso = (msAgo: number) => new Date(Date.now() - msAgo).toISOString();
const H = 3600_000;
const D = 24 * H;

/** Hace que `job.detail` avance por las líneas de log a medida que progresa. */
function withLog(job: Job & { onTick?: (p: number) => void }, lines: string[]) {
  job.onTick = (p) => {
    job.detail = lines[Math.min(lines.length - 1, Math.floor(p * lines.length))];
  };
  return job;
}

export function m4Handlers(ctx: MockM4Ctx): { [M in Method]?: (p: Params<M>) => Result<M> } {
  const setupPending = typeof location !== "undefined" && new URLSearchParams(location.search).has("setup");
  const st = {
    setup: setupPending ? ("pending" as const) : ("done" as const),
    hostname: "tango",
    tangoos: { current: "0.1.0-dev", available: "0.1.1" as string | null },
    debian: [
      { name: "openssl", from: "3.5.1-1", to: "3.5.1-1+deb13u1", security: true },
      { name: "libssl3t64", from: "3.5.1-1", to: "3.5.1-1+deb13u1", security: true },
      { name: "linux-image-amd64", from: "6.12.111-1", to: "6.12.115-1", security: true },
      { name: "samba", from: "2:4.22.3+dfsg-4", to: "2:4.22.4+dfsg-1~deb13u1", security: true },
      { name: "libc6", from: "2.41-12", to: "2.41-12+deb13u1", security: true },
      { name: "openssh-server", from: "1:10.0p1-7", to: "1:10.0p1-7+deb13u1", security: true },
      { name: "tzdata", from: "2025b-4", to: "2026a-0+deb13u1", security: false },
      { name: "ca-certificates", from: "20250419", to: "20250419+deb13u1", security: false },
    ],
    rollback: [
      { id: "rootfs-2026-10-01T03:12", at: iso(13 * D), label: t("Antes de actualizar a {version}", { version: "0.1.0-dev" }) },
      { id: "rootfs-2026-09-20T03:05", at: iso(24 * D), label: t("Antes de {n} parches de seguridad de Debian", { n: 9 }) },
    ] as RollbackPoint[],
    cert: { mode: "selfsigned", subject: "CN=tango.local", sans: ["tango", "tango.local", "192.168.1.50"], notAfter: new Date(Date.now() + 360 * D).toISOString(), fingerprint: "SHA-256 1D:CD:71:53:F3:4F:A3:43:2A:6C:3F:D4:59:8F:4F:F7:CD:0E:58:E7:DE:7F:17:23:2D:EE:0A:6B:DC:A7:59:12" } as CertInfo,
    services: [
      { id: "smb", enabled: true, running: true, port: 445, detail: t("SMB 3.1.1 · Time Machine activado") },
      { id: "nfs", enabled: false, running: false, port: 2049, detail: "NFS v4.2" },
      { id: "sftp", enabled: true, running: true, port: 22, detail: t("Solo SFTP, cada usuario en sus carpetas") },
      { id: "webdav", enabled: false, running: false, port: 5006, detail: t("WebDAV sobre HTTPS") },
      { id: "iscsi", enabled: false, running: false, port: 3260, detail: "LIO (targetcli)" },
    ] as ServiceInfo[],
    nfs: new Map<string, NfsClient[]>([["media", [{ host: "192.168.1.0/24", access: "ro" }]]]),
    iscsi: [] as IscsiTarget[],
    notify: {
      email: { enabled: false, host: "smtp.gmail.com", port: 587, user: "", password: "", from: "tango@casa.lan", to: "", tls: "starttls" },
      ntfy: { enabled: true, server: "https://ntfy.sh", topic: "tango-casa-7f3a", token: "" },
      telegram: { enabled: false, botToken: "", chatId: "" },
      levels: ["crit", "warn"],
    } as NotifyChannels,
    audit: [] as AuditEvent[],
  };

  const audit = (method: string, target: string, result: AuditEvent["result"] = "ok") => {
    const e: AuditEvent = { at: new Date().toISOString(), actor: "federico", ip: "192.168.1.23", client: "web", method, target, result };
    st.audit.unshift(e);
    ctx.emit("audit.event", e);
  };

  // Auditoría inicial creíble.
  const seed: [number, string, string, string, AuditEvent["result"]][] = [
    [2 * 60_000, "auth.login", "federico", "ok", "ok"],
    [9 * 60_000, "snapshots.create", "Fotos", "ok", "ok"],
    [41 * 60_000, "shares.permissions.set", "Familia", "ok", "ok"],
    [2 * H, "auth.login", "admin", "error", "error"],
    [2 * H + 5000, "auth.login", "admin", "error", "error"],
    [3 * H, "apps.install", "jellyfin", "ok", "ok"],
    [5 * H, "auth.login", "sofia", "ok", "ok"],
    [26 * H, "users.create", "martin", "ok", "ok"],
    [2 * D, "storage.scrub.start", "volume1", "ok", "ok"],
    [3 * D, "storage.volumes.create", "volume1", "ok", "ok"],
  ];
  st.audit = seed.map(([ago, method, target, , result]) => ({
    at: iso(ago),
    actor: method === "auth.login" ? target : "federico",
    ip: target === "sofia" ? "192.168.1.44" : ago === 2 * H || ago === 2 * H + 5000 ? "192.168.1.77" : "192.168.1.23",
    client: target === "sofia" ? "smb" : "web",
    method,
    target,
    result,
  }));

  const units = ["tangod", "tango-agent", "smbd", "docker", "kernel"] as const;
  const logMsgs: Record<(typeof units)[number], [number, string][]> = {
    tangod: [[6, "sesión WebRTC conectada · par host/udp 192.168.1.23 ↔ 192.168.1.50:8443"], [6, "métricas: 4 Hz a 2 sesiones"], [4, "login fallido para admin desde 192.168.1.77 (1/5)"], [6, "job j_81 storage.scrub succeeded"]],
    "tango-agent": [[6, "op snapshot.create share=fotos ok (12 ms)"], [6, "op disk.smart dev=wwn-0x5000c500a1b2c3 ok"], [5, "journal: 0 operaciones interrumpidas"]],
    smbd: [[6, "sofia conectada a Familia desde 192.168.1.44"], [5, "tv-living conectada a Media (solo lectura)"]],
    docker: [[6, "container tango-jellyfin-jellyfin-1 healthy"], [6, "pull jellyfin/jellyfin:12.2 complete"]],
    kernel: [[6, "md127: data-check of RAID array md127 done"], [4, "ata3: SATA link up 6.0 Gbps"], [6, "BTRFS info (device md127): scrub: finished on devid 1 with status: 0"]],
  };
  const logs: LogLine[] = [];
  for (let i = 0; i < 120; i++) {
    const u = units[i % units.length];
    const [prio, msg] = logMsgs[u][i % logMsgs[u].length];
    logs.push({ at: iso(i * 47_000 + (i % 7) * 3000), unit: u, prio: i % 23 === 0 ? 3 : prio, msg });
  }

  const typed = (got: string, want: string) => {
    if (got !== want) throw new RpcError("CONFIRMATION_MISMATCH", t("Escribí {word} para confirmar", { word: want }));
  };

  return {
    "setup.state": () => ({ state: st.setup, hostname: st.hostname, suggestedHostname: "tango" }),
    "setup.complete": ({ hostname, admin, timezone, certificate }) => {
      if (st.setup !== "pending") throw new RpcError("SETUP_DONE", "El NAS ya está configurado");
      const h = validHostname(hostname);
      if (h) throw new RpcError("INVALID_PARAMS", h);
      if (!/^[a-z][a-z0-9_-]{1,31}$/.test(admin.name)) throw new RpcError("INVALID_PARAMS", "Nombre de usuario inválido");
      if (passwordStrength(admin.password, [admin.name, admin.full, hostname]).score < 2) throw new RpcError("WEAK_PASSWORD", "La contraseña es demasiado débil");
      if (!timezone) throw new RpcError("INVALID_PARAMS", "Elegí una zona horaria");
      st.setup = "done";
      st.hostname = hostname;
      st.cert = { ...st.cert, mode: certificate, subject: `CN=${hostname}.local`, sans: [hostname, `${hostname}.local`] };
      audit("setup.complete", hostname);
      const u = data.users()[0];
      return { user: { ...u, name: admin.name, full: admin.full, groups: ["administrators"], admin: true }, token: "" };
    },
    "system.updates.check": () => ({ tangoos: st.tangoos, debian: st.debian, checkedAt: new Date().toISOString() } satisfies UpdatesInfo),
    "system.updates.apply": ({ scope }) => {
      const pkgs = scope === "security" ? st.debian.filter((d) => d.security) : st.debian;
      const lines = [
        t("Creando instantánea del sistema (@rootfs)…"),
        t("Instantánea {name} lista", { name: "rootfs-" + new Date().toISOString().slice(0, 16) }),
        t("Descargando paquetes…"),
        ...pkgs.map((p) => t("Desempaquetando {name} ({version})…", { name: p.name, version: p.to })),
        ...(scope === "all" && st.tangoos.available ? [t("Desempaquetando {name} ({version})…", { name: "tangoos", version: st.tangoos.available })] : []),
        t("Configurando paquetes…"),
        t("Reiniciando servicios (tangod sigue disponible)…"),
        t("Listo"),
      ];
      const j = withLog(
        ctx.startJob("system.updates.apply", scope === "security" ? t("Instalando parches de seguridad") : t("Actualizando el sistema"), lines[0], 0, 0.035, undefined, () => {
          st.rollback.unshift({ id: `rootfs-${new Date().toISOString().slice(0, 16)}`, at: new Date().toISOString(), label: scope === "security" ? t("Antes de {n} parches de seguridad", { n: pkgs.length }) : t("Antes de actualizar el sistema") });
          st.debian = st.debian.filter((d) => !pkgs.includes(d));
          if (scope === "all" && st.tangoos.available) st.tangoos = { current: st.tangoos.available, available: null };
          ctx.notify("ok", "tango.settings", t("Sistema actualizado"), t("{n} paquetes. Si algo falla, podés volver al estado anterior.", { n: pkgs.length }));
        }),
        lines,
      );
      audit("system.updates.apply", scope);
      return { job: j.id };
    },
    "system.rollback.list": () => st.rollback,
    "system.rollback.apply": ({ id, typed: word }) => {
      typed(word, "VOLVER");
      const p = st.rollback.find((r) => r.id === id);
      if (!p) throw new RpcError("NOT_FOUND", "Ese punto ya no existe");
      const j = withLog(ctx.startJob("system.rollback", t("Volviendo a una versión anterior"), t("Marcando la instantánea como sistema por defecto…"), 0, 0.08, undefined, () => {
        ctx.notify("info", "tango.settings", t("Listo para volver atrás"), t("Demo: en un NAS real se reinicia con el sistema anterior."));
      }), [t("Marcando la instantánea como sistema por defecto…"), t("Actualizando el menú de arranque…"), t("Reinicio programado")]);
      audit("system.rollback.apply", id);
      return { job: j.id };
    },
    "system.reboot": ({ typed: word }) => {
      typed(word, "REINICIAR");
      audit("system.reboot", st.hostname);
      return {};
    },
    "system.shutdown": ({ typed: word }) => {
      typed(word, "APAGAR");
      audit("system.shutdown", st.hostname);
      return {};
    },
    "cert.info": () => st.cert,
    "cert.localca": () => {
      const j = withLog(ctx.startJob("cert.localca", t("Creando la CA local"), t("Generando la autoridad certificante…"), 0, 0.12, undefined, () => {
        st.cert = { ...st.cert, mode: "localca", fingerprint: "SHA-256 7A:02:9C:" + Math.random().toString(16).slice(2, 10).toUpperCase().replace(/(..)/g, "$1:") + "…" };
        ctx.notify("ok", "tango.settings", t("Certificado listo"), t("Instalá el certificado raíz en tus equipos para no ver más advertencias."));
      }), [t("Generando la autoridad certificante…"), t("Firmando el certificado del NAS…"), t("Recargando HTTPS sin cortar sesiones…"), t("Listo")]);
      audit("cert.localca", st.hostname);
      return { job: j.id };
    },
    "cert.caDownload": () => {
      if (st.cert.mode !== "localca") throw new RpcError("NOT_AVAILABLE", "Primero activá la CA local");
      return { filename: "tangoos-ca.crt", pem: "-----BEGIN CERTIFICATE-----\nMIIBdDCCARqgAwIBAgIUDEMODEMODEMODEMODEMODEMODEMOwCgYIKoZIzj0EAwIw\nRGVtbyBkZSBUYW5nb09TOiBlc3RlIGNlcnRpZmljYWRvIG5vIHNpcnZlIHBhcmEg\nbmFkYS4=\n-----END CERTIFICATE-----\n" };
    },
    "services.list": () => st.services,
    "services.set": ({ id, enabled }) => {
      const s = st.services.find((x) => x.id === id);
      if (!s) throw new RpcError("NOT_FOUND", "Servicio desconocido");
      const j = ctx.startJob("services.set", enabled ? t("Activando {name}", { name: id.toUpperCase() }) : t("Desactivando {name}", { name: id.toUpperCase() }), enabled ? t("Generando configuración y arrancando") : t("Deteniendo"), 0, 0.34, undefined, () => {
        s.enabled = enabled;
        s.running = enabled;
        ctx.emit("services.changed", { id });
      });
      audit("services.set", `${id}=${enabled ? "on" : "off"}`);
      return { job: j.id };
    },
    "nfs.exports.get": ({ share }) => ({ clients: st.nfs.get(share) ?? [] }),
    "nfs.exports.set": ({ share, clients }) => {
      for (const c of clients) if (!/^[\w.*:/-]+$/.test(c.host)) throw new RpcError("INVALID_PARAMS", t("«{host}» no es un host, IP o red válida", { host: c.host }));
      st.nfs.set(share, clients.map((c) => ({ ...c })));
      audit("nfs.exports.set", share);
      return {};
    },
    "iscsi.targets.list": () => st.iscsi,
    "iscsi.targets.create": ({ name, volume, sizeBytes, chap, zvol, pool, thin }) => {
      if (!/^[a-z0-9][a-z0-9-]{0,30}$/.test(name) || name === "thin") throw new RpcError("INVALID_PARAMS", "Usá minúsculas, números y guiones");
      if (st.iscsi.some((t) => t.name === name)) throw new RpcError("TARGET_EXISTS", "Ya existe un destino con ese nombre");
      if (chap && (chap.secret.length < 12 || chap.secret.length > 16)) throw new RpcError("INVALID_PARAMS", "El secreto CHAP debe tener entre 12 y 16 caracteres");
      let target: IscsiTarget;
      let detail = t("LUN sobre archivo en Btrfs (nodatacow)");
      if (pool) {
        if (sizeBytes < 64 * 1024 ** 2) throw new RpcError("INVALID_PARAMS", "El tamaño debe estar entre 64 MB y 64 TB");
        target = { name, iqn: `iqn.2026-10.local.${st.hostname}:${name}`, volume: pool, sizeBytes, chap: !!chap, sessions: 0, lv: `/dev/${pool.replace(/^pool_/, "")}/il_${name}`, thin: !!thin };
        detail = thin ? t("LUN en un volumen lógico thin del grupo") : t("LUN en un volumen lógico del grupo");
      } else if (zvol) {
        target = { name, iqn: `iqn.2026-10.local.${st.hostname}:${name}`, volume: "zfs:tanque", sizeBytes, chap: !!chap, sessions: 0, zvol };
        detail = t("LUN sobre un volumen de bloque ZFS");
      } else {
        const v = ctx.vols().find((x) => x.id === volume);
        if (!v) throw new RpcError("VOLUME_NOT_FOUND", "No existe el volumen");
        if (sizeBytes <= 0 || sizeBytes > v.size - v.used) throw new RpcError("NO_SPACE", "No hay espacio suficiente en el volumen");
        target = { name, iqn: `iqn.2026-10.local.${st.hostname}:${name}`, volume: v.id, sizeBytes, chap: !!chap, sessions: 0 };
      }
      const j = ctx.startJob("iscsi.targets.create", t("Creando destino {name}", { name }), detail, 0, 0.2, undefined, () => {
        st.iscsi.push(target);
        ctx.emit("services.changed", { id: "iscsi" });
      });
      audit("iscsi.targets.create", name);
      return { job: j.id };
    },
    "iscsi.targets.delete": ({ name, typed: word }) => {
      typed(word, name);
      if (!st.iscsi.some((x) => x.name === name)) throw new RpcError("NOT_FOUND", "No existe ese destino");
      const j = ctx.startJob("iscsi.targets.delete", t("Eliminando destino {name}", { name }), t("Desconectando sesiones y borrando el LUN"), 0, 0.3, undefined, () => {
        st.iscsi = st.iscsi.filter((x) => x.name !== name);
        ctx.emit("services.changed", { id: "iscsi" });
      });
      audit("iscsi.targets.delete", name);
      return { job: j.id };
    },
    "notify.channels.get": () => structuredClone(st.notify),
    "notify.channels.set": (c) => {
      if (c.email.enabled && (!c.email.host || !c.email.to)) throw new RpcError("INVALID_PARAMS", "Completá el servidor SMTP y el destinatario");
      if (c.ntfy.enabled && !/^https?:\/\//.test(c.ntfy.server)) throw new RpcError("INVALID_PARAMS", "La URL de ntfy tiene que empezar con https://");
      if (c.telegram.enabled && (!c.telegram.botToken || !c.telegram.chatId)) throw new RpcError("INVALID_PARAMS", "Completá el token del bot y el chat");
      st.notify = structuredClone(c);
      audit("notify.channels.set", "canales");
      return {};
    },
    "notify.test": ({ channel }) => {
      if (!st.notify[channel].enabled) throw new RpcError("CHANNEL_DISABLED", "Activá el canal antes de probarlo");
      ctx.notify("info", "tango.settings", t("Notificación de prueba"), t("Demo: se habría enviado por {channel}.", { channel: channel === "email" ? t("correo") : channel === "ntfy" ? "ntfy" : "Telegram" }));
      return {};
    },
    "audit.list": ({ q, limit }) => {
      const n = q?.toLowerCase();
      return st.audit.filter((e) => !n || `${e.actor} ${e.method} ${e.target} ${e.ip}`.toLowerCase().includes(n)).slice(0, limit);
    },
    "logs.query": ({ unit, q, limit }) => {
      const n = q?.toLowerCase();
      return logs.filter((l) => (!unit || l.unit === unit) && (!n || l.msg.toLowerCase().includes(n))).slice(0, limit);
    },
    "term.open": () => ({ id: 1 }),
    "term.resize": () => ({}),
    "term.close": () => ({}),
    "apps.ticket": ({ id }) => ({ url: `data:text/html;charset=utf-8,${encodeURIComponent(`<h1 style="font-family:system-ui">${id}</h1><p>${t("Demo: el NAS real devuelve una URL de un solo uso que abre la app con tu sesión.")}</p>`)}` }),
  };
}
