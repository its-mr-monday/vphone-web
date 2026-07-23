import { useState } from "react";
import { Home, Lock, Volume2, Volume1, Camera } from "lucide-react";
import { api } from "../../api/client";

/**
 * VMHardwareBar renders device-style hardware controls that inject via the
 * vphone.sock control socket: Home, Lock (side button), Volume Up/Down, and a
 * Screenshot capture that downloads a PNG. Rendered below the display.
 */
export function VMHardwareBar({ vmId, disabled }: { vmId: string; disabled: boolean }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function pressKey(key: string) {
    setError(null);
    setBusy(key);
    try {
      await api.key(vmId, key);
    } catch (e) {
      setError(e instanceof Error ? e.message : "action failed");
    } finally {
      setBusy(null);
    }
  }

  async function screenshot() {
    setError(null);
    setBusy("screenshot");
    try {
      // Prefer capturing the live noVNC canvas: it's the real guest framebuffer
      // and it works for headless VMs (which have no host capture view). Fall back
      // to the server-side capture only if the canvas isn't available.
      let blob: Blob | null = null;
      const canvas = document.querySelector<HTMLCanvasElement>("[data-vm-display] canvas");
      if (canvas && canvas.width > 0 && canvas.height > 0) {
        blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/png"));
      }
      if (!blob) {
        const res = await fetch(api.screenshotURL(vmId), { method: "POST" });
        if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || "capture failed");
        blob = await res.blob();
      }
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `vphone-${vmId.slice(0, 8)}-${Date.now()}.png`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      setError(e instanceof Error ? e.message : "capture failed");
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <PillButton label="Home" icon={<Home className="h-4 w-4" />} disabled={disabled} busy={busy === "home"} onClick={() => pressKey("home")} />
      <PillButton label="Lock" icon={<Lock className="h-4 w-4" />} disabled={disabled} busy={busy === "lock"} onClick={() => pressKey("lock")} />
      <PillButton label="Vol +" icon={<Volume2 className="h-4 w-4" />} disabled={disabled} busy={busy === "volume_up"} onClick={() => pressKey("volume_up")} />
      <PillButton label="Vol −" icon={<Volume1 className="h-4 w-4" />} disabled={disabled} busy={busy === "volume_down"} onClick={() => pressKey("volume_down")} />
      <div className="mx-1 h-5 w-px bg-border" />
      <PillButton label="Screenshot" icon={<Camera className="h-4 w-4" />} disabled={disabled} busy={busy === "screenshot"} onClick={screenshot} />
      {error && <span className="font-mono text-[11px] text-error">{error}</span>}
    </div>
  );
}

function PillButton({
  label,
  icon,
  onClick,
  disabled,
  busy,
}: {
  label: string;
  icon: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  busy?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled || busy}
      title={label}
      className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface-2 px-3 py-1.5 font-mono text-[11px] text-fg-muted transition-colors hover:border-accent/60 hover:text-accent disabled:cursor-not-allowed disabled:opacity-40"
    >
      {icon}
      <span className="hidden sm:inline">{label}</span>
    </button>
  );
}
