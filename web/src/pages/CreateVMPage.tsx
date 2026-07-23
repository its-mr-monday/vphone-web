import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { ChevronLeft, ChevronRight, Check, HardDriveDownload, Cpu } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError, formatBytes, type Variant, type NetworkMode } from "../api/client";
import { useCreateVM } from "../hooks/useVM";
import { Button } from "../components/ui/Button";

const VARIANTS: { value: Variant; label: string; desc: string }[] = [
  { value: "regular", label: "Regular", desc: "stock behavior with the VM boot chain" },
  { value: "dev", label: "Development", desc: "+ TXM entitlement/debug bypasses + rpcserver" },
  { value: "jb", label: "Jailbreak", desc: "full bypass · Sileo + apt + TrollStore on first boot" },
  { value: "exp", label: "Experimental", desc: "JB superset + anti-VM-detection research patches" },
];

export function CreateVMPage() {
  const navigate = useNavigate();
  const create = useCreateVM();

  const [step, setStep] = useState(0);
  const [ipswId, setIpswId] = useState<string>("");
  const [cloudosId, setCloudosId] = useState<string>("");
  const [variant, setVariant] = useState<Variant>("regular");
  const [name, setName] = useState("");
  const [cpu, setCpu] = useState(4);
  const [memory, setMemory] = useState(4096);
  const [disk, setDisk] = useState(16384);
  const [network, setNetwork] = useState<NetworkMode>("nat");
  const [iface, setIface] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [nodeId, setNodeId] = useState(""); // "" = this host
  const { data: interfaces } = useQuery({ queryKey: ["interfaces"], queryFn: api.systemInterfaces });
  const { data: nodes } = useQuery({ queryKey: ["nodes"], queryFn: api.listNodes });
  const onlineNodes = (nodes ?? []).filter((n) => n.status === "ONLINE");
  // IPSW library of the deploy target (this host, or a worker node's library).
  const { data: ipsws } = useQuery({
    queryKey: ["ipsws", nodeId || "local"],
    queryFn: () => (nodeId ? api.nodeIPSWs(nodeId) : api.listIPSWs()),
  });

  const ready = (ipsws ?? []).filter((i) => i.status === "READY" || i.status === "REGISTERED");
  const iphoneIpsws = ready.filter((i) => i.kind !== "cloudos");
  const cloudosIpsws = ready.filter((i) => i.kind === "cloudos");
  const selectedIpsw = ready.find((i) => i.id === ipswId);
  const selectedCloudos = cloudosIpsws.find((i) => i.id === cloudosId);

  const steps = ["Firmware", "Variant", "Resources", "Confirm"];

  async function submit() {
    setError(null);
    try {
      const vm = await create.mutateAsync({
        name: name.trim(),
        variant,
        ios_version: selectedIpsw?.version ?? "",
        ipsw_id: ipswId || undefined,
        cloudos_ipsw_id: cloudosId || undefined,
        network_mode: network,
        network_interface: network === "bridged" ? iface || undefined : undefined,
        cpu,
        memory,
        disk_size: disk,
        node_id: nodeId || undefined,
      });
      // Remote builds are proxied; the returned id lives on that node but the
      // controller routes to it transparently.
      navigate(`/vms/${vm.id}?tab=jobs`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "failed to create VM");
    }
  }

  const canNext =
    (step === 0) || // firmware optional (bare allowed)
    (step === 1) ||
    (step === 2 && name.trim().length > 0) ||
    step === 3;

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-3 border-b border-border px-6 py-4">
        <button onClick={() => navigate("/")} className="text-fg-dim hover:text-fg">
          <ChevronLeft className="h-5 w-5" />
        </button>
        <div>
          <h1 className="font-sans text-lg font-semibold text-fg">Create Device</h1>
          <p className="font-mono text-xs text-fg-dim">provision a new virtual iPhone</p>
        </div>
      </div>

      {/* Stepper */}
      <div className="flex items-center gap-2 border-b border-border px-6 py-3">
        {steps.map((s, i) => (
          <div key={s} className="flex items-center gap-2">
            <div
              className={`flex h-6 w-6 items-center justify-center rounded-full font-mono text-[11px] ${
                i < step
                  ? "bg-success/20 text-success"
                  : i === step
                    ? "bg-accent/20 text-accent"
                    : "bg-surface-2 text-fg-dim"
              }`}
            >
              {i < step ? <Check className="h-3 w-3" /> : i + 1}
            </div>
            <span className={`font-mono text-xs ${i === step ? "text-fg" : "text-fg-dim"}`}>{s}</span>
            {i < steps.length - 1 && <div className="mx-2 h-px w-8 bg-border" />}
          </div>
        ))}
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        <div className="mx-auto max-w-2xl">
          {step === 0 && (
            <Section title="Choose firmware">
              {onlineNodes.length > 0 && (
                <div className="mb-4 rounded-md border border-border bg-surface p-3">
                  <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-fg-dim">
                    Deploy to
                  </span>
                  <select
                    value={nodeId}
                    onChange={(e) => {
                      setNodeId(e.target.value);
                      setIpswId(""); // firmware library differs per node
                      setCloudosId("");
                    }}
                    className="wiz-input"
                  >
                    <option value="">This host (controller)</option>
                    {onlineNodes.map((n) => (
                      <option key={n.id} value={n.id}>{n.name} · {n.address}</option>
                    ))}
                  </select>
                  <p className="mt-1 font-mono text-[10px] text-fg-dim">
                    firmware below comes from the selected host's IPSW library
                  </p>
                </div>
              )}

              <div className="mb-1.5 font-mono text-[10px] uppercase tracking-widest text-fg-dim">
                iPhone firmware (IPHONE_SOURCE)
              </div>
              <button
                onClick={() => setIpswId("")}
                className={`mb-2 w-full rounded-md border px-4 py-3 text-left transition-colors ${
                  ipswId === "" ? "border-accent bg-accent/10" : "border-border hover:border-border-bright"
                }`}
              >
                <div className="font-mono text-xs text-fg">No firmware (bare shell)</div>
                <div className="font-mono text-[10px] text-fg-dim">
                  create the VM directory only — provision firmware later
                </div>
              </button>
              {iphoneIpsws.length === 0 ? (
                <p className="rounded-sm border border-warn/40 bg-warn/10 px-3 py-2 font-mono text-[11px] text-warn">
                  No iPhone IPSWs in {nodeId ? "this node's" : "the"} library. Add one on the IPSW page to run the full pipeline.
                </p>
              ) : (
                iphoneIpsws.map((it) => (
                  <button
                    key={it.id}
                    onClick={() => setIpswId(it.id)}
                    className={`mb-2 flex w-full items-center gap-3 rounded-md border px-4 py-3 text-left transition-colors ${
                      ipswId === it.id ? "border-accent bg-accent/10" : "border-border hover:border-border-bright"
                    }`}
                  >
                    <HardDriveDownload className="h-4 w-4 text-fg-dim" />
                    <div className="min-w-0 flex-1">
                      <div className="font-mono text-xs text-fg">
                        {it.version || "unknown"} {it.build && `(${it.build})`}
                      </div>
                      <div className="truncate font-mono text-[10px] text-fg-dim">
                        {it.device || "device ?"} · {formatBytes(it.size)}
                      </div>
                    </div>
                  </button>
                ))
              )}

              {/* CloudOS (PCC) source — only relevant when provisioning firmware. */}
              {ipswId && (
                <div className="mt-5">
                  <div className="mb-1.5 font-mono text-[10px] uppercase tracking-widest text-fg-dim">
                    CloudOS firmware (CLOUDOS_SOURCE)
                  </div>
                  <button
                    onClick={() => setCloudosId("")}
                    className={`mb-2 w-full rounded-md border px-4 py-3 text-left transition-colors ${
                      cloudosId === "" ? "border-accent bg-accent/10" : "border-border hover:border-border-bright"
                    }`}
                  >
                    <div className="font-mono text-xs text-fg">Default (fw_prepare built-in)</div>
                    <div className="font-mono text-[10px] text-fg-dim">
                      uses the CLI's default PCC stack — fine for iOS ≤ 26.1
                    </div>
                  </button>
                  {cloudosIpsws.map((it) => (
                    <button
                      key={it.id}
                      onClick={() => setCloudosId(it.id)}
                      className={`mb-2 flex w-full items-center gap-3 rounded-md border px-4 py-3 text-left transition-colors ${
                        cloudosId === it.id ? "border-accent bg-accent/10" : "border-border hover:border-border-bright"
                      }`}
                    >
                      <HardDriveDownload className="h-4 w-4 text-fg-dim" />
                      <div className="min-w-0 flex-1">
                        <div className="font-mono text-xs text-fg">
                          {it.version || "CloudOS"} {it.build && `(${it.build})`}
                        </div>
                        <div className="truncate font-mono text-[10px] text-fg-dim">
                          PCC stack · {formatBytes(it.size)}
                        </div>
                      </div>
                    </button>
                  ))}
                  <p className="mt-1 font-mono text-[10px] text-fg-dim">
                    Newer iOS needs a matching PCC stack — e.g. iOS 27 pairs iPhone 27.0 with CloudOS 26.4.
                    Add a CloudOS IPSW (kind “cloudos”) on the IPSW page.
                  </p>
                </div>
              )}
            </Section>
          )}

          {step === 1 && (
            <Section title="Firmware variant">
              <div className="grid grid-cols-1 gap-2">
                {VARIANTS.map((v) => (
                  <button
                    key={v.value}
                    onClick={() => setVariant(v.value)}
                    className={`rounded-md border px-4 py-3 text-left transition-colors ${
                      variant === v.value ? "border-accent bg-accent/10" : "border-border hover:border-border-bright"
                    }`}
                  >
                    <div className="font-mono text-xs text-fg">{v.label}</div>
                    <div className="font-mono text-[10px] text-fg-dim">{v.desc}</div>
                  </button>
                ))}
              </div>
            </Section>
          )}

          {step === 2 && (
            <Section title="Name & resources">
              <label className="mb-4 block">
                <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-fg-dim">Name</span>
                <input value={name} onChange={(e) => setName(e.target.value)} placeholder="research-01" className="wiz-input" autoFocus />
              </label>
              <div className="grid grid-cols-3 gap-3">
                <Num label="CPU cores" value={cpu} onChange={setCpu} min={1} max={16} step={1} />
                <Num label="RAM (MiB)" value={memory} onChange={setMemory} min={1024} max={65536} step={512} />
                <Num label="Disk (MiB)" value={disk} onChange={setDisk} min={4096} max={262144} step={1024} />
              </div>

              <div className="mt-4">
                <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-fg-dim">Network</span>
                <div className="grid grid-cols-2 gap-2">
                  {([
                    { v: "nat", label: "NAT (shared)", desc: "private 192.168.64.x, host-only reachable" },
                    { v: "bridged", label: "Bridged (LAN)", desc: "own DHCP lease on your subnet — direct SSH from peers" },
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

                {network === "bridged" && (
                  <div className="mt-3">
                    <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-fg-dim">
                      Bridge interface
                    </span>
                    <select value={iface} onChange={(e) => setIface(e.target.value)} className="wiz-input">
                      <option value="">first available</option>
                      {(interfaces ?? []).map((i) => (
                        <option key={i.name} value={i.name}>
                          {i.name} · {i.type} · {i.addrs[0] ?? "no ip"}
                          {!i.wired ? " (Wi-Fi — bridging won't get DHCP)" : ""}
                        </option>
                      ))}
                    </select>
                    {(() => {
                      const sel = (interfaces ?? []).find((i) => i.name === iface);
                      if (sel && !sel.wired) {
                        return (
                          <p className="mt-1 font-mono text-[10px] text-warn">
                            {sel.type} interfaces can't be bridged (the AP won't pass the VM's MAC). Use a wired NIC for a LAN lease.
                          </p>
                        );
                      }
                      if (!(interfaces ?? []).some((i) => i.wired)) {
                        return (
                          <p className="mt-1 font-mono text-[10px] text-warn">
                            No wired interface detected — bridged mode will fall back to link-local (169.254.x). Works on wired hosts.
                          </p>
                        );
                      }
                      return null;
                    })()}
                  </div>
                )}
              </div>
              <style>{wizInputStyle}</style>
            </Section>
          )}

          {step === 3 && (
            <Section title="Confirm">
              <div className="rounded-md border border-border bg-surface p-4">
                <Row k="Name" v={name || "—"} />
                <Row k="Deploy to" v={nodeId ? (onlineNodes.find((n) => n.id === nodeId)?.name ?? nodeId) : "This host"} />
                <Row k="Firmware" v={selectedIpsw ? `${selectedIpsw.version} (${selectedIpsw.build})` : "none (bare)"} />
                {selectedIpsw && (
                  <Row k="CloudOS" v={selectedCloudos ? `${selectedCloudos.version} (${selectedCloudos.build})` : "default (built-in)"} />
                )}
                <Row k="Variant" v={VARIANTS.find((v) => v.value === variant)!.label} />
                <Row k="Network" v={network === "bridged" ? "Bridged (LAN)" : "NAT (shared)"} />
                <Row k="CPU" v={`${cpu} cores`} />
                <Row k="Memory" v={`${memory} MiB`} />
                <Row k="Disk" v={`${disk} MiB`} />
              </div>
              {selectedIpsw ? (
                <p className="mt-3 flex items-center gap-2 font-mono text-[11px] text-fg-dim">
                  <Cpu className="h-3.5 w-3.5" />
                  provisioning runs fw_prepare → fw_patch → restore → cfw_install as background jobs
                </p>
              ) : (
                <p className="mt-3 font-mono text-[11px] text-warn">
                  bare VM — not bootable until firmware is provisioned
                </p>
              )}
              {error && <p className="mt-3 font-mono text-xs text-error">{error}</p>}
            </Section>
          )}
        </div>
      </div>

      {/* Nav */}
      <div className="flex items-center justify-between border-t border-border px-6 py-4">
        <Button variant="ghost" icon={<ChevronLeft className="h-3.5 w-3.5" />} disabled={step === 0} onClick={() => setStep((s) => s - 1)}>
          Back
        </Button>
        {step < 3 ? (
          <Button variant="primary" icon={<ChevronRight className="h-3.5 w-3.5" />} disabled={!canNext} onClick={() => setStep((s) => s + 1)}>
            Next
          </Button>
        ) : (
          <Button variant="success" icon={<Check className="h-3.5 w-3.5" />} disabled={!name.trim() || create.isPending} onClick={submit}>
            {create.isPending ? "Creating…" : "Create Device"}
          </Button>
        )}
      </div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h2 className="mb-4 font-mono text-sm text-fg">{title}</h2>
      {children}
    </div>
  );
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex justify-between border-b border-border/50 py-2 last:border-0">
      <span className="font-mono text-[10px] uppercase tracking-widest text-fg-dim">{k}</span>
      <span className="font-mono text-xs text-fg">{v}</span>
    </div>
  );
}

function Num({
  label,
  value,
  onChange,
  min,
  max,
  step,
}: {
  label: string;
  value: number;
  onChange: (n: number) => void;
  min: number;
  max: number;
  step: number;
}) {
  return (
    <label className="block">
      <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-fg-dim">{label}</span>
      <input
        type="number"
        value={value}
        min={min}
        max={max}
        step={step}
        onChange={(e) => onChange(+e.target.value)}
        className="wiz-input"
      />
    </label>
  );
}

const wizInputStyle = `
  .wiz-input {
    width: 100%;
    background: var(--color-base);
    border: 1px solid var(--color-border);
    border-radius: 2px;
    padding: 0.5rem 0.7rem;
    font-family: var(--font-mono);
    font-size: 0.8rem;
    color: var(--color-fg);
    outline: none;
  }
  .wiz-input:focus { border-color: var(--color-accent); }
`;
