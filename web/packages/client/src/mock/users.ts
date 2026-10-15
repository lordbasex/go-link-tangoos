// Contraseñas de app simuladas (protocol/README.md §14). Datos DEMO.
import { RpcError } from "../protocol";
import type { Method, Params, Result } from "../protocol";
import type { AppPassword } from "../protocol-users";

export interface MockUsersCtx {
  me: () => string;
}

function demoPassword(): string {
  const abc = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  const groups: string[] = [];
  for (let g = 0; g < 6; g++) {
    let s = "";
    for (let i = 0; i < 4; i++) s += abc[Math.floor(Math.random() * abc.length)];
    groups.push(s);
  }
  return groups.join("-");
}

export function usersHandlers(ctx: MockUsersCtx): { [M in Method]?: (p: Params<M>) => Result<M> } {
  const byUser = new Map<string, AppPassword[]>();
  const list = (name: string) => byUser.get(name) ?? [];
  return {
    "users.appPasswords.list": ({ name }) => list(name || ctx.me()),
    "users.appPasswords.create": ({ label, smb }) => {
      const me = ctx.me();
      if (!label?.trim()) throw new RpcError("INVALID_PARAMS", "Poné un nombre para reconocerla");
      const cur = list(me);
      if (cur.length >= 20) throw new RpcError("TOO_MANY", "Ya tenés 20 contraseñas de app: revocá alguna");
      const p: AppPassword = { id: `ap_${Math.random().toString(16).slice(2, 10)}`, label: label.trim(), created: new Date().toISOString(), lastUsed: null, smb: !!smb };
      byUser.set(me, [...cur.map((x) => (smb ? { ...x, smb: false } : x)), p]);
      return { ...p, password: demoPassword() };
    },
    "users.appPasswords.revoke": ({ id, name }) => {
      const who = name || ctx.me();
      const cur = list(who);
      if (!cur.some((p) => p.id === id)) throw new RpcError("NOT_FOUND", "No existe esa contraseña de app");
      byUser.set(who, cur.filter((p) => p.id !== id));
      return {};
    },
  };
}
