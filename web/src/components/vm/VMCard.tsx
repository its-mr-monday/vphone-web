import { Link } from "react-router-dom";
import { Cpu, HardDrive, MemoryStick, Server } from "lucide-react";
import type { VM } from "../../api/client";
import { StatusDot } from "../ui/StatusDot";

/** Summary card for a VM in the dashboard grid. */
export function VMCard({ vm }: { vm: VM }) {
  return (
    <Link
      to={`/vms/${vm.id}`}
      className="group flex flex-col gap-3 rounded-md border border-border bg-surface p-4 transition-colors hover:border-border-bright hover:bg-surface-2"
    >
      <div className="flex items-start justify-between">
        <div className="min-w-0">
          <div className="truncate font-mono text-sm text-fg group-hover:text-accent">
            {vm.name}
          </div>
          <div className="font-mono text-[10px] uppercase tracking-widest text-fg-dim">
            {vm.variant} · {vm.ios_version || "no firmware"}
          </div>
          {vm.node_id ? (
            <div className="mt-1 inline-flex items-center gap-1 rounded-sm border border-accent/30 bg-accent/5 px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-wider text-accent">
              <Server className="h-2.5 w-2.5" /> {vm.node_name}
            </div>
          ) : null}
        </div>
        <StatusDot status={vm.status} showLabel />
      </div>

      <div className="flex items-center gap-4 font-mono text-[11px] text-fg-muted">
        <span className="flex items-center gap-1">
          <Cpu className="h-3 w-3 text-fg-dim" />
          {vm.cpu}
        </span>
        <span className="flex items-center gap-1">
          <MemoryStick className="h-3 w-3 text-fg-dim" />
          {(vm.memory / 1024).toFixed(0)}G
        </span>
        <span className="flex items-center gap-1">
          <HardDrive className="h-3 w-3 text-fg-dim" />
          {(vm.disk_size / 1024).toFixed(0)}G
        </span>
      </div>

      <div className="flex items-center justify-between border-t border-border pt-2 font-mono text-[10px] text-fg-dim">
        <span>vnc :{vm.ports.vnc}</span>
        <span>ssh :{vm.ports.ssh}</span>
        <span>rpc :{vm.ports.rpc}</span>
      </div>
    </Link>
  );
}
