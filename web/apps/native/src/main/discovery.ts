// NAS TangoOS en la red local: avahi los anuncia como _tangoos._tcp (system/avahi/tangoos.service).
import { Bonjour, type Service } from "bonjour-service";

export interface Found {
  name: string;
  host: string;
  port: number;
  addresses: string[];
}

export function discover(onChange: (all: Found[]) => void): () => void {
  const bonjour = new Bonjour();
  const found = new Map<string, Found>();
  const emit = () => onChange([...found.values()].sort((a, b) => a.name.localeCompare(b.name)));
  const key = (s: Service) => `${s.name}|${s.port}`;
  const browser = bonjour.find({ type: "tangoos" });
  browser.on("up", (s: Service) => {
    const v4 = (s.addresses ?? []).filter((a) => /^\d+\.\d+\.\d+\.\d+$/.test(a));
    found.set(key(s), { name: s.name, host: v4[0] ?? s.host, port: s.port, addresses: s.addresses ?? [] });
    emit();
  });
  browser.on("down", (s: Service) => {
    found.delete(key(s));
    emit();
  });
  // Pregunta de nuevo cada tanto: un NAS que se prende después aparece solo.
  const timer = setInterval(() => browser.update(), 15_000);
  return () => {
    clearInterval(timer);
    browser.stop();
    bonjour.destroy();
  };
}
