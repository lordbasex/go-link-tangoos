import { describe, expect, it, vi } from "vitest";
import { decodeTermFrame, encodeTermFrame, BaseTermSession } from "./terminal";
import { RtcTerminals } from "./rtc/term";
import { MockTerminals } from "./mock/terminal";
import { passwordStrength, validHostname } from "./protocol-m4";
import { m4Handlers } from "./mock/m4";
import { RpcError } from "./protocol";
import type { Job } from "./protocol";

const dec = new TextDecoder();

describe("canal term: frames [u32 id][datos]", () => {
  it("codifica y decodifica id grande y datos UTF-8", () => {
    const f = encodeTermFrame(0xfffffffe, "hola ñandú");
    const d = decodeTermFrame(f)!;
    expect(d.id).toBe(0xfffffffe);
    expect(dec.decode(d.data)).toBe("hola ñandú");
  });

  it("rechaza frames de menos de 4 bytes", () => {
    expect(decodeTermFrame(new ArrayBuffer(3))).toBeNull();
  });

  it("RtcTerminals enruta por id y un frame vacío cierra la sesión", async () => {
    const sent: ArrayBuffer[] = [];
    const call = vi.fn(async (m: string) => (m === "term.open" ? { id: 7 } : {}));
    const ch = { readyState: "open", send: (b: ArrayBuffer) => sent.push(b) } as unknown as RTCDataChannel;
    const terms = new RtcTerminals({ call: call as never, channel: () => ch });
    const s = await terms.open(80, 24);
    expect(call).toHaveBeenCalledWith("term.open", { cols: 80, rows: 24 });
    const got: string[] = [];
    let exitReason = "";
    s.onData((c) => got.push(dec.decode(c)));
    s.onExit((r) => (exitReason = r));
    s.write("ls\r");
    expect(decodeTermFrame(sent[0])!.id).toBe(7);
    terms.onFrame(encodeTermFrame(99, "ajeno")); // otra terminal: se ignora
    terms.onFrame(encodeTermFrame(7, "salida"));
    expect(got).toEqual(["salida"]);
    terms.onFrame(encodeTermFrame(7, new Uint8Array()));
    expect(exitReason).toBe("exit");
  });

  it("failAll avisa desconexión a las sesiones abiertas", async () => {
    const terms = new RtcTerminals({ call: (async () => ({ id: 1 })) as never, channel: () => undefined });
    const s = await terms.open(80, 24);
    let reason = "";
    s.onExit((r) => (reason = r));
    terms.failAll();
    expect(reason).toBe("disconnected");
  });

  it("close() pide term.close una sola vez", () => {
    const close = vi.fn();
    const s = new BaseTermSession(3, { send: () => undefined, resize: () => undefined, close });
    s.close();
    s.close();
    expect(close).toHaveBeenCalledTimes(1);
  });

  it("la terminal DEMO responde comandos conocidos", async () => {
    vi.useFakeTimers();
    const s = await new MockTerminals().open();
    const out: string[] = [];
    s.onData((c) => out.push(dec.decode(c)));
    s.write("whoami\r");
    await vi.runAllTimersAsync();
    expect(out.join("")).toContain("admin");
    vi.useRealTimers();
  });
});

describe("fuerza de contraseña y nombre de host", () => {
  it("rechaza contraseñas comunes o con el nombre", () => {
    expect(passwordStrength("password123").score).toBe(0);
    expect(passwordStrength("federico2026!", ["federico"]).score).toBe(0);
  });
  it("corta las contraseñas cortas a débil", () => {
    expect(passwordStrength("Ab1!xyz").score).toBeLessThanOrEqual(1);
  });
  it("una frase larga es fuerte", () => {
    expect(passwordStrength("mate amargo en la terraza de lanús").score).toBeGreaterThanOrEqual(3);
  });
  it("valida nombres de host", () => {
    expect(validHostname("tango")).toBeNull();
    expect(validHostname("nas-casa-2")).toBeNull();
    expect(validHostname("-malo")).not.toBeNull();
    expect(validHostname("Mayus")).not.toBeNull();
    expect(validHostname("a".repeat(64))).not.toBeNull();
  });
});

describe("mock M4", () => {
  const jobs: (Job & { onTick?: (p: number) => void; onDone?: () => void })[] = [];
  const h = m4Handlers({
    scenario: () => "healthy",
    emit: () => undefined,
    startJob: (type, title, detail, progress, _step, _rate, onDone) => {
      const j = { id: `j${jobs.length}`, type, title, detail, progress, state: "running" as const, startedAt: "", onDone };
      jobs.push(j);
      return j;
    },
    notify: () => undefined,
    vols: () => [{ id: "vol1", name: "volume1", mount: "/volume1", profile: "raid1", fs: "btrfs", size: 100e9, used: 40e9, state: "healthy", disks: [], lastScrub: null, growth30d: [] }],
    shares: () => [],
  });
  const err = (fn: () => unknown) => {
    try {
      fn();
    } catch (e) {
      return (e as RpcError).code;
    }
    return null;
  };

  it("sin ?setup el NAS ya está configurado", () => {
    expect(h["setup.state"]!(undefined).state).toBe("done");
    expect(err(() => h["setup.complete"]!({ hostname: "tango", admin: { name: "fede", full: "Fede", password: "una frase bien larga de prueba" }, timezone: "UTC", certificate: "localca" }))).toBe("SETUP_DONE");
  });

  it("reiniciar y apagar exigen la palabra exacta", () => {
    expect(err(() => h["system.reboot"]!({ typed: "reiniciar" }))).toBe("CONFIRMATION_MISMATCH");
    expect(err(() => h["system.reboot"]!({ typed: "REINICIAR" }))).toBeNull();
    expect(err(() => h["system.shutdown"]!({ typed: "REINICIAR" }))).toBe("CONFIRMATION_MISMATCH");
  });

  it("iSCSI valida nombre, espacio y CHAP", () => {
    expect(err(() => h["iscsi.targets.create"]!({ name: "VM 1", volume: "vol1", sizeBytes: 1e9 }))).toBe("INVALID_PARAMS");
    expect(err(() => h["iscsi.targets.create"]!({ name: "vm1", volume: "vol1", sizeBytes: 200e9 }))).toBe("NO_SPACE");
    expect(err(() => h["iscsi.targets.create"]!({ name: "vm1", volume: "vol1", sizeBytes: 1e9, chap: { user: "u", secret: "corto" } }))).toBe("INVALID_PARAMS");
    expect(err(() => h["iscsi.targets.create"]!({ name: "vm1", volume: "vol1", sizeBytes: 10e9 }))).toBeNull();
  });

  it("la descarga de la CA requiere CA local", () => {
    expect(err(() => h["cert.caDownload"]!(undefined))).toBe("NOT_AVAILABLE");
  });

  it("la actualización deja un log progresivo en job.detail", () => {
    const { job } = h["system.updates.apply"]!({ scope: "security" });
    const j = jobs.find((x) => x.id === job)!;
    const seen = new Set<string>();
    for (let p = 0; p <= 1; p += 0.05) {
      j.onTick?.(p);
      seen.add(j.detail);
    }
    expect(seen.size).toBeGreaterThan(4);
    expect([...seen][0]).toMatch(/instantánea/i);
  });

  it("notify.test exige el canal activo", () => {
    expect(err(() => h["notify.test"]!({ channel: "telegram" }))).toBe("CHANNEL_DISABLED");
    expect(err(() => h["notify.test"]!({ channel: "ntfy" }))).toBeNull();
  });

  it("audit.list filtra por texto", () => {
    const all = h["audit.list"]!({ limit: 200 });
    const logins = h["audit.list"]!({ q: "auth.login", limit: 200 });
    expect(all.length).toBeGreaterThan(logins.length);
    expect(logins.every((e) => e.method === "auth.login")).toBe(true);
  });
});
