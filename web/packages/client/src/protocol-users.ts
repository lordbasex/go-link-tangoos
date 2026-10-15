// Cierre v1.0 (protocol/README.md §14): borrar usuarios y contraseñas de app. Se suma a
// Methods/Topics por module augmentation, como protocol-v1.ts.

export interface AppPassword {
  id: string;
  label: string;
  created: string;
  lastUsed: string | null;
  /** Es la contraseña SMB del usuario (solo una a la vez). */
  smb: boolean;
}

declare module "./protocol" {
  interface Methods {
    "users.delete": { params: { name: string; typed_name: string; keepFiles?: boolean }; result: Record<string, never> };
    "users.appPasswords.list": { params: { name?: string }; result: AppPassword[] };
    "users.appPasswords.create": { params: { label: string; smb?: boolean }; result: AppPassword & { password: string } };
    "users.appPasswords.revoke": { params: { id: string; name?: string }; result: Record<string, never> };
  }
  interface Topics {
    "users.changed": { user?: string };
  }
}
