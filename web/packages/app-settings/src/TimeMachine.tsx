import { useEffect, useState } from "react";
import { Clock3, Laptop } from "lucide-react";
import { RpcError, t, tr, useTransport } from "@tango/client";
import type { Share } from "@tango/client";
import { toast } from "@tango/shell";
import { Button, Row, Sheet, Status, TextInput, Toggle } from "@tango/ui";
import { tj } from "./common";

// Time Machine por SMB (protocol/README.md §13): una carpeta compartida puede ser destino
// de los respaldos de las Mac; TangoOS la anuncia por Bonjour (_adisk._tcp).

const GB = 1e9;
export const TM_MIN_GB = 54; // 50 GiB ≈ 54 GB: el mínimo que acepta el NAS

/** Pasa "500" (GB) a bytes; "" o 0 = sin tope; NaN si no es un número. */
export function parseMaxGB(v: string): number | null {
  const s = v.trim().replace(",", ".");
  if (!s || s === "0") return null;
  const n = Number(s);
  return Number.isFinite(n) && n > 0 ? Math.round(n * GB) : NaN;
}

export function maxGBError(v: string): string | null {
  const b = parseMaxGB(v);
  if (b === null) return null;
  if (Number.isNaN(b)) return t("Escribí un número de GB (o dejalo vacío para no poner tope).");
  if (b < TM_MIN_GB * GB) return t("El tope tiene que ser de al menos {n} GB.", { n: TM_MIN_GB });
  return null;
}

/** Hoja con los pasos para usar la carpeta desde macOS. */
export function TimeMachineHelp({ open, onClose, shareName = "TimeMachine" }: { open: boolean; onClose: () => void; shareName?: string }) {
  const host = location.hostname || "tango.local";
  return (
    <Sheet open={open} onClose={onClose} width={540} footer={<Button variant="primary" onClick={onClose}>{t("Entendido")}</Button>}>
      <h2 className="tg-h2">
        <Clock3 size={18} style={{ verticalAlign: -3 }} /> {t("Respaldar una Mac con Time Machine")}
      </h2>
      <ol className="tset-steps">
        <li>
          {tj("En la Mac abrí {settings} y tocá {add} (en versiones anteriores: «Seleccionar disco»).", { settings: <b>{t("Ajustes del Sistema → General → Time Machine")}</b>, add: <b>{t("Agregar disco de respaldo…")}</b> })}
        </li>
        <li>
          {tj("Elegí {share} de la lista (TangoOS lo anuncia en la red; aparece con el ícono de Time Capsule).", { share: <b>{t("«{name}» en el NAS", { name: shareName })}</b> })}
        </li>
        <li>
          {tj("Si no aparece: en el Finder apretá {key}, escribí {url}, conectate y volvé al paso 1.", { key: <b>⌘K</b>, url: <code>smb://{host}/{shareName}</code> })}
        </li>
        <li>{t("Cuando te pida usuario y contraseña, usá tu usuario de TangoOS. Podés activar «Cifrar los respaldos».")}</li>
      </ol>
      <p className="tg-muted">
        <Laptop size={13} style={{ verticalAlign: -2 }} />{" "}
        {tj("Conviene que {own}: así cada respaldo queda aislado y podés darle permisos solo a quien corresponde. El primer respaldo puede tardar horas; los siguientes son incrementales.", { own: <b>{t("cada Mac use su propio usuario de TangoOS")}</b> })}
      </p>
    </Sheet>
  );
}

/** Activar/desactivar Time Machine en una carpeta existente (shares.update). */
export function TimeMachineSheet({ share, onClose }: { share: Share; onClose: () => void }) {
  const tp = useTransport();
  const [on, setOn] = useState(!!share.timeMachine);
  const [max, setMax] = useState(share.timeMachineMaxBytes ? String(Math.round(share.timeMachineMaxBytes / GB)) : "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [help, setHelp] = useState(false);
  useEffect(() => setErr(null), [on, max]);
  const maxErr = on ? maxGBError(max) : null;

  const save = async () => {
    if (maxErr) return;
    setBusy(true);
    try {
      await tp.call("shares.update", { share: share.id, smb: on ? true : undefined, timeMachine: on, timeMachineMaxBytes: on ? parseMaxGB(max) : null });
      toast({ level: "ok", title: on ? t("«{name}» es destino de Time Machine", { name: share.name }) : t("«{name}» dejó de ser destino de Time Machine", { name: share.name }), body: on ? t("Ya se anuncia en la red para las Mac.") : "", app: "tango.settings" });
      onClose();
    } catch (e) {
      setErr(e instanceof RpcError ? e.message : tr(String(e)));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Sheet
        open
        onClose={onClose}
        width={500}
        footer={
          <>
            <Button variant="plain" onClick={() => setHelp(true)} style={{ marginRight: "auto" }}>
              {t("¿Cómo lo uso desde la Mac?")}
            </Button>
            <Button onClick={onClose}>{t("Cancelar")}</Button>
            <Button variant="primary" disabled={busy || !!maxErr} onClick={save}>
              {t("Guardar")}
            </Button>
          </>
        }
      >
        <h2 className="tg-h2">{t("Time Machine en «{name}»", { name: share.name })}</h2>
        <p className="tg-lead">{t("Las Mac de la red pueden respaldarse en esta carpeta. Se publica por SMB y se anuncia por Bonjour.")}</p>
        <div className="tg-group">
          <Row label={t("Usar como destino de Time Machine")}>
            <Toggle checked={on} onChange={setOn} label={t("Usar como destino de Time Machine")} />
          </Row>
          {on && (
            <Row label={t("Tope de espacio")} hint={t("Time Machine borra los respaldos más viejos al llegar al tope. Vacío = sin tope.")}>
              <span className="tset-tm-max">
                <TextInput value={max} onChange={(e) => setMax(e.target.value)} inputMode="decimal" placeholder={t("Sin tope")} aria-label={t("Tope en GB")} style={{ width: 90 }} /> GB
              </span>
            </Row>
          )}
        </div>
        {(maxErr || err) && (
          <div style={{ marginTop: 8 }}>
            <Status tone="crit">{maxErr || err}</Status>
          </div>
        )}
      </Sheet>
      <TimeMachineHelp open={help} onClose={() => setHelp(false)} shareName={share.name} />
    </>
  );
}
