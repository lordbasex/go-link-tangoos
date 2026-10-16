// TangoOS para escritorio (ADR 0018): Electron abre el escritorio que sirve cada NAS (https://<nas>/),
// con su certificado fijado, o el portal TangoConnect para entrar desde fuera de la red.
import { BrowserWindow, Menu, Tray, app, ipcMain, nativeImage, session, shell, type Session } from "electron";
import path from "node:path";
import { discover, type Found } from "./discovery";
import { derOfPEM, fingerprintOfDER, parseAddress, probe } from "./pin";
import { NasStore, nasId, type SavedNas } from "./store";

const PORTAL = "https://tangoos.go-link.org";
const preload = (name: string) => path.join(__dirname, `preload-${name}.js`);
const assets = path.join(__dirname, "..", "assets");

let store: NasStore;
let launcher: BrowserWindow | null = null;
let tray: Tray | null = null;
let found: Found[] = [];
const nasWindows = new Map<string, BrowserWindow>();
const pinRefused = new Set<string>(); // hosts que presentaron otro certificado que el fijado
let quitting = false;

// Una sola instancia: abrir la app de nuevo trae el lanzador al frente.
if (!app.requestSingleInstanceLock()) app.quit();
app.on("second-instance", () => showLauncher());

/** Sesión propia por NAS: almacenamiento separado y el certificado fijado de ese NAS. */
function nasSession(n: SavedNas): Session {
  const ses = session.fromPartition(`persist:nas-${n.id}`);
  ses.setCertificateVerifyProc((req, cb) => {
    if (req.hostname === n.host) {
      let ok = false;
      try {
        ok = fingerprintOfDER(derOfPEM(req.certificate.data)) === n.fingerprint;
      } catch {
        ok = false;
      }
      if (ok) pinRefused.delete(n.host);
      else pinRefused.add(n.host);
      return cb(ok ? 0 : -2);
    }
    cb(-3); // otros hosts: la verificación normal de Chromium
  });
  // Solo lo que usa el escritorio: notificaciones y portapapeles.
  ses.setPermissionRequestHandler((_wc, perm, ok) => ok(["notifications", "clipboard-read", "clipboard-sanitized-write", "fullscreen"].includes(perm)));
  return ses;
}

/** Abre en el navegador del sistema solo http(s): nunca otros esquemas (file:, smb:, ms-…). */
function external(url: string) {
  try {
    const u = new URL(url);
    if (u.protocol === "https:" || u.protocol === "http:") void shell.openExternal(u.toString());
  } catch {
    /* URL inválida: se ignora */
  }
}

const sameNas = (n: SavedNas, url: string) => {
  try {
    const u = new URL(url);
    return u.protocol === "https:" && u.hostname === n.host;
  } catch {
    return false;
  }
};

function openNas(id: string) {
  const n = store.get(id);
  if (!n) return;
  const open = nasWindows.get(id);
  if (open && !open.isDestroyed()) {
    open.show();
    open.focus();
    return;
  }
  const ses = nasSession(n);
  const win = new BrowserWindow({
    width: 1440,
    height: 900,
    minWidth: 900,
    minHeight: 600,
    title: n.name,
    backgroundColor: "#1b1530",
    autoHideMenuBar: true,
    icon: path.join(assets, "icon.png"),
    webPreferences: { session: ses, preload: preload("nas"), contextIsolation: true, sandbox: true, nodeIntegration: false, spellcheck: false },
  });
  nasWindows.set(id, win);
  win.on("closed", () => nasWindows.delete(id));
  // Navegación: solo el NAS (mismo host, cualquier puerto: el proxy de apps usa otro). Lo demás, al navegador.
  win.webContents.on("will-navigate", (e, url) => {
    if (!sameNas(n, url)) {
      e.preventDefault();
      external(url);
    }
  });
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (sameNas(n, url)) return { action: "allow", overrideBrowserWindowOptions: { autoHideMenuBar: true, webPreferences: { session: ses, preload: preload("nas"), contextIsolation: true, sandbox: true } } };
    external(url);
    return { action: "deny" };
  });
  win.webContents.on("did-fail-load", (_e, code, desc, url) => {
    if (code === -3) return; // navegación cancelada
    void win.loadFile(path.join(__dirname, "..", "launcher", "error.html"), { query: { code: String(code), desc, url, name: n.name, cert: pinRefused.has(n.host) ? "1" : "" } });
  });
  void win.loadURL(`https://${n.host}${n.port === 443 ? "" : ":" + n.port}/`);
  store.touch(id);
  pushState();
  launcher?.hide();
}

function openPortal() {
  const ses = session.fromPartition("persist:portal");
  const win = new BrowserWindow({
    width: 1440,
    height: 900,
    title: "TangoConnect",
    backgroundColor: "#1b1530",
    autoHideMenuBar: true,
    webPreferences: { session: ses, preload: preload("nas"), contextIsolation: true, sandbox: true, nodeIntegration: false },
  });
  const inPortal = (url: string) => url.startsWith(PORTAL + "/");
  win.webContents.on("will-navigate", (e, url) => {
    if (!inPortal(url)) {
      e.preventDefault();
      external(url);
    }
  });
  win.webContents.setWindowOpenHandler(({ url }) => {
    external(url);
    return { action: "deny" };
  });
  void win.loadURL(PORTAL + "/nas");
  launcher?.hide();
}

function pushState() {
  launcher?.webContents.send("state", { saved: store.all(), found });
  rebuildTray();
}

function showLauncher() {
  if (launcher && !launcher.isDestroyed()) {
    launcher.show();
    launcher.focus();
    return;
  }
  launcher = new BrowserWindow({
    width: 980,
    height: 680,
    minWidth: 720,
    minHeight: 520,
    title: "TangoOS",
    backgroundColor: "#1b1530",
    autoHideMenuBar: true,
    icon: path.join(assets, "icon.png"),
    webPreferences: { preload: preload("launcher"), contextIsolation: true, sandbox: true, nodeIntegration: false },
  });
  launcher.on("closed", () => (launcher = null));
  launcher.webContents.on("will-navigate", (e) => e.preventDefault());
  launcher.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  void launcher.loadFile(path.join(__dirname, "..", "launcher", "index.html"));
}

function rebuildTray() {
  if (!tray) return;
  const saved = store.all();
  tray.setContextMenu(
    Menu.buildFromTemplate([
      ...saved.slice(0, 8).map((n) => ({ label: n.name, click: () => openNas(n.id) })),
      ...(saved.length ? [{ type: "separator" as const }] : []),
      { label: "TangoOS…", click: () => showLauncher() },
      { label: "TangoConnect…", click: () => openPortal() },
      { type: "separator" },
      { role: "quit" },
    ]),
  );
}

// ---- IPC del lanzador (solo desde la página local del lanzador) ----

function fromLauncher(e: Electron.IpcMainInvokeEvent) {
  if (!launcher || e.sender !== launcher.webContents || !e.senderFrame?.url.startsWith("file://")) throw new Error("origen no permitido");
}

ipcMain.handle("launcher:state", (e) => {
  fromLauncher(e);
  return { saved: store.all(), found, version: app.getVersion() };
});
ipcMain.handle("launcher:probe", async (e, address: string) => {
  fromLauncher(e);
  const { host, port } = parseAddress(String(address));
  const p = await probe(host, port);
  const known = store.byHost(host, port);
  return { ...p, known: known?.fingerprint ?? null };
});
ipcMain.handle("launcher:add", (e, a: { host: string; port: number; fingerprint: string; name: string }) => {
  fromLauncher(e);
  const { host, port } = parseAddress(`${a.host}:${a.port}`);
  if (!/^sha-256 ([0-9A-F]{2}:){31}[0-9A-F]{2}$/.test(a.fingerprint)) throw new Error("huella inválida");
  const name = String(a.name || host).slice(0, 64);
  const n: SavedNas = { id: nasId(host, port), name, host, port, fingerprint: a.fingerprint, addedAt: new Date().toISOString() };
  store.upsert(n);
  pushState();
  return n.id;
});
ipcMain.handle("launcher:remove", (e, id: string) => {
  fromLauncher(e);
  nasWindows.get(id)?.close();
  void session.fromPartition(`persist:nas-${id}`).clearStorageData();
  store.remove(String(id));
  pushState();
});
ipcMain.handle("launcher:open", (e, id: string) => {
  fromLauncher(e);
  openNas(String(id));
});
ipcMain.handle("launcher:portal", (e) => {
  fromLauncher(e);
  openPortal();
});

// ---- IPC de las ventanas de NAS (window.tangoNative) ----

ipcMain.handle("nas:openShare", (e, share: string) => {
  const n = [...nasWindows.entries()].find(([, w]) => w.webContents === e.sender);
  const saved = n && store.get(n[0]);
  if (!saved || !/^[\w .-]{1,64}$/.test(String(share))) throw new Error("carpeta inválida");
  const url = process.platform === "win32" ? `\\\\${saved.host}\\${share}` : `smb://${saved.host}/${encodeURIComponent(share)}`;
  return process.platform === "win32" ? shell.openPath(url) : shell.openExternal(url);
});
ipcMain.handle("nas:launcher", () => showLauncher());

app.whenReady().then(() => {
  store = new NasStore(path.join(app.getPath("userData"), "nas.json"));
  Menu.setApplicationMenu(null);
  try {
    tray = new Tray(nativeImage.createFromPath(path.join(assets, "tray.png")));
    tray.setToolTip("TangoOS");
    tray.on("click", () => showLauncher());
  } catch {
    tray = null; // sin bandeja (algunos escritorios de Linux): la app sigue igual
  }
  discover((all) => {
    found = all;
    pushState();
  });
  showLauncher();
  rebuildTray();
});

// Con la bandeja, cerrar las ventanas no cierra la app (como un cliente de sincronización);
// "Salir" (o el sistema) sí la termina.
app.on("before-quit", () => (quitting = true));
app.on("window-all-closed", () => {
  if (!tray || quitting) app.quit();
});
app.on("activate", () => showLauncher());
// Ninguna página puede crear webviews ni pedir permisos raros.
app.on("web-contents-created", (_e, wc) => wc.on("will-attach-webview", (ev) => ev.preventDefault()));
