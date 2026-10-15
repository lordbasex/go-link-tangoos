import "./settings.css";
import { useEffect, useMemo, useState } from "react";
import type { ComponentType } from "react";
import { Bell, Cpu, FolderLock, Globe, Info, Keyboard, Network, Palette, RefreshCw, Server, ShieldCheck, Users } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useLive, useIsReal } from "@tango/client";
import { Avatar, WindowToolbar, ui } from "@tango/shell";
import type { AppProps } from "@tango/shell";
import { DemoBadge, SearchField } from "@tango/ui";
import { General } from "./General";
import { Appearance } from "./Appearance";
import { NetworkSection } from "./Network";
import { UsersSection } from "./Users";
import { SharesSection } from "./Shares";
import { Services } from "./Services";
import { Security } from "./Security";
import { ConnectSection } from "./Connect";
import { Notifications } from "./Notifications";
import { Updates } from "./Updates";
import { Hardware } from "./Hardware";
import { KeyboardSection } from "./Keyboard";

export interface SectionDef {
  id: string;
  title: string;
  icon: LucideIcon;
  color: string;
  keywords: string;
  component: ComponentType;
  group: number;
}

const SECTIONS: SectionDef[] = [
  { id: "general", title: "General", icon: Info, color: "#8E8E96", keywords: "acerca nas modelo cpu memoria ram serie versión debian kernel tiempo encendido", component: General, group: 0 },
  { id: "appearance", title: "Apariencia", icon: Palette, color: "#8B5CF6", keywords: "tema claro oscuro auto acento color fondo wallpaper dock tamaño magnificación widgets", component: Appearance, group: 0 },
  { id: "network", title: "Red", icon: Network, color: "#2F7CF6", keywords: "ip mac interfaz ethernet dns gateway hostname puerta de enlace", component: NetworkSection, group: 1 },
  { id: "users", title: "Usuarios y grupos", icon: Users, color: "#2F7CF6", keywords: "usuario cuenta grupo administrador 2fa cuota permisos", component: UsersSection, group: 1 },
  { id: "shares", title: "Carpetas compartidas", icon: FolderLock, color: "#F97316", keywords: "share carpeta compartida smb nfs permisos papelera instantáneas", component: SharesSection, group: 1 },
  { id: "services", title: "Servicios de archivos", icon: Server, color: "#22C55E", keywords: "smb samba time machine nfs sftp webdav iscsi descubrimiento mdns ws-discovery", component: Services, group: 1 },
  { id: "connect", title: "TangoConnect", icon: Globe, color: "#0EA5E9", keywords: "acceso remoto internet portal vincular navegador enlace público compartir señalización signalhub go-link", component: ConnectSection, group: 2 },
  { id: "security", title: "Seguridad", icon: ShieldCheck, color: "#E5484D", keywords: "sesiones 2fa firewall certificado tls https bloqueo", component: Security, group: 2 },
  { id: "notifications", title: "Notificaciones", icon: Bell, color: "#EF4444", keywords: "email correo ntfy telegram alertas", component: Notifications, group: 2 },
  { id: "updates", title: "Actualizaciones", icon: RefreshCw, color: "#8E8E96", keywords: "update apt debian paquetes seguridad versión", component: Updates, group: 2 },
  { id: "hardware", title: "Hardware y energía", icon: Cpu, color: "#EAB308", keywords: "ventilador ups energía apagado programado temperatura", component: Hardware, group: 3 },
  { id: "keyboard", title: "Atajos de teclado", icon: Keyboard, color: "#8E8E96", keywords: "teclado atajos shortcuts", component: KeyboardSection, group: 3 },
];

const norm = (s: string) => s.normalize("NFD").replace(/\p{Diacritic}/gu, "").toLowerCase();
const SIDE_W = 232;

export default function SettingsApp({ win }: AppProps) {
  const realData = useIsReal("system.info");
  const initial = typeof win.args.section === "string" && SECTIONS.some((s) => s.id === win.args.section) ? (win.args.section as string) : "general";
  const [current, setCurrent] = useState(initial);
  const [q, setQ] = useState("");
  const session = ui.use((s) => s.user);
  const users = useLive("users.list", undefined).data ?? [];
  const me = users.find((u) => u.name === session?.name) ?? users[0];

  // Abrir con otra sección mientras la ventana ya existe (singleInstance).
  useEffect(() => {
    if (typeof win.args.section === "string" && SECTIONS.some((s) => s.id === win.args.section)) setCurrent(win.args.section as string);
  }, [win.args.section]);

  const visible = useMemo(() => {
    const n = norm(q.trim());
    if (!n) return SECTIONS;
    return SECTIONS.filter((s) => n.split(/\s+/).every((w) => norm(`${s.title} ${s.keywords}`).includes(w)));
  }, [q]);

  useEffect(() => {
    if (visible.length && !visible.some((s) => s.id === current)) setCurrent(visible[0].id);
  }, [visible, current]);

  const sec = SECTIONS.find((s) => s.id === current) ?? SECTIONS[0];
  const Comp = sec.component;

  return (
    <div className="tg-app tset" style={{ ["--side" as string]: `${SIDE_W}px` }}>
      <WindowToolbar win={win} inset={SIDE_W}>
        <div className="tg-tb-title">{sec.title}</div>
        <div className="tg-tb-spacer" />
        {!realData && <DemoBadge />}
      </WindowToolbar>
      <aside className="tg-sidebar tg-app-side tset-side">
        <div className="tset-search">
          <SearchField value={q} onChange={setQ} placeholder="Buscar" />
        </div>
        {me && !q && (
          <button type="button" className="tset-me" onClick={() => setCurrent("users")}>
            <Avatar user={me} size={38} />
            <span>
              <strong>{me.full}</strong>
              <small>{me.admin ? "Administrador" : "Usuario"} · {me.email || me.name}</small>
            </span>
          </button>
        )}
        {visible.map((s, i) => (
          <div key={s.id}>
            {i > 0 && visible[i - 1].group !== s.group && !q && <div className="tset-gap" />}
            <button type="button" className="tg-side-item tset-item" aria-current={s.id === current || undefined} onClick={() => setCurrent(s.id)}>
              <span className="tset-icon" style={{ background: s.color }}>
                <s.icon size={13} strokeWidth={2.2} />
              </span>
              <span>{s.title}</span>
            </button>
          </div>
        ))}
        {visible.length === 0 && <div className="tset-none">Ningún ajuste coincide con «{q}».</div>}
      </aside>
      <div className="tg-app-main">
        <div className="tg-app-content">
          <div className="tset-pane" key={sec.id}>
            <Comp />
          </div>
        </div>
      </div>
    </div>
  );
}
