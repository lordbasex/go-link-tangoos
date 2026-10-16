// Replicación de volúmenes ZFS (protocol/README.md §18.1, ADR 0016): tareas que mandan un
// volumen a otro pool o a otro TangoOS, los NAS vinculados, recibir réplicas y convertir
// una réplica recibida en un volumen.
import { useEffect, useState } from "react";
import { AlertTriangle, ArrowRight, Check, Copy, Fingerprint, Inbox, Link2, Lock, Play, Plus, Repeat, Send, Server, Trash2, Unlink } from "lucide-react";
import { REPL_EVERY, RpcError, fmtAgo, fmtBytes, locale, t, tn, tr, useJobs, useLive, useTransport } from "@tango/client";
import type { ReplReplica, ReplStatus, ReplTask, Volume, ZfsPool } from "@tango/client";
import { Badge, Button, Progress, Sheet, Status, TextInput, Toggle } from "@tango/ui";
import { toast } from "@tango/shell";
import { useCountdown } from "./CreateVolume";

const errText = (e: unknown) => (e instanceof RpcError ? e.message : (e as Error).message);

export function ReplSection({ vols }: { vols: Volume[] }) {
  const st = useLive("storage.zfs.repl.status", undefined, ["storage.changed"]).data;
  const zst = useLive("storage.zfs.status", undefined, ["storage.changed"]).data;
  const [creating, setCreating] = useState(false);
  const [linking, setLinking] = useState(false);
  const [promoting, setPromoting] = useState<ReplReplica | null>(null);
  if (!st || !zst) return <p className="tg-muted">{t("Cargando…")}</p>;
  const pools = zst.pools;
  const zvols = vols.filter((v) => v.kind === "zfs");
  return (
    <div className="ts-stack">
      <section className="tg-card">
        <div className="ts-repl-head">
          <div className="ts-card-title">{t("Tareas")}</div>
          <span className="ts-push" />
          <Button variant="primary" disabled={!zvols.length} onClick={() => setCreating(true)}>
            <Plus size={14} /> {t("Replicar un volumen…")}
          </Button>
        </div>
        {st.tasks.length === 0 ? (
          <p className="tg-muted ts-repl-empty">
            {zvols.length
              ? t("Ningún volumen se replica todavía. Una réplica es una copia en otro pool o en otro NAS que se actualiza sola: el primer envío copia todo y los siguientes, solo los cambios.")
              : t("Creá un volumen en un pool ZFS para poder replicarlo.")}
          </p>
        ) : (
          st.tasks.map((task) => <TaskRow key={task.id} t={task} />)
        )}
      </section>

      <section className="tg-card">
        <div className="ts-repl-head">
          <div className="ts-card-title">{t("Otros NAS adonde mandar")}</div>
          <span className="ts-push" />
          <Button onClick={() => setLinking(true)}>
            <Link2 size={14} /> {t("Vincular un NAS…")}
          </Button>
        </div>
        {st.targets.length === 0 ? (
          <p className="tg-muted ts-repl-empty">{t("Para replicar a otro TangoOS, prendé «Recibir réplicas» en ese NAS y vinculalo acá con su código.")}</p>
        ) : (
          st.targets.map((tg) => (
            <div key={tg.id} className="ts-repl-row">
              <Server size={18} />
              <div>
                <strong>{tg.name}</strong>
                <small className="tg-muted">
                  {tg.addr} · {t("huella")} <span className="tg-num">{tg.fingerprint}</span>
                </small>
              </div>
              <span className="ts-push" />
              <UnlinkButton id={tg.id} kind="target" name={tg.name} />
            </div>
          ))
        )}
      </section>

      <ReceiveCard st={st} pools={pools} />

      <section className="tg-card">
        <div className="ts-card-title">{t("Réplicas en este NAS")}</div>
        {st.replicas.length === 0 ? (
          <p className="tg-muted ts-repl-empty">{t("Acá aparecen las copias que llegan de este u otros NAS. No se montan ni se ven en la red hasta que las conviertas en un volumen.")}</p>
        ) : (
          st.replicas.map((r) => <ReplicaRow key={`${r.pool}/${r.origin}/${r.vol}`} r={r} onPromote={() => setPromoting(r)} />)
        )}
      </section>

      <CreateTaskSheet open={creating} onClose={() => setCreating(false)} st={st} vols={zvols} pools={pools} />
      <LinkSheet open={linking} onClose={() => setLinking(false)} />
      <PromoteSheet r={promoting} vols={vols} onClose={() => setPromoting(null)} />
    </div>
  );
}

// ---- Tareas -------------------------------------------------------------------------------

function TaskRow({ t: task }: { t: ReplTask }) {
  const rpc = useTransport();
  const jobs = useJobs();
  const job = jobs.find((j) => j.id === task.job && j.state === "running");
  const [confirmDel, setConfirmDel] = useState(false);
  const status = (): { tone: "ok" | "warn" | "crit" | "info"; text: string } => {
    if (job) return { tone: "info", text: t("Enviando") };
    if (!task.last) return { tone: "info", text: t("Todavía no se envió") };
    if (!task.last.ok) return { tone: "warn", text: t("El último envío falló") };
    return { tone: "ok", text: t("Al día · {ago}", { ago: fmtAgo(task.last.at) }) };
  };
  const s = status();
  const run = () =>
    rpc.call("storage.zfs.repl.tasks.run", { id: task.id }).then(
      () => toast({ level: "info", title: t("Replicando {name}", { name: task.vol }), body: t("A {target}.", { target: task.targetLabel }) }),
      (e) => toast({ level: "warn", title: t("No se pudo empezar"), body: errText(e) }),
    );
  const update = (every: number, enabled: boolean) => rpc.call("storage.zfs.repl.tasks.update", { id: task.id, every, enabled }).catch((e) => toast({ level: "warn", title: t("No se pudo cambiar"), body: errText(e) }));
  const del = () =>
    rpc.call("storage.zfs.repl.tasks.delete", { id: task.id }).then(
      () => toast({ level: "ok", title: t("Tarea borrada"), body: t("La réplica que ya está en {target} queda ahí.", { target: task.targetLabel }) }),
      (e) => toast({ level: "warn", title: t("No se pudo borrar"), body: errText(e) }),
    );
  return (
    <div className="ts-repl-task">
      <div className="ts-repl-route">
        <strong>{task.vol}</strong>
        <ArrowRight size={14} />
        <span>{task.targetLabel}</span>
        <span className="ts-push" />
        <Status tone={s.tone}>{s.text}</Status>
      </div>
      {job ? (
        <div className="ts-wiz-progress">
          <Progress value={job.progress} />
          <span className="tg-num">{Math.round(job.progress * 100)} %</span>
          <span className="tg-muted ts-zfs-stage">{tr(job.detail)}</span>
        </div>
      ) : (
        <p className="ts-note">
          {task.last && !task.last.ok ? (
            <>
              {tr(task.last.error)} {task.enabled && task.every > 0 ? t("Se reintenta solo en unos minutos y sigue desde donde quedó.") : t("El próximo envío sigue desde donde quedó.")}
            </>
          ) : task.last ? (
            <>
              {t("Último envío: {size} en {time}.", { size: fmtBytes(task.last.bytes), time: task.last.seconds < 90 ? `${task.last.seconds} s` : `${Math.round(task.last.seconds / 60)} min` })}
              {task.next && task.enabled ? ` ${t("Próximo: {when}.", { when: new Date(task.next).toLocaleString(locale(), { weekday: "short", hour: "2-digit", minute: "2-digit" }) })}` : ""}
            </>
          ) : (
            t("El primer envío copia el volumen entero con sus instantáneas; después solo viajan los cambios.")
          )}
        </p>
      )}
      <div className="ts-row-actions">
        <select className="tg-select" aria-label={t("Frecuencia")} value={task.every} onChange={(e) => update(Number(e.target.value), task.enabled)}>
          {REPL_EVERY.map((x) => (
            <option key={x.hours} value={x.hours}>
              {x.label}
            </option>
          ))}
        </select>
        {task.every > 0 && <Toggle checked={task.enabled} onChange={(v) => update(task.every, v)} label={t("Automática")} />}
        <span className="ts-push" />
        <Button disabled={!!job} onClick={run}>
          <Play size={14} /> {t("Replicar ahora")}
        </Button>
        {confirmDel ? (
          <>
            <Button onClick={() => setConfirmDel(false)}>{t("No")}</Button>
            <Button variant="danger" onClick={del}>
              {t("Borrar la tarea")}
            </Button>
          </>
        ) : (
          <Button disabled={!!job} onClick={() => setConfirmDel(true)} aria-label={t("Borrar la tarea")}>
            <Trash2 size={14} />
          </Button>
        )}
      </div>
    </div>
  );
}

function CreateTaskSheet({ open, onClose, st, vols, pools }: { open: boolean; onClose: () => void; st: ReplStatus; vols: Volume[]; pools: ZfsPool[] }) {
  const rpc = useTransport();
  const [vol, setVol] = useState("");
  const [target, setTarget] = useState("");
  const [every, setEvery] = useState(24);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setVol(vols[0]?.id ?? "");
      setTarget("");
      setEvery(24);
      setError(null);
    }
  }, [open, vols]);
  const v = vols.find((x) => x.id === vol);
  const srcPool = v?.pool?.replace(/^zfs:/, "");
  const locals = pools.filter((p) => p.name !== srcPool && !p.readonly);
  const go = async () => {
    setBusy(true);
    setError(null);
    try {
      await rpc.call("storage.zfs.repl.tasks.create", { volume: vol, target, every, runNow: true });
      toast({ level: "info", title: t("Replicando {name}", { name: String(v?.name) }), body: t("El primer envío copia todo: puede tardar.") });
      onClose();
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      open={open}
      onClose={onClose}
      width={520}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={busy || !vol || !target} onClick={go}>
            <Send size={14} /> {t("Replicar")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Replicar un volumen")}</h2>
      <p className="tg-muted ts-wiz-lead">
        {t("La réplica guarda el volumen con sus carpetas e instantáneas. Si está cifrado viaja cifrado: el destino nunca necesita la frase.")}
      </p>
      <div className="ts-form">
        <label className="ts-field">
          <span>{t("Volumen")}</span>
          <select className="tg-select" value={vol} onChange={(e) => setVol(e.target.value)}>
            {vols.map((x) => (
              <option key={x.id} value={x.id}>
                {x.name} ({x.pool?.replace(/^zfs:/, "pool ")}){x.encrypted ? ` · ${t("cifrado")}` : ""}
              </option>
            ))}
          </select>
        </label>
        <div className="ts-field">
          <span>{t("Destino")}</span>
          <div className="ts-key-list">
            {locals.map((p) => (
              <button key={p.id} type="button" className="ts-pick-disk" aria-pressed={target === `local:${p.name}`} onClick={() => setTarget(`local:${p.name}`)}>
                <span className="ts-pick-check">{target === `local:${p.name}` && <Check size={12} strokeWidth={3} />}</span>
                <Repeat size={20} strokeWidth={1.6} />
                <span className="ts-pick-text">
                  <strong>{t("Pool {name} de este NAS", { name: p.name })}</strong>
                  <small>{t("{free} libres · protege si fallan los discos del otro pool", { free: fmtBytes(p.free) })}</small>
                </span>
              </button>
            ))}
            {st.targets.map((tg) => (
              <button key={tg.id} type="button" className="ts-pick-disk" aria-pressed={target === tg.id} onClick={() => setTarget(tg.id)}>
                <span className="ts-pick-check">{target === tg.id && <Check size={12} strokeWidth={3} />}</span>
                <Server size={20} strokeWidth={1.6} />
                <span className="ts-pick-text">
                  <strong>{tg.name}</strong>
                  <small>{tg.addr} · {t("otro NAS: protege si se pierde este equipo")}</small>
                </span>
              </button>
            ))}
            {!locals.length && !st.targets.length && <small className="tg-muted">{t("No hay otro pool ZFS ni otro NAS vinculado. Vinculá un NAS desde «Otros NAS adonde mandar».")}</small>}
          </div>
        </div>
        <label className="ts-field">
          <span>{t("Frecuencia")}</span>
          <select className="tg-select" value={every} onChange={(e) => setEvery(Number(e.target.value))}>
            {REPL_EVERY.map((x) => (
              <option key={x.hours} value={x.hours}>
                {x.label}
              </option>
            ))}
          </select>
          <small>{t("En el destino quedan las mismas instantáneas que en el origen: lo que la programación de instantáneas borre acá, se borra allá.")}</small>
        </label>
      </div>
    </Sheet>
  );
}

// ---- Vincular y recibir --------------------------------------------------------------------

function LinkSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const rpc = useTransport();
  const [addr, setAddr] = useState("");
  const [probe, setProbe] = useState<{ probe: string; name: string; fingerprint: string; address: string } | null>(null);
  const [same, setSame] = useState(false);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setAddr("");
      setProbe(null);
      setSame(false);
      setCode("");
      setError(null);
    }
  }, [open]);
  const connect = async () => {
    setBusy(true);
    setError(null);
    try {
      setProbe(await rpc.call("storage.zfs.repl.targets.probe", { address: addr }));
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };
  const link = async () => {
    if (!probe) return;
    setBusy(true);
    setError(null);
    try {
      const tg = await rpc.call("storage.zfs.repl.targets.add", { probe: probe.probe, code });
      toast({ level: "ok", title: t("{name} vinculado", { name: tg.name }), body: t("Ya podés replicar volúmenes a ese NAS.") });
      onClose();
    } catch (e) {
      setError(errText(e));
      setProbe(null);
      setSame(false);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      open={open}
      onClose={onClose}
      width={500}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          {probe ? (
            <Button variant="primary" disabled={busy || !same || code.replace(/[-\s]/g, "").length !== 10} onClick={link}>
              {t("Vincular")}
            </Button>
          ) : (
            <Button variant="primary" disabled={busy || !addr.trim()} onClick={connect}>
              {t("Conectar")}
            </Button>
          )}
        </>
      }
    >
      <h2 className="tg-h2">{t("Vincular un NAS")}</h2>
      <p className="tg-muted ts-wiz-lead">{t("En el otro TangoOS: Almacenamiento → Replicación → «Recibir réplicas» → «Mostrar código».")}</p>
      <div className="ts-form">
        <label className="ts-field">
          <span>{t("Dirección del otro NAS")}</span>
          <TextInput value={addr} disabled={!!probe} onChange={(e) => setAddr(e.target.value)} placeholder="192.168.1.40" autoFocus aria-label={t("Dirección del otro NAS")} />
        </label>
        {probe && (
          <>
            <div className="ts-note-box">
              <Fingerprint size={16} />
              <div>
                <strong>{probe.name || probe.address}</strong> {t("presenta la huella")}
                <div className="ts-repl-fp tg-num">{probe.fingerprint}</div>
                {t("Comparala con la que muestra ese NAS. Si no es igual, no sigas: podría ser otro equipo.")}
              </div>
            </div>
            <label className="ts-field ts-field-inline">
              <input type="checkbox" checked={same} onChange={(e) => setSame(e.target.checked)} /> {t("Es la misma huella")}
            </label>
            <label className="ts-field">
              <span>{t("Código de vinculación")}</span>
              <TextInput value={code} disabled={!same} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="ABCDE-FGHJK" aria-label={t("Código de vinculación")} />
              <small>{t("Sirve una vez y vence a los 15 minutos.")}</small>
            </label>
          </>
        )}
      </div>
    </Sheet>
  );
}

function ReceiveCard({ st, pools }: { st: ReplStatus; pools: ZfsPool[] }) {
  const rpc = useTransport();
  const r = st.receive;
  const [pool, setPool] = useState(r.pool || pools[0]?.name || "");
  useEffect(() => setPool(r.pool || pools[0]?.name || ""), [r.pool, pools]);
  const left = useCountdown(r.invite?.expires);
  const set = (enabled: boolean, p = pool) =>
    rpc.call("storage.zfs.repl.receive", { enabled, pool: p }).catch((e) => toast({ level: "warn", title: t("No se pudo cambiar"), body: errText(e) }));
  const invite = () => rpc.call("storage.zfs.repl.invite", undefined).catch((e) => toast({ level: "warn", title: t("No se pudo crear el código"), body: errText(e) }));
  const code = r.invite && left ? r.invite.code : null;
  return (
    <section className="tg-card">
      <div className="ts-repl-head">
        <div className="ts-card-title">{t("Recibir réplicas de otros NAS")}</div>
        <span className="ts-push" />
        <Toggle checked={r.enabled} disabled={!pools.length} onChange={(v) => set(v)} label={t("Recibir réplicas")} />
      </div>
      {!pools.length ? (
        <p className="tg-muted ts-repl-empty">{t("Hace falta un pool ZFS donde guardarlas.")}</p>
      ) : !r.enabled ? (
        <p className="tg-muted ts-repl-empty">{t("Prendelo en el NAS que va a guardar las copias. Solo entran los NAS que vincules con un código.")}</p>
      ) : (
        <>
          <div className="ts-repl-recv">
            <label className="ts-field">
              <span>{t("Guardarlas en")}</span>
              <select className="tg-select" value={pool} onChange={(e) => (setPool(e.target.value), set(true, e.target.value))}>
                {pools.map((p) => (
                  <option key={p.id} value={p.name}>
                    {t("Pool {name} · {free} libres", { name: p.name, free: fmtBytes(p.free) })}
                  </option>
                ))}
              </select>
            </label>
            <div className="ts-field">
              <span>{t("Este NAS")}</span>
              <small>
                {st.identity.name} · {r.addrs.join(", ")} {t("(puerto {port})", { port: r.port })}
              </small>
              <small>
                {t("Huella")} <span className="tg-num">{st.identity.fingerprint}</span>
              </small>
            </div>
          </div>
          {code ? (
            <div className="ts-note-box">
              <Inbox size={16} />
              <div>
                {t("Código de vinculación:")} <strong className="ts-repl-code tg-num">{code}</strong>
                <Button size={undefined} variant="plain" onClick={() => navigator.clipboard?.writeText(code)} aria-label={t("Copiar el código")}>
                  <Copy size={13} />
                </Button>
                <br />
                {t("Escribilo en el otro NAS y comprobá que ahí se vea esta huella. Vence en {time}.", { time: `${Math.floor(left! / 60)}:${String(left! % 60).padStart(2, "0")}` })}
              </div>
            </div>
          ) : (
            <div className="ts-row-actions">
              <Button onClick={invite}>
                <Link2 size={14} /> {t("Mostrar código de vinculación")}
              </Button>
            </div>
          )}
          {st.sources.length > 0 && (
            <>
              <div className="ts-card-subtitle">{t("NAS que pueden mandar acá")}</div>
              {st.sources.map((s) => (
                <div key={s.id} className="ts-repl-row">
                  <Server size={18} />
                  <div>
                    <strong>{s.name}</strong>
                    <small className="tg-muted">
                      {t("huella")} <span className="tg-num">{s.fingerprint}</span> · {t("vinculado {ago}", { ago: fmtAgo(s.addedAt) })}
                    </small>
                  </div>
                  <span className="ts-push" />
                  <UnlinkButton id={s.id} kind="source" name={s.name} />
                </div>
              ))}
            </>
          )}
        </>
      )}
    </section>
  );
}

function UnlinkButton({ id, kind, name }: { id: string; kind: "target" | "source"; name: string }) {
  const rpc = useTransport();
  const [ask, setAsk] = useState(false);
  const go = () =>
    (kind === "target" ? rpc.call("storage.zfs.repl.targets.remove", { id }) : rpc.call("storage.zfs.repl.sources.remove", { id })).then(
      () => toast({ level: "ok", title: t("{name} desvinculado", { name }), body: kind === "source" ? t("Ya no puede mandar réplicas a este NAS. Las que mandó siguen acá.") : t("Ya no se le manda nada.") }),
      (e) => toast({ level: "warn", title: t("No se pudo desvincular"), body: errText(e) }),
    );
  return ask ? (
    <>
      <Button onClick={() => setAsk(false)}>{t("No")}</Button>
      <Button variant="danger" onClick={go}>
        {t("Desvincular")}
      </Button>
    </>
  ) : (
    <Button onClick={() => setAsk(true)}>
      <Unlink size={14} /> {t("Desvincular")}
    </Button>
  );
}

// ---- Réplicas recibidas -----------------------------------------------------------------------

function ReplicaRow({ r, onPromote }: { r: ReplReplica; onPromote: () => void }) {
  const rpc = useTransport();
  const [ask, setAsk] = useState("");
  const del = () =>
    rpc.call("storage.zfs.repl.replicas.delete", { pool: r.pool, origin: r.origin, vol: r.vol }).then(
      () => toast({ level: "ok", title: t("Réplica borrada"), body: t("Si la tarea sigue en {from}, el próximo envío vuelve a copiar todo.", { from: r.from }) }),
      (e) => toast({ level: "warn", title: t("No se pudo borrar"), body: errText(e) }),
    );
  return (
    <div className="ts-repl-replica">
      <div className="ts-repl-row">
        {r.encrypted ? <Lock size={18} /> : <Repeat size={18} />}
        <div>
          <strong>
            {r.vol} <span className="tg-muted">{t("de {from}", { from: r.from })}</span>
          </strong>
          <small className="tg-muted">
            {t("{size} en el pool {pool}", { size: fmtBytes(r.used), pool: r.pool })} · {tn(r.snapshots, "{n} instantánea", "{n} instantáneas")}
            {r.lastAt ? ` · ${t("última copia {ago}", { ago: fmtAgo(r.lastAt) })}` : ""}
            {r.encrypted ? ` · ${t("cifrada (hace falta la frase del origen para abrirla)")}` : ""}
          </small>
        </div>
        <span className="ts-push" />
        {r.partial && <Badge tone="warn">{t("Envío a medias")}</Badge>}
        <Button onClick={onPromote} disabled={r.partial}>
          {t("Convertir en volumen…")}
        </Button>
        <Button onClick={() => setAsk(ask ? "" : " ")} aria-label={t("Borrar la réplica")}>
          <Trash2 size={14} />
        </Button>
      </div>
      {ask && (
        <div className="ts-row-actions ts-repl-confirm">
          <span className="tg-muted">{t("Para borrar la réplica escribí {name}:", { name: r.vol })}</span>
          <TextInput value={ask.trim()} onChange={(e) => setAsk(e.target.value || " ")} aria-label={t("Nombre de la réplica")} />
          <Button variant="danger" disabled={ask.trim() !== r.vol} onClick={del}>
            {t("Borrar")}
          </Button>
        </div>
      )}
    </div>
  );
}

function PromoteSheet({ r, vols, onClose }: { r: ReplReplica | null; vols: Volume[]; onClose: () => void }) {
  const rpc = useTransport();
  const [name, setName] = useState("");
  const [pass, setPass] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!r) return;
    let n = 1;
    while (vols.some((v) => v.name === `volume${n}`)) n++;
    setName(vols.some((v) => v.name === r.vol) ? `volume${n}` : r.vol);
    setPass("");
    setError(null);
  }, [r, vols]);
  if (!r) return null;
  const go = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await rpc.call("storage.zfs.repl.replicas.promote", { pool: r.pool, origin: r.origin, vol: r.vol, name, passphrase: pass || undefined });
      toast({ level: "ok", title: t("{name} está listo", { name }), body: res.locked ? t("Quedó bloqueado: desbloquealo con la frase del origen.") : t("Montado en /{name}. Creá las carpetas compartidas que necesites.", { name }) });
      onClose();
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      open={!!r}
      onClose={onClose}
      width={480}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={busy || !/^[a-z][a-z0-9]{0,31}$/.test(name)} onClick={go}>
            {t("Convertir")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Convertir {name} en un volumen", { name: r.vol })}</h2>
      <p className="tg-muted ts-wiz-lead">
        {t("La réplica pasa a ser un volumen normal de este NAS, con sus carpetas e instantáneas. Deja de ser una réplica: si {from} sigue mandando, su próximo envío empieza una copia nueva.", { from: r.from })}
      </p>
      <div className="ts-form">
        <label className="ts-field">
          <span>{t("Nombre del volumen")}</span>
          <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} aria-label={t("Nombre del volumen")} />
          <small>{t("Se monta en /{name}.", { name: name || "…" })}</small>
        </label>
        {r.encrypted && (
          <label className="ts-field">
            <span>{t("Frase del volumen de origen")}</span>
            <TextInput type="password" value={pass} onChange={(e) => setPass(e.target.value)} aria-label={t("Frase del volumen de origen")} autoComplete="off" />
            <small>{t("Opcional: sin la frase queda bloqueado y lo desbloqueás después. Una llave USB del origen también sirve.")}</small>
          </label>
        )}
      </div>
    </Sheet>
  );
}
