import type { VMStatus } from "../api/client";

interface StatusMeta {
  /** Tailwind text/bg color token suffix, e.g. "success" -> text-success. */
  color: "success" | "accent" | "warn" | "error" | "fg-dim";
  label: string;
  /** Whether the status is transitional (worth animating). */
  transient: boolean;
}

export const STATUS_META: Record<VMStatus, StatusMeta> = {
  RUNNING: { color: "success", label: "running", transient: false },
  BOOTING: { color: "accent", label: "booting", transient: true },
  STOPPING: { color: "warn", label: "stopping", transient: true },
  DELETING: { color: "error", label: "deleting", transient: true },
  STOPPED: { color: "fg-dim", label: "stopped", transient: false },
  CREATING: { color: "accent", label: "creating", transient: true },
  RESTORING: { color: "accent", label: "restoring", transient: true },
  INSTALLING_CFW: { color: "accent", label: "installing", transient: true },
  ERROR: { color: "error", label: "error", transient: false },
};

/** Hex color for a status, for inline styles (e.g. the status dot). */
export const STATUS_HEX: Record<StatusMeta["color"], string> = {
  success: "#00e676",
  accent: "#00e5ff",
  warn: "#ffab00",
  error: "#ff1744",
  "fg-dim": "#55555e",
};

export function statusColor(status: VMStatus): string {
  return STATUS_HEX[STATUS_META[status].color];
}
