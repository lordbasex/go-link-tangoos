import { EmptyState } from "@tango/ui";
import type { AppProps } from "@tango/shell";
import type { LucideIcon } from "lucide-react";

/** Apps del Dock que llegan en hitos posteriores: estado vacío diseñado, nunca una ventana rota. */
export function upcoming(icon: LucideIcon, title: string, body: string) {
  return function Upcoming(_: AppProps) {
    return (
      <div className="tg-app">
        <div className="tg-app-main" style={{ paddingTop: 34 }}>
          <div className="tg-app-content">
            <EmptyState icon={icon} title={title}>
              {body}
            </EmptyState>
          </div>
        </div>
      </div>
    );
  };
}
