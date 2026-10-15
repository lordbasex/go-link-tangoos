import { describe, expect, it } from "vitest";
import { usersHandlers } from "./mock/users";
import { RpcError } from "./protocol";

const codeOf = (fn: () => unknown) => {
  try {
    fn();
  } catch (e) {
    return e instanceof RpcError ? e.code : "?";
  }
  return "";
};

describe("contraseñas de app (mock, protocol/README.md §14)", () => {
  it("crea, marca SMB exclusiva y revoca", () => {
    const h = usersHandlers({ me: () => "sofia" });
    const a = h["users.appPasswords.create"]!({ label: "Mac", smb: true });
    const b = h["users.appPasswords.create"]!({ label: "PC", smb: true });
    expect(a.password).toMatch(/^[A-Z2-7]{4}(-[A-Z2-7]{4}){5}$/);
    const list = h["users.appPasswords.list"]!({});
    expect(list.map((p) => [p.label, p.smb])).toEqual([
      ["Mac", false],
      ["PC", true],
    ]);
    expect(list.some((p) => "password" in p)).toBe(false);
    h["users.appPasswords.revoke"]!({ id: b.id });
    expect(h["users.appPasswords.list"]!({}).map((p) => p.label)).toEqual(["Mac"]);
    expect(codeOf(() => h["users.appPasswords.revoke"]!({ id: b.id }))).toBe("NOT_FOUND");
    expect(codeOf(() => h["users.appPasswords.create"]!({ label: "  " }))).toBe("INVALID_PARAMS");
  });

  it("tope de 20 por usuario", () => {
    const h = usersHandlers({ me: () => "martin" });
    for (let i = 0; i < 20; i++) h["users.appPasswords.create"]!({ label: `app ${i}` });
    expect(codeOf(() => h["users.appPasswords.create"]!({ label: "una más" }))).toBe("TOO_MANY");
  });
});
