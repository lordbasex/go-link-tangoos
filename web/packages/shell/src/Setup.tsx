// Asistente de primer arranque (protocol §12, docs/08): al estilo del Asistente de Configuración
// de macOS. Corre sin sesión mientras setup.state == "pending" y termina con el usuario adentro.
import { useEffect, useMemo, useRef, useState } from "react";
import type { ComponentType, ReactNode } from "react";
import { Globe, KeyRound, Lock, ServerCog, ShieldCheck, UserRound } from "lucide-react";
import { LANGS, RpcError, getLang, locale, passwordStrength, setLang, setToken, t, tr, useTransport, validHostname } from "@tango/client";
import type { SetupParams } from "@tango/client";
import { openApp, toast, ui } from "./store";

type Step = "welcome" | "name" | "admin" | "timezone" | "cert" | "summary";
const STEPS: Step[] = ["welcome", "name", "admin", "timezone", "cert", "summary"];

const FALLBACK_TZ = ["America/Argentina/Buenos_Aires", "America/Argentina/Cordoba", "America/Argentina/Mendoza", "America/Montevideo", "America/Santiago", "America/Sao_Paulo", "America/Bogota", "America/Lima", "America/Mexico_City", "America/New_York", "America/Los_Angeles", "Europe/Madrid", "Europe/London", "Europe/Berlin", "UTC"];

function timezones(): string[] {
  try {
    const f = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf;
    const all = f?.("timeZone");
    if (all?.length) return all.includes("UTC") ? all : [...all, "UTC"];
  } catch {
    /* navegador viejo */
  }
  return FALLBACK_TZ;
}

function tzLabel(tz: string) {
  const city = tz.split("/").pop()!.replace(/_/g, " ");
  try {
    const off = new Intl.DateTimeFormat(locale(), { timeZone: tz, timeZoneName: "shortOffset" }).formatToParts(new Date()).find((p) => p.type === "timeZoneName")?.value;
    return { city, region: tz.split("/").slice(0, -1).join(" / ").replace(/_/g, " "), off: off ?? "" };
  } catch {
    return { city, region: "", off: "" };
  }
}

const userFrom = (full: string) =>
  full
    .normalize("NFD")
    .replace(/\p{Diacritic}/gu, "")
    .toLowerCase()
    .split(/\s+/)[0]
    ?.replace(/[^a-z0-9_-]/g, "")
    .slice(0, 32) ?? "";

export function SetupAssistant({ logo: Logo, suggestedHostname }: { logo: ComponentType<{ size?: number; color?: string }>; suggestedHostname: string }) {
  const tp = useTransport();
  const [step, setStep] = useState<Step>("welcome");
  const [hostname, setHostname] = useState(suggestedHostname || "tango");
  const [full, setFull] = useState("");
  const [name, setName] = useState("");
  const [nameTouched, setNameTouched] = useState(false);
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const [tz, setTz] = useState(() => {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone || "America/Argentina/Buenos_Aires";
    } catch {
      return "America/Argentina/Buenos_Aires";
    }
  });
  const [tzQ, setTzQ] = useState("");
  const [cert, setCert] = useState<SetupParams["certificate"]>("localca");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const card = useRef<HTMLDivElement>(null);

  const effectiveName = nameTouched ? name : userFrom(full);
  const strength = passwordStrength(pw, [effectiveName, full, hostname]);
  const hostErr = validHostname(hostname);
  const nameErr = !effectiveName ? t("Elegí un nombre de usuario.") : !/^[a-z][a-z0-9_-]{1,31}$/.test(effectiveName) ? t("Empezá con una letra; solo minúsculas, números, guion y guion bajo.") : ["root", "tango", "admin"].includes(effectiveName) ? t("Ese nombre está reservado.") : null;
  const pwErr = pw.length > 0 && strength.score < 2 ? t("La contraseña es demasiado débil.") : pw2 && pw !== pw2 ? t("Las contraseñas no coinciden.") : null;

  const valid: Record<Step, boolean> = {
    welcome: true,
    name: !hostErr,
    admin: !!full.trim() && !nameErr && strength.score >= 2 && pw === pw2,
    timezone: !!tz,
    cert: true,
    summary: true,
  };

  const idx = STEPS.indexOf(step);
  const go = (d: 1 | -1) => {
    setErr(null);
    const next = STEPS[idx + d];
    if (!next) return;
    const el = card.current?.querySelector(".tg-setup-body");
    el?.animate([{ opacity: 0, transform: `translateX(${d * 24}px)` }, { opacity: 1, transform: "none" }], { duration: 220, easing: "cubic-bezier(.2,.8,.2,1)" });
    setStep(next);
  };

  const finish = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await tp.call("setup.complete", { hostname, admin: { name: effectiveName, full: full.trim(), password: pw }, timezone: tz, certificate: cert });
      setToken(r.token || null);
      document.querySelector(".tg-setup")?.animate([{ opacity: 1 }, { opacity: 0, transform: "scale(1.03)" }], { duration: 320, easing: "ease-in", fill: "forwards" });
      setTimeout(() => {
        ui.set((s) => ({ ...s, session: "desktop", user: { name: r.user.name, full: r.user.full, color: r.user.color } }));
        setTimeout(
          () =>
            toast({
              level: "info",
              title: t("Tu NAS está listo"),
              body: t("El siguiente paso es crear tu primer volumen con los discos."),
              app: "tango.storage",
              action: { label: t("Crear volumen"), run: () => openApp("tango.storage", { action: "create" }) },
            }),
          700,
        );
      }, 300);
    } catch (e) {
      setBusy(false);
      setErr(e instanceof RpcError ? e.message : t("No se pudo completar la configuración. Revisá la conexión con el NAS."));
      card.current?.animate([{ transform: "translateX(0)" }, { transform: "translateX(-8px)" }, { transform: "translateX(8px)" }, { transform: "translateX(0)" }], { duration: 280 });
    }
  };

  const allTz = useMemo(timezones, []);
  const tzList = useMemo(() => {
    const q = tzQ
      .normalize("NFD")
      .replace(/\p{Diacritic}/gu, "")
      .toLowerCase()
      .replace(/\s+/g, "_");
    return (q ? allTz.filter((z) => z.toLowerCase().includes(q)) : allTz).slice(0, 80);
  }, [allTz, tzQ]);

  // La zona elegida siempre a la vista al entrar al paso.
  useEffect(() => {
    if (step === "timezone") card.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "center" });
  }, [step]);

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key !== "Enter" || (e.target as HTMLElement).tagName === "BUTTON") return;
    if (step === "summary") void finish();
    else if (valid[step]) go(1);
  };

  return (
    <div className="tg-setup" onKeyDown={onKey}>
      <div className="tg-setup-card" ref={card} role="dialog" aria-label={t("Configuración inicial")}>
        <div className="tg-setup-body">
          {step === "welcome" && (
            <div className="tg-setup-welcome">
              <div className="tg-setup-logo">
                <Logo size={56} color="#fff" />
              </div>
              <h1>{t("Configuremos tu NAS")}</h1>
              <p>{t("Son cinco pasos: un nombre para el equipo, tu cuenta de administrador, la zona horaria y cómo se protege la conexión. Los discos no se tocan.")}</p>
              <div className="tg-setup-lang" role="group" aria-label={t("Idioma")}>
                <Globe size={13} />
                {LANGS.map((l) => (
                  <button key={l.id} type="button" lang={l.id} aria-pressed={l.id === getLang()} onClick={() => l.id !== getLang() && setLang(l.id)}>
                    {l.name}
                  </button>
                ))}
              </div>
            </div>
          )}
          {step === "name" && (
            <Pane icon={ServerCog} title={t("Nombre del NAS")} lead={t("Así lo vas a encontrar en la red y en el Finder o el Explorador.")}>
              <label className="tg-setup-field">
                <span>{t("Nombre")}</span>
                <input autoFocus value={hostname} onChange={(e) => setHostname(e.target.value.toLowerCase())} spellCheck={false} aria-invalid={!!hostErr} />
              </label>
              <div className="tg-setup-preview">
                {hostErr ? (
                  <span className="tg-setup-err">{tr(hostErr)}</span>
                ) : (
                  <>
                    {t("Vas a entrar desde")} <b>https://{hostname}.local</b>
                  </>
                )}
              </div>
            </Pane>
          )}
          {step === "admin" && (
            <Pane icon={UserRound} title={t("Cuenta de administrador")} lead={t("Con esta cuenta administrás el NAS. Después podés crear cuentas para tu familia o tu equipo.")}>
              <label className="tg-setup-field">
                <span>{t("Nombre completo")}</span>
                <input autoFocus value={full} onChange={(e) => setFull(e.target.value)} placeholder="Federico Pereira" autoComplete="name" />
              </label>
              <label className="tg-setup-field">
                <span>{t("Usuario")}</span>
                <input
                  value={effectiveName}
                  onChange={(e) => {
                    setNameTouched(true);
                    setName(e.target.value.toLowerCase());
                  }}
                  spellCheck={false}
                  autoComplete="username"
                  aria-invalid={!!(full && nameErr)}
                />
              </label>
              {full && nameErr && <div className="tg-setup-err">{nameErr}</div>}
              <label className="tg-setup-field">
                <span>{t("Contraseña")}</span>
                <input type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="new-password" />
              </label>
              <div className="tg-setup-strength" data-score={pw ? strength.score : undefined} aria-live="polite">
                <div className="tg-setup-meter">
                  {[0, 1, 2, 3].map((i) => (
                    <i key={i} data-on={pw && strength.score > i ? "" : undefined} />
                  ))}
                </div>
                <span>{pw ? `${tr(strength.label)}. ${tr(strength.hint)}` : t("Usá al menos 10 caracteres. Una frase de varias palabras funciona bien.")}</span>
              </div>
              <label className="tg-setup-field">
                <span>{t("Repetir contraseña")}</span>
                <input type="password" value={pw2} onChange={(e) => setPw2(e.target.value)} autoComplete="new-password" />
              </label>
              {pwErr && <div className="tg-setup-err">{pwErr}</div>}
            </Pane>
          )}
          {step === "timezone" && (
            <Pane icon={Globe} title={t("Zona horaria")} lead={t("Para que las instantáneas programadas y los registros tengan la hora correcta.")}>
              <label className="tg-setup-search">
                <input autoFocus placeholder={t("Buscar ciudad")} value={tzQ} onChange={(e) => setTzQ(e.target.value)} aria-label={t("Buscar ciudad")} />
              </label>
              <div className="tg-setup-tzlist" role="listbox" aria-label={t("Zonas horarias")}>
                {tzList.map((z) => {
                  const l = tzLabel(z);
                  return (
                    <div key={z} role="option" aria-selected={z === tz} className="tg-setup-tz" onClick={() => setTz(z)}>
                      <b>{l.city}</b>
                      <span>{l.region}</span>
                      <em>{l.off}</em>
                    </div>
                  );
                })}
                {tzList.length === 0 && <div className="tg-setup-none">{t("Ninguna zona coincide con «{q}».", { q: tzQ })}</div>}
              </div>
            </Pane>
          )}
          {step === "cert" && (
            <Pane icon={ShieldCheck} title={t("Conexión segura")} lead={t("El escritorio siempre viaja cifrado. Lo que cambia es si tu navegador confía en el NAS sin advertencias.")}>
              <div className="tg-setup-choices" role="radiogroup">
                <Choice checked={cert === "localca"} onSelect={() => setCert("localca")} icon={KeyRound} title={t("CA local de TangoOS (recomendado)")}>
                  {t("El NAS crea su propia autoridad certificante. Instalás el certificado raíz una vez en cada equipo y desaparecen las advertencias. Habilita la vista previa de videos y las descargas grandes.")}
                </Choice>
                <Choice checked={cert === "selfsigned"} onSelect={() => setCert("selfsigned")} icon={Lock} title={t("Autofirmado")}>
                  {t("No hay que instalar nada, pero el navegador muestra una advertencia la primera vez y algunas funciones quedan limitadas. Podés cambiarlo después en Ajustes → Seguridad.")}
                </Choice>
              </div>
            </Pane>
          )}
          {step === "summary" && (
            <Pane icon={ServerCog} title={t("Todo listo")} lead={t("Revisá y confirmá. Podés cambiar cualquiera de estas opciones después en Ajustes.")}>
              <dl className="tg-setup-summary">
                <dt>{t("Nombre del NAS")}</dt>
                <dd>{hostname}.local</dd>
                <dt>{t("Administrador")}</dt>
                <dd>
                  {full} ({effectiveName})
                </dd>
                <dt>{t("Zona horaria")}</dt>
                <dd>{tzLabel(tz).city}</dd>
                <dt>{t("Certificado")}</dt>
                <dd>{cert === "localca" ? t("CA local de TangoOS") : t("Autofirmado")}</dd>
              </dl>
              {err && <div className="tg-setup-err" role="alert">{err}</div>}
            </Pane>
          )}
        </div>
        <div className="tg-setup-foot">
          <div className="tg-setup-dots" aria-hidden="true">
            {STEPS.map((s, i) => (
              <i key={s} data-on={i <= idx ? "" : undefined} />
            ))}
          </div>
          <div className="tg-setup-actions">
            {idx > 0 && (
              <button type="button" className="tg-btn" onClick={() => go(-1)} disabled={busy}>
                {t("Atrás")}
              </button>
            )}
            {step === "summary" ? (
              <button type="button" className="tg-btn" data-variant="primary" onClick={() => void finish()} disabled={busy}>
                {busy ? t("Configurando…") : t("Empezar a usar TangoOS")}
              </button>
            ) : (
              <button type="button" className="tg-btn" data-variant="primary" onClick={() => go(1)} disabled={!valid[step]}>
                {step === "welcome" ? t("Empezar") : t("Continuar")}
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function Pane({ icon: Icon, title, lead, children }: { icon: ComponentType<{ size?: number; strokeWidth?: number }>; title: string; lead: string; children: ReactNode }) {
  return (
    <div className="tg-setup-pane">
      <div className="tg-setup-icon">
        <Icon size={30} strokeWidth={1.6} />
      </div>
      <h2>{title}</h2>
      <p className="tg-setup-lead">{lead}</p>
      <div className="tg-setup-content">{children}</div>
    </div>
  );
}

function Choice({ checked, onSelect, icon: Icon, title, children }: { checked: boolean; onSelect: () => void; icon: ComponentType<{ size?: number }>; title: string; children: ReactNode }) {
  return (
    <button type="button" role="radio" aria-checked={checked} className="tg-setup-choice" onClick={onSelect}>
      <Icon size={20} />
      <span>
        <b>{title}</b>
        <small>{children}</small>
      </span>
    </button>
  );
}
