import { useEffect, useRef, useState } from "react";
import { ArrowRight, Globe, HardDrive, Power, RotateCcw } from "lucide-react";
import { LANGS, RpcError, getLang, locale, login, loginTotp, setLang, t, tr, useIsReal, useLive, useTransport } from "@tango/client";
import type { SessionUser, User } from "@tango/client";
import { DemoBadge } from "@tango/ui";
import { ui } from "./store";

export function Boot({ logo: Logo, onDone }: { logo: React.ComponentType<{ size?: number; color?: string }>; onDone: () => void }) {
  const [p, setP] = useState(0);
  useEffect(() => {
    const start = performance.now();
    let raf = 0;
    const tick = (t: number) => {
      const v = Math.min(1, (t - start) / 1100);
      setP(1 - Math.pow(1 - v, 3));
      if (v < 1) raf = requestAnimationFrame(tick);
      else setTimeout(onDone, 150);
    };
    raf = requestAnimationFrame(tick);
    // rAF no corre en pestañas ocultas: el arranque nunca debe quedar colgado.
    const fallback = setTimeout(onDone, 1600);
    return () => {
      cancelAnimationFrame(raf);
      clearTimeout(fallback);
    };
  }, [onDone]);
  return (
    <div className="tg-boot">
      <Logo size={64} color="#fff" />
      <div className="tg-boot-bar">
        <div style={{ width: `${p * 100}%` }} />
      </div>
    </div>
  );
}

export function Avatar({ user, size = 96 }: { user: Pick<User, "full" | "color">; size?: number }) {
  const initials = user.full
    .split(" ")
    .map((w) => w[0])
    .slice(0, 2)
    .join("");
  return (
    <div className="tg-avatar" style={{ width: size, height: size, fontSize: size * 0.38, background: `linear-gradient(160deg, color-mix(in srgb, ${user.color} 70%, white), ${user.color})` }} aria-hidden="true">
      {initials}
    </div>
  );
}

export function Login({ locked }: { locked?: boolean }) {
  const tp = useTransport();
  const users = useLive("auth.users", undefined).data ?? [];
  const real = useIsReal("auth.login");
  const current = ui.use((s) => s.user);
  const [who, setWho] = useState<string>(current?.name ?? "federico");
  const [pw, setPw] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [step, setStep] = useState<"password" | "totp">("password");
  const [now, setNow] = useState(new Date());
  const card = useRef<HTMLFormElement>(null);
  const user = users.find((u) => u.name === who) ?? users[0];

  useEffect(() => {
    const i = setInterval(() => setNow(new Date()), 10_000);
    return () => clearInterval(i);
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!user || busy) return;
    setBusy(true);
    setErr(null);
    try {
      const u = await login(tp, user.name, pw);
      setPw("");
      if (u === "totp") {
        setStep("totp"); // §13: contraseña bien, falta el código
        setBusy(false);
        return;
      }
      enter(u);
    } catch (x) {
      setErr(tr((x as Error).message));
      setPw("");
      shake();
      setBusy(false);
    }
  };

  const enter = (u: SessionUser) => {
    document.querySelector(".tg-login")?.animate([{ opacity: 1, transform: "none" }, { opacity: 0, transform: "scale(1.04)" }], { duration: 260, easing: "ease-in", fill: "forwards" });
    setTimeout(() => ui.set((s) => ({ ...s, session: "desktop", user: u })), 240);
  };
  const shake = () =>
    card.current?.animate([{ transform: "translateX(0)" }, { transform: "translateX(-9px)" }, { transform: "translateX(9px)" }, { transform: "translateX(-6px)" }, { transform: "translateX(5px)" }, { transform: "translateX(0)" }], { duration: 320, easing: "ease-out" });

  const submitCode = async (code: string) => {
    if (busy) return;
    setBusy(true);
    setErr(null);
    try {
      enter(await loginTotp(tp, code));
    } catch (x) {
      const e = x as RpcError;
      setErr(e.message);
      shake();
      if (e.code === "TOTP_LOCKED" || e.code === "TOTP_NOT_PENDING") setStep("password");
      setBusy(false);
    }
  };

  return (
    <div className="tg-login">
      <div className="tg-login-clock">
        <div className="tg-login-date">{cap(now.toLocaleDateString(locale(), { weekday: "long", day: "numeric", month: "long" }))}</div>
        <div className="tg-login-time">{now.toLocaleTimeString(locale(), { hour: "2-digit", minute: "2-digit", hourCycle: "h23" })}</div>
      </div>
      {user && step === "totp" && (
        <form className="tg-login-card" ref={card} onSubmit={(e) => e.preventDefault()}>
          <Avatar user={user} />
          <div className="tg-login-name">{user.full}</div>
          <TotpEntry busy={busy} onCode={submitCode} />
          <div className="tg-login-hint" role="status">
            {err ?? t("Escribí el código de 6 dígitos de tu app de autenticación.")}
          </div>
          <button type="button" className="tg-login-back" onClick={() => (setStep("password"), setErr(null))}>
            {t("Volver")}
          </button>
        </form>
      )}
      {user && step === "password" && (
        <form className="tg-login-card" ref={card} onSubmit={submit}>
          <Avatar user={user} />
          <div className="tg-login-name">{user.full}</div>
          <div className="tg-login-field">
            <input type="password" autoFocus placeholder={locked ? t("Contraseña para desbloquear") : t("Contraseña")} value={pw} onChange={(e) => setPw(e.target.value)} aria-label={t("Contraseña")} aria-invalid={!!err} />
            <button type="submit" aria-label={t("Entrar")} disabled={busy}>
              <ArrowRight size={14} strokeWidth={2.4} />
            </button>
          </div>
          <div className="tg-login-hint" role="status">
            {err ?? (locked ? t("Pantalla bloqueada. Tus ventanas siguen abiertas.") : real ? "" : t("Demo: cualquier contraseña entra, «error» falla."))}
          </div>
          {!locked && users.length > 1 && (
            <div className="tg-login-users">
              {users
                .filter((u) => u.name !== who)
                .map((u) => (
                  <button key={u.name} type="button" onClick={() => (setWho(u.name), setErr(null))} title={u.full}>
                    <Avatar user={u} size={34} />
                  </button>
                ))}
            </div>
          )}
        </form>
      )}
      <div className="tg-login-foot">
        <span>
          <HardDrive size={13} /> {location.hostname || "tango.local"}
          {!real && <> · <DemoBadge /></>}
        </span>
        <span className="tg-login-power">
          {!locked && <LangSwitch />}
          <button type="button" disabled aria-label={t("Reiniciar")}>
            <RotateCcw size={15} />
          </button>
          <button type="button" disabled aria-label={t("Apagar")}>
            <Power size={15} />
          </button>
        </span>
      </div>
    </div>
  );
}

const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** Cambio de idioma antes de entrar: muestra el otro idioma (la página se vuelve a cargar). */
function LangSwitch() {
  const other = LANGS.find((l) => l.id !== getLang()) ?? LANGS[0];
  return (
    <button type="button" className="tg-login-lang" lang={other.id} onClick={() => setLang(other.id)} title={t("Idioma")}>
      <Globe size={13} />
      {other.name}
    </button>
  );
}

/**
 * Código de verificación: 6 casillas que avanzan solas (también acepta pegar el código
 * entero) y envían al completar. "Usar un código de recuperación" cambia a texto libre.
 */
function TotpEntry({ busy, onCode }: { busy: boolean; onCode: (code: string) => void }) {
  const [digits, setDigits] = useState<string[]>(["", "", "", "", "", ""]);
  const [recovery, setRecovery] = useState(false);
  const [rc, setRc] = useState("");
  const refs = useRef<(HTMLInputElement | null)[]>([]);

  useEffect(() => {
    if (!busy && !recovery) {
      setDigits(["", "", "", "", "", ""]);
      refs.current[0]?.focus();
    }
  }, [busy, recovery]);

  const set = (i: number, raw: string) => {
    const only = raw.replace(/\D/g, "");
    if (!only) {
      setDigits((d) => d.map((x, j) => (j === i ? "" : x)));
      return;
    }
    const next = [...digits];
    for (let k = 0; k < only.length && i + k < 6; k++) next[i + k] = only[k];
    setDigits(next);
    const filled = next.findIndex((x) => !x);
    if (filled === -1) onCode(next.join(""));
    else refs.current[Math.min(filled, 5)]?.focus();
  };

  if (recovery)
    return (
      <>
        <div className="tg-login-field">
          <input autoFocus placeholder="xxxxx-xxxxx" value={rc} onChange={(e) => setRc(e.target.value)} aria-label={t("Código de recuperación")} onKeyDown={(e) => e.key === "Enter" && rc.trim() && onCode(rc.trim())} />
          <button type="button" aria-label={t("Entrar")} disabled={busy || !rc.trim()} onClick={() => onCode(rc.trim())}>
            <ArrowRight size={14} strokeWidth={2.4} />
          </button>
        </div>
        <button type="button" className="tg-login-back" onClick={() => setRecovery(false)}>
          {t("Usar la app de autenticación")}
        </button>
      </>
    );
  return (
    <>
      <div className="tg-otp" role="group" aria-label={t("Código de verificación")}>
        {digits.map((d, i) => (
          <input
            key={i}
            ref={(el) => {
              refs.current[i] = el;
            }}
            inputMode="numeric"
            autoComplete={i === 0 ? "one-time-code" : "off"}
            maxLength={6}
            value={d}
            disabled={busy}
            aria-label={t("Dígito {n}", { n: i + 1 })}
            onChange={(e) => set(i, e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Backspace" && !digits[i] && i > 0) refs.current[i - 1]?.focus();
              if (e.key === "ArrowLeft" && i > 0) refs.current[i - 1]?.focus();
              if (e.key === "ArrowRight" && i < 5) refs.current[i + 1]?.focus();
            }}
            onPaste={(e) => {
              e.preventDefault();
              set(i, e.clipboardData.getData("text"));
            }}
          />
        ))}
      </div>
      <button type="button" className="tg-login-back" onClick={() => setRecovery(true)}>
        {t("Usar un código de recuperación")}
      </button>
    </>
  );
}
