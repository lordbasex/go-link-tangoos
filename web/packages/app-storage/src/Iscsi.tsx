// iSCSI (protocol §12): destinos LIO con un LUN sobre un archivo en un volumen, un volumen
// lógico de un grupo de almacenamiento o un volumen de bloque ZFS.
import { useMemo, useState } from "react";
import { Copy, HardDrive, Network, Plus, Trash2 } from "lucide-react";
import { RpcError, fmtBytes, useIsReal, useLive, useTopic, useTransport } from "@tango/client";
import { useAvailable } from "./util";
import type { IscsiTarget, Job, Pool, Volume, ZfsZvol } from "@tango/client";
import { openApp, toast } from "@tango/shell";
import { Button, DemoBadge, EmptyState, Progress, Segmented, Sheet, Status, TextInput, Toggle } from "@tango/ui";

const GB = 1e9;
const NAME_RE = /^[a-z0-9][a-z0-9-]{0,30}$/;

function copy(text: string) {
  void navigator.clipboard?.writeText(text).then(
    () => toast({ level: "ok", title: "Copiado", body: text, app: "tango.storage" }),
    () => toast({ level: "warn", title: "No se pudo copiar", body: "Seleccioná el texto y copialo a mano.", app: "tango.storage" }),
  );
}

export function Iscsi({ vols }: { vols: Volume[] }) {
  const real = useIsReal("iscsi.targets.list");
  const { data: targets } = useLive("iscsi.targets.list", undefined, ["services.changed"]);
  const svc = useLive("services.list", undefined, ["services.changed"]).data?.find((s) => s.id === "iscsi");
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState<IscsiTarget | null>(null);
  const usable = vols.filter((v) => (v.state === "healthy" || v.state === "scrubbing") && v.mount && (v.fs === "btrfs" || v.fs === "zfs"));
  const poolsOn = useAvailable("storage.pools.list");
  const pools = (useLive("storage.pools.list", undefined, ["storage.changed"]).data ?? []).filter((p) => poolsOn && p.state !== "building");
  const zfsOn = useAvailable("storage.zfs.status");
  const zst = useLive("storage.zfs.status", undefined, ["storage.changed"]).data;
  const used = new Set((targets ?? []).map((x) => x.zvol).filter(Boolean));
  const zvols = zfsOn && zst?.enabled ? zst.pools.flatMap((p) => p.zvols.map((z) => ({ ...z, pool: p.name }))).filter((z) => !used.has(z.device)) : [];
  const canCreate = usable.length > 0 || pools.length > 0 || zvols.length > 0;
  const where = (tg: IscsiTarget) => {
    if (tg.lv) return `un volumen lógico${tg.thin ? " thin" : ""} del grupo ${pools.find((p) => p.id === tg.volume)?.name ?? tg.lv.split("/")[2]}`;
    if (tg.zvol) return `el volumen de bloque ZFS ${tg.zvol.split("/").pop()}`;
    return vols.find((v) => v.id === tg.volume)?.name ?? tg.volume;
  };

  return (
    <div className="ts-iscsi">
      <div className="ts-iscsi-head">
        <div>
          <p className="tg-lead" style={{ margin: 0 }}>
            Un destino iSCSI es un disco de red: una máquina virtual o un servidor lo ve como un disco propio y le da formato. TangoOS lo guarda como un archivo dentro de un volumen, en un volumen lógico de un grupo o en un volumen de bloque ZFS.
          </p>
          {svc && !svc.enabled && (
            <div className="ts-iscsi-warn">
              <Status tone="warn">El servicio iSCSI está desactivado.</Status>
              <Button variant="plain" onClick={() => openApp("tango.settings", { section: "services" })}>
                Activarlo en Ajustes…
              </Button>
            </div>
          )}
        </div>
        <div className="ts-iscsi-actions">
          {!real && <DemoBadge />}
          <Button variant="primary" onClick={() => setCreating(true)} disabled={!canCreate}>
            <Plus size={14} /> Nuevo destino
          </Button>
        </div>
      </div>

      {!canCreate && !targets?.length && (
        <EmptyState icon={HardDrive} title="Primero creá un volumen" action={<Button onClick={() => openApp("tango.storage", { action: "create" })}>Crear volumen…</Button>}>
          Los destinos iSCSI se guardan dentro de un volumen.
        </EmptyState>
      )}
      {canCreate && targets?.length === 0 && (
        <EmptyState icon={Network} title="Sin destinos iSCSI" action={<Button onClick={() => setCreating(true)}>Crear el primero</Button>}>
          Creá un disco de red para una máquina virtual de Proxmox, VMware o Hyper-V.
        </EmptyState>
      )}

      {(targets ?? []).map((tg) => (
        <div key={tg.name} className="ts-card ts-iscsi-target">
          <div className="ts-iscsi-icon">
            <Network size={20} />
          </div>
          <div className="ts-iscsi-info">
            <b>{tg.name}</b>
            <div className="ts-iscsi-iqn">
              <span className="ts-mono">{tg.iqn}</span>
              <Button variant="plain" onClick={() => copy(tg.iqn)} aria-label="Copiar IQN" title="Copiar IQN">
                <Copy size={13} />
              </Button>
            </div>
            <small>
              {fmtBytes(tg.sizeBytes)} en {where(tg)} · {tg.chap ? "con CHAP" : "sin autenticación"} · {tg.sessions ? `${tg.sessions} sesión${tg.sessions > 1 ? "es" : ""} conectada${tg.sessions > 1 ? "s" : ""}` : "sin conexiones"}
            </small>
          </div>
          <Button variant="plain" onClick={() => setDeleting(tg)} aria-label={`Eliminar ${tg.name}`} title={`Eliminar ${tg.name}`}>
            <Trash2 size={14} color="var(--tg-crit)" />
          </Button>
        </div>
      ))}

      {(targets?.length ?? 0) > 0 && (
        <div className="ts-card ts-iscsi-howto">
          <b>Cómo conectarse</b>
          <ol>
            <li>
              En el equipo cliente, buscá destinos en <span className="ts-mono">{location.hostname || "tango.local"}:3260</span>.
            </li>
            <li>Elegí el IQN del destino e iniciá sesión (con usuario y secreto CHAP si lo configuraste).</li>
            <li>El disco aparece como nuevo: inicializalo y dale formato desde ese equipo, nunca desde dos a la vez.</li>
          </ol>
          <span className="ts-mono ts-iscsi-cmd">sudo iscsiadm -m discovery -t st -p {location.hostname || "tango.local"}</span>
        </div>
      )}

      {creating && <NewTarget vols={usable} pools={pools} zvols={zvols} existing={targets ?? []} onClose={() => setCreating(false)} />}
      {deleting && <DeleteTarget target={deleting} onClose={() => setDeleting(null)} />}
    </div>
  );
}

function useJob() {
  const [id, setId] = useState<string | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  useTopic("jobs.changed", (e) => e.data.id === id && setJob(e.data));
  return { id, job, start: setId, done: job?.state === "succeeded", failed: !!job && ["failed", "interrupted", "cancelled"].includes(job.state) };
}

type Backing = "file" | "lv" | "zvol";

function NewTarget({ vols, pools, zvols, existing, onClose }: { vols: Volume[]; pools: Pool[]; zvols: (ZfsZvol & { pool: string })[]; existing: IscsiTarget[]; onClose: () => void }) {
  const t = useTransport();
  const [name, setName] = useState("vm-disk1");
  const [backing, setBacking] = useState<Backing>(vols.length ? "file" : pools.length ? "lv" : "zvol");
  const [volume, setVolume] = useState(vols[0]?.id ?? "");
  const [pool, setPool] = useState(pools[0]?.id ?? "");
  const [thin, setThin] = useState(false);
  const [zvol, setZvol] = useState(zvols[0]?.device ?? "");
  const [size, setSize] = useState("50");
  const [chap, setChap] = useState(false);
  const [user, setUser] = useState("");
  const [secret, setSecret] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const job = useJob();

  const vol = vols.find((v) => v.id === volume);
  const pl = pools.find((p) => p.id === pool);
  const free = backing === "file" ? (vol ? vol.size - vol.used : 0) : backing === "lv" ? (pl ? (thin ? pl.free * 0.8 : pl.free) : 0) : Infinity;
  const where = backing === "file" ? vol?.name : `el grupo ${pl?.name}`;
  const bytes = backing === "zvol" ? (zvols.find((z) => z.device === zvol)?.size ?? 0) : Math.round(Number(size.replace(",", ".")) * GB);
  const errors = useMemo(
    () => ({
      name: !NAME_RE.test(name) || name === "thin" ? "Usá minúsculas, números y guiones (hasta 31)." : existing.some((x) => x.name === name) ? "Ya existe un destino con ese nombre." : null,
      size: backing === "zvol" ? (zvol ? null : "Elegí un volumen de bloque.") : !(bytes > 0) ? "Indicá un tamaño en GB." : bytes > free ? `No entra: quedan ${fmtBytes(free)} libres en ${where}.` : null,
      chap: chap && (!user || secret.length < 12 || secret.length > 16) ? "CHAP necesita usuario y un secreto de 12 a 16 caracteres." : null,
    }),
    [name, existing, bytes, free, where, backing, zvol, chap, user, secret],
  );
  const valid = !errors.name && !errors.size && !errors.chap;

  const submit = async () => {
    setBusy(true);
    setErr(null);
    try {
      const c = chap ? { user, secret } : undefined;
      const r = await t.call(
        "iscsi.targets.create",
        backing === "lv" ? { name, pool, thin, sizeBytes: bytes, chap: c } : backing === "zvol" ? { name, zvol, sizeBytes: bytes, chap: c } : { name, volume, sizeBytes: bytes, chap: c },
      );
      job.start(r.job);
    } catch (e) {
      setErr(e instanceof RpcError ? e.message : String(e));
      setBusy(false);
    }
  };

  return (
    <Sheet
      open
      onClose={onClose}
      width={500}
      footer={
        job.id ? (
          <Button variant="primary" onClick={onClose}>
            {job.done || job.failed ? "Cerrar" : "Seguir en segundo plano"}
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>Cancelar</Button>
            <Button variant="primary" onClick={() => void submit()} disabled={!valid || busy}>
              Crear destino
            </Button>
          </>
        )
      }
    >
      <h2 className="tg-h2">Nuevo destino iSCSI</h2>
      {job.id ? (
        <div className="ts-iscsi-progress">
          <Progress value={job.job?.progress ?? 0} tone={job.failed ? "crit" : job.done ? "ok" : undefined} />
          <small>{job.failed ? job.job?.detail : job.done ? "Destino creado. Ya podés conectarte desde el cliente." : (job.job?.detail ?? "Preparando…")}</small>
        </div>
      ) : (
        <div className="ts-form">
          <label>
            <span>Nombre</span>
            <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} aria-invalid={!!errors.name} />
          </label>
          {errors.name && <span className="ts-err">{errors.name}</span>}
          <div className="ts-iscsi-backing">
            <span>Dónde va el disco</span>
            <Segmented<Backing>
              label="Dónde va el disco"
              value={backing}
              onChange={setBacking}
              options={[
                ...(vols.length ? [{ value: "file" as const, label: "Archivo en un volumen" }] : []),
                ...(pools.length ? [{ value: "lv" as const, label: "Volumen lógico de un grupo" }] : []),
                ...(zvols.length ? [{ value: "zvol" as const, label: "Volumen de bloque ZFS" }] : []),
              ]}
            />
            <small className="tg-muted">
              {backing === "file"
                ? "Un archivo dentro del volumen: lo más simple, entra en las instantáneas de la carpeta del volumen."
                : backing === "lv"
                  ? "Un volumen lógico del grupo, sin sistema de archivos en el medio: el camino más directo a los discos."
                  : "Un volumen de bloque de un pool ZFS, creado antes en Pools ZFS."}
            </small>
          </div>
          {backing === "file" && (
            <label>
              <span>Volumen</span>
              <select className="tg-input" value={volume} onChange={(e) => setVolume(e.target.value)}>
                {vols.map((v) => (
                  <option key={v.id} value={v.id}>
                    {v.name} · {fmtBytes(v.size - v.used)} libres
                  </option>
                ))}
              </select>
            </label>
          )}
          {backing === "lv" && (
            <>
              <label>
                <span>Grupo</span>
                <select className="tg-input" value={pool} onChange={(e) => setPool(e.target.value)}>
                  {pools.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name} · {fmtBytes(p.free)} libres
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>Reservar solo lo que se escribe (thin)</span>
                <Toggle checked={thin} onChange={setThin} label="Thin" />
              </label>
              {thin && <small className="tg-muted">El pool thin se agranda un 25 % más que el disco. Si se llena, el disco deja de aceptar escrituras: dejá espacio libre en el grupo.</small>}
            </>
          )}
          {backing === "zvol" && (
            <label>
              <span>Volumen de bloque</span>
              <select className="tg-input" value={zvol} onChange={(e) => setZvol(e.target.value)}>
                {zvols.map((z) => (
                  <option key={z.device} value={z.device}>
                    {z.name} (pool {z.pool}) · {fmtBytes(z.size)}
                  </option>
                ))}
              </select>
            </label>
          )}
          {backing !== "zvol" && (
            <label>
              <span>Tamaño (GB)</span>
              <TextInput value={size} inputMode="decimal" onChange={(e) => setSize(e.target.value)} aria-invalid={!!errors.size} />
            </label>
          )}
          {errors.size && <span className="ts-err">{errors.size}</span>}
          <label>
            <span>Autenticación CHAP</span>
            <Toggle checked={chap} onChange={setChap} label="CHAP" />
          </label>
          {chap && (
            <>
              <label>
                <span>Usuario</span>
                <TextInput value={user} onChange={(e) => setUser(e.target.value)} autoComplete="off" />
              </label>
              <label>
                <span>Secreto</span>
                <TextInput type="password" value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="new-password" />
              </label>
              {errors.chap && <span className="ts-err">{errors.chap}</span>}
            </>
          )}
          {err && <span className="ts-err">{err}</span>}
        </div>
      )}
    </Sheet>
  );
}

function DeleteTarget({ target, onClose }: { target: IscsiTarget; onClose: () => void }) {
  const t = useTransport();
  const [typed, setTyped] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const job = useJob();

  const remove = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await t.call("iscsi.targets.delete", { name: target.name, typed });
      job.start(r.job);
    } catch (e) {
      setErr(e instanceof RpcError ? e.message : String(e));
      setBusy(false);
    }
  };

  return (
    <Sheet
      open
      onClose={onClose}
      width={460}
      footer={
        job.id ? (
          <Button variant="primary" onClick={onClose}>
            {job.done || job.failed ? "Cerrar" : "Seguir en segundo plano"}
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>Cancelar</Button>
            <Button variant="danger" disabled={typed !== target.name || busy} onClick={() => void remove()}>
              Eliminar destino y sus datos
            </Button>
          </>
        )
      }
    >
      <h2 className="tg-h2">¿Eliminar «{target.name}»?</h2>
      {job.id ? (
        <div className="ts-iscsi-progress">
          <Progress value={job.job?.progress ?? 0} tone={job.failed ? "crit" : job.done ? "ok" : undefined} />
          <small>{job.done ? "Destino eliminado." : (job.job?.detail ?? "Preparando…")}</small>
        </div>
      ) : (
        <>
          <p className="tg-lead" style={{ marginTop: 6 }}>
            Se borra el disco de red ({fmtBytes(target.sizeBytes)}) con todo lo que tenga. {target.sessions > 0 && `Hay ${target.sessions} equipo${target.sessions > 1 ? "s" : ""} conectado${target.sessions > 1 ? "s" : ""}: se desconectan.`}
          </p>
          <label className="ts-typed">
            Para confirmar, escribí <code>{target.name}</code>
            <TextInput value={typed} onChange={(e) => setTyped(e.target.value)} autoFocus aria-label={`Escribí ${target.name}`} />
          </label>
          {err && <span className="ts-err">{err}</span>}
        </>
      )}
    </Sheet>
  );
}
