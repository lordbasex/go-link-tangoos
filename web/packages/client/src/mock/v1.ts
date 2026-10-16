// Fase v1.0 simulada (protocol/README.md §13): 2FA, UPS, files.stat y shares.update. Datos DEMO.
import { RpcError } from "../protocol";
import { t } from "../i18n";
import type { Job, Method, Notice, Params, Result, Share, Topic, Topics } from "../protocol";
import type { UpsConfig, UpsStatus } from "../protocol-v1";
import type { MemFS } from "./data";

export interface MockV1Ctx {
  emit: <T extends Topic>(topic: T, d: Topics[T]) => void;
  startJob: (type: string, title: string, detail: string, progress: number, step: number, rate?: string, onDone?: () => void) => Job & { onTick?: (p: number) => void };
  notify: (level: Notice["level"], app: string, title: string, body: string) => void;
  shares: () => Share[];
  fs: () => MemFS;
}

/** QR de demostración (no escaneable): un patrón con la palabra DEMO. */
function demoQR(): string {
  const cells: string[] = [];
  for (let y = 0; y < 25; y++)
    for (let x = 0; x < 25; x++) {
      const finder = (x < 7 && y < 7) || (x > 17 && y < 7) || (x < 7 && y > 17);
      const ring = finder && (x % 6 === 0 || y % 6 === 0 || (x % 18 > 1 && x % 18 < 5 && y % 18 > 1 && y % 18 < 5));
      if ((finder && ring) || (!finder && ((x * 7 + y * 13) % 5 === 0))) cells.push(`M${x} ${y}h1v1h-1z`);
    }
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 25 25"><rect width="25" height="25" fill="#fff"/><path d="${cells.join("")}"/><text x="12.5" y="14" font-size="4" text-anchor="middle" fill="#d33" font-family="sans-serif">DEMO</text></svg>`;
  return `data:image/svg+xml;base64,${btoa(svg)}`;
}

export function v1Handlers(ctx: MockV1Ctx): { [M in Method]?: (p: Params<M>) => Result<M> } {
  let pending = false;
  let ups: UpsConfig = { mode: "none", host: "", port: 3493, upsName: "ups", netServer: false, policy: { action: "shutdown", afterMinutes: 5, belowPercent: 20 } };
  const upsStatus = (): UpsStatus => {
    const on = ups.mode !== "none";
    return {
      mode: ups.mode, configured: on, online: on, name: ups.upsName, model: on ? "APC Back-UPS ES 700 (DEMO)" : "",
      status: on ? "OL CHRG" : "", statusText: on ? t("Con luz, cargando") : t("Sin datos"),
      batteryPct: on ? 96 : null, runtimeSec: on ? 2280 : null, loadPct: on ? 18 : null, inputVoltage: on ? 221 : null,
      lastUpdate: new Date().toISOString(), onBatterySince: null,
      detected: [{ vendor: "American Power Conversion", product: "Back-UPS ES 700", id: "051d:0002" }],
    };
  };
  return {
    "users.totp.begin": () => {
      pending = true;
      return { secret: "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP", uri: "otpauth://totp/TangoOS:federico?secret=JBSWY3DPEHPK3PXP&issuer=TangoOS", qr: demoQR() };
    },
    "users.totp.confirm": ({ code }) => {
      if (!pending) throw new RpcError("TOTP_NOT_PENDING", "Primero generá el código QR");
      if (!/^\d{6}$/.test(code)) throw new RpcError("INVALID_CODE", "El código no es correcto");
      pending = false;
      return { recoveryCodes: Array.from({ length: 10 }, (_, i) => `demo${i}-abcd${i}`) };
    },
    "users.totp.disable": ({ password }) => {
      if (password === "error") throw new RpcError("INVALID_CREDENTIALS", "La contraseña no es correcta");
      return {};
    },
    "users.totp.reset": () => ({}),
    "auth.totp": () => {
      throw new RpcError("TOTP_NOT_PENDING", "Demo: el login simulado no pide código");
    },
    "ups.status": () => upsStatus(),
    "ups.config.get": () => ({ ...ups, password: "" }),
    "ups.config.set": (p) => {
      if (p.mode === "remote" && !p.host) throw new RpcError("INVALID_PARAMS", "Indicá la dirección del equipo con el UPS");
      const j = ctx.startJob("ups.config", p.mode === "none" ? t("Desactivando el UPS") : t("Configurando el UPS"), "DEMO", 0, 0.34, undefined, () => {
        ups = { ...p, password: "" };
        ctx.emit("ups.changed", upsStatus());
        ctx.emit("services.changed", { id: "ups" as never });
      });
      return { job: j.id };
    },
    "files.stat": ({ path }) => {
      const dir = path.slice(0, path.lastIndexOf("/")) || "/";
      const e = ctx.fs().list(dir)?.find((x) => x.path === path);
      if (!e) throw new RpcError("PATH_NOT_FOUND", t("No existe {path}", { path }));
      return e;
    },
    "shares.update": ({ share, description, smb, timeMachine, timeMachineMaxBytes }) => {
      const sh = ctx.shares().find((x) => x.id === share);
      if (!sh) throw new RpcError("SHARE_NOT_FOUND", "No existe la carpeta compartida");
      const j = ctx.startJob("shares.update", t("Actualizando {name}", { name: sh.name }), "DEMO", 0, 0.5, undefined, () => {
        if (description !== undefined) sh.description = description;
        if (smb !== undefined) sh.smb = smb;
        if (timeMachine !== undefined) sh.timeMachine = timeMachine && sh.smb;
        sh.timeMachineMaxBytes = sh.timeMachine ? (timeMachineMaxBytes ?? null) : null;
        ctx.emit("shares.changed", { id: share });
      });
      return { job: j.id };
    },
  };
}
