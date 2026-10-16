// Terminal de administración sobre el canal `term` (protocol §12). Solo admin; queda auditada.
import "@xterm/xterm/css/xterm.css";
import "./terminal.css";
import { useEffect, useRef, useState } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import type { ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { Copy, ClipboardPaste, RotateCcw, ShieldAlert } from "lucide-react";
import { RpcError, t, tr, useIsReal, useTransport } from "@tango/client";
import type { TermSession } from "@tango/client";
import { WindowToolbar, prefs, toast, useWindowMenus } from "@tango/shell";
import type { AppProps } from "@tango/shell";
import { Button, DemoBadge, IconButton } from "@tango/ui";

const DARK: ITheme = {
  background: "#16171c",
  foreground: "#e4e6eb",
  cursor: "#e4e6eb",
  selectionBackground: "#3a5a9a88",
  black: "#1c1d22",
  red: "#ff6b6e",
  green: "#3dd68c",
  yellow: "#f5b43c",
  blue: "#5b9bff",
  magenta: "#c08bff",
  cyan: "#4fd6d6",
  white: "#d6d8de",
  brightBlack: "#6b6e78",
  brightRed: "#ff8a8c",
  brightGreen: "#6ee7a8",
  brightYellow: "#ffd27a",
  brightBlue: "#8ab8ff",
  brightMagenta: "#d4adff",
  brightCyan: "#7ee8e8",
  brightWhite: "#ffffff",
};

const LIGHT: ITheme = {
  ...DARK,
  background: "#fbfbfd",
  foreground: "#1c1c1f",
  cursor: "#1c1c1f",
  selectionBackground: "#2f7cf633",
  black: "#1c1c1f",
  white: "#6b6e78",
  brightWhite: "#1c1c1f",
  yellow: "#b07300",
  brightYellow: "#d98a00",
  cyan: "#0e8a8a",
};

function isDark() {
  const forced = document.documentElement.getAttribute("data-theme");
  if (forced) return forced === "dark";
  return matchMedia("(prefers-color-scheme: dark)").matches;
}

export default function TerminalApp({ win }: AppProps) {
  const tp = useTransport();
  const real = useIsReal("term.open");
  const host = useRef<HTMLDivElement>(null);
  const term = useRef<XTerm | null>(null);
  const session = useRef<TermSession | null>(null);
  const [state, setState] = useState<"connecting" | "open" | "closed" | "error">("connecting");
  const [err, setErr] = useState<string | null>(null);
  const [gen, setGen] = useState(0);
  const mode = prefs.use((p) => p.mode);

  useEffect(() => {
    win.setTitle("Terminal");
  }, [win]);

  // Crear la terminal y abrir la sesión (se recrea con "Nueva sesión").
  useEffect(() => {
    if (!host.current) return;
    const x = new XTerm({
      fontFamily: 'ui-monospace, "SF Mono", Menlo, Consolas, "DejaVu Sans Mono", monospace',
      fontSize: 13,
      lineHeight: 1.2,
      cursorBlink: true,
      allowProposedApi: false,
      scrollback: 5000,
      theme: isDark() ? DARK : LIGHT,
    });
    const fit = new FitAddon();
    x.loadAddon(fit);
    x.open(host.current);
    term.current = x;
    try {
      fit.fit();
    } catch {
      /* contenedor todavía sin tamaño */
    }
    let s: TermSession | null = null;
    let disposed = false;
    const offs: (() => void)[] = [];
    setState("connecting");
    setErr(null);

    (async () => {
      if (!tp.terminals) throw new RpcError("NOT_IMPLEMENTED", "Este transporte no soporta terminales");
      s = await tp.terminals.open(x.cols, x.rows);
      if (disposed) return s.close();
      session.current = s;
      setState("open");
      const dec = new TextDecoder();
      offs.push(s.onData((c) => x.write(dec.decode(c, { stream: true }))));
      offs.push(
        s.onExit((reason) => {
          setState("closed");
          x.write(`\r\n\x1b[2m[${reason === "disconnected" ? t("Se cortó la conexión con el NAS") : t("Sesión terminada")}]\x1b[0m\r\n`);
        }),
      );
      const d = x.onData((data) => s?.write(data));
      offs.push(() => d.dispose());
      x.focus();
    })().catch((e: unknown) => {
      if (disposed) return;
      setState("error");
      setErr(e instanceof RpcError ? (e.code === "PERMISSION_DENIED" ? t("La terminal es solo para administradores.") : e.message) : tr(String(e)));
    });

    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
        session.current?.resize(x.cols, x.rows);
      } catch {
        /* ventana minimizada */
      }
    });
    ro.observe(host.current);

    return () => {
      disposed = true;
      ro.disconnect();
      offs.forEach((f) => f());
      s?.close();
      session.current = null;
      x.dispose();
      term.current = null;
    };
  }, [tp, gen]);

  // Tema según claro/oscuro.
  useEffect(() => {
    const apply = () => term.current && (term.current.options.theme = isDark() ? DARK : LIGHT);
    apply();
    const mq = matchMedia("(prefers-color-scheme: dark)");
    mq.addEventListener("change", apply);
    return () => mq.removeEventListener("change", apply);
  }, [mode]);

  const copySel = () => {
    const sel = term.current?.getSelection();
    if (!sel) return toast({ level: "info", title: t("No hay texto seleccionado"), app: "tango.terminal" });
    void navigator.clipboard?.writeText(sel);
  };
  const paste = async () => {
    try {
      const txt = await navigator.clipboard.readText();
      if (txt) session.current?.write(txt);
      term.current?.focus();
    } catch {
      toast({ level: "info", title: t("Pegá con el teclado"), body: t("Este navegador no deja leer el portapapeles desde un botón: usá Ctrl+Shift+V o ⌘V."), app: "tango.terminal" });
    }
  };

  useWindowMenus(
    win,
    [
      {
        label: "Shell",
        items: [
          { label: t("Nueva sesión"), action: () => setGen((g) => g + 1) },
          { separator: true },
          { label: t("Copiar"), action: copySel },
          { label: t("Pegar"), action: () => void paste() },
          { label: t("Limpiar pantalla"), action: () => term.current?.clear() },
        ],
      },
    ],
    [],
  );

  return (
    <div className="tterm">
      <WindowToolbar win={win}>
        <span className="tg-tb-title">Terminal</span>
        <span className="tterm-admin" title={t("Todo lo que hagas acá queda en el registro de auditoría")}>
          <ShieldAlert size={13} /> {t("Administrador · auditada")}
        </span>
        <span className="tg-tb-spacer" />
        {!real && <DemoBadge />}
        <IconButton icon={Copy} label={t("Copiar selección")} onClick={copySel} />
        <IconButton icon={ClipboardPaste} label={t("Pegar")} onClick={() => void paste()} />
        <IconButton icon={RotateCcw} label={t("Nueva sesión")} onClick={() => setGen((g) => g + 1)} />
      </WindowToolbar>
      <div className="tterm-body">
        <div className="tterm-host" ref={host} />
        {state === "error" && (
          <div className="tterm-overlay">
            <ShieldAlert size={30} />
            <strong>{t("No se pudo abrir la terminal")}</strong>
            <p>{err}</p>
            <Button onClick={() => setGen((g) => g + 1)}>{t("Reintentar")}</Button>
          </div>
        )}
        {state === "closed" && (
          <div className="tterm-reopen">
            <Button onClick={() => setGen((g) => g + 1)}>{t("Abrir una sesión nueva")}</Button>
          </div>
        )}
      </div>
    </div>
  );
}
