import { useEffect, useState } from "react";
import { X } from "lucide-react";
import { useJobs, useCancelJob } from "../../hooks/useJobs";
import { JobBadge, jobDuration } from "./JobBadge";
import { JobLog } from "./JobLog";

/**
 * JobsPanel shows the provisioning/operation jobs for a VM alongside a live log
 * viewer for the selected job. The most recent active job is auto-selected so
 * users watch provisioning progress without clicking.
 */
export function JobsPanel({ vmId }: { vmId: string }) {
  const { data: jobs } = useJobs(vmId);
  const cancel = useCancelJob();
  const [selected, setSelected] = useState<string | null>(null);

  // Auto-select the newest active job (or newest overall) until the user picks.
  const [userPicked, setUserPicked] = useState(false);
  useEffect(() => {
    if (userPicked || !jobs || jobs.length === 0) return;
    const activeJob = jobs.find((j) => j.status === "RUNNING" || j.status === "PENDING");
    setSelected((activeJob ?? jobs[0]).id);
  }, [jobs, userPicked]);

  return (
    <div className="grid h-full grid-cols-1 gap-4 overflow-hidden lg:grid-cols-[320px_1fr]">
      {/* Job list */}
      <div className="flex flex-col overflow-hidden rounded-md border border-border bg-surface">
        <div className="border-b border-border px-3 py-2 font-mono text-[10px] uppercase tracking-widest text-fg-dim">
          Jobs {jobs ? `(${jobs.length})` : ""}
        </div>
        <div className="flex-1 overflow-y-auto">
          {jobs && jobs.length > 0 ? (
            jobs.map((j) => (
              <button
                key={j.id}
                onClick={() => {
                  setSelected(j.id);
                  setUserPicked(true);
                }}
                className={`flex w-full items-center justify-between gap-2 border-b border-border/40 px-3 py-2.5 text-left transition-colors ${
                  selected === j.id ? "bg-elevated" : "hover:bg-surface-2"
                }`}
              >
                <div className="min-w-0">
                  <div className="truncate font-mono text-xs text-fg">{j.label || j.type}</div>
                  <div className="font-mono text-[10px] text-fg-dim">
                    {jobDuration(j.started_at, j.finished_at)}
                    {j.exit_code != null ? ` · exit ${j.exit_code}` : ""}
                  </div>
                </div>
                <div className="flex items-center gap-1.5">
                  <JobBadge status={j.status} />
                  {(j.status === "RUNNING" || j.status === "PENDING") && (
                    <span
                      role="button"
                      tabIndex={0}
                      onClick={(e) => {
                        e.stopPropagation();
                        cancel.mutate(j.id);
                      }}
                      className="text-fg-dim hover:text-error"
                      title="Cancel job"
                    >
                      <X className="h-3.5 w-3.5" />
                    </span>
                  )}
                </div>
              </button>
            ))
          ) : (
            <p className="px-3 py-4 font-mono text-xs text-fg-dim">no jobs yet</p>
          )}
        </div>
      </div>

      {/* Log viewer */}
      <div className="min-h-0 overflow-hidden">
        {selected ? (
          <JobLog jobId={selected} />
        ) : (
          <div className="flex h-full items-center justify-center rounded-md border border-border bg-black font-mono text-xs text-fg-dim">
            select a job to view its log
          </div>
        )}
      </div>
    </div>
  );
}
