import { useEffect, useState } from "react";
import { AlertTriangle, CheckCircle2, Folder, FolderPlus, HardDrive, Trash2, Users } from "lucide-react";
import { RpcError, fmtBytes, t, tn, tr, useIsReal, useJobs, useLive, useTransport } from "@tango/client";
import type { PermissionEntry, Share, ShareAccess, Volume } from "@tango/client";
import { Avatar, openApp, toast } from "@tango/shell";
import { Badge, Button, DemoBadge, EmptyState, Group, Progress, Row, Sheet, TextInput, Toggle } from "@tango/ui";
import { tj } from "./common";
import { NfsAccess } from "./NfsAccess";
import { TimeMachineHelp, TimeMachineSheet, maxGBError, parseMaxGB } from "./TimeMachine";

type Access = ShareAccess;
const LABEL: Record<Access, string> = { rw: "Lectura y escritura", ro: "Solo lectura", none: "Sin acceso" };

export function SharesSection() {
  const shares = useLive("shares.list", undefined, ["shares.changed"]).data ?? [];
  const vols = useLive("storage.volumes.list", undefined, ["storage.changed"]).data ?? [];
  const real = useIsReal("shares.list");
  const [perm, setPerm] = useState<Share | null>(null);
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState<Share | null>(null);
  const [nfs, setNfs] = useState<Share | null>(null);
  const [tm, setTm] = useState<Share | null>(null);
  const nfsEnabled = useLive("services.list", undefined, ["services.changed"]).data?.find((x) => x.id === "nfs")?.enabled ?? false;
  const usable = vols.filter((v) => v.state !== "building" && v.state !== "readonly");

  if (!vols.length && !shares.length)
    return (
      <EmptyState
        icon={HardDrive}
        title={t("Primero creá un volumen")}
        action={
          <Button variant="primary" onClick={() => openApp("tango.storage", { action: "create" })}>
            {t("Abrir Almacenamiento")}
          </Button>
        }
      >
        {t("Las carpetas compartidas viven dentro de un volumen. Creá uno en Almacenamiento y volvé acá.")}
      </EmptyState>
    );

  return (
    <>
      <div className="tset-titlerow">
        <div className="tg-group-title">
          {t("Carpetas compartidas")} {!real && <DemoBadge />}
        </div>
        <Button onClick={() => setCreating(true)} disabled={!usable.length} title={usable.length ? undefined : t("El volumen se está construyendo")}>
          <FolderPlus size={14} /> {t("Nueva carpeta compartida…")}
        </Button>
      </div>
      {shares.length === 0 ? (
        <div className="tg-group">
          <EmptyState icon={Folder} title={t("Todavía no hay carpetas compartidas")} action={<Button onClick={() => setCreating(true)} disabled={!usable.length}>{t("Crear la primera")}</Button>}>
            {t("Por ejemplo «Familia», «Fotos» o «Documentos». Cada una puede tener permisos, instantáneas y verse en la red.")}
          </EmptyState>
        </div>
      ) : (
        <div className="tg-group">
          {shares.map((s) => (
            <Row
              key={s.id}
              icon={
                <span className="tset-row-icon" style={{ background: s.color }}>
                  <Folder size={15} />
                </span>
              }
              label={s.name}
              hint={`${s.description ? `${s.description} · ` : ""}${fmtBytes(s.size)} · ${vols.find((v) => v.id === s.volume)?.name ?? s.volume}`}
            >
              <div className="tset-badges">
                {s.smb && <Badge>SMB</Badge>}
                {s.timeMachine && <Badge tone="info">Time Machine</Badge>}
                {s.nfs && <Badge>NFS</Badge>}
                {s.snapshots && <Badge tone="ok">{t("Instantáneas")}</Badge>}
                {s.recycleBin && <Badge>{t("Papelera")}</Badge>}
              </div>
              <Button onClick={() => setPerm(s)}>{t("Permisos…")}</Button>
              <Button onClick={() => setNfs(s)}>NFS…</Button>
              <Button onClick={() => setTm(s)} title={t("Usar como destino de Time Machine")}>
                Time Machine…
              </Button>
              <Button variant="plain" onClick={() => setDeleting(s)} aria-label={t("Eliminar {name}", { name: s.name })} title={t("Eliminar {name}", { name: s.name })}>
                <Trash2 size={14} color="var(--tg-crit)" />
              </Button>
            </Row>
          ))}
        </div>
      )}
      <p className="tset-foot">{t("Cada carpeta compartida es un subvolumen Btrfs: puede tener instantáneas y cuota propias.")}</p>
      {perm && <Permissions share={perm} onClose={() => setPerm(null)} />}
      {deleting && <DeleteShare share={deleting} onClose={() => setDeleting(null)} />}
      {nfs && <NfsAccess share={nfs} nfsEnabled={nfsEnabled} onClose={() => setNfs(null)} />}
      {tm && <TimeMachineSheet share={tm} onClose={() => setTm(null)} />}
      <NewShare open={creating} onClose={() => setCreating(false)} vols={usable} shares={shares} />
    </>
  );
}

const NAME_RE = /^[\p{L}\p{N} _-]{1,64}$/u;

function NewShare({ open, onClose, vols, shares }: { open: boolean; onClose: () => void; vols: Volume[]; shares: Share[] }) {
  const tp = useTransport();
  const jobs = useJobs();
  const [name, setName] = useState("");
  const [volume, setVolume] = useState("");
  const [description, setDescription] = useState("");
  const [smb, setSmb] = useState(true);
  const [timeMachine, setTimeMachine] = useState(false);
  const [tmMax, setTmMax] = useState("");
  const [help, setHelp] = useState(false);
  const [touched, setTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [serverErr, setServerErr] = useState<string | null>(null);
  const [jobId, setJobId] = useState<string | null>(null);
  const [seen, setSeen] = useState(false);

  useEffect(() => {
    if (!open) return;
    setName("");
    setDescription("");
    setSmb(true);
    setTimeMachine(false);
    setTmMax("");
    setTouched(false);
    setBusy(false);
    setServerErr(null);
    setJobId(null);
    setSeen(false);
    setVolume(vols[0]?.id ?? "");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const trimmed = name.trim();
  const nameErr = !trimmed
    ? t("Escribí un nombre.")
    : !NAME_RE.test(trimmed)
      ? t("Solo letras, números, espacios, guiones y guion bajo (hasta 64).")
      : shares.some((s) => s.name.toLowerCase() === trimmed.toLowerCase())
        ? t("Ya existe una carpeta llamada «{name}».", { name: trimmed })
        : null;
  const tmErr = timeMachine ? maxGBError(tmMax) : null;
  const valid = !nameErr && !!volume && !tmErr;

  const job = jobs.find((j) => j.id === jobId);
  useEffect(() => {
    if (job) setSeen(true);
  }, [job]);
  const failed = !!job && (job.state === "failed" || job.state === "interrupted" || job.state === "cancelled");
  const done = !!jobId && !failed && ((job && job.state === "succeeded") || (seen && !job));

  const submit = async () => {
    setTouched(true);
    if (!valid || busy) return;
    setBusy(true);
    setServerErr(null);
    try {
      const tmOn = smb && timeMachine;
      const r = await tp.call("shares.create", { name: trimmed, volume, description: description.trim(), smb, timeMachine: tmOn, timeMachineMaxBytes: tmOn ? parseMaxGB(tmMax) : null });
      setJobId(r.job);
    } catch (e) {
      const code = e instanceof RpcError ? e.code : "";
      setServerErr(code === "SHARE_EXISTS" ? t("Ya existe una carpeta llamada «{name}».", { name: trimmed }) : tr((e as Error).message));
    } finally {
      setBusy(false);
    }
  };

  const close = () => {
    if (jobId && !done && !failed) toast({ level: "info", title: t("Creando «{name}»", { name: trimmed }), body: t("Podés ver el progreso en Actividad.") });
    onClose();
  };

  if (jobId)
    return (
      <Sheet open={open} onClose={close} width={460} footer={<Button variant="primary" onClick={close}>{done || failed ? t("Cerrar") : t("Seguir en segundo plano")}</Button>}>
        <div className="tset-done">
          {done ? <CheckCircle2 size={40} color="var(--tg-ok)" /> : <Folder size={36} color="var(--tg-text-3)" />}
          <h2 className="tg-h2">{done ? t("«{name}» está lista", { name: trimmed }) : failed ? t("No se pudo crear «{name}»", { name: trimmed }) : t("Creando «{name}»…", { name: trimmed })}</h2>
          <p className="tg-muted">
            {done
              ? smb
                ? t("Ya se ve en la red desde Windows y Mac (SMB). Ajustá quién puede entrar en Permisos.")
                : t("Ya podés usarla desde Archivos. Ajustá quién puede entrar en Permisos.")
              : failed
                ? job?.detail ? tr(job.detail) : t("La tarea falló.")
                : t("Se crea un subvolumen Btrfs y, si elegiste SMB, se publica en la red.")}
          </p>
          {!done && !failed && <Progress value={job?.progress ?? 0} indeterminate={!job || job.state !== "running"} />}
        </div>
      </Sheet>
    );

  const err = (m: string | null) => touched && m && <small className="tset-err">{m}</small>;

  return (
    <Sheet
      open={open}
      onClose={close}
      width={500}
      footer={
        <>
          {serverErr && <span className="tset-err" style={{ marginRight: "auto", marginTop: 0 }}>{serverErr}</span>}
          <Button onClick={close}>{t("Cancelar")}</Button>
          <Button variant="primary" onClick={submit} disabled={(touched && !valid) || busy}>
            {t("Crear carpeta")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Nueva carpeta compartida")}</h2>
      <p className="tg-lead" style={{ marginTop: 4 }}>
        {t("Una carpeta compartida es un espacio con sus propios permisos, instantáneas y papelera.")}
      </p>
      <form className="tset-form" onSubmit={(e) => (e.preventDefault(), void submit())}>
        <label>
          <span>{t("Nombre")}</span>
          <TextInput value={name} onChange={(e) => setName(e.target.value)} placeholder={t("Familia")} autoFocus />
          {err(nameErr)}
        </label>
        <label>
          <span>{t("Volumen")}</span>
          <select className="tg-input" value={volume} onChange={(e) => setVolume(e.target.value)}>
            {vols.map((v) => (
              <option key={v.id} value={v.id}>
                {t("{name} · {free} libres", { name: v.name, free: fmtBytes(v.size - v.used) })}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>{t("Descripción (opcional)")}</span>
          <TextInput value={description} onChange={(e) => setDescription(e.target.value)} placeholder={t("Lo compartido de la casa")} />
        </label>
        <div className="tset-form-row">
          <span>{t("Compartir en la red (SMB)")}</span>
          <Toggle checked={smb} onChange={(v) => (setSmb(v), !v && setTimeMachine(false))} label={t("Compartir en la red (SMB)")} />
        </div>
        <div className="tset-form-row">
          <span>
            {t("Usar como destino de Time Machine")}
            <small className="tg-muted" style={{ display: "block" }}>
              {t("Recomendado: una carpeta dedicada, por ejemplo «TimeMachine».")}{" "}
              <button type="button" className="tset-link" onClick={() => setHelp(true)}>
                {t("¿Cómo se usa?")}
              </button>
            </small>
          </span>
          <Toggle
            checked={timeMachine}
            disabled={!smb}
            onChange={(v) => {
              setTimeMachine(v);
              if (v && !name.trim()) setName("TimeMachine");
            }}
            label={t("Usar como destino de Time Machine")}
          />
        </div>
        {timeMachine && (
          <label>
            <span>{t("Tope para los respaldos (GB, opcional)")}</span>
            <TextInput value={tmMax} onChange={(e) => setTmMax(e.target.value)} inputMode="decimal" placeholder={t("Sin tope")} />
            {err(tmErr)}
          </label>
        )}
        <button type="submit" hidden />
      </form>
      <TimeMachineHelp open={help} onClose={() => setHelp(false)} shareName={trimmed || "TimeMachine"} />
    </Sheet>
  );
}

function Permissions({ share, onClose }: { share: Share; onClose: () => void }) {
  const tp = useTransport();
  const real = useIsReal("shares.permissions.get");
  const users = useLive("users.list", undefined).data ?? [];
  const groups = useLive("groups.list", undefined).data ?? [];
  const [entries, setEntries] = useState<PermissionEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    tp.call("shares.permissions.get", { share: share.id }).then(
      (r) => setEntries(r.entries),
      (e: Error) => setError(tr(e.message)),
    );
  }, [tp, share.id]);

  // Filas: lo que manda el servidor + usuarios/grupos que todavía no tienen entrada ("Sin acceso").
  const key = (k: PermissionEntry["kind"], n: string) => `${k}:${n}`;
  const byKey = new Map((entries ?? []).map((e) => [key(e.kind, e.name), e]));
  const rows = [
    ...groups.map((g) => ({ kind: "group" as const, name: g.name, label: g.name, sub: g.description || t("Grupo") })),
    ...(entries ?? []).filter((e) => e.kind === "group" && !groups.some((g) => g.name === e.name)).map((e) => ({ kind: "group" as const, name: e.name, label: e.name, sub: t("Grupo") })),
    ...users.map((u) => ({ kind: "user" as const, name: u.name, label: u.full, sub: u.name, user: u })),
  ];
  const get = (k: PermissionEntry["kind"], n: string): Access => byKey.get(key(k, n))?.access ?? "none";
  const set = (k: PermissionEntry["kind"], n: string, access: Access) =>
    setEntries((list) => [...(list ?? []).filter((e) => !(e.kind === k && e.name === n)), { kind: k, name: n, access }]);

  const save = async () => {
    if (!entries) return;
    setBusy(true);
    setError(null);
    try {
      const full = rows.map((r) => ({ kind: r.kind, name: r.name, access: get(r.kind, r.name) }));
      await tp.call("shares.permissions.set", { share: share.id, entries: full });
      toast({ level: "ok", title: t("Permisos de «{name}» guardados", { name: share.name }), body: real ? t("Ya rigen en el escritorio y en SMB.") : t("Demo: se guardaron en el NAS simulado."), app: "tango.settings" });
      onClose();
    } catch (e) {
      setError(tr((e as Error).message));
      setBusy(false);
    }
  };

  return (
    <Sheet
      open
      onClose={onClose}
      width={620}
      footer={
        <>
          {error && (
            <span className="tset-err" style={{ marginRight: "auto", marginTop: 0 }}>
              {error}
            </span>
          )}
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" onClick={save} disabled={!entries || busy}>
            {t("Guardar")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">
        {t("Permisos de «{name}»", { name: share.name })} {!real && <DemoBadge />}
      </h2>
      <p className="tg-lead" style={{ marginTop: 4 }}>
        {t("Se aplican igual desde el escritorio y desde SMB: los hace cumplir el propio sistema.")}
      </p>
      {!entries && !error && <p className="tg-muted">{t("Leyendo los permisos actuales…")}</p>}
      {entries && (
        <table className="tset-matrix">
          <thead>
            <tr>
              <th />
              {(Object.keys(LABEL) as Access[]).map((a) => (
                <th key={a}>{t(LABEL[a])}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={key(r.kind, r.name)}>
                <td>
                  <span className="tset-who">
                    {"user" in r && r.user ? (
                      <Avatar user={r.user} size={22} />
                    ) : (
                      <span className="tset-row-icon" style={{ background: "#8E8E96", width: 22, height: 22 }}>
                        <Users size={12} />
                      </span>
                    )}
                    <span>
                      {r.label}
                      <small>{r.sub}</small>
                    </span>
                  </span>
                </td>
                {(Object.keys(LABEL) as Access[]).map((a) => (
                  <td key={a}>
                    <input
                      type="radio"
                      name={`acl-${key(r.kind, r.name)}`}
                      aria-label={`${r.label}: ${t(LABEL[a])}`}
                      checked={get(r.kind, r.name) === a}
                      onChange={() => set(r.kind, r.name, a)}
                    />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <Group>
        <Row label={t("Si hay conflicto")} hint={t("«Sin acceso» de un usuario gana sobre lo que permita su grupo")} />
      </Group>
    </Sheet>
  );
}

/** Eliminar una carpeta compartida: hay que escribir su nombre; se borran también sus instantáneas. */
function DeleteShare({ share, onClose }: { share: Share; onClose: () => void }) {
  const tp = useTransport();
  const jobs = useJobs();
  const snaps = useLive("snapshots.list", { share: share.id }).data ?? [];
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [jobId, setJobId] = useState<string | null>(null);
  const [seen, setSeen] = useState(false);
  const job = jobs.find((j) => j.id === jobId);
  useEffect(() => {
    if (job) setSeen(true);
  }, [job]);
  const failed = !!job && (job.state === "failed" || job.state === "interrupted" || job.state === "cancelled");
  const done = !!jobId && !failed && ((job && job.state === "succeeded") || (seen && !job));

  const remove = async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await tp.call("shares.delete", { share: share.id, typed_name: typed });
      setJobId(r.job);
    } catch (e) {
      const code = e instanceof RpcError ? e.code : "";
      setError(code === "CONFIRMATION_MISMATCH" ? t("El nombre escrito no coincide.") : code === "SHARE_BUSY" ? t("La carpeta está en uso. Cerrá las conexiones SMB y volvé a intentar.") : tr((e as Error).message));
    } finally {
      setBusy(false);
    }
  };

  if (jobId)
    return (
      <Sheet open onClose={onClose} width={460} footer={<Button variant="primary" onClick={onClose}>{done || failed ? t("Cerrar") : t("Seguir en segundo plano")}</Button>}>
        <div className="tset-done">
          {done ? <CheckCircle2 size={40} color="var(--tg-ok)" /> : <Trash2 size={36} color="var(--tg-text-3)" />}
          <h2 className="tg-h2">{done ? t("«{name}» se eliminó", { name: share.name }) : failed ? t("No se pudo eliminar «{name}»", { name: share.name }) : t("Eliminando «{name}»…", { name: share.name })}</h2>
          <p className="tg-muted">{done ? t("Se borraron la carpeta, sus instantáneas y su publicación en la red.") : failed ? (job?.detail ? tr(job.detail) : t("La tarea falló.")) : t("Se quita de la red y se borra el subvolumen con sus instantáneas.")}</p>
          {!done && !failed && <Progress value={job?.progress ?? 0} tone="crit" indeterminate={!job || job.state !== "running"} />}
        </div>
      </Sheet>
    );

  return (
    <Sheet
      open
      onClose={onClose}
      width={500}
      footer={
        <>
          {error && (
            <span className="tset-err" style={{ marginRight: "auto", marginTop: 0 }}>
              {error}
            </span>
          )}
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="danger" disabled={typed !== share.name || busy} onClick={remove}>
            {t("Eliminar carpeta y sus datos")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Eliminar «{name}»", { name: share.name })}</h2>
      <div className="tset-danger">
        <AlertTriangle size={18} />
        <p>
          {snaps.length
            ? tn(snaps.length, "Se borran todos los archivos de la carpeta y su instantánea. Deja de verse en la red. No se puede deshacer.", "Se borran todos los archivos de la carpeta y sus {n} instantáneas. Deja de verse en la red. No se puede deshacer.")
            : t("Se borran todos los archivos de la carpeta. Deja de verse en la red. No se puede deshacer.")}
        </p>
      </div>
      <label className="tset-form" style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        <span>
          {tj("Para confirmar, escribí {name}", { name: <code>{share.name}</code> })}
        </span>
        <TextInput value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" autoFocus aria-label={t("Escribí {name} para confirmar", { name: share.name })} />
      </label>
    </Sheet>
  );
}
