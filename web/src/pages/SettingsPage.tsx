import { useSystemConfig, useSystemStatus } from "../hooks/useSystem";

export function SettingsPage() {
  const { data: cfg } = useSystemConfig();
  const { data: sys } = useSystemStatus();

  const rows: [string, string][] = cfg
    ? [
        ["VM root", cfg.vm_root],
        ["IPSW directory", cfg.ipsw_dir],
        ["vphone-cli path", cfg.vphone_cli_dir],
        ["Port base", String(cfg.port_base)],
        ["Port block size", String(cfg.port_block_size)],
        ["Max concurrent VMs", String(cfg.max_concurrent_vms)],
        ["Max concurrent jobs", String(cfg.max_concurrent_jobs)],
      ]
    : [];

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-border px-6 py-4">
        <h1 className="font-sans text-lg font-semibold text-fg">Settings</h1>
        <p className="font-mono text-xs text-fg-dim">
          {sys ? `${sys.hostname} · ${sys.os}/${sys.arch} · ${sys.num_cpu} CPUs` : "…"}
        </p>
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        <div className="max-w-2xl rounded-md border border-border bg-surface p-5">
          <h3 className="mb-4 font-mono text-[10px] uppercase tracking-widest text-fg-dim">
            Server Configuration
          </h3>
          <table className="w-full">
            <tbody>
              {rows.map(([k, v]) => (
                <tr key={k} className="border-b border-border/50">
                  <td className="py-2 pr-6 font-mono text-[10px] uppercase tracking-widest text-fg-dim">
                    {k}
                  </td>
                  <td className="break-all py-2 font-mono text-xs text-fg">{v}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="mt-4 font-mono text-[10px] text-fg-dim">
            Configuration is read from{" "}
            <span className="text-fg-muted">~/.config/vphone-web/config.toml</span>{" "}
            with environment overrides. Editing is read-only in this phase.
          </p>
        </div>
      </div>
    </div>
  );
}
