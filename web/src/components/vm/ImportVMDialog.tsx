import { useState } from "react";
import { X } from "lucide-react";
import { ApiError, type Variant, type VM } from "../../api/client";
import { useImportVM } from "../../hooks/useVM";
import { Button } from "../ui/Button";

const VARIANTS: Variant[] = ["regular", "dev", "jb", "exp"];

/**
 * ImportVMDialog adopts an already-provisioned VM directory (e.g. one built
 * directly with vphone-cli) as a managed VM, without re-running the pipeline.
 * The directory is left on disk when such a VM is deleted.
 */
export function ImportVMDialog({ onClose, onImported }: { onClose: () => void; onImported: (vm: VM) => void }) {
  const importVM = useImportVM();
  const [name, setName] = useState("");
  const [vmDir, setVmDir] = useState("");
  const [variant, setVariant] = useState<Variant>("jb");
  const [iosVersion, setIosVersion] = useState("");
  const [error, setError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      const vm = await importVM.mutateAsync({
        name: name.trim(),
        vm_dir: vmDir.trim(),
        variant,
        ios_version: iosVersion.trim(),
      });
      onImported(vm);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "import failed");
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm" onClick={onClose}>
      <form
        onClick={(e) => e.stopPropagation()}
        onSubmit={submit}
        className="w-[460px] rounded-md border border-border-bright bg-surface shadow-2xl"
      >
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <h2 className="font-mono text-sm uppercase tracking-wide text-fg">Import Device</h2>
          <button type="button" onClick={onClose} className="text-fg-dim hover:text-fg">
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="space-y-4 px-4 py-4">
          <Field label="Name">
            <input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="imported-jb" className="imp-input" />
          </Field>
          <Field label="VM directory (absolute path with config.plist)">
            <input value={vmDir} onChange={(e) => setVmDir(e.target.value)} placeholder="/path/to/vphone-cli/vm" className="imp-input" />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Variant">
              <div className="grid grid-cols-4 gap-1">
                {VARIANTS.map((v) => (
                  <button
                    key={v}
                    type="button"
                    onClick={() => setVariant(v)}
                    className={`rounded-sm border px-1 py-1.5 font-mono text-[10px] uppercase transition-colors ${
                      variant === v ? "border-accent bg-accent/10 text-accent" : "border-border text-fg-muted"
                    }`}
                  >
                    {v}
                  </button>
                ))}
              </div>
            </Field>
            <Field label="iOS version">
              <input value={iosVersion} onChange={(e) => setIosVersion(e.target.value)} placeholder="26.1" className="imp-input" />
            </Field>
          </div>
          {error && (
            <div className="rounded-sm border border-error/40 bg-error/10 px-3 py-2 font-mono text-xs text-error">{error}</div>
          )}
        </div>
        <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
          <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={importVM.isPending || !name.trim() || !vmDir.trim()}>
            {importVM.isPending ? "Importing…" : "Import"}
          </Button>
        </div>
        <style>{`
          .imp-input { width:100%; background:var(--color-base); border:1px solid var(--color-border); border-radius:2px; padding:0.4rem 0.6rem; font-family:var(--font-mono); font-size:0.75rem; color:var(--color-fg); outline:none; }
          .imp-input:focus { border-color:var(--color-accent); }
        `}</style>
      </form>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1.5 block font-mono text-[10px] uppercase tracking-widest text-fg-dim">{label}</span>
      {children}
    </label>
  );
}
