// Errores del NAS en castellano, con el contexto que el usuario entiende (la carpeta, el archivo).
import { RpcError } from "@tango/client";

/** "/Fotos/2026/a.jpg" → "Fotos" */
export const shareOf = (path: string) => path.split("/").filter(Boolean)[0] ?? "";

export function errText(e: unknown, ctx: { path?: string; name?: string; write?: boolean } = {}): string {
  const code = e instanceof RpcError ? e.code : "";
  const share = ctx.path ? shareOf(ctx.path) : "";
  const what = ctx.name ? `«${ctx.name}»` : "el archivo";
  switch (code) {
    case "PERMISSION_DENIED":
      return share ? `No tenés permiso para ${ctx.write === false ? "ver" : "escribir en"} «${share}».` : "No tenés permiso para hacer eso.";
    case "PATH_NOT_FOUND":
      return `${what === "el archivo" ? "El archivo" : what} ya no existe. Puede que lo hayan movido o borrado desde otra sesión.`;
    case "EXISTS":
      return `Ya existe ${what} en esta carpeta.`;
    case "NO_SPACE":
      return share ? `No queda espacio en el volumen de «${share}».` : "No queda espacio en el volumen.";
    case "INVALID_NAME":
      return "Ese nombre no es válido: no puede tener «/» ni estar vacío.";
    case "CANCELLED":
      return "Cancelaste la transferencia.";
    case "DISCONNECTED":
      return "Se cortó la conexión con el NAS. Volvé a intentarlo cuando se reconecte.";
    case "XFER_FAILED":
    case "XFER_INCOMPLETE":
      return `La transferencia de ${what} se cortó antes de terminar.`;
    case "NOT_SUPPORTED":
      return "El NAS todavía no puede mostrar esto.";
    case "RATE_LIMITED":
      return "Demasiadas operaciones seguidas. Esperá un momento.";
    default:
      return e instanceof Error && e.message ? e.message : "Algo salió mal en el NAS.";
  }
}
