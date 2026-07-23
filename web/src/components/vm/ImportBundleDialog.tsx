import { useRef, useState } from "react";
import { X, Upload, PackageOpen } from "lucide-react";
import type { VM } from "../../api/client";
import { Button } from "../ui/Button";

/**
 * ImportBundleDialog uploads a .vphonevm.zip bundle (produced by Export) and
 * registers it as a new VM. Uses XHR for an upload progress bar since bundles
 * (disk images) can be multiple GB.
 */
export function ImportBundleDialog({ onClose, onImported }: { onClose: () => void; onImported: (vm: VM) => void }) {
  const fileRef = useRef<HTMLInputElement>(null);
  const [name, setName] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);

  function upload() {
    if (!file) return;
    setError(null);
    setProgress(0);
    const form = new FormData();
    form.append("file", file);
    if (name.trim()) form.append("name", name.trim());
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/v1/vms/import-bundle");
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) setProgress(Math.round((e.loaded / e.total) * 100));
    };
    xhr.onload = () => {
      if (xhr.status >= 400) {
        setProgress(null);
        setError(JSON.parse(xhr.responseText || "{}").error || "import failed");
        return;
      }
      onImported(JSON.parse(xhr.responseText) as VM);
    };
    xhr.onerror = () => {
      setProgress(null);
      setError("upload failed");
    };
    xhr.send(form);
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm" onClick={progress === null ? onClose : undefined}>
      <div onClick={(e) => e.stopPropagation()} className="w-[460px] rounded-md border border-border-bright bg-surface shadow-2xl">
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <div className="flex items-center gap-2">
            <PackageOpen className="h-4 w-4 text-accent" />
            <h2 className="font-mono text-sm uppercase tracking-wide text-fg">Import VM Bundle</h2>
          </div>
          {progress === null && (
            <button onClick={onClose} className="text-fg-dim hover:text-fg"><X className="h-4 w-4" /></button>
          )}
        </div>

        <div className="space-y-4 px-4 py-4">
          <p className="font-mono text-[11px] text-fg-dim">
            Upload a <code className="text-fg-muted">.vphonevm.zip</code> exported from any vphone-web instance or node.
          </p>
          <label className="block">
            <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-fg-dim">Name (optional)</span>
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="from bundle" className="ib-input" disabled={progress !== null} />
          </label>

          <input ref={fileRef} type="file" accept=".zip" className="hidden" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
          {progress === null ? (
            <button onClick={() => fileRef.current?.click()} className="flex w-full items-center gap-2 rounded-sm border border-border bg-base px-3 py-2 font-mono text-xs text-fg-muted hover:border-accent">
              <Upload className="h-3.5 w-3.5" />
              {file ? file.name : "Choose .vphonevm.zip"}
            </button>
          ) : (
            <div>
              <div className="h-1.5 w-full overflow-hidden rounded-full bg-border">
                <div className="h-full bg-accent transition-all" style={{ width: `${progress}%` }} />
              </div>
              <p className="mt-1 font-mono text-[10px] text-fg-dim">{progress}% — uploading & extracting…</p>
            </div>
          )}

          {error && <p className="rounded-sm border border-error/40 bg-error/10 px-3 py-2 font-mono text-xs text-error">{error}</p>}
        </div>

        {progress === null && (
          <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
            <Button variant="ghost" onClick={onClose}>Cancel</Button>
            <Button variant="primary" onClick={upload} disabled={!file}>Import</Button>
          </div>
        )}
        <style>{`.ib-input{width:100%;background:var(--color-base);border:1px solid var(--color-border);border-radius:2px;padding:0.4rem 0.6rem;font-family:var(--font-mono);font-size:0.75rem;color:var(--color-fg);outline:none;}.ib-input:focus{border-color:var(--color-accent);}`}</style>
      </div>
    </div>
  );
}
