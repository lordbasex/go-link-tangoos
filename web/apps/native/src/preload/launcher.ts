// Puente del lanzador (página local): solo estas funciones, nada de Node.
import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("tangoLauncher", {
  state: () => ipcRenderer.invoke("launcher:state"),
  probe: (address: string) => ipcRenderer.invoke("launcher:probe", address),
  add: (a: { host: string; port: number; fingerprint: string; name: string }) => ipcRenderer.invoke("launcher:add", a),
  remove: (id: string) => ipcRenderer.invoke("launcher:remove", id),
  open: (id: string) => ipcRenderer.invoke("launcher:open", id),
  portal: () => ipcRenderer.invoke("launcher:portal"),
  onState: (f: (s: unknown) => void) => {
    const h = (_e: unknown, s: unknown) => f(s);
    ipcRenderer.on("state", h);
    return () => ipcRenderer.removeListener("state", h);
  },
});
