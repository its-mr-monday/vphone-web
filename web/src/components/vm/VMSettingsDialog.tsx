import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { ApiError, api, type NetworkMode, type VM } from "../../api/client";
import { useUpdateVM } from "../../hooks/useVM";
import { Button } from "../ui/Button";

/**
 * VMSettingsDialog edits a STOPPED VM's tweakable settings — name, CPU cores,
 * memory, and network mode/interface. CPU/memory/network are rewritten into the
 * VM's config.plist so the next boot honors them. Disk size, variant, and screen
 * geometry are baked in at provisioning time and are not editable here.
 */
export function VMSettingsDialog({ vm, onClose }: { vm: VM; onClose: () => void }) {
  const update = useUpdateVM();
  const { data: interfaces } = useQuery({ queryKey: ["interfaces"], queryFn: api.systemInterfaces });
  const [name, setName] = useState(vm.name);
  const [cpu, setCpu] = useState(vm.cpu);
  const [memory, setMemory] = useState(vm.memory);
  const [network, setNetwork] = useState<NetworkMode>(
    (vm.network_mode as NetworkMode) || "nat",
  );
  const [iface, setIface] = useState(vm.network_interface || "");
  const [error, setError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await update.mutateAsync({
        id: vm.id,
        body: {
          name: name.trim(),
          cpu,
          memory,
          network_mode: network,
          network_interface: network === "bridged" ? iface : "",
        },
      });
      onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "update failed");
    }
  }

  const selIface = (interfaces ?? []).find((i) => i.name === iface);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
      onClick={onClose}
    >
      <form
        onClick={(e) => e.stopPropagation()}
        onSubmit={submit}
        className="w-[480px] rounded-md border border-border-bright bg-surface shadow-2xl"
      >
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <h2 className="font-mono text-sm uppercase tracking-wide text-fg">
            Edit Configuration · {vm.name}
          </h2>
          <button type="button" onClick={onClose} className="text-fg-dim hover:text-fg">
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-4 px-4 py-4">
          <Field label="Name">
            <input
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="set-input"
            />
          </Field>

          <div className="grid grid-cols-2 gap-3">
            <Field label={`CPU cores · ${cpu}`}>
              <input
                type="range"
                min={1}
                max={16}
                value={cpu}
                onChange={(e) => setCpu(Number(e.target.value))}
                className="w-full accent-[var(--color-accent)]"
              />
            </Field>
            <Field label={`Memory · ${(memory / 1024).toFixed(memory % 1024 ? 1 : 0)} GiB`}>
              <input
                type="range"
                min={2048}
                max={16384}
                step={1024}
                value={memory}
                onChange={(e) => setMemory(Number(e.target.value))}
                className="w-full accent-[var(--color-accent)]"
              />
            </Field>
          </div>

          <Field label="Network">
            <div className="grid grid-cols-2 gap-2">
              {([
                { v: "nat", label: "NAT (shared)", desc: "private 192.168.64.x" },
                { v: "bridged", label: "Bridged (LAN)", desc: "own DHCP lease on your subnet" },
              ] as { v: NetworkMode; label: string; desc: string }[]).map((n) => (
                <button
                  key={n.v}
                  type="button"
                  onClick={() => setNetwork(n.v)}
                  className={`rounded-md border px-3 py-2 text-left transition-colors ${
                    network === n.v ? "border-accent bg-accent/10" : "border-border hover:border-border-bright"
                  }`}
                >
                  <div className="font-mono text-xs text-fg">{n.label}</div>
                  <div className="font-mono text-[10px] text-fg-dim">{n.desc}</div>
                </button>
              ))}
            </div>
          </Field>

          {network === "bridged" && (
            <Field label="Bridge interface">
              <select value={iface} onChange={(e) => setIface(e.target.value)} className="set-input">
                <option value="">first available</option>
                {(interfaces ?? []).map((i) => (
                  <option key={i.name} value={i.name}>
                    {i.name} · {i.type} · {i.addrs[0] ?? "no ip"}
                    {!i.wired ? " (Wi-Fi — bridging won't get DHCP)" : ""}
                  </option>
                ))}
              </select>
              {selIface && !selIface.wired && (
                <p className="mt-1 font-mono text-[10px] text-warn">
                  {selIface.type} interfaces can't be bridged (the AP won't pass the VM's MAC). Use a wired NIC for a LAN lease.
                </p>
              )}
            </Field>
          )}

          {error && (
            <div className="rounded-sm border border-error/40 bg-error/10 px-3 py-2 font-mono text-xs text-error">
              {error}
            </div>
          )}
        </div>

        <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={update.isPending || !name.trim()}>
            {update.isPending ? "Saving…" : "Save"}
          </Button>
        </div>

        <style>{`
          .set-input { width:100%; background:var(--color-base); border:1px solid var(--color-border); border-radius:2px; padding:0.4rem 0.6rem; font-family:var(--font-mono); font-size:0.75rem; color:var(--color-fg); outline:none; }
          .set-input:focus { border-color:var(--color-accent); }
        `}</style>
      </form>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-fg-dim">
        {label}
      </span>
      {children}
    </label>
  );
}
