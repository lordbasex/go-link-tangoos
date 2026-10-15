// ◆ → Reiniciar…/Apagar…: hoja de confirmación con palabra escrita y, después del reinicio,
// una pantalla "Reconectando…" que espera a que el transporte vuelva (protocol §12).
import { useEffect, useRef, useState } from "react";
import { Power, RotateCcw } from "lucide-react";
import { RpcError, useTransport } from "@tango/client";
import { closeOverlay, power } from "./store";

const WORD = { reboot: "REINICIAR", shutdown: "APAGAR" } as const;

export function PowerHost() {
  const st = power.use();
  if (!st) return null;
  if (st.phase === "confirm") return <PowerConfirm kind={st.kind} />;
  return <PowerWait kind={st.kind} off={st.phase === "off"} />;
}

function PowerConfirm({ kind }: { kind: "reboot" | "shutdown" }) {
  const t = useTransport();
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const word = WORD[kind];
  const ok = typed === word;

  const run = async () => {
    if (!ok || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await t.call(kind === "reboot" ? "system.reboot" : "system.shutdown", { typed });
      power.set({ kind, phase: kind === "reboot" ? "waiting" : "off" });
    } catch (e) {
      setBusy(false);
      setErr(e instanceof RpcError ? e.message : "No se pudo enviar la orden al NAS.");
    }
  };

  return (
    <div className="tg-power-backdrop" onMouseDown={(e) => e.target === e.currentTarget && !busy && power.set(null)}>
      <div className="tg-power" role="alertdialog" aria-labelledby="tg-power-title" onKeyDown={(e) => e.key === "Escape" && !busy && power.set(null)}>
        <div className="tg-power-icon" data-kind={kind}>
          {kind === "reboot" ? <RotateCcw size={26} /> : <Power size={26} />}
        </div>
        <h2 id="tg-power-title">{kind === "reboot" ? "¿Reiniciar el NAS?" : "¿Apagar el NAS?"}</h2>
        <p>
          {kind === "reboot"
            ? "Las carpetas compartidas, las apps y las transferencias en curso se cortan durante uno o dos minutos. El escritorio se reconecta solo."
            : "Las carpetas compartidas y las apps dejan de estar disponibles hasta que lo vuelvas a encender con el botón del equipo."}
        </p>
        <label className="tg-power-field">
          Para confirmar, escribí <code>{word}</code>
          <input autoFocus value={typed} onChange={(e) => setTyped(e.target.value.toUpperCase())} onKeyDown={(e) => e.key === "Enter" && void run()} spellCheck={false} aria-label={`Escribí ${word}`} />
        </label>
        {err && (
          <div className="tg-power-err" role="alert">
            {err}
          </div>
        )}
        <div className="tg-power-actions">
          <button type="button" className="tg-btn" onClick={() => power.set(null)} disabled={busy}>
            Cancelar
          </button>
          <button type="button" className="tg-btn" data-variant="danger" onClick={() => void run()} disabled={!ok || busy}>
            {busy ? "Enviando…" : kind === "reboot" ? "Reiniciar" : "Apagar"}
          </button>
        </div>
      </div>
    </div>
  );
}

/** Espera a que el transporte se caiga y vuelva (reinicio) o se queda en "apagado". */
function PowerWait({ kind, off }: { kind: "reboot" | "shutdown"; off: boolean }) {
  const t = useTransport();
  const [secs, setSecs] = useState(0);
  const sawDown = useRef(false);

  useEffect(() => {
    const started = Date.now();
    const tick = setInterval(() => setSecs(Math.round((Date.now() - started) / 1000)), 1000);
    if (off) return () => clearInterval(tick);
    const check = (s: string) => {
      if (s !== "connected") sawDown.current = true;
      else if (sawDown.current) power.set(null);
    };
    const offState = t.onState(check);
    // Con el mock el transporte nunca cae: se simula un reinicio de 6 s.
    const sim = t.kind === "mock" ? setTimeout(() => power.set(null), 6000) : undefined;
    return () => {
      clearInterval(tick);
      offState();
      clearTimeout(sim);
    };
  }, [t, off]);

  return (
    <div className="tg-power-wait" role="status" aria-live="polite">
      {off ? (
        <>
          <Power size={34} />
          <h2>El NAS se está apagando</h2>
          <p>Cuando la luz del equipo se apague, podés desenchufarlo. Para volver a usarlo, encendelo con su botón y recargá esta página.</p>
        </>
      ) : (
        <>
          <div className="tg-power-spinner" />
          <h2>{kind === "reboot" ? "Reiniciando el NAS…" : "Esperando…"}</h2>
          <p>{secs < 90 ? "El escritorio se reconecta solo en cuanto el NAS vuelva." : "Está tardando más de lo normal. Si el equipo tiene monitor, fijate si muestra algún mensaje."}</p>
          <small>{secs} s</small>
        </>
      )}
    </div>
  );
}

export const askPower = (kind: "reboot" | "shutdown") => {
  closeOverlay();
  power.set({ kind, phase: "confirm" });
};
