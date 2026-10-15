import { describe, expect, it } from "vitest";
import { validNfsHost } from "./NfsAccess";

describe("acceso NFS: hosts válidos", () => {
  it.each(["192.168.1.20", "192.168.1.0/24", "10.0.0.0/8", "pc.casa.lan", "*.casa.lan", "*", "fd00::1", "fd00::/64"])("acepta %s", (h) => {
    expect(validNfsHost(h)).toBeNull();
  });
  it.each(["", "192.168.1.300", "192.168.1.0/33", "pc casa", "rm -rf", "a;b"])("rechaza «%s»", (h) => {
    expect(validNfsHost(h)).not.toBeNull();
  });
});
