import { useCallback, useEffect, useRef, useState } from "react";
import { useTopic } from "@tango/client";
import type { Job } from "@tango/client";
import { Progress } from "@tango/ui";

/** Sigue un job por id (`jobs.changed`) y arma un log con cada cambio de `detail`. */
export function useJobLog(onEnd?: (j: Job) => void) {
  const [id, setId] = useState<string | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  const [lines, setLines] = useState<string[]>([]);
  const endRef = useRef(onEnd);
  endRef.current = onEnd;

  useTopic("jobs.changed", (e) => {
    const j = e.data;
    if (j.id !== id) return;
    setJob(j);
    setLines((l) => (j.detail && l[l.length - 1] !== j.detail ? [...l, j.detail] : l));
    if (j.state === "succeeded" || j.state === "failed" || j.state === "interrupted" || j.state === "cancelled") endRef.current?.(j);
  });

  const start = useCallback((jobId: string, first = "Preparando…") => {
    setId(jobId);
    setJob(null);
    setLines([first]);
  }, []);

  const reset = useCallback(() => {
    setId(null);
    setJob(null);
    setLines([]);
  }, []);

  const running = !!id && (!job || job.state === "queued" || job.state === "validating" || job.state === "running");
  const failed = !!job && (job.state === "failed" || job.state === "interrupted" || job.state === "cancelled");
  return { id, job, lines, running, failed, done: job?.state === "succeeded", start, reset };
}

/** Caja estilo terminal con el log del job y su barra de progreso. */
export function JobLog({ job, lines, failed }: { job: Job | null; lines: string[]; failed?: boolean }) {
  const box = useRef<HTMLPreElement>(null);
  useEffect(() => {
    box.current?.scrollTo({ top: box.current.scrollHeight });
  }, [lines.length]);
  return (
    <div className="tset-joblog">
      <Progress value={job?.progress ?? 0} tone={failed ? "crit" : job?.state === "succeeded" ? "ok" : undefined} indeterminate={!job} />
      <pre ref={box} aria-live="polite">
        {lines.map((l, i) => (
          <span key={i} data-last={i === lines.length - 1 || undefined}>
            {l}
            {"\n"}
          </span>
        ))}
      </pre>
    </div>
  );
}
