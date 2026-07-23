import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Server, Plus, Trash2, Cpu, MemoryStick, Boxes, Lock, ShieldAlert } from "lucide-react";
import {
  api,
  ApiError,
  formatBytes,
  type ClusterNode,
  type NodeStatus,
  type CertTrustPrompt,
} from "../api/client";
import { useAuth } from "../hooks/useAuth";
import { Button } from "../components/ui/Button";

/**
 * NodesPage is the admin-only cluster view: register worker nodes (each a
 * `vphone-web --agent`) by address + system password, and watch their live
 * status. The controller health-polls each node; status reflects reachability.
 */
export function NodesPage() {
  const { isAdmin } = useAuth();
  const qc = useQueryClient();
  const { data: nodes } = useQuery({
    queryKey: ["nodes"],
    queryFn: api.listNodes,
    enabled: isAdmin,
    refetchInterval: 5000,
  });

  const [name, setName] = useState("");
  const [address, setAddress] = useState("");
  const [pw, setPw] = useState("");
  const [tls, setTls] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [trust, setTrust] = useState<CertTrustPrompt | null>(null);
  const [busy, setBusy] = useState(false);

  const invalidate = () => qc.invalidateQueries({ queryKey: ["nodes"] });
  const del = useMutation({ mutationFn: (id: string) => api.deleteNode(id), onSuccess: invalidate });

  if (!isAdmin) return <Centered>Admin access required.</Centered>;

  function reset() {
    setName("");
    setAddress("");
    setPw("");
    setTls(false);
  }

  // Register (optionally accepting a pinned cert fingerprint on the 2nd attempt).
  async function doRegister(trustFingerprint?: string) {
    setBusy(true);
    setError(null);
    try {
      const res = await api.registerNode({
        name: name.trim(),
        address: address.trim(),
        system_password: pw,
        tls,
        trust_fingerprint: trustFingerprint,
      });
      if ("trust" in res) {
        setTrust(res.trust); // untrusted cert → prompt to accept
        return;
      }
      setTrust(null);
      reset();
      invalidate();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "failed to register node");
    } finally {
      setBusy(false);
    }
  }

  function submit(e: React.FormEvent) {
    e.preventDefault();
    void doRegister();
  }

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-border px-6 py-4">
        <h1 className="font-sans text-lg font-semibold text-fg">Cluster Nodes</h1>
        <p className="font-mono text-xs text-fg-dim">
          worker hosts running <code className="text-fg-muted">vphone-web --agent</code>, health-monitored by this controller
        </p>
      </div>

      {/* Add node */}
      <div className="border-b border-border p-6">
        <form onSubmit={submit} className="flex flex-wrap items-end gap-3">
          <Field label="Name">
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="mini-01" className="n-input w-40" />
          </Field>
          <Field label="Address (host:port)">
            <input value={address} onChange={(e) => setAddress(e.target.value)} placeholder="192.168.10.20:8080" className="n-input w-56" />
          </Field>
          <Field label="System password">
            <input type="password" value={pw} onChange={(e) => setPw(e.target.value)} placeholder="shared secret" className="n-input w-48" />
          </Field>
          <label className="flex cursor-pointer items-center gap-2 pb-1.5">
            <input type="checkbox" checked={tls} onChange={(e) => setTls(e.target.checked)} className="accent-[var(--color-accent)]" />
            <span className="flex items-center gap-1 font-mono text-[11px] text-fg-muted">
              <Lock className="h-3 w-3" /> HTTPS (TLS)
            </span>
          </label>
          <Button type="submit" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} disabled={busy || !name.trim() || !address.trim()}>
            {busy ? "Connecting…" : "Add node"}
          </Button>
        </form>
        {error && <p className="mt-2 font-mono text-xs text-error">{error}</p>}
        <p className="mt-2 font-mono text-[10px] text-fg-dim">
          The agent prints its address + password hint on startup. Registration validates the control link before saving.
          Enable <span className="text-fg-muted">HTTPS (TLS)</span> if the agent serves over TLS — you'll confirm its
          certificate fingerprint on first contact.
        </p>
      </div>

      {trust && (
        <TrustDialog
          prompt={trust}
          address={address.trim()}
          busy={busy}
          onAccept={() => doRegister(trust.fingerprint)}
          onCancel={() => setTrust(null)}
        />
      )}

      <div className="flex-1 overflow-y-auto p-6">
        {nodes && nodes.length > 0 ? (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {nodes.map((n) => (
              <NodeCard key={n.id} node={n} onDelete={() => confirm(`Remove node "${n.name}"?`) && del.mutate(n.id)} />
            ))}
          </div>
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-center">
            <Server className="h-12 w-12 text-fg-dim" />
            <p className="font-mono text-sm text-fg-muted">No worker nodes</p>
            <p className="max-w-md font-mono text-xs text-fg-dim">
              Run <code className="text-fg-muted">VPHONE_SYSTEM_PASSWORD=… vphone-web --agent</code> on another Mac, then add it here.
            </p>
          </div>
        )}
      </div>

      <style>{`
        .n-input { background:var(--color-base); border:1px solid var(--color-border); border-radius:2px; padding:0.4rem 0.6rem; font-family:var(--font-mono); font-size:0.75rem; color:var(--color-fg); outline:none; }
        .n-input:focus { border-color:var(--color-accent); }
      `}</style>
    </div>
  );
}

// TrustDialog implements trust-on-first-use: show the presented certificate's
// fingerprint so the operator can verify it out-of-band before pinning it.
function TrustDialog({
  prompt,
  address,
  busy,
  onAccept,
  onCancel,
}: {
  prompt: CertTrustPrompt;
  address: string;
  busy: boolean;
  onAccept: () => void;
  onCancel: () => void;
}) {
  const fp = prompt.fingerprint.match(/.{1,2}/g)?.join(":").toUpperCase() ?? prompt.fingerprint;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm" onClick={onCancel}>
      <div onClick={(e) => e.stopPropagation()} className="w-[520px] rounded-md border border-warn/50 bg-surface shadow-2xl">
        <div className="flex items-center gap-2 border-b border-border px-4 py-3">
          <ShieldAlert className="h-4 w-4 text-warn" />
          <h2 className="font-mono text-sm uppercase tracking-wide text-fg">Untrusted certificate</h2>
        </div>
        <div className="space-y-3 px-4 py-4 font-mono text-xs text-fg-muted">
          <p>
            <span className="text-fg">{address}</span> presented a TLS certificate that isn't signed by a trusted
            authority (typical for a self-signed agent). Verify the fingerprint below matches the agent, then accept to
            pin it. If it later changes, you'll be warned again — protecting against man-in-the-middle.
          </p>
          <div className="rounded-sm border border-border bg-base p-3">
            <Row k="SHA-256" v={fp} accent />
            {prompt.subject && <Row k="Subject" v={prompt.subject} />}
            {prompt.issuer && <Row k="Issuer" v={prompt.issuer} />}
            {prompt.expires && <Row k="Expires" v={prompt.expires} />}
          </div>
        </div>
        <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
          <Button type="button" variant="ghost" onClick={onCancel} disabled={busy}>Cancel</Button>
          <Button type="button" variant="primary" onClick={onAccept} disabled={busy}>
            {busy ? "Pinning…" : "Trust & add"}
          </Button>
        </div>
      </div>
    </div>
  );
}

function Row({ k, v, accent }: { k: string; v: string; accent?: boolean }) {
  return (
    <div className="flex gap-3 py-0.5">
      <span className="w-20 shrink-0 text-[10px] uppercase tracking-widest text-fg-dim">{k}</span>
      <span className={`break-all ${accent ? "text-accent" : "text-fg"}`}>{v}</span>
    </div>
  );
}

function NodeCard({ node, onDelete }: { node: ClusterNode; onDelete: () => void }) {
  return (
    <div className="rounded-md border border-border bg-surface p-4">
      <div className="flex items-start justify-between">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <StatusDot status={node.status} />
            <span className="truncate font-mono text-sm text-fg">{node.name}</span>
            {node.tls && <Lock className="h-3 w-3 text-success" aria-label="HTTPS control link" />}
          </div>
          <div className="mt-0.5 truncate font-mono text-[10px] text-fg-dim">
            {node.tls ? "https://" : "http://"}{node.address}
          </div>
        </div>
        <button onClick={onDelete} className="text-fg-dim hover:text-error" title="Remove node">
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </div>

      <div className="mt-3 flex items-center gap-4 font-mono text-[11px] text-fg-muted">
        <span className="flex items-center gap-1"><Cpu className="h-3 w-3 text-fg-dim" />{node.cpu || "—"}</span>
        <span className="flex items-center gap-1"><MemoryStick className="h-3 w-3 text-fg-dim" />{node.memory_mb ? formatBytes(node.memory_mb * 1024 * 1024) : "—"}</span>
        <span className="flex items-center gap-1"><Boxes className="h-3 w-3 text-fg-dim" />{node.running_vms} VMs</span>
      </div>

      <div className="mt-2 flex items-center justify-between border-t border-border pt-2 font-mono text-[10px] text-fg-dim">
        <span className="truncate">{node.chip || node.hostname || "—"}{node.version ? ` · v${node.version}` : ""}</span>
        <span>{node.last_seen ? `seen ${timeAgo(node.last_seen)}` : "never"}</span>
      </div>
      {node.status === "OFFLINE" && node.error && (
        <p className="mt-2 truncate font-mono text-[10px] text-error" title={node.error}>{node.error}</p>
      )}
    </div>
  );
}

function StatusDot({ status }: { status: NodeStatus }) {
  const color = status === "ONLINE" ? "#00e676" : status === "OFFLINE" ? "#ff1744" : "#8a8a94";
  return (
    <span className="inline-flex items-center gap-1.5">
      <span
        className={`inline-block h-2 w-2 rounded-full ${status === "ONLINE" ? "" : ""}`}
        style={{ background: color, boxShadow: `0 0 6px ${color}` }}
      />
      <span className="font-mono text-[10px] uppercase tracking-wider" style={{ color }}>{status.toLowerCase()}</span>
    </span>
  );
}

function timeAgo(iso: string): string {
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  return `${Math.floor(s / 3600)}h ago`;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-fg-dim">{label}</span>
      {children}
    </label>
  );
}

function Centered({ children }: { children: React.ReactNode }) {
  return <div className="flex h-full items-center justify-center font-mono text-sm text-fg-dim">{children}</div>;
}
