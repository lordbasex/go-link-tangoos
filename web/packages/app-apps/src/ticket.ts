import type { InstalledApp, Transport } from "@tango/client";

/**
 * URL para abrir una app (protocol §12): si el NAS emite tickets, una URL de un solo uso que
 * deja una cookie de sesión en el proxy de esa app; si no, la URL directa del proxy.
 */
export async function appUrl(t: Transport, a: InstalledApp): Promise<string | null> {
  if (t.isReal("apps.ticket")) {
    try {
      return (await t.call("apps.ticket", { id: a.id })).url;
    } catch {
      /* sin ticket: probamos la URL directa, el proxy pedirá sesión si hace falta */
    }
  }
  return a.url;
}
