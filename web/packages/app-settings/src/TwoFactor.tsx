import { useState } from "react";
import { Copy, Download, KeyRound, ShieldCheck } from "lucide-react";
import { RpcError, useIsReal, useLive, useTransport } from "@tango/client";
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
  const t = useTransport();
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
      setBegin(await t.call("users.totp.begin", undefined));
      setSheet("enable");
    } catch (e) {
      toast({ level: "crit", title: "No se pudo empezar", body: (e as Error).message, app: "tango.settings" });
    } finally {
      setBusy(false);
    }
  };

  const confirm = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await t.call("users.totp.confirm", { code: code.replace(/\s+/g, "") });
      setCodes(r.recoveryCodes);
      setSheet("codes");
      users.reload();
    } catch (e) {
      setErr(e instanceof RpcError && e.code === "INVALID_CODE" ? "El código no coincide. Revisá que la hora del teléfono sea la correcta." : (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const disable = async () => {
    setBusy(true);
    setErr(null);
    try {
      await t.call("users.totp.disable", { password: pw });
      setSheet(null);
      setPw("");
      users.reload();
      toast({ level: "ok", title: "Verificación en dos pasos desactivada", body: "Desde ahora alcanza con la contraseña para entrar.", app: "tango.settings" });
    } catch {
      setErr("La contraseña no es correcta.");
    } finally {
      setBusy(false);
    }
  };

  const codesText = `TangoOS — códigos de recuperación de ${me?.name ?? ""}\nCada código sirve una sola vez si perdés el teléfono.\n\n${codes.join("\n")}\n`;

  return (
    <>
      <Row
        label={
          <span className="tset-uname">
            Verificación en dos pasos {!real && <DemoBadge />}
          </span>
        }
        hint={enabled ? "Al entrar se pide un código de tu app de autenticación." : "Además de la contraseña, un código de 6 dígitos de una app como Aegis, 2FAS o Google Authenticator."}
      >
        {enabled ? (
          <>
            <Status tone="ok">Activa</Status>
            <Button onClick={() => (setErr(null), setPw(""), setSheet("disable"))}>Desactivar…</Button>
          </>
        ) : (
          <Button variant="primary" disabled={busy || !me} onClick={start}>
            Activar…
          </Button>
        )}
      </Row>

      <Sheet
        open={sheet === "enable"}
        onClose={() => setSheet(null)}
        width={520}
        footer={
          <>
            <Button onClick={() => setSheet(null)}>Cancelar</Button>
            <Button variant="primary" disabled={busy || code.replace(/\D/g, "").length !== 6} onClick={confirm}>
              Activar
            </Button>
          </>
        }
      >
        <h2 className="tg-h2">Activar la verificación en dos pasos</h2>
        <p className="tg-lead">1. Escaneá el código con tu app de autenticación. 2. Escribí el código de 6 dígitos que muestra.</p>
        {begin && (
          <div className="tset-totp">
            <img src={begin.qr} alt="Código QR para la app de autenticación" width={184} height={184} />
            <div className="tset-totp-side">
              <div className="tg-muted">¿No podés escanear? Cargá esta clave a mano:</div>
              <code className="tset-totp-secret">{begin.secret.replace(/(.{4})/g, "$1 ").trim()}</code>
              <Button onClick={() => navigator.clipboard?.writeText(begin.secret).then(() => toast({ level: "ok", title: "Clave copiada", body: "", app: "tango.settings" }))}>
                <Copy size={13} /> Copiar la clave
              </Button>
              <label className="tset-totp-code">
                Código de 6 dígitos
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
            <Button onClick={() => saveText("tangoos-codigos-de-recuperacion.txt", codesText)}>
              <Download size={13} /> Descargar
            </Button>
            <Button variant="primary" onClick={() => setSheet(null)}>
              Ya los guardé
            </Button>
          </>
        }
      >
        <h2 className="tg-h2">
          <ShieldCheck size={18} style={{ verticalAlign: -3, color: "var(--tg-ok)" }} /> Listo: la verificación está activa
        </h2>
        <p className="tg-lead">Guardá estos códigos en un lugar seguro. Cada uno sirve una sola vez para entrar si perdés el teléfono. No se van a volver a mostrar.</p>
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
            <Button onClick={() => setSheet(null)}>Cancelar</Button>
            <Button variant="danger" disabled={busy || !pw} onClick={disable}>
              Desactivar
            </Button>
          </>
        }
      >
        <h2 className="tg-h2">
          <KeyRound size={17} style={{ verticalAlign: -3 }} /> Desactivar la verificación en dos pasos
        </h2>
        <p className="tg-lead">Para confirmar que sos vos, escribí tu contraseña.</p>
        <TextInput size="lg" type="password" autoFocus value={pw} placeholder="Contraseña" onChange={(e) => setPw(e.target.value)} onKeyDown={(e) => e.key === "Enter" && pw && disable()} />
        {err && (
          <div style={{ marginTop: 8 }}>
            <Status tone="crit">{err}</Status>
          </div>
        )}
      </Sheet>
    </>
  );
}
