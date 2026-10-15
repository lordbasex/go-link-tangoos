// Sesión del navegador: token de `auth.login` en sessionStorage (por pestaña, docs/07 §2).
import type { Transport, SessionUser } from "./transport";
import { RpcError } from "./protocol";
import { verifyHost } from "./hostproof";
import type { LoginUser, User } from "./protocol";

const KEY = "tango.session";

export function getToken(): string | null {
  try {
    return sessionStorage.getItem(KEY);
  } catch {
    return null;
  }
}

export function setToken(token: string | null) {
  try {
    if (token) sessionStorage.setItem(KEY, token);
    else sessionStorage.removeItem(KEY);
  } catch {
    /* almacenamiento bloqueado: la sesión dura lo que la conexión */
  }
}

const pick = (u: User): SessionUser => ({ name: u.name, full: u.full, color: u.color });

/** Usuarios para la pantalla de login. */
export function loginUsers(t: Transport): Promise<LoginUser[]> {
  return t.call("auth.users", undefined);
}

/**
 * Login: guarda el token si el servidor real lo emite (el mock no lo hace). Si el usuario
 * tiene verificación en dos pasos devuelve "totp": hay que seguir con `loginTotp` (§13).
 */
export async function login(t: Transport, user: string, password: string): Promise<SessionUser | "totp"> {
  const r = await t.call("auth.login", { user, password });
  if (r.needs === "totp" || !r.user) return "totp";
  setToken(r.token ?? null);
  return pick(r.user);
}

/** Segundo paso del login: código de la app de autenticación o de recuperación. */
export async function loginTotp(t: Transport, code: string): Promise<SessionUser> {
  const r = await t.call("auth.totp", { code: code.replace(/\s+/g, "") });
  setToken(r.token ?? null);
  return pick(r.user);
}

/**
 * Reanuda la sesión guardada (recarga de página). Devuelve null si no hay token,
 * si el servidor no lo acepta o si el backend no implementa sesiones (mock).
 */
export async function resume(t: Transport): Promise<SessionUser | null> {
  const token = getToken();
  if (!token || !t.isReal("auth.resume")) return null;
  try {
    await verifyHost((m, p) => t.call(m, p), token, t.isReal("auth.challenge"));
    const r = await t.call("auth.resume", { token });
    return pick(r.user);
  } catch (e) {
    if (e instanceof RpcError && (e.code === "UNAUTHENTICATED" || e.code === "INVALID_PARAMS")) setToken(null);
    return null;
  }
}

export async function logout(t: Transport): Promise<void> {
  try {
    if (t.isReal("auth.logout")) await t.call("auth.logout", undefined);
  } catch {
    /* igual cerramos la sesión local */
  }
  setToken(null);
}
