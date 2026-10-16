// Sesión remota con un NAS a través de signalhub (protocol/README.md §15, docs/16).
//   claim → código de 9 dígitos → connect.link → guarda el vínculo → las reconexiones usan reach
//   reach → el NAS prueba primero (connect.challenge) → connect.resume {linkId, token}
//   guest → enlace público: reach sin vínculo → guest.open {link, secret}
// El transporte es el RtcTransport de @tango/client (rtcFromSignal): los métodos llegan por
// connect.hello y `beforeAuth` corre el paso de vínculo antes del auth.resume, en cada reconexión.
import { rtcFromSignal, setToken, t, type RtcTransport } from "@tango/client";
import { SIGNAL_URL } from "../config";
import { FrameCollector } from "../bundle/frames";
import { joinSignal, type Stage, type Target } from "../signal/socket";
import { humanSignalError, SignalError } from "../signal/signalhub";
import { observedPeerConnection, remoteFingerprint } from "./peer";
import { linkProof, newNonce, normalizeFingerprint, sameText, tokenKey } from "./proof";
import { NasStore, type LinkedNas } from "../store/nasStore";

export type RemoteStage = Stage | "linking" | "verifying" | "connected" | "retrying";

export interface LinkResult {
  deviceId: string;
  name: string;
}

/** Prefijo del mensaje de error cuando la huella DTLS no es la fijada al vincular (la UI ofrece re-vincular).
 * Con la interfaz en inglés, el mensaje empieza por t(FINGERPRINT_PREFIX). */
export const FINGERPRINT_PREFIX = "La huella del certificado de tu NAS cambió";

/** Errores de signalhub que no se arreglan reintentando. */
const FATAL_SIGNAL = new Set(["invalid or expired code", "invalid or expired invite", "app not allowed"]);
/** Errores del NAS que no se arreglan reintentando. */
const FATAL_RPC = new Set(["FINGERPRINT_CHANGED", "NAS_NOT_VERIFIED", "LINK_NOT_FOUND", "LINK_INVALID", "NOT_PAIRED", "SHARE_LINK_NOT_FOUND", "SHARE_LINK_EXPIRED", "FORBIDDEN", "CONNECT_DISABLED"]);

export interface RemoteSession {
  transport: RtcTransport;
  collector: FrameCollector;
  linked: Promise<LinkResult | null>;
  close(): void;
}

const DEVICE_KEY = "tangoconnect.device";

/** La sesión de TangoOS (sessionStorage) es de un NAS: al cambiar de NAS se descarta. */
function isolateSession(id: string) {
  try {
    if (sessionStorage.getItem(DEVICE_KEY) !== id) {
      setToken(null);
      sessionStorage.setItem(DEVICE_KEY, id);
    }
  } catch {
    setToken(null);
  }
}

class StepError extends Error {
  constructor(
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

type Call = (method: string, params: unknown) => Promise<unknown>;

export function openRemote(opts: {
  target: Target;
  /** Para reach: el vínculo guardado. */
  nas?: LinkedNas;
  /** Nombre de este navegador para el NAS ("Chrome en Mac"). */
  label: string;
  store?: NasStore;
  onStage?: (s: RemoteStage) => void;
  onFatal?: (message: string) => void;
}): RemoteSession {
  const store = opts.store ?? new NasStore();
  const collector = new FrameCollector();
  let target = opts.target;
  let nas = opts.nas;
  let pc: RTCPeerConnection | null = null;
  let settled = false;
  let settle: (r: LinkResult | null) => void = () => {};
  let fail: (e: Error) => void = () => {};
  const linked = new Promise<LinkResult | null>((res, rej) => {
    settle = (r) => {
      settled = true;
      res(r);
    };
    fail = rej;
  });
  linked.catch(() => {});

  if (target.mode === "guest") isolateSession(`guest:${target.deviceId}.${target.link}`);
  else if (target.mode === "reach") isolateSession(target.deviceId);
  else setToken(null);

  let transport: RtcTransport;
  let dead = false;
  const fatal = (message: string) => {
    if (dead) return;
    dead = true;
    opts.onFatal?.(message);
    fail(new Error(message));
    collector.failAll(new Error(message));
    transport?.close();
  };

  const nasName = async (call: Call) => {
    try {
      return ((await call("connect.hello", null)) as { name?: string })?.name || "";
    } catch {
      return "";
    }
  };

  const beforeAuth = async (call: Call) => {
    if (target.mode === "guest") {
      await call("guest.open", { link: target.link, secret: target.secret });
      if (!settled) settle(null);
      return;
    }
    if (target.mode === "claim") {
      opts.onStage?.("linking");
      const r = (await call("connect.link", { label: opts.label })) as { linkId?: string; token?: string; deviceId?: string; fingerprint?: string };
      if (!r?.linkId || !r?.token || !r?.deviceId) throw new StepError("BAD_LINK", t("El NAS no entregó el vínculo"));
      const name = (await nasName(call)) || t("Mi NAS");
      const fp = normalizeFingerprint(r.fingerprint);
      nas = {
        deviceId: r.deviceId,
        linkId: r.linkId,
        name,
        token: r.token,
        fingerprint: fp || null,
        linkedAt: new Date().toISOString(),
        lastSeen: new Date().toISOString(),
      };
      await store.save(nas);
      // La sesión de TangoOS sigue siendo de este NAS; las reconexiones ya son reach.
      try {
        sessionStorage.setItem(DEVICE_KEY, nas.deviceId);
      } catch {
        /* sin sessionStorage */
      }
      target = { mode: "reach", deviceId: nas.deviceId };
      const seen = normalizeFingerprint(remoteFingerprint(pc));
      if (fp && seen && fp !== seen) throw new StepError("FINGERPRINT_CHANGED", t("La huella del certificado de tu NAS cambió durante la vinculación: no lo guardamos."));
      settle({ deviceId: nas.deviceId, name });
      return;
    }
    // reach: el NAS prueba primero; recién después le damos el token.
    if (!nas) throw new StepError("NO_LINK", t("Este navegador no tiene el vínculo de ese NAS"));
    opts.onStage?.("verifying");
    // Pin DTLS: la conexión tiene que presentar la huella que el NAS informó al vincular. Si cambió,
    // no seguimos (ni la prueba ni el token): el usuario decide si vuelve a vincular.
    const seenNow = normalizeFingerprint(remoteFingerprint(pc));
    if (nas.fingerprint && seenNow && seenNow !== nas.fingerprint)
      throw new StepError(
        "FINGERPRINT_CHANGED",
        t("La huella del certificado de tu NAS cambió desde que lo vinculaste. No le enviamos tu vínculo: si reinstalaste el NAS, olvidalo y volvé a vincularlo."),
      );
    const nonce = newNonce();
    const r = (await call("connect.challenge", { linkId: nas.linkId, nonce })) as { proof?: string };
    const want = await linkProof(await tokenKey(nas.token), nonce);
    if (typeof r?.proof !== "string" || !sameText(r.proof, want))
      throw new StepError("NAS_NOT_VERIFIED", t("Este NAS no pudo probar que es el mismo que vinculaste: no le enviamos tu vínculo."));
    await call("connect.resume", { linkId: nas.linkId, token: nas.token });
    const name = (await nasName(call)) || nas.name;
    await store.touch(nas.deviceId, { lastSeen: new Date().toISOString(), name });
    if (!settled) settle({ deviceId: nas.deviceId, name });
  };

  const join = () =>
    joinSignal(target, { signalUrl: SIGNAL_URL, onStage: (s) => opts.onStage?.(s) }).catch((e) => {
      if (e instanceof SignalError && FATAL_SIGNAL.has(e.message)) fatal(humanSignalError(e));
      else opts.onStage?.("retrying");
      throw e;
    });

  transport = rtcFromSignal(join, {
    client: "web",
    RTCPeerConnectionImpl: observedPeerConnection({
      onPeer: (p) => (pc = p),
      onChannel: (ch) => {
        if (ch.label.startsWith("files.")) collector.attach(ch);
      },
    }),
    remote: {
      beforeAuth,
      onError: (e) => {
        const code = (e as { code?: string })?.code ?? "";
        const msg = (e as Error)?.message ?? String(e);
        // Un corte en medio del paso de vínculo se reintenta; un rechazo del NAS no.
        if (FATAL_RPC.has(code)) fatal(code === "NAS_NOT_VERIFIED" || code === "FINGERPRINT_CHANGED" ? msg : `${msg}.`);
        else {
          // El RtcTransport se detiene tras un error de beforeAuth: lo volvemos a arrancar.
          opts.onStage?.("retrying");
          setTimeout(() => !dead && transport.connect(), 3000);
        }
      },
    },
  });
  transport.onState((s) => {
    if (s === "connected") opts.onStage?.("connected");
    if (s === "reconnecting") opts.onStage?.("retrying");
  });

  return {
    transport,
    collector,
    linked,
    close() {
      dead = true;
      collector.failAll(new Error(t("Sesión cerrada")));
      transport.close();
    },
  };
}

/** Nombre de este navegador para la lista de vínculos del NAS. */
export function browserLabel(): string {
  const ua = navigator.userAgent;
  const b = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : t("Navegador");
  const os = /iPhone|iPad/.test(ua) ? "iOS" : /Android/.test(ua) ? "Android" : /Mac OS X/.test(ua) ? "Mac" : /Windows/.test(ua) ? "Windows" : /Linux/.test(ua) ? "Linux" : "";
  return os ? t("{browser} en {os}", { browser: b, os }) : b;
}
