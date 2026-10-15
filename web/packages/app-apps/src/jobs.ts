import { useCallback, useState } from "react";
import { useTopic } from "@tango/client";
import type { Job } from "@tango/client";

export interface TrackedJob {
  job: Job;
  /** Líneas de log: cada cambio de `job.detail` es una línea nueva. */
  lines: string[];
}

/**
 * Sigue jobs por id a partir de `jobs.changed`. `track(key, id)` asocia un job a una clave
 * (p. ej. el id de la app) para mostrar su progreso en cualquier vista.
 */
export function useTrackedJobs() {
  const [byKey, setByKey] = useState<Record<string, string>>({});
  const [jobs, setJobs] = useState<Record<string, TrackedJob>>({});

  useTopic("jobs.changed", (e) => {
    const j = e.data;
    setJobs((m) => {
      const prev = m[j.id];
      if (!prev) return m; // solo los jobs que alguien registró con track()
      const lines = prev ? (prev.lines[prev.lines.length - 1] === j.detail ? prev.lines : [...prev.lines, j.detail]) : [j.detail];
      return { ...m, [j.id]: { job: j, lines } };
    });
  });

  const track = useCallback((key: string, id: string) => {
    setByKey((m) => ({ ...m, [key]: id }));
    setJobs((m) => (m[id] ? m : { ...m, [id]: { job: { id, type: "", title: "", detail: "Preparando…", progress: 0, state: "queued", startedAt: new Date().toISOString() }, lines: [] } }));
  }, []);

  const forget = useCallback((key: string) => setByKey(({ [key]: _, ...rest }) => rest), []);

  const get = (key: string): TrackedJob | undefined => {
    const id = byKey[key];
    return id ? jobs[id] : undefined;
  };

  return { track, forget, get };
}

export const isActive = (t?: TrackedJob) => !!t && (t.job.state === "queued" || t.job.state === "validating" || t.job.state === "running");
export const isFailed = (t?: TrackedJob) => !!t && (t.job.state === "failed" || t.job.state === "interrupted" || t.job.state === "cancelled");
