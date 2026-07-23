import { NavLink, useNavigate } from "react-router-dom";
import { LayoutGrid, Plus, Smartphone, Settings, HardDriveDownload } from "lucide-react";
import { useVMs } from "../../hooks/useVM";
import { StatusDot } from "../ui/StatusDot";

export function Sidebar() {
  const { data: vms, isLoading } = useVMs();
  const navigate = useNavigate();

  return (
    <aside className="flex h-full w-64 shrink-0 flex-col border-r border-border bg-surface">
      {/* Brand */}
      <div className="flex items-center gap-2 border-b border-border px-4 py-3">
        <Smartphone className="h-5 w-5 text-accent" />
        <div className="leading-tight">
          <div className="font-mono text-sm font-semibold text-fg">vphone</div>
          <div className="font-mono text-[10px] uppercase tracking-widest text-fg-dim">
            web console
          </div>
        </div>
      </div>

      {/* Nav */}
      <nav className="border-b border-border px-2 py-2">
        <NavItem to="/" icon={<LayoutGrid className="h-4 w-4" />} label="Dashboard" end />
        <NavItem to="/ipsws" icon={<HardDriveDownload className="h-4 w-4" />} label="IPSW Library" />
        <NavItem to="/settings" icon={<Settings className="h-4 w-4" />} label="Settings" />
      </nav>

      {/* VM list */}
      <div className="flex items-center justify-between px-4 py-2">
        <span className="font-mono text-[10px] uppercase tracking-widest text-fg-dim">
          Devices {vms ? `(${vms.length})` : ""}
        </span>
        <button
          onClick={() => navigate("/create")}
          className="text-fg-muted transition-colors hover:text-accent"
          title="Create VM"
        >
          <Plus className="h-4 w-4" />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto px-2 pb-2">
        {isLoading && (
          <div className="px-2 py-1 font-mono text-xs text-fg-dim">loading…</div>
        )}
        {vms && vms.length === 0 && (
          <div className="px-2 py-4 text-center font-mono text-xs text-fg-dim">
            no devices
          </div>
        )}
        {vms?.map((vm) => (
          <NavLink
            key={vm.id}
            to={`/vms/${vm.id}`}
            className={({ isActive }) =>
              `mb-0.5 flex items-center gap-2.5 rounded-sm px-2 py-2 transition-colors ${
                isActive
                  ? "bg-elevated text-fg"
                  : "text-fg-muted hover:bg-surface-2 hover:text-fg"
              }`
            }
          >
            <StatusDot status={vm.status} />
            <div className="min-w-0 flex-1">
              <div className="truncate font-mono text-xs">{vm.name}</div>
              <div className="truncate font-mono text-[10px] text-fg-dim">
                {vm.variant} · {vm.ios_version || "no fw"}
              </div>
            </div>
          </NavLink>
        ))}
      </div>
    </aside>
  );
}

function NavItem({
  to,
  icon,
  label,
  end,
}: {
  to: string;
  icon: React.ReactNode;
  label: string;
  end?: boolean;
}) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        `flex items-center gap-2.5 rounded-sm px-2 py-1.5 font-mono text-xs transition-colors ${
          isActive ? "bg-elevated text-accent" : "text-fg-muted hover:text-fg"
        }`
      }
    >
      {icon}
      {label}
    </NavLink>
  );
}
