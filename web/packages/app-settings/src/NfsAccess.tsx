import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { RpcError, useLive, useTransport } from "@tango/client";
import type { NfsClient, Share } from "@tango/client";
import { toast } from "@tango/shell";
import { Button, Segmented, Sheet, TextInput } from "@tango/ui";

/** Host, IP, red CIDR o comodín de NFS (`*.casa.lan`). */
export function validNfsHost(h: string): string | null {
  const v = h.trim();
  if (!v) return "Escribí un host, una IP o una red.";
  if (v === "*") return null;
  if (/^(\d{1,3}\.){3}\d{1,3}(\/(\d|[12]\d|3[0-2]))?$/.test(v)) return v.split("/")[0].split(".").every((n) => Number(n) <= 255) ? null : "IP inválida.";
  if (/^[0-9a-f:]+(\/\d{1,3})?$/i.test(v) && v.includes(":")) return null; // IPv6
  if (/^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$/i.test(v)) return null;
  return "Usá una IP (192.168.1.20), una red (192.168.1.0/24) o un nombre (pc.casa.lan).";
}

export function NfsAccess({ share, nfsEnabled, onClose }: { share: Share; nfsEnabled: boolean; onClose: () => void }) {
  const t = useTransport();
  const { data } = useLive("nfs.exports.get", { share: share.id });
  const [rows, setRows] = useState<NfsClient[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (data && !rows) setRows(data.clients.map((c) => ({ ...c })));
  }, [data, rows]);

  const errors = (rows ?? []).map((r) => validNfsHost(r.host));
  const valid = !errors.some(Boolean);

  const save = async () => {
    if (!rows || !valid) return;
    setBusy(true);
    setErr(null);
    try {
      await t.call("nfs.exports.set", { share: share.id, clients: rows.map((r) => ({ host: r.host.trim(), access: r.access })) });
      toast({ level: "ok", title: `Acceso NFS de «${share.name}» guardado`, app: "tango.settings" });
      onClose();
    } catch (e) {
      setErr(e instanceof RpcError ? e.message : String(e));
      setBusy(false);
    }
  };

  return (
    <Sheet
      open
      onClose={onClose}
      width={520}
      footer={
        <>
          <Button onClick={onClose}>Cancelar</Button>
          <Button variant="primary" onClick={() => void save()} disabled={!rows || !valid || busy}>
            {busy ? "Guardando…" : "Guardar"}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">Acceso NFS a «{share.name}»</h2>
      <p className="tg-lead" style={{ marginTop: 6 }}>
        NFS no usa contraseñas: el acceso se da por equipo o por red. Sin entradas, la carpeta no se publica por NFS.
        {!nfsEnabled && " El servicio NFS está desactivado en Servicios de archivos."}
      </p>
      <div className="tset-nfs">
        {(rows ?? []).map((r, i) => (
          <div key={i} className="tset-nfs-row">
            <div>
              <TextInput
                value={r.host}
                autoFocus={i === rows!.length - 1 && !r.host}
                placeholder="192.168.1.0/24"
                onChange={(e) => setRows((x) => x!.map((y, j) => (j === i ? { ...y, host: e.target.value } : y)))}
                aria-invalid={!!errors[i] && !!r.host}
                aria-label="Equipo o red"
              />
              {errors[i] && r.host && <span className="tset-err">{errors[i]}</span>}
            </div>
            <Segmented
              label="Acceso"
              value={r.access}
              onChange={(v) => setRows((x) => x!.map((y, j) => (j === i ? { ...y, access: v } : y)))}
              options={[
                { value: "rw", label: "Lectura y escritura" },
                { value: "ro", label: "Solo lectura" },
              ]}
            />
            <Button variant="plain" aria-label="Quitar" onClick={() => setRows((x) => x!.filter((_, j) => j !== i))}>
              <Trash2 size={14} color="var(--tg-crit)" />
            </Button>
          </div>
        ))}
        <Button onClick={() => setRows((x) => [...(x ?? []), { host: "", access: "ro" }])}>
          <Plus size={14} /> Agregar equipo o red
        </Button>
      </div>
      {err && <div className="tset-err">{err}</div>}
      <p className="tset-foot">
        Montar desde Linux: <span className="tset-mono">sudo mount -t nfs4 tango.local:/{share.name} /mnt/{share.name.toLowerCase()}</span>
      </p>
    </Sheet>
  );
}
