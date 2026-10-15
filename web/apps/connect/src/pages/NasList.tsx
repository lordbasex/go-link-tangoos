// /nas y /nas/vincular: como la home de desk.go-link.org/web (la de la app de iPhone): la tarjeta del
// código, "Mis NAS" vinculados en este navegador y una nota. Todo vive solo en este navegador.
import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { applyDigits, CODE_LEN, deleteDigit, isCompleteCode, normalizeCode } from "../connect/code";
import { browserLabel, openRemote, type RemoteSession, type RemoteStage } from "../connect/remote";
import { TangoMark } from "../components/site";
import { Link, navigate } from "../router";
import { NasStore, type LinkedNas } from "../store/nasStore";

const store = new NasStore();

function Glyph({ d, size = 22 }: { d: string; size?: number }) {
  return (
    <svg className="ios-glyph" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <path d={d} />
    </svg>
  );
}
const NAS_GLYPH = "M5 4h14a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1Z M8 8h8 M8 12h8 M8 16h3 M16.5 16h.01";
const CHEVRON = "M9 6l6 6-6 6";
const PLUS = "M12 5v14M5 12h14";
const HOME = "M3 11l9-7 9 7 M5 10v10h14V10";

function when(iso: string | null): string {
  return iso ? new Date(iso).toLocaleString("es-AR", { dateStyle: "medium", timeStyle: "short" }) : "nunca";
}

export function NasList() {
  const [list, setList] = useState<LinkedNas[] | null>(null);
  const [menu, setMenu] = useState<string | null>(null);
  const reload = () => void store.list().then(setList, () => setList([]));
  useEffect(reload, []);
  return (
    <div className="ios">
      <div className="ios-col">
        <header className="ios-head">
          <TangoMark size={44} />
          <div>
            <h1 className="ios-title">TangoOS Connect</h1>
            <p className="ios-subtitle">Tu NAS desde cualquier lado</p>
          </div>
          <Link className="ios-round" to="/" aria-label="Inicio">
            <Glyph d={HOME} />
          </Link>
        </header>

        <section className="ios-sec" aria-labelledby="ios-nas">
          <div className="ios-sec-head">
            <h2 id="ios-nas">Mis NAS</h2>
            <Link className="ios-action" to="/nas/vincular">
              <Glyph d={PLUS} size={16} /> Vincular
            </Link>
          </div>
          {list === null ? null : list.length === 0 ? (
            <div className="ios-card ios-connect">
              <p className="ios-card-title">Todavía no vinculaste ningún NAS</p>
              <p className="ios-note">En tu NAS: Ajustes → TangoConnect → «Vincular navegador». Te muestra un código de 9 dígitos que dura 10 minutos.</p>
              <button type="button" className="ios-primary" onClick={() => navigate("/nas/vincular")}>
                Vincular un NAS
              </button>
            </div>
          ) : (
            <ul className="ios-list">
              {list.map((n) => (
                <li key={n.deviceId} className="ios-row" style={{ flexWrap: "wrap" }}>
                  <Link to={`/nas/${n.deviceId}`} className="ios-row-link" aria-label={`Entrar a ${n.name}`}>
                    <span className="ios-row-icon">
                      <Glyph d={NAS_GLYPH} />
                    </span>
                    <span className="ios-row-text">
                      <span className="ios-row-title">{n.name}</span>
                      <span className="ios-row-detail">Último acceso: {when(n.lastSeen)}</span>
                    </span>
                    <span className="ios-row-trailing">
                      <Glyph d={CHEVRON} size={18} />
                    </span>
                  </Link>
                  <button type="button" className="ios-more" aria-label={`Opciones de ${n.name}`} aria-expanded={menu === n.deviceId} onClick={() => setMenu(menu === n.deviceId ? null : n.deviceId)}>
                    ···
                  </button>
                  {menu === n.deviceId && (
                    <div className="ios-row-menu" style={{ flexBasis: "100%" }}>
                      <button
                        type="button"
                        className="ios-chip-button ios-chip-danger"
                        onClick={() => {
                          void store.remove(n.deviceId).then(reload);
                          setMenu(null);
                        }}
                      >
                        Olvidar en este navegador
                      </button>
                    </div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </section>

        <p className="ios-note">
          Los vínculos se guardan solo en este navegador. Para cortar el acceso de un navegador perdido, revocalo desde tu NAS en Ajustes → TangoConnect.
        </p>
      </div>
    </div>
  );
}

/** Nueve casillas, una por dígito: escribir avanza, pegar reparte, borrar retrocede. */
export function CodeDigits({ value, onChange, onSubmit, disabled }: { value: string; onChange: (v: string) => void; onSubmit: () => void; disabled?: boolean }) {
  const refs = useRef<(HTMLInputElement | null)[]>([]);
  const cells = value.padEnd(CODE_LEN, " ").split("");
  const focus = (i: number) => refs.current[Math.max(0, Math.min(CODE_LEN - 1, i))]?.focus();
  const onKey = (i: number) => (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Backspace") {
      e.preventDefault();
      const r = deleteDigit(value, i);
      onChange(r.value);
      focus(r.focus);
    } else if (e.key === "ArrowLeft") focus(i - 1);
    else if (e.key === "ArrowRight") focus(i + 1);
    else if (e.key === "Enter" && isCompleteCode(value.replace(/\s/g, ""))) onSubmit();
  };
  return (
    <div className="tc-digits" role="group" aria-label="Código de 9 dígitos">
      {cells.map((c, i) => (
        <input
          key={i}
          ref={(el) => {
            refs.current[i] = el;
          }}
          className="tc-digit"
          inputMode="numeric"
          autoComplete="one-time-code"
          aria-label={`Dígito ${i + 1}`}
          maxLength={CODE_LEN}
          disabled={disabled}
          value={c.trim()}
          data-gap={i === 2 || i === 5 || undefined}
          onChange={(e) => {
            const r = applyDigits(value, i, e.target.value);
            onChange(r.value);
            focus(r.focus);
          }}
          onKeyDown={onKey(i)}
          onFocus={(e) => e.target.select()}
        />
      ))}
    </div>
  );
}

const STAGE_TEXT: Partial<Record<RemoteStage, string>> = {
  signal: "Conectando con el servidor de señalización…",
  "waiting-nas": "Buscando tu NAS…",
  paired: "Armando la conexión directa…",
  linking: "Guardando el vínculo…",
  connected: "Listo",
  retrying: "Reintentando…",
};

export function LinkNas() {
  const initial = normalizeCode(new URLSearchParams(location.search).get("code") ?? "");
  const [code, setCode] = useState(initial);
  const [stage, setStage] = useState<RemoteStage | null>(null);
  const [error, setError] = useState<string | null>(null);
  const session = useRef<RemoteSession | null>(null);
  useEffect(() => () => session.current?.close(), []);
  const ready = isCompleteCode(code.replace(/\s/g, ""));
  const submit = () => {
    if (!ready || stage) return;
    setError(null);
    const s = openRemote({
      target: { mode: "claim", code: code.replace(/\s/g, "") },
      label: browserLabel(),
      onStage: setStage,
      onFatal: (m) => {
        setError(m);
        setStage(null);
        session.current = null;
      },
    });
    session.current = s;
    s.linked.then(
      (r) => {
        if (!r) return;
        // El vínculo quedó guardado: vamos al escritorio con esta misma conexión cerrada (la reabre reach).
        s.close();
        session.current = null;
        navigate(`/nas/${r.deviceId}`, { replace: true });
      },
      () => {},
    );
  };
  return (
    <div className="ios">
      <div className="ios-col">
        <header className="ios-head">
          <TangoMark size={44} />
          <div>
            <h1 className="ios-title">Vincular un NAS</h1>
            <p className="ios-subtitle">Una sola vez por navegador</p>
          </div>
          <Link className="ios-round" to="/nas" aria-label="Mis NAS">
            <Glyph d="M15 6l-6 6 6 6" />
          </Link>
        </header>
        <form
          className="ios-card ios-connect"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
          noValidate
        >
          <label className="ios-card-title">Escribí el código que muestra tu NAS</label>
          <CodeDigits value={code} onChange={setCode} onSubmit={submit} disabled={!!stage} />
          {error && (
            <p className="ios-error" role="alert">
              {error}
            </p>
          )}
          {stage && (
            <p className="ios-note" role="status">
              {STAGE_TEXT[stage] ?? "Conectando…"}
            </p>
          )}
          <button type="submit" className="ios-primary" disabled={!ready || !!stage}>
            {stage ? "Vinculando…" : "Vincular"}
          </button>
        </form>
        <section className="ios-sec">
          <h2 className="ios-card-title">¿Dónde está el código?</h2>
          <ol className="ios-note" style={{ paddingLeft: 18, display: "grid", gap: 6 }}>
            <li>Abrí el escritorio de tu NAS en tu casa (https://tango.local).</li>
            <li>Ajustes → TangoConnect → activalo si está apagado.</li>
            <li>«Vincular navegador»: aparece un código de 9 dígitos que dura 10 minutos.</li>
          </ol>
        </section>
      </div>
    </div>
  );
}
