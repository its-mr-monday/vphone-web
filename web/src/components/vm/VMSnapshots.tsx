import { useState } from "react";
import { Camera, RotateCcw, Trash2, Plus } from "lucide-react";
import { ApiError, formatBytes, type VM } from "../../api/client";
import {
  useSnapshots,
  useCreateSnapshot,
  useRestoreSnapshot,
  useDeleteSnapshot,
} from "../../hooks/useSnapshots";
import { Button } from "../ui/Button";

/**
 * VMSnapshots lists a VM's backups and provides create/restore/delete. All
 * mutating operations require the VM to be STOPPED (enforced server-side and
 * reflected in the UI).
 */
export function VMSnapshots({ vm }: { vm: VM }) {
  const { data: snaps, isLoading } = useSnapshots(vm.id);
  const create = useCreateSnapshot(vm.id);
  const restore = useRestoreSnapshot(vm.id);
  const del = useDeleteSnapshot(vm.id);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const stopped = vm.status === "STOPPED";

  async function guard(fn: () => Promise<unknown>) {
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "action failed");
    }
  }

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto">
      {/* Create row */}
      <div className="rounded-md border border-border bg-surface p-4">
        <h3 className="mb-3 font-mono text-[10px] uppercase tracking-widest text-fg-dim">
          Create Snapshot
        </h3>
        {!stopped && (
          <p className="mb-3 rounded-sm border border-warn/40 bg-warn/10 px-3 py-2 font-mono text-[11px] text-warn">
            VM must be stopped to create or restore snapshots (current: {vm.status.toLowerCase()}).
          </p>
        )}
        <div className="flex items-center gap-2">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="26.1-clean"
            disabled={!stopped}
            className="flex-1 rounded-sm border border-border bg-base px-3 py-1.5 font-mono text-xs text-fg outline-none focus:border-accent disabled:opacity-40"
          />
          <Button
            variant="primary"
            icon={<Plus className="h-3.5 w-3.5" />}
            disabled={!stopped || !name.trim() || create.isPending}
            onClick={() =>
              guard(async () => {
                await create.mutateAsync(name.trim());
                setName("");
              })
            }
          >
            {create.isPending ? "Saving…" : "Snapshot"}
          </Button>
        </div>
        {error && <p className="mt-2 font-mono text-[11px] text-error">{error}</p>}
      </div>

      {/* List */}
      <div className="rounded-md border border-border bg-surface p-4">
        <h3 className="mb-3 font-mono text-[10px] uppercase tracking-widest text-fg-dim">
          Snapshots {snaps ? `(${snaps.length})` : ""}
        </h3>
        {isLoading ? (
          <p className="font-mono text-xs text-fg-dim">loading…</p>
        ) : snaps && snaps.length > 0 ? (
          <div className="divide-y divide-border/50">
            {snaps.map((s) => (
              <div key={s.id} className="flex items-center justify-between py-2.5">
                <div className="flex items-center gap-3">
                  <Camera className="h-4 w-4 text-fg-dim" />
                  <div>
                    <div className="font-mono text-xs text-fg">{s.name}</div>
                    <div className="font-mono text-[10px] text-fg-dim">
                      {formatBytes(s.size)} · {new Date(s.created_at).toLocaleString()}
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="primary"
                    icon={<RotateCcw className="h-3.5 w-3.5" />}
                    disabled={!stopped || restore.isPending}
                    onClick={() =>
                      guard(async () => {
                        if (!confirm(`Restore snapshot "${s.name}"? Current disk state is replaced.`)) return;
                        await restore.mutateAsync(s.name);
                      })
                    }
                  >
                    Restore
                  </Button>
                  <Button
                    variant="danger"
                    icon={<Trash2 className="h-3.5 w-3.5" />}
                    disabled={!stopped || del.isPending}
                    onClick={() =>
                      guard(async () => {
                        if (!confirm(`Delete snapshot "${s.name}"?`)) return;
                        await del.mutateAsync(s.name);
                      })
                    }
                  >
                    Delete
                  </Button>
                </div>
              </div>
            ))}
          </div>
        ) : (
          <p className="py-4 text-center font-mono text-xs text-fg-dim">no snapshots yet</p>
        )}
      </div>
    </div>
  );
}
