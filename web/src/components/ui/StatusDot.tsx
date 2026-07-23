import type { VMStatus } from "../../api/client";
import { STATUS_META, statusColor } from "../../lib/status";

interface Props {
  status: VMStatus;
  size?: number;
  showLabel?: boolean;
}

/** A colored status indicator; pulses while the VM is in a transitional state. */
export function StatusDot({ status, size = 8, showLabel = false }: Props) {
  const meta = STATUS_META[status];
  const color = statusColor(status);
  return (
    <span className="inline-flex items-center gap-2">
      <span
        className={`relative inline-block rounded-full ${meta.transient ? "animate-pulse-dot" : ""}`}
        style={{
          width: size,
          height: size,
          background: color,
          boxShadow: `0 0 ${size}px ${color}`,
        }}
      />
      {showLabel && (
        <span
          className="font-mono text-xs uppercase tracking-wider"
          style={{ color }}
        >
          {meta.label}
        </span>
      )}
    </span>
  );
}
