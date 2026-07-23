import { Play, Square, RotateCw, Trash2 } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { ApiError, type VM } from "../../api/client";
import { useBootVM, useStopVM } from "../../hooks/useVM";
import { api } from "../../api/client";
import { Button } from "../ui/Button";
import { useState } from "react";
import { DeleteVMDialog } from "./DeleteVMDialog";

/** Power controls for a single VM. */
export function VMControls({ vm }: { vm: VM }) {
  const boot = useBootVM();
  const stop = useStopVM();
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [deleting, setDeleting] = useState(false);

  const busy =
    boot.isPending ||
    stop.isPending ||
    restarting ||
    vm.status === "BOOTING" ||
    vm.status === "STOPPING" ||
    vm.status === "DELETING";

  const isRunning = vm.status === "RUNNING";

  async function guard(fn: () => Promise<unknown>) {
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "action failed");
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      {isRunning ? (
        <Button
          variant="danger"
          icon={<Square className="h-3.5 w-3.5" />}
          disabled={busy}
          onClick={() => guard(() => stop.mutateAsync(vm.id))}
        >
          Stop
        </Button>
      ) : (
        <Button
          variant="success"
          icon={<Play className="h-3.5 w-3.5" />}
          disabled={busy}
          onClick={() => guard(() => boot.mutateAsync(vm.id))}
        >
          Boot
        </Button>
      )}

      <Button
        variant="primary"
        icon={<RotateCw className="h-3.5 w-3.5" />}
        disabled={busy || !isRunning}
        onClick={() =>
          guard(async () => {
            setRestarting(true);
            try {
              await api.restartVM(vm.id);
            } finally {
              setRestarting(false);
            }
          })
        }
      >
        Restart
      </Button>

      <Button
        variant="ghost"
        icon={<Trash2 className="h-3.5 w-3.5" />}
        disabled={busy}
        onClick={() => setDeleting(true)}
      >
        Destroy
      </Button>

      {error && (
        <span className="font-mono text-xs text-error">{error}</span>
      )}

      {deleting && (
        <DeleteVMDialog
          vm={vm}
          onClose={() => setDeleting(false)}
          onDeleted={() => {
            setDeleting(false);
            navigate("/");
          }}
        />
      )}
    </div>
  );
}
