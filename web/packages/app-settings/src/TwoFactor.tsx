import { useState } from "react";
import { Copy, Download, KeyRound, ShieldCheck } from "lucide-react";
import { RpcError, t, tr, useIsReal, useLive, useTransport } from "@tango/client";
import type { TotpBegin } from "@tango/client";
import { toast, ui } from "@tango/shell";
import { Button, DemoBadge, Row, Sheet, Status, TextInput } from "@tango/ui";

// Verificación en dos pasos (protocol/README.md §13): alta con QR + confirmación, códigos de
// recuperación (se muestran una sola vez) y baja con la contraseña.

function saveText(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

export function TwoFactor() {
  const tp = useTransport();
  const me = ui.use((s) => s.user);
  const users = useLive("users.list", undefined);
  const real = useIsReal("users.totp.begin");
  const enabled = !!users.data?.find((u) => u.name === me?.name)?.twofa;
  const [sheet, setSheet] = useState<null | "enable" | "codes" | "disable">(null);
  const [begin, setBegin] = useState<TotpBegin | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[]>([]);
  const [pw, setPw] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const start = async () => {
    setErr(null);
    setCode("");
    setBusy(true);
    try {
      setBegin(await tp.call("users.totp.begin", undefined));
      setSheet("enable");
    } catch (e) {
      toast({ level: "crit", title: t("No se pudo empezar"), body: tr((e as Error).message), app: "tango.settings" });
    } finally {
      setBusy(false);
    }
  };

  const confirm = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await tp.call("users.totp.confirm", { code: code.replace(/\s+/g, "") });
      setCodes(r.recoveryCodes);
      setSheet("codes");
      users.reload();
    } catch (e) {
      setErr(e instanceof RpcError && e.code === "INVALID_CODE" ? t("El código no coincide. Revisá que la hora del teléfono sea la correcta.") : tr((e as Error).message));
    } finally {
      setBusy(false);
    }
  };

  const disable = async () => {
    setBusy(true);
    setErr(null);
    try {
      await tp.call("users.totp.disable", { password: pw });
      setSheet(null);
      setPw("");
      users.reload();
      toast({ level: "ok", title: t("Verificación en dos pasos desactivada"), body: t("Desde ahora alcanza con la contraseña para entrar."), app: "tango.settings" });
    } catch {
      setErr(t("La contraseña no es correcta."));
    } finally {
      setBusy(false);
    }
  };

  const codesText = `${t("TangoOS — códigos de recuperación de {name}", { name: me?.name ?? "" })}\n${t("Cada código sirve una sola vez si perdés el teléfono.")}\n\n${codes.join("\n")}\n`;

  return (
    <>
      <Row
        label={
          <span className="tset-uname">
            {t("Verificación en dos pasos")} {!real && <DemoBadge />}
          </span>
        }
        hint={enabled ? t("Al entrar se pide un código de tu app de autenticación.") : t("Además de la contraseña, un código de 6 dígitos de una app como Aegis, 2FAS o Google Authenticator.")}
      >
        {enabled ? (
          <>
            <Status tone="ok">{t("Activa")}</Status>
            <Button onClick={() => (setErr(null), setPw(""), setSheet("disable"))}>{t("Desactivar…")}</Button>
          </>
        ) : (
          <Button variant="primary" disabled={busy || !me} onClick={start}>
            {t("Activar…")}
          </Button>
        )}
      </Row>

      <Sheet
        open={sheet === "enable"}
        onClose={() => setSheet(null)}
        width={520}
        footer={
          <>
            <Button onClick={() => setSheet(null)}>{t("Cancelar")}</Button>
            <Button variant="primary" disabled={busy || code.replace(/\D/g, "").length !== 6} onClick={confirm}>
              {t("Activar")}
            </Button>
          </>
        }
      >
        <h2 className="tg-h2">{t("Activar la verificación en dos pasos")}</h2>
        <p className="tg-lead">{t("1. Escaneá el código con tu app de autenticación. 2. Escribí el código de 6 dígitos que muestra.")}</p>
        {begin && (
          <div className="tset-totp">
            <img src={begin.qr} alt={t("Código QR para la app de autenticación")} width={184} height={184} />
            <div className="tset-totp-side">
              <div className="tg-muted">{t("¿No podés escanear? Cargá esta clave a mano:")}</div>
              <code className="tset-totp-secret">{begin.secret.replace(/(.{4})/g, "$1 ").trim()}</code>
              <Button onClick={() => navigator.clipboard?.writeText(begin.secret).then(() => toast({ level: "ok", title: t("Clave copiada"), body: "", app: "tango.settings" }))}>
                <Copy size={13} /> {t("Copiar la clave")}
              </Button>
              <label className="tset-totp-code">
                {t("Código de 6 dígitos")}
                <TextInput size="lg" inputMode="numeric" autoComplete="one-time-code" placeholder="123 456" value={code} maxLength={7} onChange={(e) => setCode(e.target.value)} onKeyDown={(e) => e.key === "Enter" && code.replace(/\D/g, "").length === 6 && confirm()} autoFocus />
              </label>
              {err && <Status tone="crit">{err}</Status>}
            </div>
          </div>
        )}
      </Sheet>

      <Sheet
        open={sheet === "codes"}
        onClose={() => setSheet(null)}
        width={480}
        footer={
          <>
            <Button onClick={() => saveText(t("tangoos-codigos-de-recuperacion.txt"), codesText)}>
              <Download size={13} /> {t("Descargar")}
            </Button>
            <Button variant="primary" onClick={() => setSheet(null)}>
              {t("Ya los guardé")}
            </Button>
          </>
        }
      >
        <h2 className="tg-h2">
          <ShieldCheck size={18} style={{ verticalAlign: -3, color: "var(--tg-ok)" }} /> {t("Listo: la verificación está activa")}
        </h2>
        <p className="tg-lead">{t("Guardá estos códigos en un lugar seguro. Cada uno sirve una sola vez para entrar si perdés el teléfono. No se van a volver a mostrar.")}</p>
        <ol className="tset-codes">
          {codes.map((c) => (
            <li key={c}>
              <code>{c}</code>
            </li>
          ))}
        </ol>
      </Sheet>

      <Sheet
        open={sheet === "disable"}
        onClose={() => setSheet(null)}
        width={440}
        footer={
          <>
            <Button onClick={() => setSheet(null)}>{t("Cancelar")}</Button>
            <Button variant="danger" disabled={busy || !pw} onClick={disable}>
              {t("Desactivar")}
            </Button>
          </>
        }
      >
        <h2 className="tg-h2">
          <KeyRound size={17} style={{ verticalAlign: -3 }} /> {t("Desactivar la verificación en dos pasos")}
        </h2>
        <p className="tg-lead">{t("Para confirmar que sos vos, escribí tu contraseña.")}</p>
        <TextInput size="lg" type="password" autoFocus value={pw} placeholder={t("Contraseña")} onChange={(e) => setPw(e.target.value)} onKeyDown={(e) => e.key === "Enter" && pw && disable()} />
        {err && (
          <div style={{ marginTop: 8 }}>
            <Status tone="crit">{err}</Status>
          </div>
        )}
      </Sheet>
    </>
  );
}
