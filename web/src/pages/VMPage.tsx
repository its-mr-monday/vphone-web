import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useParams, Link, useSearchParams } from "react-router-dom";
import { ChevronLeft, Monitor, Info, TerminalSquare, Camera, ListChecks, Settings } from "lucide-react";
import { api } from "../api/client";
import { useVM } from "../hooks/useVM";
import { useAuth } from "../hooks/useAuth";
import { VMSettingsDialog } from "../components/vm/VMSettingsDialog";
import { useJobs } from "../hooks/useJobs";
import { VMDisplay } from "../components/vm/VMDisplay";
import { VMControls } from "../components/vm/VMControls";
import { VMHardwareBar } from "../components/vm/VMHardwareBar";
import { VMTerminal } from "../components/vm/VMTerminal";
import { VMSnapshots } from "../components/vm/VMSnapshots";
import { JobsPanel } from "../components/jobs/JobsPanel";
import { StatusDot } from "../components/ui/StatusDot";
import type { VM } from "../api/client";

type Tab = "display" | "terminal" | "info" | "snapshots" | "jobs";

export function VMPage() {
  const { id } = useParams<{ id: string }>();
  const { data: vm, isLoading, error } = useVM(id);
  const [params, setParams] = useSearchParams();
  const [tab, setTabState] = useState<Tab>((params.get("tab") as Tab) || "display");
  const { data: jobs } = useJobs(id);

  const setTab = (t: Tab) => {
    setTabState(t);
    setParams((p) => {
      p.set("tab", t);
      return p;
    });
  };

  if (isLoading) return <Centered text="loading device…" />;
  if (error || !vm) return <Centered text="device not found" />;

  const running = vm.status === "RUNNING";
  const activeJobs = jobs?.filter((j) => j.status === "RUNNING" || j.status === "PENDING").length ?? 0;

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-border px-6 py-3">
        <div className="flex items-center gap-3">
          <Link to="/" className="text-fg-dim hover:text-fg">
            <ChevronLeft className="h-5 w-5" />
          </Link>
          <StatusDot status={vm.status} size={10} />
          <div>
            <div className="flex items-center gap-2">
              <h1 className="font-mono text-sm text-fg">{vm.name}</h1>
              <span className="rounded-sm border border-border px-1.5 py-0.5 font-mono text-[10px] uppercase text-fg-dim">
                {vm.variant}
              </span>
              <StatusDot status={vm.status} showLabel />
            </div>
            <div className="font-mono text-[10px] text-fg-dim">
              {vm.ios_version || "no firmware"} · {vm.id.slice(0, 8)}
            </div>
          </div>
        </div>
        <VMControls vm={vm} />
      </div>

      {/* Error banner */}
      {vm.status === "ERROR" && vm.error_message && (
        <div className="border-b border-error/30 bg-error/10 px-6 py-2">
          <pre className="max-h-24 overflow-auto whitespace-pre-wrap font-mono text-[11px] text-error">
            {vm.error_message}
          </pre>
        </div>
      )}

      {/* Tabs */}
      <div className="flex gap-1 border-b border-border px-4">
        <TabButton active={tab === "display"} onClick={() => setTab("display")} icon={<Monitor className="h-3.5 w-3.5" />}>
          Display
        </TabButton>
        <TabButton active={tab === "terminal"} onClick={() => setTab("terminal")} icon={<TerminalSquare className="h-3.5 w-3.5" />}>
          Terminal
        </TabButton>
        <TabButton active={tab === "info"} onClick={() => setTab("info")} icon={<Info className="h-3.5 w-3.5" />}>
          Info
        </TabButton>
        <TabButton active={tab === "snapshots"} onClick={() => setTab("snapshots")} icon={<Camera className="h-3.5 w-3.5" />}>
          Snapshots
        </TabButton>
        <TabButton active={tab === "jobs"} onClick={() => setTab("jobs")} icon={<ListChecks className="h-3.5 w-3.5" />} badge={activeJobs}>
          Jobs
        </TabButton>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-hidden p-4">
        {tab === "display" && (
          <div className="flex h-full flex-col gap-3">
            <div className="min-h-0 flex-1">
              <VMDisplay vm={vm} />
            </div>
            <VMHardwareBar vmId={vm.id} disabled={!running} />
          </div>
        )}
        {tab === "terminal" && <VMTerminal vmId={vm.id} active={running} />}
        {tab === "info" && <InfoPanel vm={vm} />}
        {tab === "snapshots" && <VMSnapshots vm={vm} />}
        {tab === "jobs" && <JobsPanel vmId={vm.id} />}
      </div>
    </div>
  );
}

function InfoPanel({ vm }: { vm: VM }) {
  const running = vm.status === "RUNNING";
  const { isAdmin } = useAuth();
  const [editing, setEditing] = useState(false);
  // Config is only editable while the VM is fully stopped.
  const editable = isAdmin && vm.status === "STOPPED";
  // Live guest metadata (IP address, reported iOS) from the control socket.
  const { data: guest } = useQuery({
    queryKey: ["vm-info", vm.id],
    queryFn: () => api.guestInfo(vm.id),
    enabled: running,
    // The guest reports its IP only after its daemon connects; poll until known.
    refetchInterval: (q) => ((q.state.data as { ip?: string } | undefined)?.ip ? 30000 : 3000),
    retry: false,
  });

  const ipAddress = running
    ? guest?.ip ?? (guest ? "acquiring…" : "waiting for guest…")
    : "—";

  const rows: [string, string][] = [
    ["ID", vm.id],
    ["Name", vm.name],
    ["Status", vm.status],
    ["IP Address", ipAddress],
    ["Variant", vm.variant],
    ["iOS Version", guest?.ios || vm.ios_version || "—"],
    [
      "Network",
      vm.network_mode === "bridged"
        ? `Bridged (LAN)${vm.network_interface ? ` · ${vm.network_interface}` : ""}`
        : vm.network_mode || "nat",
    ],
    ["CPU cores", String(vm.cpu)],
    ["Memory", `${vm.memory} MiB`],
    ["Disk", `${vm.disk_size} MiB`],
    ["Screen", `${vm.screen_width}×${vm.screen_height}`],
    ["VM directory", vm.vm_dir],
    ["PID", vm.pid ? String(vm.pid) : "—"],
    ["Created", new Date(vm.created_at).toLocaleString()],
    ["Updated", new Date(vm.updated_at).toLocaleString()],
  ];
  const ports: [string, number][] = [
    ["VNC", vm.ports.vnc],
    ["SSH", vm.ports.ssh],
    ["SSH (alt)", vm.ports.ssh2],
    ["RPC", vm.ports.rpc],
  ];

  return (
    <div className="grid h-full grid-cols-1 gap-4 overflow-y-auto lg:grid-cols-2">
      <Section
        title="Configuration"
        action={
          isAdmin ? (
            <button
              onClick={() => editable && setEditing(true)}
              disabled={!editable}
              title={
                editable
                  ? "Edit configuration"
                  : "Stop the VM to edit its configuration"
              }
              className={`-my-1 rounded-sm p-1 transition-colors ${
                editable
                  ? "text-fg-dim hover:bg-border/40 hover:text-accent"
                  : "cursor-not-allowed text-fg-muted/40"
              }`}
            >
              <Settings className="h-3.5 w-3.5" />
            </button>
          ) : null
        }
      >
        <table className="w-full">
          <tbody>
            {rows.map(([k, v]) => (
              <tr key={k} className="border-b border-border/50">
                <td className="py-1.5 pr-4 font-mono text-[10px] uppercase tracking-widest text-fg-dim">{k}</td>
                <td className="break-all py-1.5 font-mono text-xs text-fg">{v}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Section>
      <Section title="Port Assignments">
        <table className="w-full">
          <tbody>
            {ports.map(([k, v]) => (
              <tr key={k} className="border-b border-border/50">
                <td className="py-1.5 pr-4 font-mono text-[10px] uppercase tracking-widest text-fg-dim">{k}</td>
                <td className="py-1.5 font-mono text-xs text-accent">127.0.0.1:{v}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Section>

      {editing && <VMSettingsDialog vm={vm} onClose={() => setEditing(false)} />}
    </div>
  );
}

function Section({
  title,
  action,
  children,
}: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-md border border-border bg-surface p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="font-mono text-[10px] uppercase tracking-widest text-fg-dim">{title}</h3>
        {action}
      </div>
      {children}
    </div>
  );
}

function TabButton({
  active,
  icon,
  children,
  onClick,
  badge,
}: {
  active: boolean;
  icon: React.ReactNode;
  children: React.ReactNode;
  onClick?: () => void;
  badge?: number;
}) {
  return (
    <button
      onClick={onClick}
      className={`-mb-px flex items-center gap-2 border-b-2 px-3 py-2.5 font-mono text-xs transition-colors ${
        active ? "border-accent text-accent" : "border-transparent text-fg-muted hover:text-fg"
      }`}
    >
      {icon}
      {children}
      {badge ? (
        <span className="rounded-full bg-accent/20 px-1.5 text-[10px] text-accent">{badge}</span>
      ) : null}
    </button>
  );
}

function Centered({ text }: { text: string }) {
  return (
    <div className="flex h-full items-center justify-center font-mono text-sm text-fg-dim">{text}</div>
  );
}
