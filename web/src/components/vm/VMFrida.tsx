import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Download, Play, Square, RefreshCw, Bug, Search, Copy, Check, Plug, Settings2 } from "lucide-react";
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
  const setPort = useMutation({
    mutationFn: (port: number) => api.fridaSetPort(vm.id, port),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["frida", vm.id] });
      qc.invalidateQueries({ queryKey: ["vms", vm.id] });
    },
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
          {st && (
            <PortEditor
              port={st.port}
              editable={isAdmin}
              pending={setPort.isPending}
              onSave={(p) => setPort.mutate(p)}
            />
          )}
        </div>
        {setPort.error && (
          <div className="mt-2 font-mono text-[11px] text-error">
            {setPort.error instanceof Error ? setPort.error.message : "failed to set port"}
          </div>
        )}

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

      {/* Remote access — connect external Frida tooling directly. */}
      {st?.running && <RemoteAccess port={st.port} />}

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

function PortEditor({
  port,
  editable,
  pending,
  onSave,
}: {
  port: number;
  editable: boolean;
  pending: boolean;
  onSave: (port: number) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(String(port));

  if (!editable || !editing) {
    return (
      <div className="flex items-center gap-2">
        <span className="text-[10px] uppercase tracking-widest text-fg-dim">Forward port</span>
        <span className="text-accent">127.0.0.1:{port}</span>
        {editable && (
          <button
            onClick={() => {
              setValue(String(port));
              setEditing(true);
            }}
            className="text-fg-dim hover:text-accent"
            title="Change the forwarded Frida port"
          >
            <Settings2 className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
    );
  }

  return (
    <div className="flex items-center gap-2">
      <span className="text-[10px] uppercase tracking-widest text-fg-dim">Forward port</span>
      <input
        autoFocus
        type="number"
        min={1024}
        max={65535}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && (onSave(Number(value)), setEditing(false))}
        className="w-24 rounded-sm border border-border bg-base px-2 py-0.5 text-xs text-fg outline-none focus:border-accent"
      />
      <button
        onClick={() => {
          onSave(Number(value));
          setEditing(false);
        }}
        disabled={pending}
        className="rounded-sm border border-accent/60 bg-accent/10 px-2 py-0.5 text-[10px] uppercase text-accent"
      >
        {pending ? "…" : "Save"}
      </button>
      <button onClick={() => setEditing(false)} className="text-[10px] uppercase text-fg-dim hover:text-fg">
        Cancel
      </button>
    </div>
  );
}

function RemoteAccess({ port }: { port: number }) {
  // The address the operator reached this UI on is, by construction, a host that
  // can route to the vphone-web server; the Frida port is bound on 0.0.0.0.
  const host = window.location.hostname || "127.0.0.1";
  const target = `${host}:${port}`;
  const examples: [string, string][] = [
    ["List apps", `frida-ps -H ${target} -a`],
    ["Attach REPL", `frida -H ${target} -n SpringBoard`],
    ["Spawn + trace", `frida-trace -H ${target} -f com.apple.mobilesafari -i "open*"`],
    ["Python", `frida.get_device_manager().add_remote_device("${target}")`],
  ];
  return (
    <div className="rounded-md border border-border bg-surface p-4">
      <h3 className="mb-2 flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-fg-dim">
        <Plug className="h-3.5 w-3.5" /> Remote access
      </h3>
      <p className="mb-3 font-mono text-[11px] text-fg-dim">
        frida-server is exposed on this host. Point any Frida tool at it from your machine:
      </p>
      <CopyRow label="Target" value={target} />
      <div className="mt-3 space-y-1.5">
        {examples.map(([label, cmd]) => (
          <CopyRow key={label} label={label} value={cmd} mono />
        ))}
      </div>
    </div>
  );
}

function CopyRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard?.writeText(value).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    });
  };
  return (
    <div className="flex items-center gap-2">
      <span className="w-24 shrink-0 font-mono text-[10px] uppercase tracking-widest text-fg-dim">{label}</span>
      <code className={`flex-1 overflow-x-auto whitespace-nowrap rounded-sm border border-border bg-base px-2 py-1 text-xs text-accent ${mono ? "" : ""}`}>
        {value}
      </code>
      <button
        onClick={copy}
        className="shrink-0 rounded-sm border border-border p-1.5 text-fg-dim hover:text-accent"
        title="Copy"
      >
        {copied ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}
      </button>
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
