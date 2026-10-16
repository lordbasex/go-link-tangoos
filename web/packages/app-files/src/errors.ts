// Errores del NAS en castellano, con el contexto que el usuario entiende (la carpeta, el archivo).
import { RpcError, t, tr } from "@tango/client";

/** "/Fotos/2026/a.jpg" → "Fotos" */
export const shareOf = (path: string) => path.split("/").filter(Boolean)[0] ?? "";

export function errText(e: unknown, ctx: { path?: string; name?: string; write?: boolean } = {}): string {
  const code = e instanceof RpcError ? e.code : "";
  const share = ctx.path ? shareOf(ctx.path) : "";
  const name = ctx.name;
  switch (code) {
    case "PERMISSION_DENIED":
      if (!share) return t("No tenés permiso para hacer eso.");
      return ctx.write === false ? t("No tenés permiso para ver «{share}».", { share }) : t("No tenés permiso para escribir en «{share}».", { share });
    case "PATH_NOT_FOUND":
      return name
        ? t("«{name}» ya no existe. Puede que lo hayan movido o borrado desde otra sesión.", { name })
        : t("El archivo ya no existe. Puede que lo hayan movido o borrado desde otra sesión.");
    case "EXISTS":
      return name ? t("Ya existe «{name}» en esta carpeta.", { name }) : t("Ya existe el archivo en esta carpeta.");
    case "NO_SPACE":
      return share ? t("No queda espacio en el volumen de «{share}».", { share }) : t("No queda espacio en el volumen.");
    case "INVALID_NAME":
      return t("Ese nombre no es válido: no puede tener «/» ni estar vacío.");
    case "CANCELLED":
      return t("Cancelaste la transferencia.");
    case "DISCONNECTED":
      return t("Se cortó la conexión con el NAS. Volvé a intentarlo cuando se reconecte.");
    case "XFER_FAILED":
    case "XFER_INCOMPLETE":
      return name ? t("La transferencia de «{name}» se cortó antes de terminar.", { name }) : t("La transferencia de el archivo se cortó antes de terminar.");
    case "NOT_SUPPORTED":
      return t("El NAS todavía no puede mostrar esto.");
    case "RATE_LIMITED":
      return t("Demasiadas operaciones seguidas. Esperá un momento.");
    default:
      if (e instanceof Error && e.message) return e instanceof RpcError ? e.message : tr(e.message);
      return t("Algo salió mal en el NAS.");
  }
}
