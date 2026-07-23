import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Download, Play, Square, RefreshCw, Bug, Search } from "lucide-react";
import { api, type VM, type FridaProcess } from "../../api/client";
import { useAuth } from "../../hooks/useAuth";
import { Button } from "../ui/Button";
import { JobLog } from "../jobs/JobLog";

/**
 * VMFrida is the Debug tab: manage the guest's frida-server (install / start /
 * stop) and enumerate running applications via the host frida-ps against the
 * forwarded frida port. Instrumentation scripting builds on this foundation.
 */
export function VMFrida({ vm }: { vm: VM }) {
  const running = vm.status === "RUNNING";
  const { isAdmin } = useAuth();
  const qc = useQueryClient();
  const [installJob, setInstallJob] = useState<string | null>(null);

  const status = useQuery({
    queryKey: ["frida", vm.id],
    queryFn: () => api.fridaStatus(vm.id),
    enabled: running,
    refetchInterval: 5000,
  });

  const start = useMutation({
    mutationFn: () => api.fridaStart(vm.id),
    onSuccess: (d) => qc.setQueryData(["frida", vm.id], d),
  });
  const stop = useMutation({
    mutationFn: () => api.fridaStop(vm.id),
    onSuccess: (d) => qc.setQueryData(["frida", vm.id], d),
  });
  const install = useMutation({
    mutationFn: () => api.fridaInstall(vm.id),
    onSuccess: (d) => setInstallJob(d.job_id),
  });

  const procs = useQuery({
    queryKey: ["frida-procs", vm.id],
    queryFn: () => api.fridaProcesses(vm.id),
    enabled: false, // fetched on demand
  });

  if (!running) {
    return <Empty text="Boot the VM to use Frida." />;
  }

  const st = status.data;

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto">
      {/* Server status + controls */}
      <div className="rounded-md border border-border bg-surface p-4">
        <div className="mb-3 flex items-center justify-between">
          <h3 className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-fg-dim">
            <Bug className="h-3.5 w-3.5" /> frida-server
          </h3>
          <button
            onClick={() => status.refetch()}
            className="text-fg-dim hover:text-accent"
            title="Refresh status"
          >
            <RefreshCw className={`h-3.5 w-3.5 ${status.isFetching ? "animate-spin" : ""}`} />
          </button>
        </div>

        <div className="flex flex-wrap items-center gap-x-6 gap-y-2 font-mono text-xs">
          <Stat label="Installed" value={st?.installed ? "yes" : "no"} good={st?.installed} />
          <Stat label="Running" value={st?.running ? "yes" : "no"} good={st?.running} />
          {st?.version && <Stat label="Version" value={st.version} />}
          <Stat label="Host port" value={st ? `127.0.0.1:${st.port}` : "—"} accent />
        </div>

        <div className="mt-4 flex flex-wrap gap-2">
          {isAdmin && (
            <Button
              variant="primary"
              icon={<Download className="h-3.5 w-3.5" />}
              disabled={install.isPending || !!installJob}
              onClick={() => install.mutate()}
            >
              {st?.installed ? "Reinstall" : "Install"}
            </Button>
          )}
          <Button
            variant="success"
            icon={<Play className="h-3.5 w-3.5" />}
            disabled={!st?.installed || st?.running || start.isPending}
            onClick={() => start.mutate()}
          >
            Start
          </Button>
          <Button
            variant="danger"
            icon={<Square className="h-3.5 w-3.5" />}
            disabled={!st?.running || stop.isPending}
            onClick={() => stop.mutate()}
          >
            Stop
          </Button>
        </div>

        {(start.error || stop.error || install.error) && (
          <div className="mt-3 rounded-sm border border-error/40 bg-error/10 px-3 py-2 font-mono text-[11px] text-error">
            {String(
              (start.error || stop.error || install.error) instanceof Error
                ? (start.error || stop.error || install.error as Error).message
                : "operation failed",
            )}
          </div>
        )}

        {installJob && (
          <div className="mt-3">
            <div className="mb-1 font-mono text-[10px] uppercase tracking-widest text-fg-dim">
              Install log
            </div>
            <div className="h-56 overflow-hidden rounded-sm border border-border">
              <JobLog jobId={installJob} />
            </div>
          </div>
        )}
      </div>

      {/* Process list */}
      <div className="flex min-h-0 flex-1 flex-col rounded-md border border-border bg-surface p-4">
        <div className="mb-3 flex items-center justify-between">
          <h3 className="font-mono text-[10px] uppercase tracking-widest text-fg-dim">
            Applications
          </h3>
          <Button
            variant="ghost"
            icon={<Search className="h-3.5 w-3.5" />}
            disabled={!st?.running || procs.isFetching}
            onClick={() => procs.refetch()}
          >
            {procs.isFetching ? "Scanning…" : "Enumerate"}
          </Button>
        </div>

        {!st?.running ? (
          <Empty text="Start frida-server to enumerate applications." small />
        ) : procs.error ? (
          <div className="rounded-sm border border-error/40 bg-error/10 px-3 py-2 font-mono text-[11px] text-error">
            {procs.error instanceof Error ? procs.error.message : "frida-ps failed"}
          </div>
        ) : procs.data ? (
          <ProcessTable procs={procs.data} />
        ) : (
          <Empty text="Click Enumerate to list running apps via frida-ps." small />
        )}
      </div>
    </div>
  );
}

function ProcessTable({ procs }: { procs: FridaProcess[] }) {
  if (procs.length === 0) return <Empty text="No applications reported." small />;
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <table className="w-full">
        <thead>
          <tr className="text-left font-mono text-[10px] uppercase tracking-widest text-fg-dim">
            <th className="w-16 py-1.5">PID</th>
            <th className="py-1.5">Name</th>
            <th className="py-1.5">Identifier</th>
          </tr>
        </thead>
        <tbody>
          {procs.map((p) => (
            <tr key={`${p.pid}-${p.name}`} className="border-t border-border/40 font-mono text-xs">
              <td className="py-1.5 text-accent">{p.pid}</td>
              <td className="py-1.5 text-fg">{p.name}</td>
              <td className="py-1.5 text-fg-dim">{p.identifier || "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Stat({ label, value, good, accent }: { label: string; value: string; good?: boolean; accent?: boolean }) {
  return (
    <div className="flex items-center gap-2">
      <span className="text-[10px] uppercase tracking-widest text-fg-dim">{label}</span>
      <span className={good ? "text-success" : accent ? "text-accent" : good === false ? "text-fg-muted" : "text-fg"}>
        {value}
      </span>
    </div>
  );
}

function Empty({ text, small }: { text: string; small?: boolean }) {
  return (
    <div className={`flex ${small ? "py-8" : "h-full"} items-center justify-center font-mono text-xs text-fg-dim`}>
      {text}
    </div>
  );
}
