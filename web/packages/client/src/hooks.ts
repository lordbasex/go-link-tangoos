import { createContext, createElement, useCallback, useContext, useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { ReactNode } from "react";
import type { Transport, TransportState } from "./transport";
import type { Evt, Job, Method, MetricsFrame, Notice, Params, Result, Topic } from "./protocol";

const Ctx = createContext<Transport | null>(null);

export function TransportProvider({ transport, children }: { transport: Transport; children: ReactNode }) {
  return createElement(Ctx.Provider, { value: transport }, children);
}

export function useTransport(): Transport {
  const t = useContext(Ctx);
  if (!t) throw new Error("useTransport fuera de TransportProvider");
  return t;
}

export function useTransportState(): TransportState {
  const t = useTransport();
  return useSyncExternalStore((cb) => t.onState(cb), () => t.state());
}

export interface CallState<T> {
  data: T | undefined;
  error: Error | undefined;
  loading: boolean;
  reload: () => void;
}

/**
 * Patrón query + subscribe (docs/03 §6): pide el estado inicial y vuelve a pedirlo
 * cuando llega un evento de alguno de los topics. Reemplaza a TanStack Query.
 */
export function useLive<M extends Method>(method: M, params: Params<M>, topics: Topic[] = [], filter?: (e: Evt) => boolean): CallState<Result<M>> {
  const t = useTransport();
  const [data, setData] = useState<Result<M>>();
  const [error, setError] = useState<Error>();
  const [loading, setLoading] = useState(true);
  const key = JSON.stringify(params ?? null);
  const gen = useRef(0);
  const filterRef = useRef(filter);
  filterRef.current = filter;

  const load = useCallback(() => {
    const g = ++gen.current;
    t.call(method, params).then(
      (r) => {
        if (g !== gen.current) return;
        setData(r);
        setError(undefined);
        setLoading(false);
      },
      (e: Error) => {
        if (g !== gen.current) return;
        setError(e);
        setLoading(false);
      },
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t, method, key]);

  useEffect(() => {
    setLoading(true);
    load();
    let pending: ReturnType<typeof setTimeout> | undefined;
    const offs = topics.map((topic) =>
      t.subscribe(topic, (e) => {
        if (filterRef.current && !filterRef.current(e as Evt)) return;
        clearTimeout(pending);
        pending = setTimeout(load, 60); // debounce de ráfagas
      }),
    );
    return () => {
      clearTimeout(pending);
      offs.forEach((off) => off());
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load, topics.join("|")]);

  return { data, error, loading, reload: load };
}

export function useTopic<T extends Topic>(topic: T, fn: (e: Evt<T>) => void) {
  const t = useTransport();
  const ref = useRef(fn);
  ref.current = fn;
  useEffect(() => t.subscribe(topic, (e) => ref.current(e as Evt<T>)), [t, topic]);
}

// ---- Métricas: un único buffer compartido por todas las vistas ----------------------

const HISTORY = 4 * 60; // 60 s a 4 Hz
class MetricsStore {
  frames: MetricsFrame[] = [];
  private subs = new Set<() => void>();
  private off?: () => void;
  attach(t: Transport) {
    if (this.off) return;
    this.off = t.subscribe("metrics", (e) => {
      const next = this.frames.length >= HISTORY ? this.frames.slice(1) : this.frames.slice();
      next.push(e.data as MetricsFrame);
      this.frames = next;
      this.subs.forEach((f) => f());
    });
  }
  subscribe = (f: () => void) => {
    this.subs.add(f);
    return () => this.subs.delete(f);
  };
  get = () => this.frames;
}
const metrics = new MetricsStore();

/** Últimos 60 s de métricas (canal `metrics`, unordered). */
export function useMetrics(): MetricsFrame[] {
  const t = useTransport();
  metrics.attach(t);
  return useSyncExternalStore(metrics.subscribe, metrics.get);
}

// ---- Jobs y avisos ------------------------------------------------------------------

export function useJobs(): Job[] {
  const { data, reload } = useLive("jobs.list", undefined, ["jobs.changed"]);
  useEffect(() => void reload, [reload]);
  return data ?? [];
}

export function useNotices(): Notice[] {
  return useLive("notices.list", undefined, ["notice"]).data ?? [];
}

/** true si `method` lo responde el NAS real (no el mock): sirve para quitar la insignia DEMO. */
export function useIsReal(method: Method): boolean {
  const t = useTransport();
  return useSyncExternalStore(
    (cb) => t.onCapabilities(cb),
    () => t.isReal(method),
  );
}
