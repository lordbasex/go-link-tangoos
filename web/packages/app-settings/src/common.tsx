import type { ReactNode } from "react";
import { toast } from "@tango/shell";

export function demoApply(what = "El cambio") {
  toast({ level: "info", title: "Demo: no se aplicó", body: `${what} se ve como quedaría, pero este prototipo no modifica el NAS.`, app: "tango.settings" });
}

export function Head({ title, children, icon }: { title: string; children?: ReactNode; icon?: ReactNode }) {
  return (
    <div className="tset-head">
      {icon}
      <div>
        <h1 className="tg-h1">{title}</h1>
        {children && <p className="tg-lead">{children}</p>}
      </div>
    </div>
  );
}

export function Soon() {
  return <span className="tset-soon">Pronto</span>;
}

export function Value({ children }: { children: ReactNode }) {
  return <span className="tset-value">{children}</span>;
}

export function Bar({ value, tone }: { value: number; tone?: "warn" | "crit" }) {
  return (
    <span className="tset-bar">
      <span style={{ width: `${Math.min(1, value) * 100}%`, background: tone ? `var(--tg-${tone})` : undefined }} />
    </span>
  );
}
