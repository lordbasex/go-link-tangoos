import { useEffect, useState } from "react";
import { Clock3, Laptop } from "lucide-react";
import { RpcError, useTransport } from "@tango/client";
import type { Share } from "@tango/client";
import { toast } from "@tango/shell";
import { Button, Row, Sheet, Status, TextInput, Toggle } from "@tango/ui";

// Time Machine por SMB (protocol/README.md §13): una carpeta compartida puede ser destino
// de los respaldos de las Mac; TangoOS la anuncia por Bonjour (_adisk._tcp).

const GB = 1e9;
export const TM_MIN_GB = 54; // 50 GiB ≈ 54 GB: el mínimo que acepta el NAS

/** Pasa "500" (GB) a bytes; "" o 0 = sin tope; NaN si no es un número. */
export function parseMaxGB(v: string): number | null {
  const t = v.trim().replace(",", ".");
  if (!t || t === "0") return null;
  const n = Number(t);
  return Number.isFinite(n) && n > 0 ? Math.round(n * GB) : NaN;
}

export function maxGBError(v: string): string | null {
  const b = parseMaxGB(v);
  if (b === null) return null;
  if (Number.isNaN(b)) return "Escribí un número de GB (o dejalo vacío para no poner tope).";
  if (b < TM_MIN_GB * GB) return `El tope tiene que ser de al menos ${TM_MIN_GB} GB.`;
  return null;
}

/** Hoja con los pasos para usar la carpeta desde macOS. */
export function TimeMachineHelp({ open, onClose, shareName = "TimeMachine" }: { open: boolean; onClose: () => void; shareName?: string }) {
  const host = location.hostname || "tango.local";
  return (
    <Sheet open={open} onClose={onClose} width={540} footer={<Button variant="primary" onClick={onClose}>Entendido</Button>}>
      <h2 className="tg-h2">
        <Clock3 size={18} style={{ verticalAlign: -3 }} /> Respaldar una Mac con Time Machine
      </h2>
      <ol className="tset-steps">
        <li>
          En la Mac abrí <b>Ajustes del Sistema → General → Time Machine</b> y tocá <b>Agregar disco de respaldo…</b> (en versiones anteriores: «Seleccionar disco»).
        </li>
        <li>
          Elegí <b>«{shareName}» en el NAS</b> de la lista (TangoOS lo anuncia en la red; aparece con el ícono de Time Capsule).
        </li>
        <li>
          Si no aparece: en el Finder apretá <b>⌘K</b>, escribí <code>smb://{host}/{shareName}</code>, conectate y volvé al paso 1.
        </li>
        <li>Cuando te pida usuario y contraseña, usá tu usuario de TangoOS. Podés activar «Cifrar los respaldos».</li>
      </ol>
      <p className="tg-muted">
        <Laptop size={13} style={{ verticalAlign: -2 }} /> Conviene que <b>cada Mac use su propio usuario de TangoOS</b>: así cada respaldo queda aislado y podés darle permisos solo a quien corresponde. El primer respaldo puede tardar horas; los siguientes son incrementales.
      </p>
    </Sheet>
  );
}

/** Activar/desactivar Time Machine en una carpeta existente (shares.update). */
export function TimeMachineSheet({ share, onClose }: { share: Share; onClose: () => void }) {
  const t = useTransport();
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
      await t.call("shares.update", { share: share.id, smb: on ? true : undefined, timeMachine: on, timeMachineMaxBytes: on ? parseMaxGB(max) : null });
      toast({ level: "ok", title: on ? `«${share.name}» es destino de Time Machine` : `«${share.name}» dejó de ser destino de Time Machine`, body: on ? "Ya se anuncia en la red para las Mac." : "", app: "tango.settings" });
      onClose();
    } catch (e) {
      setErr(e instanceof RpcError ? e.message : String(e));
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
              ¿Cómo lo uso desde la Mac?
            </Button>
            <Button onClick={onClose}>Cancelar</Button>
            <Button variant="primary" disabled={busy || !!maxErr} onClick={save}>
              Guardar
            </Button>
          </>
        }
      >
        <h2 className="tg-h2">Time Machine en «{share.name}»</h2>
        <p className="tg-lead">Las Mac de la red pueden respaldarse en esta carpeta. Se publica por SMB y se anuncia por Bonjour.</p>
        <div className="tg-group">
          <Row label="Usar como destino de Time Machine">
            <Toggle checked={on} onChange={setOn} label="Usar como destino de Time Machine" />
          </Row>
          {on && (
            <Row label="Tope de espacio" hint="Time Machine borra los respaldos más viejos al llegar al tope. Vacío = sin tope.">
              <span className="tset-tm-max">
                <TextInput value={max} onChange={(e) => setMax(e.target.value)} inputMode="decimal" placeholder="Sin tope" aria-label="Tope en GB" style={{ width: 90 }} /> GB
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
