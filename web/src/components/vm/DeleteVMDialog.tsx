import { useEffect, useState } from "react";
import { X, AlertTriangle, Loader2, CheckCircle2 } from "lucide-react";
import { ApiError, type VM } from "../../api/client";
import { useDeleteVM } from "../../hooks/useVM";
import { useJob } from "../../hooks/useJobs";
import { JobLog } from "../jobs/JobLog";
import { Button } from "../ui/Button";

/**
 * DeleteVMDialog confirms and then performs VM deletion as a tracked job,
 * streaming live progress (stop → remove disk → release ports → drop record).
 * When the delete job completes it invokes onDeleted so the caller can navigate
 * away. Managed VMs have their disk removed; imported VMs keep their directory.
 */
export function DeleteVMDialog({ vm, onClose, onDeleted }: { vm: VM; onClose: () => void; onDeleted: () => void }) {
  const del = useDeleteVM();
  const [jobId, setJobId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { data: job } = useJob(jobId ?? undefined);

  useEffect(() => {
    if (job?.status === "COMPLETED") {
      const t = setTimeout(onDeleted, 700); // let the final log line render
      return () => clearTimeout(t);
    }
  }, [job?.status, onDeleted]);

  async function start() {
    setError(null);
    try {
      const res = await del.mutateAsync(vm.id);
      if (res?.job_id) setJobId(res.job_id);
      else onDeleted(); // synchronous fallback
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "delete failed");
    }
  }

  const running = jobId !== null && job?.status !== "COMPLETED" && job?.status !== "FAILED";
  const done = job?.status === "COMPLETED";
  const failed = job?.status === "FAILED";

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm" onClick={jobId ? undefined : onClose}>
      <div onClick={(e) => e.stopPropagation()} className="w-[560px] rounded-md border border-error/40 bg-surface shadow-2xl">
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <div className="flex items-center gap-2">
            <AlertTriangle className="h-4 w-4 text-error" />
            <h2 className="font-mono text-sm uppercase tracking-wide text-fg">Destroy Device</h2>
          </div>
          {!running && (
            <button onClick={onClose} className="text-fg-dim hover:text-fg">
              <X className="h-4 w-4" />
            </button>
          )}
        </div>

        <div className="space-y-4 px-4 py-4">
          {!jobId ? (
            <>
              <p className="font-mono text-xs text-fg-muted">
                Permanently destroy <span className="text-fg">{vm.name}</span>? This will:
              </p>
              <ul className="space-y-1 font-mono text-[11px] text-fg-dim">
                <li>• stop the VM and kill its VNC/SSH tunnels</li>
                <li>• release its port block ({vm.port_block_base}–{vm.port_block_base + 9})</li>
                <li>• delete snapshots and the DB record</li>
                <li>• {isManagedDir(vm) ? "remove the VM directory and disk image" : "leave the imported directory on disk"}</li>
              </ul>
              <p className="rounded-sm border border-error/40 bg-error/10 px-3 py-2 font-mono text-[11px] text-error">
                This cannot be undone.
              </p>
              {error && <p className="font-mono text-xs text-error">{error}</p>}
            </>
          ) : (
            <>
              <div className="flex items-center gap-2 font-mono text-xs">
                {done ? (
                  <>
                    <CheckCircle2 className="h-4 w-4 text-success" />
                    <span className="text-success">deleted</span>
                  </>
                ) : failed ? (
                  <>
                    <AlertTriangle className="h-4 w-4 text-error" />
                    <span className="text-error">delete failed — see log</span>
                  </>
                ) : (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin text-error" />
                    <span className="text-fg-muted">deleting…</span>
                  </>
                )}
              </div>
              <div className="h-56">
                <JobLog jobId={jobId} />
              </div>
            </>
          )}
        </div>

        <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
          {!jobId ? (
            <>
              <Button variant="ghost" onClick={onClose}>Cancel</Button>
              <Button variant="danger" onClick={start} disabled={del.isPending}>
                {del.isPending ? "Starting…" : "Destroy"}
              </Button>
            </>
          ) : (
            <Button variant="ghost" onClick={done ? onDeleted : onClose} disabled={running}>
              {done ? "Close" : running ? "Working…" : "Close"}
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}

// isManagedDir mirrors the server's managed-directory check heuristically: a VM
// created through the app lives under the managed root; imported ones don't. We
// can't know the root client-side, so we show a neutral hint based on ipsw_id
// presence is unreliable — instead always describe both cases faithfully via the
// server. Here we default to "remove" unless the path looks like the CLI submodule.
function isManagedDir(vm: VM): boolean {
  return !vm.vm_dir.includes("/vphone-cli/");
}
