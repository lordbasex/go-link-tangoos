// Clúster, segunda etapa: unión con código y huella, TLS entre nodos, bricks en volumen
// dedicado, operaciones de bricks (agregar, quitar, reemplazar, rebalancear), cuotas,
// instantáneas y NFS (protocol/README.md §17, docs/17-cluster.md).
import { Fragment, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { Camera, Gauge, HardDrive, KeyRound, Lock, LockOpen, Network, Plus, RefreshCw, Replace, ShieldCheck, Trash2 } from "lucide-react";
import { RpcError, fmtBytes, locale, t, tn, tr, useTransport } from "@tango/client";
import type { ClusterBrickSpec, ClusterNode, ClusterQuota, ClusterSnapshot, ClusterStatus, ClusterTask, ClusterVolume, NFSClientRule } from "@tango/client";
import { toast } from "@tango/shell";
import { Badge, Button, Group, IconButton, Progress, Row, Segmented, Sheet, Status, TextInput, Toggle } from "@tango/ui";

const APP = "tango.cluster";
const GB = 1024 ** 3;
const errText = (e: unknown) => (e instanceof RpcError ? e.message : String(e));

/** Texto ya traducido con {marcas} reemplazadas por nodos de React (negritas, código). */
export function rich(s: string, nodes: Record<string, ReactNode>): ReactNode {
  return s.split(/\{(\w+)\}/).map((part, i) => (i % 2 ? <Fragment key={i}>{part in nodes ? nodes[part] : `{${part}}`}</Fragment> : part));
}

export function useAct() {
  return async (title: string, fn: () => Promise<unknown>, ok?: string) => {
    try {
      await fn();
      if (ok) toast({ level: "ok", title: ok, app: APP });
      return true;
    } catch (e) {
      toast({ level: "warn", title, body: errText(e), app: APP });
      return false;
    }
  };
}

/** Huella en dos renglones de 4 grupos, para comparar a ojo entre dos pantallas. */
export function Fingerprint({ value, label }: { value: string; label: string }) {
  const g = value.split(" ");
  return (
    <div className="tcl-fp" aria-label={`${label}: ${value}`}>
      <span>{label}</span>
      <code>{g.slice(0, 4).join(" ")}</code>
      <code>{g.slice(4).join(" ")}</code>
    </div>
  );
}

// ---- Unión ---------------------------------------------------------------------------

export function InviteSheet({ st, onClose }: { st: ClusterStatus; onClose: () => void }) {
  const tp = useTransport();
  const act = useAct();
  const [host, setHost] = useState("");
  const [inv, setInv] = useState<{ code?: string; fingerprint?: string } | null>(null);
  const live = st.invites.find((i) => i.host === host.trim());
  const send = () => void act(t("No se pudo invitar"), async () => setInv(await tp.call("cluster.invite", { host: host.trim() })));
  return (
    <Sheet
      open
      onClose={onClose}
      width={480}
      footer={
        inv ? (
          <Button variant="primary" onClick={onClose}>
            {t("Listo")}
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>{t("Cancelar")}</Button>
            <Button variant="primary" disabled={!host.trim()} onClick={send}>
              {t("Invitar")}
            </Button>
          </>
        )
      }
    >
      <h2 className="tg-h2">{t("Invitar un nodo")}</h2>
      {inv ? (
        live?.joined ? (
          <>
            <p className="tg-lead">
              <Status tone="ok">{t("Este NAS se unió al clúster de {host}.", { host })}</Status>
            </p>
            <Fingerprint label={t("CA del clúster")} value={live.joined} />
          </>
        ) : (
          <>
            <p className="tg-lead">
              {rich(t("En {host}, abrí Clúster → «Agregar un nodo», escribí {addr} y este código. Vence en 15 minutos."), { host: <b>{host}</b>, addr: <b>{st.selfAddrs?.[0] ?? st.self}</b> })}
            </p>
            <div className="tcl-code" aria-label={t("Código de unión")}>
              {inv.code}
            </div>
            {inv.fingerprint && <Fingerprint label={t("Huella de este NAS: tiene que coincidir con la que muestre el otro")} value={inv.fingerprint} />}
          </>
        )
      ) : (
        <>
          <p className="tg-lead">
            {t("Es la confirmación de este lado: GlusterFS no pide contraseña, así que este NAS solo acepta a quien invites, y solo con el código que vas a ver acá.")}
          </p>
          <label className="tcl-field">
            <span>{t("IP o nombre del NAS que va a sumar a este")}</span>
            <TextInput autoFocus value={host} placeholder="192.168.1.21" onChange={(e) => setHost(e.target.value)} onKeyDown={(e) => e.key === "Enter" && host.trim() && send()} />
          </label>
        </>
      )}
    </Sheet>
  );
}

export function AddNodeSheet({ st, onClose, certifyHost }: { st: ClusterStatus; onClose: () => void; certifyHost?: string }) {
  const tp = useTransport();
  const act = useAct();
  const [host, setHost] = useState(certifyHost ?? "");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [prep, setPrep] = useState<{ id: string; cn: string; fingerprint: string } | null>(null);
  const certify = !!certifyHost || st.nodes.some((n) => n.host === host.trim() && !n.self);
  const prepare = () => {
    setBusy(true);
    void act(t("No se pudo conectar"), async () => setPrep(await tp.call("cluster.peer.prepare", { host: host.trim(), code }))).then(() => setBusy(false));
  };
  const confirm = () => {
    if (!prep) return;
    setBusy(true);
    void act(t("No se pudo terminar la unión"), () => tp.call("cluster.peer.confirm", { id: prep.id, certify }), certify ? t("{host} tiene su certificado", { host: host.trim() }) : t("{host} se unió al clúster", { host: host.trim() })).then((ok) => {
      setBusy(false);
      if (ok) onClose();
    });
  };
  return (
    <Sheet
      open
      onClose={onClose}
      width={500}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          {prep ? (
            <Button variant="primary" disabled={busy} onClick={confirm}>
              {certify ? t("Coinciden: certificar") : t("Coinciden: unir")}
            </Button>
          ) : (
            <Button variant="primary" disabled={!host.trim() || code.replace(/[-\s]/g, "").length !== 10 || busy} onClick={prepare}>
              {t("Continuar")}
            </Button>
          )}
        </>
      }
    >
      <h2 className="tg-h2">{certify ? (host ? t("Certificar {host}", { host }) : t("Certificar un nodo")) : t("Agregar un nodo")}</h2>
      {prep ? (
        <>
          <p className="tg-lead">
            {rich(t("Compará la huella con la que muestra {host} en su invitación. Si no coincide, cancelá: alguien en la red podría estar haciéndose pasar por ese equipo."), { host: <b>{host}</b> })}
          </p>
          <Fingerprint label={t("Huella de {name}", { name: prep.cn })} value={prep.fingerprint} />
          <p className="tcl-note">
            {st.tls.ca
              ? certify
                ? t("Al confirmar, este NAS le entrega su certificado del clúster.")
                : t("Al confirmar, este NAS le entrega su certificado del clúster y lo suma con GlusterFS.")
              : certify
                ? t("Al confirmar, este NAS le entrega su certificado del clúster (se crea la CA del clúster).")
                : t("Al confirmar, este NAS le entrega su certificado del clúster (se crea la CA del clúster) y lo suma con GlusterFS.")}
          </p>
        </>
      ) : (
        <>
          <p className="tg-lead">
            {rich(t("Antes, en el otro equipo, abrí Clúster → «Invitar un nodo» y escribí {addr}: te va a mostrar un código."), { addr: <b>{st.selfAddrs?.[0] ?? st.self}</b> })}
            {certify && ` ${t("Este nodo ya está en el clúster pero no tiene certificado: después de certificarlo vas a poder prender el TLS.")}`}
          </p>
          <label className="tcl-field">
            <span>{t("IP o nombre del otro TangoOS")}</span>
            <TextInput autoFocus={!certifyHost} value={host} disabled={!!certifyHost} placeholder="192.168.1.21" onChange={(e) => setHost(e.target.value)} />
          </label>
          <label className="tcl-field">
            <span>{t("Código que muestra ese equipo")}</span>
            <TextInput
              autoFocus={!!certifyHost}
              value={code}
              placeholder="ABCDE-FGHJK"
              className="tcl-codein"
              onChange={(e) => setCode(e.target.value.toUpperCase())}
              onKeyDown={(e) => e.key === "Enter" && code.replace(/[-\s]/g, "").length === 10 && prepare()}
            />
          </label>
        </>
      )}
      {busy && <Progress indeterminate />}
    </Sheet>
  );
}

// ---- TLS -------------------------------------------------------------------------------

const fmtDate = (iso?: string) => (iso ? new Date(iso).toLocaleDateString(locale(), { day: "numeric", month: "long", year: "numeric" }) : "");

export function SecurityGroup({ st, onTLS, onCertify }: { st: ClusterStatus; onTLS: () => void; onCertify: (host: string) => void }) {
  const [rotate, setRotate] = useState(false);
  const peers = st.nodes.filter((n) => !n.self);
  const uncert = peers.filter((n) => !st.nodeInfo[n.host]);
  const revoke = st.tls.revoke ?? [];
  return (
    <Group title={t("Cifrado entre nodos")}>
      <Row
        icon={st.tls.secure ? <Lock size={16} /> : <LockOpen size={16} />}
        label={st.tls.secure ? t("TLS prendido") : t("TLS apagado")}
        hint={
          st.tls.secure
            ? t("glusterd y los bricks hablan TLS con certificados de la CA del clúster; cada volumen solo acepta a sus nodos.")
            : t("El tráfico entre nodos va sin cifrar, aunque solo entre los equipos del clúster (firewall).")
        }
      >
        <Status tone={st.tls.secure ? "ok" : "warn"}>{st.tls.secure ? t("Cifrado", undefined, "estado") : t("Sin cifrar")}</Status>
        <Button onClick={onTLS}>{st.tls.secure ? t("Apagar…", undefined, "tls") : t("Prender…")}</Button>
      </Row>
      {st.tls.caFingerprint && (
        <Row icon={<ShieldCheck size={16} />} label={t("CA del clúster")} hint={st.tls.caSince ? `${st.tls.caFingerprint} · ${t("desde el {date}", { date: fmtDate(st.tls.caSince) })}` : st.tls.caFingerprint}>
          <Button onClick={() => setRotate(true)}>{t("Rotar…")}</Button>
        </Row>
      )}
      {revoke.length > 0 && (
        <Row
          icon={<KeyRound size={16} />}
          label={revoke.length === 1 ? t("{name} salió del clúster con la clave de la CA", { name: revoke[0].name }) : t("{n} nodos salieron con la clave de la CA", { n: revoke.length })}
          hint={t("Ya no entra por el firewall ni por la lista de cada volumen, pero puede firmarse certificados nuevos. Rotar la CA los deja sin valor.")}
        >
          <Status tone="warn">{t("Pendiente")}</Status>
          <Button variant="primary" onClick={() => setRotate(true)}>
            {t("Rotar la CA…")}
          </Button>
        </Row>
      )}
      {rotate && <RotateCASheet st={st} onClose={() => setRotate(false)} />}
      {uncert.map((n) => (
        <Row key={n.host} icon={<KeyRound size={16} />} label={t("{host} sin certificado", { host: n.host })} hint={t("Se unió antes del TLS o no responde por el canal de control (puerto 24010).")}>
          <Button onClick={() => onCertify(n.host)}>{t("Certificar…")}</Button>
        </Row>
      ))}
    </Group>
  );
}

export function RotateCASheet({ st, onClose }: { st: ClusterStatus; onClose: () => void }) {
  const tp = useTransport();
  const act = useAct();
  const off = st.nodes.filter((n) => !n.self && !n.connected);
  const tlsUsed = st.tls.secure || st.volumes.some((v) => v.tls);
  const revoke = st.tls.revoke ?? [];
  return (
    <Sheet
      open
      onClose={onClose}
      width={520}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={off.length > 0} onClick={() => void act(t("No se pudo rotar la CA"), () => tp.call("cluster.ca.rotate", {}), t("Rotando la CA…")).then((ok) => ok && onClose())}>
            {t("Rotar la CA")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Rotar la CA del clúster")}</h2>
      <p className="tg-lead">
        {t("Cada nodo guarda la clave de la CA para poder sumar a otros. Un nodo que sale se la lleva: rotar crea una CA nueva, le da a cada nodo un certificado nuevo y deja sin valor todo lo firmado con la anterior.")}
      </p>
      {revoke.length > 0 && <p className="tcl-note">{t("Salieron desde la última rotación: {names}.", { names: revoke.map((r) => r.name).join(", ") })}</p>}
      <ol className="tcl-steps">
        <li>{t("Cada nodo recibe la CA nueva y su certificado, y acepta las dos CA mientras dura el cambio.")}</li>
        <li>{t("Todos pasan a presentar el certificado nuevo.")}</li>
        <li>{t("Todos dejan de aceptar la CA anterior.")}</li>
        {tlsUsed && <li>{t("Como GlusterFS usa TLS, los volúmenes se detienen, glusterd se reinicia en cada nodo y vuelven a arrancar y montarse donde estaban.")}</li>}
      </ol>
      {off.length > 0 ? (
        <p className="tcl-alert" data-tone="crit">
          {t("{hosts} no está conectado: un nodo apagado se quedaría con la CA anterior y fuera del clúster. Prendelo o sacalo antes de rotar.", { hosts: off.map((n) => n.host).join(", ") })}
        </p>
      ) : (
        <p className="tcl-note">{tlsUsed ? t("Es una parada controlada de un par de minutos: mientras tanto, SMB y NFS de estos volúmenes no responden.") : t("GlusterFS no usa TLS: los volúmenes siguen andando.")} {t("Las huellas de los nodos no cambian.")}</p>
      )}
    </Sheet>
  );
}

export function TLSSheet({ st, onClose }: { st: ClusterStatus; onClose: () => void }) {
  const tp = useTransport();
  const act = useAct();
  const on = !st.tls.secure;
  const missing = st.nodes.filter((n) => !n.self && !st.nodeInfo[n.host]);
  const mounted = st.mounts.filter((m) => m.mounted).length;
  return (
    <Sheet
      open
      onClose={onClose}
      width={520}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={on && missing.length > 0} onClick={() => void act(t("No se pudo cambiar el TLS"), () => tp.call("cluster.tls.set", { enabled: on }), on ? t("Prendiendo TLS…") : t("Apagando TLS…")).then((ok) => ok && onClose())}>
            {on ? t("Prender TLS") : t("Apagar TLS")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{on ? t("Prender TLS en el clúster") : t("Apagar TLS en el clúster")}</h2>
      <p className="tg-lead">{t("Es una parada controlada de un par de minutos:")}</p>
      <ol className="tcl-steps">
        <li>{t("Se desmontan los volúmenes en cada nodo ({n} montados en este NAS) y se detienen.", { n: mounted })}</li>
        <li>{on ? t("Cada nodo prende el TLS de management y reinicia glusterd.") : t("Cada nodo apaga el TLS de management y reinicia glusterd.")}</li>
        <li>{on ? t("Cada volumen pasa a aceptar solo a los nodos del clúster, con cifrado.") : t("Los volúmenes vuelven a hablar sin cifrar.")}</li>
        <li>{t("Se arrancan y se vuelven a montar donde estaban.")}</li>
      </ol>
      {on && missing.length > 0 && (
        <p className="tcl-alert" data-tone="crit">
          {t("{hosts} no tiene certificado del clúster: certificalo primero desde «Nodos».", { hosts: missing.map((n) => n.host).join(", ") })}
        </p>
      )}
      <p className="tcl-note">{t("Durante la parada, SMB y NFS de estos volúmenes no responden.")}</p>
    </Sheet>
  );
}

// ---- Selector de bricks ------------------------------------------------------------------

export type BrickChoice = { on: boolean; kind: "dir" | "lv"; tangoVolume: string; pool: string; sizeGB: number; thin: boolean };

export function defaultChoice(st: ClusterStatus, n: ClusterNode): BrickChoice {
  const info = st.nodeInfo[n.host];
  const tv = (n.self ? st.tangoVolumes : info?.tangoVolumes ?? [])[0]?.name ?? "volume1";
  const pool = (n.self ? st.pools : info?.pools ?? [])[0]?.name ?? "";
  return { on: true, kind: pool ? "lv" : "dir", tangoVolume: tv, pool, sizeGB: 100, thin: true };
}

export function choiceSpec(host: string, c: BrickChoice): ClusterBrickSpec {
  return c.kind === "lv" ? { host, kind: "lv", pool: c.pool, size: Math.round(c.sizeGB * GB), thin: c.thin } : { host, kind: "dir", tangoVolume: c.tangoVolume };
}

export function BrickPicker({ st, node, vol, value, onChange, toggle = true }: { st: ClusterStatus; node: ClusterNode; vol: string; value?: BrickChoice; onChange: (c?: BrickChoice) => void; toggle?: boolean }) {
  const info = st.nodeInfo[node.host];
  const tvs = node.self ? st.tangoVolumes : info?.tangoVolumes ?? [{ name: "volume1", mount: "/volume1", free: 0 }];
  const pools = node.self ? st.pools : info?.pools ?? [];
  const c = value;
  return (
    <div className="tcl-brick tcl-brick2" data-on={c ? true : undefined}>
      <div className="tcl-brick-head">
        {toggle && <Toggle checked={!!c} label={node.host} onChange={(on) => onChange(on ? defaultChoice(st, node) : undefined)} />}
        <b>{node.host}</b>
        {node.self && <Badge>{t("Este NAS")}</Badge>}
        <span className="tcl-grow" />
        {c && (
          <Segmented
            label={t("Tipo de brick en {host}", { host: node.host })}
            value={c.kind}
            onChange={(k) => onChange({ ...c, kind: k })}
            options={[
              { value: "dir", label: t("Carpeta") },
              { value: "lv", label: t("Volumen dedicado") },
            ]}
          />
        )}
      </div>
      {c && c.kind === "dir" && (
        <div className="tcl-brick-body">
          <select className="tg-select" value={c.tangoVolume} aria-label={t("Volumen de {host}", { host: node.host })} onChange={(e) => onChange({ ...c, tangoVolume: e.target.value })}>
            {tvs.map((x) => (
              <option key={x.name} value={x.name}>
                {x.name}
                {x.free ? ` · ${t("{free} libres", { free: fmtBytes(x.free) })}` : ""}
              </option>
            ))}
          </select>
          <code className="tcl-path">
            /{c.tangoVolume}/@gluster/{vol || "…"}
          </code>
        </div>
      )}
      {c && c.kind === "lv" && (
        <div className="tcl-brick-body">
          {pools.length === 0 ? (
            <small className="tcl-warn">{info || node.self ? t("{host} no tiene grupos de almacenamiento.", { host: node.host }) : t("{host} no tiene grupos de almacenamiento (o no tiene certificado del clúster).", { host: node.host })}</small>
          ) : (
            <>
              <select className="tg-select" value={c.pool} aria-label={t("Grupo de {host}", { host: node.host })} onChange={(e) => onChange({ ...c, pool: e.target.value })}>
                {pools.map((p) => (
                  <option key={p.name} value={p.name}>
                    {t("{name} · {free} libres", { name: p.name, free: fmtBytes(p.free) })}
                  </option>
                ))}
              </select>
              <TextInput
                aria-label={t("Tamaño en GB en {host}", { host: node.host })}
                value={String(c.sizeGB)}
                style={{ width: 70 }}
                onChange={(e) => onChange({ ...c, sizeGB: Math.max(1, Number(e.target.value.replace(/\D/g, "")) || 1) })}
              />
              <span className="tcl-sum">GB</span>
              <Toggle checked={c.thin} label={t("Thin (instantáneas)")} onChange={(thin) => onChange({ ...c, thin })} />
              <span className="tcl-sum">thin</span>
              <code className="tcl-path">/srv/gluster/{vol || "…"}/brick · XFS</code>
            </>
          )}
        </div>
      )}
    </div>
  );
}

// ---- Bricks: agregar, quitar, reemplazar, rebalancear --------------------------------------

function TaskProgress({ task }: { task: ClusterTask | null }) {
  if (!task) return null;
  return (
    <div className="tcl-task">
      <Progress value={task.done ? 1 : undefined} indeterminate={!task.done} tone={task.failed ? "crit" : undefined} />
      <span>
        {task.status === "completed" ? t("Terminado") : task.status === "in progress" ? t("En curso") : tr(task.status)} · {t("{files} archivos movidos ({size}) · {lookups} revisados", { files: task.total.files, size: fmtBytes(task.total.size), lookups: task.total.lookups })}
        {task.total.failures > 0 && ` · ${t("{n} con error", { n: task.total.failures })}`}
      </span>
    </div>
  );
}

export function BricksSheet({ st, v, onClose }: { st: ClusterStatus; v: ClusterVolume; onClose: () => void }) {
  const tp = useTransport();
  const act = useAct();
  const [mode, setMode] = useState<"add" | "remove" | "replace" | "rebalance">("add");
  const connected = st.nodes.filter((n) => n.connected);
  const [picked, setPicked] = useState<Record<string, BrickChoice>>({});
  const [grow, setGrow] = useState<"set" | "replica">("set");
  const sets = useMemo(() => {
    const out: (typeof v.bricks)[] = [];
    for (let i = 0; i < v.bricks.length; i += Math.max(1, v.replica)) out.push(v.bricks.slice(i, i + Math.max(1, v.replica)));
    return out;
  }, [v]);
  const [removeSet, setRemoveSet] = useState(sets.length - 1);
  const [task, setTask] = useState<ClusterTask | null>(null);
  const [phase, setPhase] = useState<"idle" | "running" | "done">("idle");
  const [from, setFrom] = useState(v.bricks.find((b) => !b.online) ?? v.bricks[0]);
  const [to, setTo] = useState<{ host: string; c: BrickChoice } | null>(() => {
    const n = connected.find((x) => x.host === from?.host);
    return n ? { host: n.host, c: defaultChoice(st, n) } : null;
  });
  const need = grow === "set" ? v.replica : v.sets;
  const hosts = Object.keys(picked);
  const removeBricks = (sets[removeSet] ?? []).map((b) => ({ host: b.host, path: b.path }));

  useEffect(() => {
    if (phase !== "running") return;
    const poll = async () => {
      try {
        const r = mode === "rebalance" ? await tp.call("cluster.volume.rebalance", { name: v.name, action: "status" }) : await tp.call("cluster.volume.bricks.remove", { name: v.name, bricks: removeBricks, phase: "status" });
        setTask(r.task);
        if (r.task?.done) setPhase("done");
      } catch (e) {
        toast({ level: "warn", title: t("No se pudo leer el avance"), body: errText(e), app: APP });
        setPhase("idle");
      }
    };
    void poll();
    const id = setInterval(() => void poll(), 2500);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [phase, mode]);

  const add = () =>
    void act(
      t("No se pudieron agregar los bricks"),
      () => tp.call("cluster.volume.bricks.add", { name: v.name, replica: grow === "replica" ? v.replica + 1 : undefined, bricks: hosts.map((h) => choiceSpec(h, picked[h])) }),
      t("Agregando bricks a {name}…", { name: v.name }),
    ).then((ok) => ok && onClose());

  return (
    <Sheet
      open
      onClose={onClose}
      width={640}
      footer={
        <>
          <span className="tcl-grow tcl-sum">{v.type} {v.sets} × {v.replica}</span>
          <Button onClick={onClose}>{t("Cerrar")}</Button>
          {mode === "add" && (
            <Button variant="primary" disabled={hosts.length !== need} onClick={add}>
              {t("Agregar {n} bricks", { n: need })}
            </Button>
          )}
          {mode === "remove" && phase === "idle" && (
            <Button
              variant="danger"
              disabled={sets.length < 2}
              onClick={() => void act(t("No se pudo empezar"), () => tp.call("cluster.volume.bricks.remove", { name: v.name, bricks: removeBricks, phase: "start" })).then((ok) => ok && setPhase("running"))}
            >
              {t("Migrar los datos y quitar")}
            </Button>
          )}
          {mode === "remove" && phase === "done" && (
            <Button variant="danger" onClick={() => void act(t("No se pudo quitar"), () => tp.call("cluster.volume.bricks.remove", { name: v.name, bricks: removeBricks, phase: "commit" }), t("Bricks quitados")).then((ok) => ok && onClose())}>
              {t("Confirmar y quitar")}
            </Button>
          )}
          {mode === "replace" && (
            <Button
              variant="primary"
              disabled={!to || !from}
              onClick={() =>
                to &&
                from &&
                void act(t("No se pudo reemplazar"), () => tp.call("cluster.volume.bricks.replace", { name: v.name, from: { host: from.host, path: from.path }, to: choiceSpec(to.host, to.c) }), t("Reemplazando el brick…")).then((ok) => ok && onClose())
              }
            >
              {t("Reemplazar")}
            </Button>
          )}
          {mode === "rebalance" && phase !== "running" && (
            <Button variant="primary" disabled={v.sets < 2} onClick={() => void act(t("No se pudo rebalancear"), () => tp.call("cluster.volume.rebalance", { name: v.name, action: "start" })).then((ok) => ok && setPhase("running"))}>
              {t("Rebalancear")}
            </Button>
          )}
        </>
      }
    >
      <h2 className="tg-h2">{t("Bricks de {name}", { name: v.name })}</h2>
      <Segmented
        label={t("Operación")}
        value={mode}
        onChange={(m) => {
          setMode(m);
          setPhase("idle");
          setTask(null);
        }}
        options={[
          { value: "add", label: t("Agregar") },
          { value: "remove", label: t("Quitar") },
          { value: "replace", label: t("Reemplazar") },
          { value: "rebalance", label: t("Rebalancear") },
        ]}
      />
      {mode === "add" && (
        <>
          <Row label={t("Cómo crece")} hint={grow === "set" ? t("Un conjunto más de {n} copias: más espacio (después conviene rebalancear).", { n: v.replica }) : t("Una copia más por conjunto: réplica {n}, más tolerancia a fallas.", { n: v.replica + 1 })}>
            <Segmented
              label={t("Cómo crece")}
              value={grow}
              onChange={(g) => setGrow(g)}
              options={[
                { value: "set", label: t("Más espacio") },
                { value: "replica", label: t("Más copias") },
              ]}
            />
          </Row>
          <div className="tcl-field">
            <span>
              {grow === "set" ? tn(need, "Elegí un nodo (uno por copia)", "Elegí {n} nodos (uno por copia)") : tn(need, "Elegí un nodo (uno por conjunto)", "Elegí {n} nodos (uno por conjunto)")}
            </span>
            <div className="tcl-bricks">
              {connected.map((n) => (
                <BrickPicker
                  key={n.host}
                  st={st}
                  node={n}
                  vol={v.name}
                  value={picked[n.host]}
                  onChange={(c) => {
                    const next = { ...picked };
                    if (c) next[n.host] = c;
                    else delete next[n.host];
                    setPicked(next);
                  }}
                />
              ))}
            </div>
          </div>
        </>
      )}
      {mode === "remove" && (
        <>
          {sets.length < 2 ? (
            <p className="tg-lead">{t("Con un solo conjunto de réplica no hay bricks para quitar sin perder copias.")}</p>
          ) : (
            <>
              <p className="tg-lead">{t("Se quita un conjunto entero: GlusterFS mueve sus archivos a los demás antes de sacarlo.")}</p>
              <div className="tcl-bricks">
                {sets.map((set, i) => (
                  <button key={i} type="button" className="tcl-card" aria-pressed={removeSet === i} disabled={phase !== "idle"} onClick={() => setRemoveSet(i)}>
                    <b>{t("Conjunto {n}", { n: i + 1 })}</b>
                    <small>{set.map((b) => `${b.host}:${b.path}`).join(" · ")}</small>
                  </button>
                ))}
              </div>
              <TaskProgress task={task} />
              {phase === "done" && <p className="tcl-note">{t("La migración terminó: confirmá para sacar los bricks del volumen. Los datos quedan en cada brick hasta que limpies los huérfanos.")}</p>}
            </>
          )}
        </>
      )}
      {mode === "replace" && (
        <>
          <p className="tg-lead">{t("Para un nodo que no vuelve, o para mover un brick: la copia nueva se completa sola con la curación.")}</p>
          <label className="tcl-field">
            <span>{t("Brick a reemplazar")}</span>
            <select className="tg-select" value={`${from?.host}:${from?.path}`} aria-label={t("Brick a reemplazar")} onChange={(e) => setFrom(v.bricks.find((b) => `${b.host}:${b.path}` === e.target.value) ?? v.bricks[0])}>
              {v.bricks.map((b) => (
                <option key={b.host + b.path} value={`${b.host}:${b.path}`}>
                  {b.host}:{b.path} {b.online ? "" : `· ${t("fuera de línea")}`}
                </option>
              ))}
            </select>
          </label>
          <div className="tcl-field">
            <span>{t("Brick nuevo")}</span>
            <div className="tcl-bricks">
              {connected.map((n) => (
                <BrickPicker key={n.host} st={st} node={n} vol={v.name} value={to?.host === n.host ? to.c : undefined} onChange={(c) => setTo(c ? { host: n.host, c } : null)} />
              ))}
            </div>
          </div>
        </>
      )}
      {mode === "rebalance" && (
        <>
          <p className="tg-lead">{v.sets < 2 ? t("Rebalancear solo tiene sentido con más de un conjunto de réplica.") : t("Reparte los archivos entre los conjuntos después de agregar bricks. El volumen sigue en uso.")}</p>
          <TaskProgress task={task} />
          {phase === "running" && (
            <Button variant="plain" onClick={() => void act(t("No se pudo detener"), () => tp.call("cluster.volume.rebalance", { name: v.name, action: "stop" })).then(() => setPhase("idle"))}>
              {t("Detener")}
            </Button>
          )}
        </>
      )}
    </Sheet>
  );
}

// ---- Cuotas -----------------------------------------------------------------------------

export function QuotaSheet({ v, onClose }: { v: ClusterVolume; onClose: () => void }) {
  const tp = useTransport();
  const act = useAct();
  const [list, setList] = useState<ClusterQuota[]>([]);
  const [path, setPath] = useState("");
  const [gb, setGb] = useState("50");
  const load = () => void tp.call("cluster.volume.quota", { name: v.name, op: "list" }).then((r) => setList(r.quotas)).catch(() => setList([]));
  useEffect(load, [tp, v.name]);
  const run = (title: string, op: "enable" | "disable" | "limit" | "remove", extra: { path?: string; limit?: number } = {}) =>
    void act(title, async () => {
      const r = await tp.call("cluster.volume.quota", { name: v.name, op, ...extra });
      setList(r.quotas);
      if (r.job) toast({ level: "info", title: op === "limit" ? t("Contando lo que ya hay en {path}", { path: extra.path ?? "" }) : t("Contando el uso de las carpetas"), body: t("El uso se actualiza cuando termina."), app: APP });
    });
  const folder = path.trim() && !path.trim().startsWith("/") ? `/${path.trim()}` : path.trim();
  return (
    <Sheet open onClose={onClose} width={560} footer={<Button onClick={onClose}>{t("Cerrar")}</Button>}>
      <h2 className="tg-h2">{t("Cuotas de {name}", { name: v.name })}</h2>
      <Row label={t("Cuotas por carpeta")} hint={t("Límite de espacio para cada carpeta de primer nivel del volumen, en todo el clúster.")}>
        <Toggle checked={v.quota} label={t("Cuotas")} onChange={(on) => run(on ? t("No se pudieron activar") : t("No se pudieron desactivar"), on ? "enable" : "disable")} />
      </Row>
      {v.quota && (
        <>
          <Group title={t("Límites")}>
            {list.length === 0 && <Row label={t("Sin límites")} hint={t("Agregá uno abajo.")} />}
            {list.map((q) => (
              <Row key={q.path} icon={<Gauge size={16} />} label={<code>{q.path}</code>} hint={t("{used} de {limit} · aviso al {pct} %", { used: fmtBytes(q.used), limit: fmtBytes(q.limit), pct: q.softPercent })}>
                <div className="tcl-qbar">
                  <Progress value={Math.min(1, q.used / q.limit)} tone={q.hardOver ? "crit" : q.softOver ? "warn" : undefined} />
                </div>
                <IconButton icon={Trash2} label={t("Quitar el límite de {path}", { path: q.path })} onClick={() => run(t("No se pudo quitar"), "remove", { path: q.path })} />
              </Row>
            ))}
          </Group>
          <div className="tcl-inline">
            <TextInput aria-label={t("Carpeta")} value={path} placeholder="/Documentos" onChange={(e) => setPath(e.target.value)} />
            <TextInput aria-label={t("Límite en GB")} value={gb} style={{ width: 70 }} onChange={(e) => setGb(e.target.value.replace(/[^\d.]/g, ""))} />
            <span className="tcl-sum">GB</span>
            <Button onClick={() => run(t("No se pudo poner el límite"), "limit", { path: folder, limit: Math.round(Number(gb) * GB) })} disabled={!Number(gb) || !folder}>
              <Plus size={13} /> {t("Poner límite")}
            </Button>
          </div>
          <p className="tcl-note">
            {t("La carpeta tiene que existir. Si ya tiene archivos, TangoOS cuenta lo que ocupan y reinicia los bricks del volumen de a un nodo. Los límites no frenan a root.")}
          </p>
        </>
      )}
    </Sheet>
  );
}

// ---- Instantáneas --------------------------------------------------------------------------

export function SnapshotsSheet({ v, onClose }: { v: ClusterVolume; onClose: () => void }) {
  const tp = useTransport();
  const act = useAct();
  const [list, setList] = useState<ClusterSnapshot[]>([]);
  const [name, setName] = useState(() => `instantanea-${new Date().toISOString().slice(0, 10)}`);
  const [restore, setRestore] = useState<string | null>(null);
  const canSnap = v.bricks.every((b) => b.kind === "lv" || b.kind === "snap");
  useEffect(() => {
    void tp.call("cluster.volume.snapshot", { name: v.name, op: "info" }).then((r) => setList(r.snapshots)).catch(() => setList([]));
  }, [tp, v.name]);
  const run = (title: string, p: { op: "create" | "delete" | "restore"; snap: string; desc?: string }, ok: string) =>
    void act(title, async () => setList((await tp.call("cluster.volume.snapshot", { name: v.name, ...p })).snapshots), ok);
  return (
    <Sheet open onClose={onClose} width={580} footer={<Button onClick={onClose}>{t("Cerrar")}</Button>}>
      <h2 className="tg-h2">{t("Instantáneas de {name}", { name: v.name })}</h2>
      {!canSnap ? (
        <p className="tcl-alert">
          <Camera size={16} />
          <span>{t("Las instantáneas de GlusterFS necesitan que todos los bricks sean volúmenes dedicados thin. Los bricks en carpeta no se pueden fotografiar: usá las instantáneas del volumen de TangoOS de cada nodo.")}</span>
        </p>
      ) : (
        <>
          <Group title={t("Instantáneas")}>
            {list.length === 0 && <Row label={t("Ninguna todavía")} />}
            {list.map((s) => (
              <Row key={s.name} icon={<Camera size={16} />} label={s.name} hint={`${s.created.slice(0, 16)}${s.desc ? ` · ${s.desc}` : ""}`}>
                {restore === s.name ? (
                  <Button variant="danger" disabled={v.status === "Started"} onClick={() => run(t("No se pudo restaurar"), { op: "restore", snap: s.name }, t("{name} restaurado a {snap}", { name: v.name, snap: s.name }))}>
                    {t("Restaurar")}
                  </Button>
                ) : (
                  <Button variant="plain" onClick={() => setRestore(s.name)}>
                    {t("Restaurar…")}
                  </Button>
                )}
                <IconButton icon={Trash2} label={t("Borrar {name}", { name: s.name })} onClick={() => run(t("No se pudo borrar"), { op: "delete", snap: s.name }, t("{name} borrada", { name: s.name }))} />
              </Row>
            ))}
          </Group>
          {restore && (
            <p className="tcl-note">
              {v.status === "Started" ? `${t("Para restaurar, primero desmontalo en cada nodo y detené el volumen.")} ` : ""}
              {t("Restaurar vuelve todo el volumen al momento de la instantánea (se pierde lo escrito después) y la instantánea restaurada desaparece. Después, montalo de nuevo con su carpeta compartida para que se reapliquen los permisos.")}
            </p>
          )}
          <div className="tcl-inline">
            <TextInput aria-label={t("Nombre de la instantánea")} value={name} onChange={(e) => setName(e.target.value.replace(/[^A-Za-z0-9_-]/g, ""))} />
            <Button onClick={() => run(t("No se pudo crear"), { op: "create", snap: name }, t("Instantánea {name} creada", { name }))} disabled={!name}>
              <Camera size={13} /> {t("Crear instantánea")}
            </Button>
          </div>
        </>
      )}
    </Sheet>
  );
}

// ---- NFS --------------------------------------------------------------------------------------

export function NFSSheet({ st, v, onClose }: { st: ClusterStatus; v: ClusterVolume; onClose: () => void }) {
  const tp = useTransport();
  const act = useAct();
  const mounted = st.mounts.some((m) => m.name === v.name && m.mounted);
  const [rules, setRules] = useState<NFSClientRule[]>(() => st.nfs[v.name] ?? []);
  const [host, setHost] = useState("");
  const save = () => void act(t("No se pudo cambiar NFS"), () => tp.call("cluster.volume.nfs", { name: v.name, clients: rules }), rules.length ? t("Exportando {name} por NFS…", { name: v.name }) : t("Export NFS quitado")).then((ok) => ok && onClose());
  return (
    <Sheet
      open
      onClose={onClose}
      width={520}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={!mounted && rules.length > 0} onClick={save}>
            {t("Guardar")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("NFS de {name}", { name: v.name })}</h2>
      <p className="tg-lead">
        {mounted ? (
          rich(t("Se exporta {path} desde este NAS (NFS v4, root_squash). Los permisos son los de la carpeta compartida: el cliente tiene que usar los mismos IDs de usuario y grupo."), { path: <code>/cluster/{v.name}</code> })
        ) : (
          t("Primero montalo en este NAS (Montaje y SMB…).")
        )}
      </p>
      <Group title={t("Equipos con acceso")}>
        {rules.length === 0 && <Row label={t("Ninguno")} hint={t("Sin equipos, el volumen no se exporta.")} />}
        {rules.map((r, i) => (
          <Row key={r.host + i} icon={<Network size={16} />} label={r.host}>
            <Segmented
              label={t("Acceso de {host}", { host: r.host })}
              value={r.access}
              onChange={(a) => setRules(rules.map((x, j) => (j === i ? { ...x, access: a } : x)))}
              options={[
                { value: "ro", label: t("Lectura") },
                { value: "rw", label: t("Lectura y escritura") },
              ]}
            />
            <IconButton icon={Trash2} label={t("Quitar {host}", { host: r.host })} onClick={() => setRules(rules.filter((_, j) => j !== i))} />
          </Row>
        ))}
      </Group>
      <div className="tcl-inline">
        <TextInput aria-label={t("Equipo o red")} value={host} placeholder="192.168.1.0/24" onChange={(e) => setHost(e.target.value)} />
        <Button
          disabled={!host.trim()}
          onClick={() => {
            setRules([...rules, { host: host.trim(), access: "ro" }]);
            setHost("");
          }}
        >
          <Plus size={13} /> {t("Agregar")}
        </Button>
      </div>
    </Sheet>
  );
}

export const icons = { HardDrive, RefreshCw, Replace };
