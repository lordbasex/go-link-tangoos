// TangoConnect simulado (protocol/README.md §15). Datos DEMO: nada sale a internet.
import { RpcError } from "../protocol";
import type { Method, Params, Result } from "../protocol";
import type { ConnectLink, ConnectStatus, ShareLinkInfo } from "../protocol-connect";

export interface MockConnectCtx {
  me: () => string;
  emit: (topic: "connect.changed", data: Record<string, never>) => void;
}

const DEVICE = "8f1c2d3e-4b5a-4c6d-8e7f-0a1b2c3d4e5f";
const rnd = (n: number) => Array.from(crypto.getRandomValues(new Uint8Array(n)), (b) => b.toString(16).padStart(2, "0")).join("");

export function connectHandlers(ctx: MockConnectCtx): { [M in Method]?: (p: Params<M>) => Result<M> } {
  const status: ConnectStatus = {
    enabled: false, connected: false, signal: "wss://signal.go-link.org/ws?v=1", portal: "https://tangoos.go-link.org",
    deviceId: DEVICE, fingerprint: "SHA-256 53:D8:8D:44:54:14:E0:75:CB:A4:D7:9F:BB:1E:99:3B:36:CF:C9:9E:0D:89:72:0E:D6:D1:83:11:7A:27:CE:6F",
    relayOnly: false, hasTurn: true, remotes: 0,
  };
  let links: ConnectLink[] = [
    { id: rnd(8), label: "Notebook de Sofi (Chrome)", createdAt: new Date(Date.now() - 6 * 864e5).toISOString(), lastSeen: new Date(Date.now() - 3 * 36e5).toISOString(), ip: "181.45.12.9" },
  ];
  let shares: ShareLinkInfo[] = [];
  const changed = () => ctx.emit("connect.changed", {});
  return {
    "connect.status": () => ({ ...status }),
    "connect.enable": ({ enabled, signalURL, portalURL }) => {
      status.enabled = enabled;
      status.connected = enabled;
      if (signalURL) status.signal = signalURL;
      if (portalURL) status.portal = portalURL.replace(/\/+$/, "");
      changed();
      return { ...status };
    },
    "connect.pair.start": () => {
      if (!status.enabled) throw new RpcError("CONNECT_DISABLED", "Activá TangoConnect primero");
      const n = String(Math.floor(Math.random() * 1e9)).padStart(9, "0");
      return { code: `${n.slice(0, 3)} ${n.slice(3, 6)} ${n.slice(6)}`, expiresAt: new Date(Date.now() + 10 * 60e3).toISOString() };
    },
    "connect.links.list": () => links,
    "connect.links.revoke": ({ id }) => {
      if (!links.some((l) => l.id === id)) throw new RpcError("LINK_NOT_FOUND", "No existe ese vínculo");
      links = links.filter((l) => l.id !== id);
      changed();
      return {};
    },
    "shares.links.create": ({ path, expiresAt, password, maxDownloads, allowUpload }) => {
      if (allowUpload) throw new RpcError("NOT_IMPLEMENTED", "Los enlaces para subir archivos todavía no están disponibles");
      if (expiresAt && new Date(expiresAt).getTime() <= Date.now()) throw new RpcError("INVALID_PARAMS", "La fecha de vencimiento ya pasó");
      const id = rnd(8);
      const name = path.split("/").filter(Boolean).pop() ?? path;
      const l: ShareLinkInfo = {
        id, url: `${status.portal}/s/${DEVICE}.${id}#${rnd(12)}`, invite: `${DEVICE}.${id}`, code: "", owner: ctx.me(), path, name,
        kind: name.includes(".") ? "file" : "dir", size: 0, createdAt: new Date().toISOString(), expiresAt, hasPassword: !!password,
        maxDownloads: maxDownloads || undefined, downloads: 0, expired: false,
      };
      shares = [...shares, l];
      return l;
    },
    "shares.links.list": (p) => shares.filter((l) => (p && "all" in p && p.all) || l.owner === ctx.me()),
    "shares.links.revoke": ({ id }) => {
      if (!shares.some((l) => l.id === id)) throw new RpcError("SHARE_LINK_NOT_FOUND", "No existe ese enlace");
      shares = shares.filter((l) => l.id !== id);
      return {};
    },
  };
}
