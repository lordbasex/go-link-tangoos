// "Mis NAS": NAS vinculados a este navegador. Vive en IndexedDB del origen del portal
// (nunca en cookies: nada de Domain=.go-link.org). El device_token es la llave del vínculo.

export interface LinkedNas {
  deviceId: string;
  /** Id del vínculo en el NAS (connect.link → linkId). */
  linkId: string;
  name: string;
  /** device_token emitido por connect.link (32 B base64url). */
  token: string;
  /** Huella DTLS que informó el NAS al vincular (hex, sin ":"); se usa para avisar si cambia. */
  fingerprint: string | null;
  linkedAt: string;
  lastSeen: string | null;
}

export interface NasBackend {
  all(): Promise<LinkedNas[]>;
  get(id: string): Promise<LinkedNas | undefined>;
  put(n: LinkedNas): Promise<void>;
  delete(id: string): Promise<void>;
}

/** Backend en memoria (tests y navegadores sin IndexedDB, p. ej. modo privado de algunos). */
export function memoryBackend(): NasBackend {
  const m = new Map<string, LinkedNas>();
  return {
    all: async () => [...m.values()].map((n) => ({ ...n })),
    get: async (id) => (m.has(id) ? { ...m.get(id)! } : undefined),
    put: async (n) => void m.set(n.deviceId, { ...n }),
    delete: async (id) => void m.delete(id),
  };
}

const DB = "tangoos-connect";
const STORE = "nas";

function req<T>(r: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => reject(r.error);
  });
}

export function indexedDbBackend(factory: IDBFactory = indexedDB): NasBackend {
  let dbp: Promise<IDBDatabase> | null = null;
  const db = () =>
    (dbp ??= new Promise((resolve, reject) => {
      const open = factory.open(DB, 1);
      open.onupgradeneeded = () => open.result.createObjectStore(STORE, { keyPath: "deviceId" });
      open.onsuccess = () => resolve(open.result);
      open.onerror = () => reject(open.error);
    }));
  const tx = async (mode: IDBTransactionMode) => (await db()).transaction(STORE, mode).objectStore(STORE);
  return {
    all: async () => req((await tx("readonly")).getAll() as IDBRequest<LinkedNas[]>),
    get: async (id) => req((await tx("readonly")).get(id) as IDBRequest<LinkedNas | undefined>),
    put: async (n) => void (await req((await tx("readwrite")).put(n))),
    delete: async (id) => void (await req((await tx("readwrite")).delete(id))),
  };
}

/** Elige IndexedDB si está disponible; si falla (bloqueado), cae a memoria. */
export function defaultBackend(): NasBackend {
  try {
    if (typeof indexedDB !== "undefined") return indexedDbBackend();
  } catch {
    /* IndexedDB bloqueado */
  }
  return memoryBackend();
}

export class NasStore {
  constructor(private readonly b: NasBackend = defaultBackend()) {}

  async list(): Promise<LinkedNas[]> {
    const all = await this.b.all();
    return all.sort((a, b) => (b.lastSeen ?? b.linkedAt).localeCompare(a.lastSeen ?? a.linkedAt));
  }

  get(id: string) {
    return this.b.get(id);
  }

  async save(n: LinkedNas) {
    if (!/^[0-9a-f-]{36}$/i.test(n.deviceId)) throw new Error("device_id inválido");
    if (!n.token || !n.linkId) throw new Error("falta el token del vínculo");
    await this.b.put(n);
  }

  async touch(id: string, patch: Partial<Pick<LinkedNas, "name" | "lastSeen" | "fingerprint">>) {
    const cur = await this.b.get(id);
    if (cur) await this.b.put({ ...cur, ...patch });
  }

  remove(id: string) {
    return this.b.delete(id);
  }
}
