import { useRef, useState } from "react";
import { HardDriveDownload, Upload, FolderInput, Trash2, Loader2, CheckCircle2, AlertTriangle } from "lucide-react";
import { ApiError, formatBytes, type IPSW, type IPSWStatus } from "../api/client";
import { useIPSWs, useRegisterIPSW, useDownloadIPSW, useDeleteIPSW } from "../hooks/useIPSW";
import { Button } from "../components/ui/Button";

export function IPSWPage() {
  const { data: ipsws, isLoading } = useIPSWs();
  const [error, setError] = useState<string | null>(null);

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-border px-6 py-4">
        <h1 className="font-sans text-lg font-semibold text-fg">IPSW Library</h1>
        <p className="font-mono text-xs text-fg-dim">
          Firmware images for VM provisioning · register from disk, upload, or download
        </p>
      </div>

      <div className="border-b border-border p-6">
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <AddFromDiskCard onError={setError} />
          <UploadCard onError={setError} />
          <DownloadCard onError={setError} />
        </div>
        {error && (
          <p className="mt-3 rounded-sm border border-error/40 bg-error/10 px-3 py-2 font-mono text-xs text-error">
            {error}
          </p>
        )}
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        {isLoading ? (
          <p className="font-mono text-xs text-fg-dim">loading…</p>
        ) : ipsws && ipsws.length > 0 ? (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {ipsws.map((it) => (
              <IPSWCard key={it.id} ipsw={it} />
            ))}
          </div>
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-center">
            <HardDriveDownload className="h-12 w-12 text-fg-dim" />
            <p className="font-mono text-sm text-fg-muted">No firmware in the library</p>
            <p className="font-mono text-xs text-fg-dim">Add an IPSW to provision VMs from it.</p>
          </div>
        )}
      </div>
    </div>
  );
}

function AddFromDiskCard({ onError }: { onError: (e: string | null) => void }) {
  const register = useRegisterIPSW();
  const [path, setPath] = useState("");
  return (
    <Card title="Add from disk" icon={<FolderInput className="h-4 w-4 text-accent" />}>
      <input
        value={path}
        onChange={(e) => setPath(e.target.value)}
        placeholder="/path/to/firmware.ipsw"
        className="input"
      />
      <Button
        variant="primary"
        disabled={!path.trim() || register.isPending}
        onClick={async () => {
          onError(null);
          try {
            await register.mutateAsync({ file_path: path.trim() });
            setPath("");
          } catch (e) {
            onError(e instanceof ApiError ? e.message : "register failed");
          }
        }}
      >
        {register.isPending ? "Registering…" : "Register"}
      </Button>
      <style>{inputStyle}</style>
    </Card>
  );
}

function UploadCard({ onError }: { onError: (e: string | null) => void }) {
  const fileRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState(0);

  function upload(file: File) {
    onError(null);
    setUploading(true);
    setProgress(0);
    const form = new FormData();
    form.append("file", file);
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/v1/ipsws/upload");
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) setProgress(Math.round((e.loaded / e.total) * 100));
    };
    xhr.onload = () => {
      setUploading(false);
      if (xhr.status >= 400) {
        onError(JSON.parse(xhr.responseText || "{}").error || "upload failed");
      }
    };
    xhr.onerror = () => {
      setUploading(false);
      onError("upload failed");
    };
    xhr.send(form);
  }

  return (
    <Card title="Upload" icon={<Upload className="h-4 w-4 text-accent" />}>
      <input
        ref={fileRef}
        type="file"
        accept=".ipsw"
        className="hidden"
        onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])}
      />
      {uploading ? (
        <div className="w-full">
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-border">
            <div className="h-full bg-accent transition-all" style={{ width: `${progress}%` }} />
          </div>
          <p className="mt-1 font-mono text-[10px] text-fg-dim">{progress}%</p>
        </div>
      ) : (
        <Button variant="primary" onClick={() => fileRef.current?.click()}>
          Choose .ipsw file
        </Button>
      )}
    </Card>
  );
}

function DownloadCard({ onError }: { onError: (e: string | null) => void }) {
  const download = useDownloadIPSW();
  const [url, setUrl] = useState("");
  return (
    <Card title="Download from URL" icon={<HardDriveDownload className="h-4 w-4 text-accent" />}>
      <input
        value={url}
        onChange={(e) => setUrl(e.target.value)}
        placeholder="https://…/firmware.ipsw"
        className="input"
      />
      <Button
        variant="primary"
        disabled={!url.trim() || download.isPending}
        onClick={async () => {
          onError(null);
          try {
            await download.mutateAsync(url.trim());
            setUrl("");
          } catch (e) {
            onError(e instanceof ApiError ? e.message : "download failed");
          }
        }}
      >
        {download.isPending ? "Starting…" : "Download"}
      </Button>
      <style>{inputStyle}</style>
    </Card>
  );
}

function IPSWCard({ ipsw }: { ipsw: IPSW }) {
  const del = useDeleteIPSW();
  return (
    <div className="rounded-md border border-border bg-surface p-4">
      <div className="flex items-start justify-between">
        <div className="min-w-0">
          <div className="truncate font-mono text-sm text-fg">
            {ipsw.version || "unknown"} {ipsw.build && <span className="text-fg-dim">({ipsw.build})</span>}
          </div>
          <div className="truncate font-mono text-[10px] text-fg-dim">
            {ipsw.device || "device ?"} · {ipsw.managed ? "managed" : "in place"}
          </div>
        </div>
        <IPSWStatusPill status={ipsw.status} />
      </div>
      <div className="mt-3 truncate font-mono text-[10px] text-fg-dim" title={ipsw.file_path}>
        {ipsw.file_path}
      </div>
      <div className="mt-2 flex items-center justify-between">
        <span className="font-mono text-[11px] text-fg-muted">
          {ipsw.size ? formatBytes(ipsw.size) : "—"}
        </span>
        <Button
          variant="danger"
          icon={<Trash2 className="h-3.5 w-3.5" />}
          disabled={del.isPending}
          onClick={() => {
            if (confirm(`Remove "${ipsw.version || ipsw.file_path}" from the library?`)) del.mutate(ipsw.id);
          }}
        >
          Remove
        </Button>
      </div>
      {ipsw.error && <p className="mt-2 font-mono text-[10px] text-error">{ipsw.error}</p>}
    </div>
  );
}

function IPSWStatusPill({ status }: { status: IPSWStatus }) {
  const map = {
    READY: { c: "#00e676", i: <CheckCircle2 className="h-3 w-3" />, t: "ready" },
    REGISTERED: { c: "#00e676", i: <CheckCircle2 className="h-3 w-3" />, t: "ready" },
    DOWNLOADING: { c: "#00e5ff", i: <Loader2 className="h-3 w-3 animate-spin" />, t: "downloading" },
    ERROR: { c: "#ff1744", i: <AlertTriangle className="h-3 w-3" />, t: "error" },
  }[status];
  return (
    <span
      className="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] uppercase"
      style={{ color: map.c, background: `${map.c}1a`, border: `1px solid ${map.c}40` }}
    >
      {map.i}
      {map.t}
    </span>
  );
}

function Card({ title, icon, children }: { title: string; icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-3 rounded-md border border-border bg-surface p-4">
      <div className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-fg-dim">
        {icon}
        {title}
      </div>
      {children}
    </div>
  );
}

const inputStyle = `
  .input {
    width: 100%;
    background: var(--color-base);
    border: 1px solid var(--color-border);
    border-radius: 2px;
    padding: 0.4rem 0.6rem;
    font-family: var(--font-mono);
    font-size: 0.75rem;
    color: var(--color-fg);
    outline: none;
  }
  .input:focus { border-color: var(--color-accent); }
`;
