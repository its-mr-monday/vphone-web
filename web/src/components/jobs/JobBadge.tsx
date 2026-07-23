import type { JobStatus } from "../../api/client";

const META: Record<JobStatus, { color: string; label: string }> = {
  PENDING: { color: "#8a8a94", label: "pending" },
  RUNNING: { color: "#00e5ff", label: "running" },
  COMPLETED: { color: "#00e676", label: "done" },
  FAILED: { color: "#ff1744", label: "failed" },
  CANCELLED: { color: "#ffab00", label: "cancelled" },
};

export function JobBadge({ status }: { status: JobStatus }) {
  const m = META[status];
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider"
      style={{ color: m.color, background: `${m.color}1a`, border: `1px solid ${m.color}40` }}
    >
      <span
        className={`inline-block h-1.5 w-1.5 rounded-full ${status === "RUNNING" ? "animate-pulse-dot" : ""}`}
        style={{ background: m.color }}
      />
      {m.label}
    </span>
  );
}

/** Duration string between two RFC3339 timestamps (or now). */
export function jobDuration(startedAt?: string, finishedAt?: string): string {
  if (!startedAt) return "—";
  const start = new Date(startedAt).getTime();
  const end = finishedAt ? new Date(finishedAt).getTime() : Date.now();
  const s = Math.max(0, Math.round((end - start) / 1000));
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${s % 60}s`;
}
