import { useEffect, useMemo, useState } from "react";
import { KeyRound, ShieldCheck, ShieldOff, Trash2, UserPlus, Users as UsersIcon } from "lucide-react";
import { RpcError, fmtAgo, fmtBytes, useIsReal, useLive, useTransport } from "@tango/client";
import type { User } from "@tango/client";
import { Avatar, toast, ui } from "@tango/shell";
import { Badge, Button, DemoBadge, Group, Row, Sheet, TextInput, Toggle } from "@tango/ui";
import { Bar } from "./common";

/** Mensajes de los errores estables de users.create / users.setPassword. */
function userError(e: unknown, user?: string): string {
  const code = e instanceof RpcError ? e.code : "";
  if (code === "USER_EXISTS") return `Ya existe un usuario «${user}».`;
  if (code === "WEAK_PASSWORD") return "La contraseña es demasiado débil: usá al menos 10 caracteres.";
  if (code === "INVALID_PARAMS") return (e as Error).message || "Algún dato no es válido.";
  return (e as Error).message;
}

export function UsersSection() {
  const usersQ = useLive("users.list", undefined);
  const users = usersQ.data ?? [];
  const tr = useTransport();
  // users.totp.reset (§13): el admin le saca la 2FA a alguien que perdió el teléfono y los códigos.
  const reset2fa = async (u: User) => {
    try {
      await tr.call("users.totp.reset", { name: u.name });
      usersQ.reload();
      toast({ level: "ok", title: `${u.full} ya entra solo con la contraseña`, body: "Puede volver a activar la verificación desde Seguridad.", app: "tango.settings" });
    } catch (e) {
      toast({ level: "crit", title: "No se pudo desactivar la 2FA", body: (e as Error).message, app: "tango.settings" });
    }
  };
  const groups = useLive("groups.list", undefined).data ?? [];
  const real = useIsReal("users.list");
  const [open, setOpen] = useState(false);
  const [pwFor, setPwFor] = useState<User | null>(null);
  const [delFor, setDelFor] = useState<User | null>(null);
  const me = ui.use((s) => s.user?.name);
  const all = users;

  return (
    <>
      <div className="tset-titlerow">
        <div className="tg-group-title">Usuarios {!real && <DemoBadge />}</div>
        <Button onClick={() => setOpen(true)}>
          <UserPlus size={14} /> Agregar usuario…
        </Button>
      </div>
      <div className="tg-group">
        {all.map((u) => (
          <Row
            key={u.name}
            icon={<Avatar user={u} size={32} />}
            label={
              <span className="tset-uname">
                {u.full}
                {u.admin && <Badge tone="info">Admin</Badge>}
              </span>
            }
            hint={`${u.name}${u.groups.length ? ` · ${u.groups.join(", ")}` : ""} · ${u.lastLogin ? `último acceso ${fmtAgo(u.lastLogin)}` : "nunca entró"}`}
          >
            <span className="tset-twofa" title={u.twofa ? "Verificación en dos pasos activa" : "Sin verificación en dos pasos"} data-on={u.twofa || undefined}>
              {u.twofa ? <ShieldCheck size={15} /> : <ShieldOff size={15} />}
              {u.twofa ? "2FA" : "Sin 2FA"}
            </span>
            {u.twofa && (
              <Button variant="plain" onClick={() => void reset2fa(u)} title={`Desactivar la verificación en dos pasos de ${u.full} (si perdió el teléfono)`}>
                Desactivar 2FA
              </Button>
            )}
            <div className="tset-quota">
              <small>{u.quota === null ? `${fmtBytes(u.quotaUsed)} · sin límite` : u.quota === 0 ? "Solo lectura" : `${fmtBytes(u.quotaUsed)} de ${fmtBytes(u.quota)}`}</small>
              {u.quota ? <Bar value={u.quotaUsed / u.quota} tone={u.quotaUsed / u.quota > 0.9 ? "warn" : undefined} /> : null}
            </div>
            <Button variant="plain" onClick={() => setPwFor(u)} title={`Cambiar la contraseña de ${u.full}`} aria-label={`Cambiar la contraseña de ${u.full}`}>
              <KeyRound size={14} />
            </Button>
            {u.name !== me && (
              <Button variant="plain" onClick={() => setDelFor(u)} title={`Eliminar a ${u.full}`} aria-label={`Eliminar a ${u.full}`}>
                <Trash2 size={14} />
              </Button>
            )}
          </Row>
        ))}
      </div>

      <Group title="Grupos">
        {groups.map((g) => (
          <Row
            key={g.name}
            icon={
              <span className="tset-row-icon" style={{ background: "#8E8E96" }}>
                <UsersIcon size={14} />
              </span>
            }
            label={g.name}
            hint={g.description}
          >
            <div className="tset-stack">
              {g.members.map((m) => {
                const u = all.find((x) => x.name === m);
                return u ? (
                  <span key={m} title={u.full}>
                    <Avatar user={u} size={22} />
                  </span>
                ) : null;
              })}
            </div>
          </Row>
        ))}
      </Group>

      <AddUser
        open={open}
        taken={all.map((u) => u.name)}
        groups={groups.map((g) => g.name)}
        onClose={() => setOpen(false)}
        onCreate={(u) => {
          setOpen(false);
          usersQ.reload();
          toast({ level: "ok", title: `Se agregó a ${u.full}`, body: real ? "Ya puede entrar al escritorio y a las carpetas que le permitas." : "Demo: el usuario existe en el NAS simulado.", app: "tango.settings" });
        }}
      />
      {pwFor && <SetPassword user={pwFor} onClose={() => setPwFor(null)} />}
      {delFor && (
        <DeleteUser
          user={delFor}
          onClose={() => setDelFor(null)}
          onDeleted={() => {
            const u = delFor;
            setDelFor(null);
            usersQ.reload();
            toast({ level: "ok", title: `Se eliminó a ${u.full}`, body: "Ya no puede entrar al NAS ni a las carpetas compartidas.", app: "tango.settings" });
          }}
        />
      )}
    </>
  );
}

function SetPassword({ user, onClose }: { user: User; onClose: () => void }) {
  const t = useTransport();
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const [touched, setTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [serverErr, setServerErr] = useState<string | null>(null);
  const err = pw.length < 10 ? "Mínimo 10 caracteres." : pw !== pw2 ? "Las contraseñas no coinciden." : null;

  const submit = async () => {
    setTouched(true);
    if (err || busy) return;
    setBusy(true);
    setServerErr(null);
    try {
      await t.call("users.setPassword", { name: user.name, password: pw });
      toast({ level: "ok", title: `Se cambió la contraseña de ${user.full}`, body: "Las sesiones abiertas siguen activas hasta que venzan.", app: "tango.settings" });
      onClose();
    } catch (e) {
      setServerErr(userError(e, user.name));
      setBusy(false);
    }
  };

  return (
    <Sheet
      open
      onClose={onClose}
      width={440}
      footer={
        <>
          {serverErr && (
            <span className="tset-err" style={{ marginRight: "auto", marginTop: 0 }}>
              {serverErr}
            </span>
          )}
          <Button onClick={onClose}>Cancelar</Button>
          <Button variant="primary" onClick={submit} disabled={(touched && !!err) || busy}>
            Cambiar contraseña
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">Cambiar la contraseña de {user.full}</h2>
      <p className="tg-lead" style={{ marginTop: 4 }}>
        Vale para el escritorio y para las carpetas compartidas en la red.
      </p>
      <form className="tset-form" onSubmit={(e) => (e.preventDefault(), void submit())}>
        <label>
          <span>Contraseña nueva</span>
          <TextInput type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoFocus autoComplete="new-password" />
        </label>
        <label>
          <span>Repetir contraseña</span>
          <TextInput type="password" value={pw2} onChange={(e) => setPw2(e.target.value)} autoComplete="new-password" />
          {touched && err && <small className="tset-err">{err}</small>}
        </label>
        <button type="submit" hidden />
      </form>
    </Sheet>
  );
}

function AddUser({ open, taken, groups, onClose, onCreate }: { open: boolean; taken: string[]; groups: string[]; onClose: () => void; onCreate: (u: User) => void }) {
  const t = useTransport();
  const [busy, setBusy] = useState(false);
  const [serverErr, setServerErr] = useState<string | null>(null);
  const [full, setFull] = useState("");
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const [admin, setAdmin] = useState(false);
  const [gs, setGs] = useState<string[]>([]);
  // Por defecto, el grupo de la familia si existe (en un NAS recién instalado no hay grupos).
  useEffect(() => {
    if (open) setGs(groups.includes("familia") ? ["familia"] : []);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  const [touched, setTouched] = useState(false);

  const suggested = useMemo(
    () =>
      full
        .normalize("NFD")
        .replace(/\p{Diacritic}/gu, "")
        .toLowerCase()
        .split(/\s+/)[0]
        ?.replace(/[^a-z0-9]/g, "") ?? "",
    [full],
  );
  const user = name || suggested;

  const errors: Record<string, string> = {};
  if (!full.trim()) errors.full = "Escribí el nombre completo.";
  if (!/^[a-z][a-z0-9_-]{1,31}$/.test(user)) errors.name = "Usá minúsculas, números, guiones; empieza con letra.";
  else if (taken.includes(user)) errors.name = `Ya existe un usuario «${user}».`;
  if (email && !/^[^@\s]+@[^@\s]+$/.test(email)) errors.email = "El correo no parece válido.";
  if (pw.length < 10) errors.pw = "Mínimo 10 caracteres.";
  else if (pw !== pw2) errors.pw2 = "Las contraseñas no coinciden.";
  const valid = Object.keys(errors).length === 0;

  const reset = () => {
    setFull("");
    setName("");
    setEmail("");
    setPw("");
    setPw2("");
    setAdmin(false);
    setGs([]);
    setTouched(false);
    setServerErr(null);
    setBusy(false);
  };

  const submit = async () => {
    setTouched(true);
    if (!valid || busy) return;
    setBusy(true);
    setServerErr(null);
    try {
      const u = await t.call("users.create", { name: user, full: full.trim(), email: email.trim(), password: pw, admin, groups: gs });
      reset();
      onCreate(u);
    } catch (e) {
      setServerErr(userError(e, user));
      setBusy(false);
    }
  };

  const err = (k: string) => touched && errors[k] && <small className="tset-err">{errors[k]}</small>;

  return (
    <Sheet
      open={open}
      onClose={() => (reset(), onClose())}
      width={480}
      footer={
        <>
          {serverErr && (
            <span className="tset-err" style={{ marginRight: "auto", marginTop: 0 }}>
              {serverErr}
            </span>
          )}
          <Button onClick={() => (reset(), onClose())}>Cancelar</Button>
          <Button variant="primary" onClick={submit} disabled={(touched && !valid) || busy}>
            Agregar usuario
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">Agregar usuario</h2>
      <p className="tg-lead" style={{ marginTop: 4 }}>
        Podrá entrar al escritorio, a las apps y a las carpetas compartidas que le permitas.
      </p>
      <form className="tset-form" onSubmit={(e) => (e.preventDefault(), void submit())}>
        <label>
          <span>Nombre completo</span>
          <TextInput value={full} onChange={(e) => setFull(e.target.value)} placeholder="Ana García" />
          {err("full")}
        </label>
        <label>
          <span>Usuario</span>
          <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder={suggested || "ana"} />
          {err("name")}
        </label>
        <label>
          <span>Correo (opcional)</span>
          <TextInput value={email} onChange={(e) => setEmail(e.target.value)} placeholder="ana@casa.lan" />
          {err("email")}
        </label>
        <label>
          <span>Contraseña</span>
          <TextInput type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="new-password" />
          {err("pw")}
        </label>
        <label>
          <span>Repetir contraseña</span>
          <TextInput type="password" value={pw2} onChange={(e) => setPw2(e.target.value)} autoComplete="new-password" />
          {err("pw2")}
        </label>
        <div className="tset-form-row">
          <span>Grupos</span>
          <div className="tset-checks">
            {!groups.some((g) => g !== "administrators") && <small className="tg-muted">Todavía no hay grupos</small>}
            {groups
              .filter((g) => g !== "administrators")
              .map((g) => (
                <label key={g} className="tset-check">
                  <input type="checkbox" checked={gs.includes(g)} onChange={(e) => setGs((s) => (e.target.checked ? [...s, g] : s.filter((x) => x !== g)))} />
                  {g}
                </label>
              ))}
          </div>
        </div>
        <div className="tset-form-row">
          <span>Administrador</span>
          <Toggle checked={admin} onChange={setAdmin} label="Administrador" />
        </div>
        <button type="submit" hidden />
      </form>
    </Sheet>
  );
}

/** Mensajes de los errores estables de users.delete (protocol/README.md §14). */
function deleteError(e: unknown): string {
  const code = e instanceof RpcError ? e.code : "";
  if (code === "CONFIRMATION_MISMATCH") return "El nombre no coincide.";
  if (code === "CANNOT_DELETE_SELF") return "No podés borrar tu propio usuario.";
  if (code === "LAST_ADMIN") return "Es el único administrador: primero hacé administrador a otra persona.";
  if (code === "USER_NOT_FOUND") return "Ese usuario ya no existe.";
  if (code === "PERMISSION_DENIED") return "Solo un administrador puede eliminar usuarios.";
  return (e as Error).message;
}

function DeleteUser({ user, onClose, onDeleted }: { user: User; onClose: () => void; onDeleted: () => void }) {
  const tr = useTransport();
  const [typed, setTyped] = useState("");
  const [keep, setKeep] = useState(true);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const ok = typed === user.name;
  const submit = async () => {
    if (!ok || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await tr.call("users.delete", { name: user.name, typed_name: typed, keepFiles: keep });
      onDeleted();
    } catch (e) {
      setErr(deleteError(e));
      setBusy(false);
    }
  };
  return (
    <Sheet
      open
      onClose={onClose}
      width={480}
      footer={
        <>
          {err && (
            <span className="tset-err" style={{ marginRight: "auto", marginTop: 0 }}>
              {err}
            </span>
          )}
          <Button onClick={onClose}>Cancelar</Button>
          <Button variant="danger" onClick={() => void submit()} disabled={!ok || busy}>
            Eliminar usuario
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">Eliminar a {user.full}</h2>
      <p className="tg-lead" style={{ marginTop: 4 }}>
        Se borra su cuenta del NAS: no va a poder entrar al escritorio, a SMB, SFTP ni WebDAV. También se cierran sus sesiones y se borran su
        verificación en dos pasos y sus contraseñas de app.
      </p>
      <form className="tset-form" onSubmit={(e) => (e.preventDefault(), void submit())}>
        <div className="tset-form-row">
          <span>Sus archivos</span>
          <div className="tset-checks">
            <label className="tset-check">
              <input type="radio" name="keep" checked={keep} onChange={() => setKeep(true)} />
              Dejarlos donde están (siguen en las carpetas compartidas)
            </label>
            <label className="tset-check">
              <input type="radio" name="keep" checked={!keep} onChange={() => setKeep(false)} />
              Pasarlos a mi usuario
            </label>
          </div>
        </div>
        <label>
          <span>
            Para confirmar, escribí <code>{user.name}</code>
          </span>
          <TextInput value={typed} onChange={(e) => setTyped(e.target.value)} autoFocus aria-label="Nombre del usuario" />
        </label>
        <button type="submit" hidden />
      </form>
    </Sheet>
  );
}
