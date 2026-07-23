import { useState } from "react";
import { Plus, Server, Activity, HardDrive, ListChecks, Download } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { useVMs } from "../hooks/useVM";
import { useAuth } from "../hooks/useAuth";
import { useSystemStatus } from "../hooks/useSystem";
import { useJobs } from "../hooks/useJobs";
import { VMCard } from "../components/vm/VMCard";
import { Button } from "../components/ui/Button";
import { ImportVMDialog } from "../components/vm/ImportVMDialog";
import { JobBadge, jobDuration } from "../components/jobs/JobBadge";
import { formatBytes } from "../api/client";

export function DashboardPage() {
  const { data: vms } = useVMs();
  const { data: sys } = useSystemStatus();
  const { data: jobs } = useJobs();
  const { isAdmin } = useAuth();
  const navigate = useNavigate();
  const [importing, setImporting] = useState(false);

  const activeJobs = jobs?.filter((j) => j.status === "RUNNING" || j.status === "PENDING") ?? [];
  const vmDiskPct = sys?.vm_disk.total_bytes
    ? Math.round((sys.vm_disk.used_bytes / sys.vm_disk.total_bytes) * 100)
    : 0;

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-border px-6 py-4">
        <div>
          <h1 className="font-sans text-lg font-semibold text-fg">Dashboard</h1>
          <p className="font-mono text-xs text-fg-dim">
            {sys ? `${sys.hostname} · ${sys.os}/${sys.arch} · ${sys.num_cpu} CPUs` : "…"}
          </p>
        </div>
        {isAdmin && (
          <div className="flex items-center gap-2">
            <Button variant="ghost" icon={<Download className="h-3.5 w-3.5" />} onClick={() => setImporting(true)}>
              Import
            </Button>
            <Button variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => navigate("/create")}>
              New Device
            </Button>
          </div>
        )}
      </div>

      {importing && (
        <ImportVMDialog
          onClose={() => setImporting(false)}
          onImported={(vm) => {
            setImporting(false);
            navigate(`/vms/${vm.id}`);
          }}
        />
      )}

      {/* Stat strip */}
      <div className="grid grid-cols-2 gap-px border-b border-border bg-border lg:grid-cols-4">
        <Stat icon={<Server className="h-4 w-4" />} label="Total VMs" value={sys ? String(sys.total_vms) : "—"} />
        <Stat
          icon={<Activity className="h-4 w-4 text-success" />}
          label="Running"
          value={sys ? `${sys.running_vms} / ${sys.max_concurrent_vms}` : "—"}
          accent
        />
        <Stat icon={<ListChecks className="h-4 w-4" />} label="Active Jobs" value={sys ? String(sys.active_jobs) : "—"} />
        <Stat
          icon={<HardDrive className="h-4 w-4" />}
          label="VM Disk"
          value={sys ? `${vmDiskPct}% · ${formatBytes(sys.vm_disk.free_bytes)} free` : "—"}
        />
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        {/* Active jobs */}
        {activeJobs.length > 0 && (
          <div className="mb-6 rounded-md border border-border bg-surface p-4">
            <h3 className="mb-3 font-mono text-[10px] uppercase tracking-widest text-fg-dim">Active Jobs</h3>
            <div className="divide-y divide-border/50">
              {activeJobs.map((j) => (
                <button
                  key={j.id}
                  onClick={() => j.vm_id && navigate(`/vms/${j.vm_id}?tab=jobs`)}
                  className="flex w-full items-center justify-between py-2 text-left hover:opacity-80"
                >
                  <div className="font-mono text-xs text-fg">{j.label || j.type}</div>
                  <div className="flex items-center gap-3">
                    <span className="font-mono text-[10px] text-fg-dim">{jobDuration(j.started_at)}</span>
                    <JobBadge status={j.status} />
                  </div>
                </button>
              ))}
            </div>
          </div>
        )}

        {/* VM grid */}
        {vms && vms.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-4 text-center">
            <Server className="h-12 w-12 text-fg-dim" />
            <div>
              <p className="font-mono text-sm text-fg-muted">No devices provisioned</p>
              <p className="font-mono text-xs text-fg-dim">Create a virtual iPhone to get started.</p>
            </div>
            <Button variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => navigate("/create")}>
              New Device
            </Button>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
            {vms?.map((vm) => (
              <VMCard key={vm.id} vm={vm} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function Stat({
  icon,
  label,
  value,
  accent,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  accent?: boolean;
}) {
  return (
    <div className="flex items-center gap-3 bg-surface px-6 py-4">
      <span className="text-fg-dim">{icon}</span>
      <div className="min-w-0">
        <div className="font-mono text-[10px] uppercase tracking-widest text-fg-dim">{label}</div>
        <div className={`truncate font-mono text-lg ${accent ? "text-success" : "text-fg"}`}>{value}</div>
      </div>
    </div>
  );
}
