import "./i18n.en";
import "./ui.css";
import { useEffect, useId, useRef } from "react";
import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from "react";
import { AlertTriangle, CheckCircle2, Info, Search, XOctagon } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { t } from "@tango/client";

export type Tone = "ok" | "warn" | "crit" | "info";

export function Button({ variant, size, ...p }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "danger" | "plain"; size?: "lg" }) {
  return <button type="button" className="tg-btn" data-variant={variant} data-size={size} {...p} />;
}

export function IconButton({ icon: Icon, label, pressed, ...p }: ButtonHTMLAttributes<HTMLButtonElement> & { icon: LucideIcon; label: string; pressed?: boolean }) {
  return (
    <button type="button" className="tg-iconbtn" aria-label={label} title={label} aria-pressed={pressed} {...p}>
      <Icon size={16} strokeWidth={1.8} />
    </button>
  );
}

export function Segmented<T extends string>({ value, onChange, options, label }: { value: T; onChange: (v: T) => void; options: { value: T; label?: string; icon?: LucideIcon }[]; label: string }) {
  return (
    <div className="tg-seg" role="group" aria-label={label}>
      {options.map((o) => (
        <button key={o.value} type="button" aria-pressed={o.value === value} aria-label={o.label ?? o.value} title={o.label} onClick={() => onChange(o.value)}>
          {o.icon && <o.icon size={14} strokeWidth={1.9} />}
          {o.icon ? null : o.label}
        </button>
      ))}
    </div>
  );
}

export function Toggle({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return <button type="button" role="switch" className="tg-toggle" aria-checked={checked} aria-label={label} disabled={disabled} onClick={() => onChange(!checked)} />;
}

export function TextInput({ size, ...p }: Omit<InputHTMLAttributes<HTMLInputElement>, "size"> & { size?: "lg" }) {
  return <input className="tg-input" data-size={size} spellCheck={false} {...p} />;
}

export function SearchField({ value, onChange, placeholder: ph, autoFocus }: { value: string; onChange: (v: string) => void; placeholder?: string; autoFocus?: boolean }) {
  const placeholder = ph ?? t("Buscar");
  return (
    <label className="tg-search">
      <Search size={14} strokeWidth={2} />
      <input value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} autoFocus={autoFocus} aria-label={placeholder} />
    </label>
  );
}

export function Badge({ tone, children }: { tone?: Tone | "demo"; children: ReactNode }) {
  return (
    <span className="tg-badge" data-tone={tone}>
      {children}
    </span>
  );
}

/** Insignia obligatoria en toda vista con datos simulados (docs/13 §8). */
export function DemoBadge() {
  return (
    <span className="tg-badge" data-tone="demo" title={t("Datos simulados: este prototipo no toca discos reales")}>
      DEMO
    </span>
  );
}

const toneIcon: Record<Tone, LucideIcon> = { ok: CheckCircle2, warn: AlertTriangle, crit: XOctagon, info: Info };

/** Estado con ícono + texto: nunca solo color. */
export function Status({ tone, children, size = 14 }: { tone: Tone; children: ReactNode; size?: number }) {
  const I = toneIcon[tone];
  return (
    <span className="tg-status" data-tone={tone}>
      <I size={size} strokeWidth={2.2} />
      {children}
    </span>
  );
}

export function Progress({ value, tone, indeterminate }: { value?: number; tone?: Tone; indeterminate?: boolean }) {
  return (
    <div className="tg-progress" role="progressbar" aria-valuenow={value === undefined ? undefined : Math.round(value * 100)} aria-valuemin={0} aria-valuemax={100} data-indeterminate={indeterminate || undefined}>
      <div style={{ width: `${(value ?? 0) * 100}%`, background: tone ? `var(--tg-${tone})` : undefined }} />
    </div>
  );
}

/** Anillo de progreso (uso de volumen, CPU…). */
export function Ring({ value, size = 64, stroke = 7, color = "var(--tg-accent)", track = "var(--tg-track)", children }: { value: number; size?: number; stroke?: number; color?: string; track?: string; children?: ReactNode }) {
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  return (
    <div style={{ position: "relative", width: size, height: size, flex: "none" }}>
      <svg width={size} height={size} style={{ transform: "rotate(-90deg)", display: "block" }} aria-hidden="true">
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke={track} strokeWidth={stroke} />
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke={color} strokeWidth={stroke} strokeLinecap="round" strokeDasharray={c} strokeDashoffset={c * (1 - Math.min(1, Math.max(0, value)))} style={{ transition: "stroke-dashoffset 400ms var(--tg-ease)" }} />
      </svg>
      {children && <div style={{ position: "absolute", inset: 0, display: "grid", placeItems: "center", textAlign: "center" }}>{children}</div>}
    </div>
  );
}

/** Gráfico de área/línea SVG sin dependencias. `values` en 0..max. */
export function AreaChart({ values, max, width = 200, height = 48, color = "var(--tg-accent)", fill = true, strokeWidth = 1.6, secondary }: { values: number[]; max?: number; width?: number; height?: number; color?: string; fill?: boolean; strokeWidth?: number; secondary?: { values: number[]; color: string } }) {
  const gid = useId().replace(/:/g, "");
  const top = max ?? Math.max(1e-9, ...values, ...(secondary?.values ?? []));
  const path = (vs: number[]) => {
    if (vs.length < 2) return "";
    const step = width / (vs.length - 1);
    return vs.map((v, i) => `${i ? "L" : "M"}${(i * step).toFixed(1)},${(height - (Math.min(v, top) / top) * (height - strokeWidth) - strokeWidth / 2).toFixed(1)}`).join("");
  };
  const d = path(values);
  return (
    <svg width="100%" height={height} viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" style={{ display: "block", overflow: "visible" }} aria-hidden="true">
      <defs>
        <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor={color} stopOpacity="0.35" />
          <stop offset="1" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      {fill && d && <path d={`${d}L${width},${height}L0,${height}Z`} fill={`url(#${gid})`} />}
      {secondary && <path d={path(secondary.values)} fill="none" stroke={secondary.color} strokeWidth={strokeWidth} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />}
      {d && <path d={d} fill="none" stroke={color} strokeWidth={strokeWidth} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />}
    </svg>
  );
}

export function SidebarItem({ icon: Icon, label, current, meta, onClick, disabled, color }: { icon?: LucideIcon; label: string; current?: boolean; meta?: ReactNode; onClick?: () => void; disabled?: boolean; color?: string }) {
  return (
    <button type="button" className="tg-side-item" aria-current={current || undefined} onClick={onClick} disabled={disabled}>
      {Icon && <Icon size={16} strokeWidth={1.8} style={color ? { color } : undefined} />}
      <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{label}</span>
      {meta !== undefined && <span className="tg-side-meta">{meta}</span>}
    </button>
  );
}

export function EmptyState({ icon: Icon, title, children, action }: { icon: LucideIcon; title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="tg-empty">
      <Icon size={44} strokeWidth={1.3} />
      <strong>{title}</strong>
      {children && <p>{children}</p>}
      {action && <div style={{ marginTop: 8 }}>{action}</div>}
    </div>
  );
}

/** Hoja modal que baja desde la barra de título de la ventana (estilo macOS). */
export function Sheet({ open, onClose, children, footer, width }: { open: boolean; onClose: () => void; children: ReactNode; footer?: ReactNode; width?: number }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const prev = document.activeElement as HTMLElement | null;
    const el = ref.current;
    el?.querySelector<HTMLElement>("input,button:not([disabled]),[tabindex]")?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
      }
    };
    el?.addEventListener("keydown", onKey);
    return () => {
      el?.removeEventListener("keydown", onKey);
      prev?.focus?.();
    };
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div className="tg-sheet-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="tg-sheet" role="dialog" aria-modal="true" ref={ref} style={width ? { width: `min(${width}px, calc(100% - 40px))` } : undefined}>
        <div className="tg-sheet-body">{children}</div>
        {footer && <div className="tg-sheet-foot">{footer}</div>}
      </div>
    </div>
  );
}

export function Group({ title, children }: { title?: ReactNode; children: ReactNode }) {
  return (
    <>
      {title && <div className="tg-group-title">{title}</div>}
      <div className="tg-group">{children}</div>
    </>
  );
}

export function Row({ label, hint, children, icon }: { label: ReactNode; hint?: ReactNode; children?: ReactNode; icon?: ReactNode }) {
  return (
    <div className="tg-row">
      {icon}
      <div className="tg-row-label">
        {label}
        {hint && <small>{hint}</small>}
      </div>
      {children}
    </div>
  );
}
