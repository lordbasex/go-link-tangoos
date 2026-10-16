import { Fragment } from "react";
import type { ReactNode } from "react";
import { t } from "@tango/client";
import { toast } from "@tango/shell";

export function demoApply(what = t("El cambio")) {
  toast({ level: "info", title: t("Demo: no se aplicó"), body: t("{what} se ve como quedaría, pero este prototipo no modifica el NAS.", { what }), app: "tango.settings" });
}

/** t() con nodos de React en lugar de {nombre}: tj("Abrí {path}.", { path: <b>…</b> }). */
export function tj(es: string, nodes: Record<string, ReactNode>): ReactNode {
  return t(es)
    .split(/(\{\w+\})/)
    .map((part, i) => {
      const m = /^\{(\w+)\}$/.exec(part);
      return m && m[1] in nodes ? <Fragment key={i}>{nodes[m[1]]}</Fragment> : part;
    });
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
  return <span className="tset-soon">{t("Pronto")}</span>;
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
