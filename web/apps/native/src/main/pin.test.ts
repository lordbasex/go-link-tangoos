import { describe, expect, it } from "vitest";
import { derOfPEM, fingerprintOfDER, parseAddress } from "./pin";

describe("dirección y huella del NAS", () => {
  it("acepta lo que escribe una persona y rechaza lo raro", () => {
    expect(parseAddress("192.168.1.20")).toEqual({ host: "192.168.1.20", port: 443 });
    expect(parseAddress(" https://Tango.local/ ")).toEqual({ host: "tango.local", port: 443 });
    expect(parseAddress("nas:8443")).toEqual({ host: "nas", port: 8443 });
    expect(parseAddress("[fd00::5]:444")).toEqual({ host: "fd00::5", port: 444 });
    for (const bad of ["", "a b", "user@nas", "nas:0", "nas:70000", "nas?x=1", "na$s"]) {
      expect(() => parseAddress(bad), bad).toThrow();
    }
  });
  it("la huella tiene el formato de TangoOS (SHA-256 del DER)", () => {
    const der = Buffer.from("certificado de prueba");
    const fp = fingerprintOfDER(der);
    expect(fp).toMatch(/^sha-256 ([0-9A-F]{2}:){31}[0-9A-F]{2}$/);
    const pem = `-----BEGIN CERTIFICATE-----\n${der.toString("base64")}\n-----END CERTIFICATE-----\n`;
    expect(fingerprintOfDER(derOfPEM(pem))).toBe(fp);
  });
});
