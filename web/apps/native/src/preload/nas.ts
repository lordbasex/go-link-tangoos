// Puente mínimo para el escritorio del NAS (window.tangoNative): el escritorio lo usa si existe.
import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("tangoNative", {
  platform: process.platform,
  /** Abre una carpeta compartida del NAS con el sistema (\\nas\carpeta o smb://nas/carpeta). */
  openShare: (share: string) => ipcRenderer.invoke("nas:openShare", share),
  /** Vuelve a la lista de NAS. */
  launcher: () => ipcRenderer.invoke("nas:launcher"),
});
