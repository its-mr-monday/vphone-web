import { useCallback, useRef, useState } from "react";
import { api, type VM } from "../../api/client";
import { Loader2, MonitorOff, Radio, ZoomIn, ZoomOut, AlertCircle } from "lucide-react";
import { DeviceFrame } from "./DeviceFrame";
import { useDisplayStream } from "../../hooks/useDisplayStream";

/**
 * VMDisplay renders the VM screen via WebCodecs H.264/HEVC decoding through the
 * server's WebSocket screen stream proxy. Touch input is routed through the RPC
 * API (tap/swipe). Replaces the former noVNC-based display.
 */
export function VMDisplay({ vm }: { vm: VM }) {
  const running = vm.status === "RUNNING";
  const [scale, setScale] = useState(1);
  const downPoint = useRef<{ x: number; y: number; t: number } | null>(null);

  const stream = useDisplayStream({
    vmId: vm.id,
    enabled: running,
    fps: 30,
    bitrate: 12_000_000,
    maxWidth: 1500,
    maxHeight: 3000,
  });

  const zoom = (delta: number) => setScale((s) => Math.min(1.5, Math.max(0.5, +(s + delta).toFixed(2))));

  const toVMCoords = useCallback(
    (clientX: number, clientY: number): { x: number; y: number } | null => {
      const canvas = stream.canvasRef.current;
      if (!canvas) return null;
      const rect = canvas.getBoundingClientRect();
      if (rect.width === 0 || rect.height === 0) return null;
      const nx = (clientX - rect.left) / rect.width;
      const ny = (clientY - rect.top) / rect.height;
      if (nx < 0 || nx > 1 || ny < 0 || ny > 1) return null;
      // vphoned expects UIKit point coordinates, not pixels (all modern iPhones are 3x)
      const pointX = nx * (stream.sourceWidth / 3);
      const pointY = ny * (stream.sourceHeight / 3);
      return { x: pointX, y: pointY };
    },
    [stream.sourceWidth, stream.sourceHeight],
  );

  const onPointerDown = (e: React.PointerEvent) => {
    const p = toVMCoords(e.clientX, e.clientY);
    if (p) downPoint.current = { x: p.x, y: p.y, t: Date.now() };
  };

  const onPointerUp = (e: React.PointerEvent) => {
    if (!downPoint.current) return;
    const start = downPoint.current;
    downPoint.current = null;
    const end = toVMCoords(e.clientX, e.clientY);
    if (!end) return;
    const dist = Math.hypot(end.x - start.x, end.y - start.y);
    if (dist > 15) {
      const ms = Math.min(400, Math.max(50, Date.now() - start.t));
      api.swipe(vm.id, start.x, start.y, end.x, end.y, ms).catch(() => {});
    } else {
      api.tap(vm.id, end.x, end.y).catch(() => {});
    }
  };

  return (
    <div className="relative flex h-full w-full items-center justify-center">
      {/* Status badge */}
      <div className="pointer-events-none absolute left-3 top-3 z-10 flex items-center gap-2 rounded-sm border border-border/60 bg-surface/80 px-2 py-1 font-mono text-[10px] uppercase tracking-wider backdrop-blur">
        {stream.status === "streaming" ? (
          <>
            <Radio className="h-3 w-3 text-success" />
            <span className="text-success">streaming</span>
            <span className="text-fg-dim">{stream.measuredFps}fps</span>
          </>
        ) : stream.status === "connecting" ? (
          <>
            <Loader2 className="h-3 w-3 animate-spin text-accent" />
            <span className="text-accent">connecting</span>
          </>
        ) : stream.status === "unsupported" ? (
          <>
            <AlertCircle className="h-3 w-3 text-warn" />
            <span className="text-warn">unsupported</span>
          </>
        ) : (
          <>
            <MonitorOff className="h-3 w-3 text-fg-dim" />
            <span className="text-fg-dim">no signal</span>
          </>
        )}
      </div>

      {/* Top-right controls: zoom */}
      {running && (
        <div className="absolute right-3 top-3 z-10 flex items-center gap-2">
          <div className="flex items-center overflow-hidden rounded-sm border border-border/60 bg-surface/80 backdrop-blur">
            <button onClick={() => zoom(-0.1)} className="px-2 py-1 text-fg-dim hover:text-accent" title="Zoom out">
              <ZoomOut className="h-3 w-3" />
            </button>
            <span className="px-1 font-mono text-[10px] text-fg-muted">{Math.round(scale * 100)}%</span>
            <button onClick={() => zoom(0.1)} className="px-2 py-1 text-fg-dim hover:text-accent" title="Zoom in">
              <ZoomIn className="h-3 w-3" />
            </button>
          </div>
        </div>
      )}

      <DeviceFrame running={running} scale={scale}>
        <canvas ref={stream.canvasRef} data-display-canvas className="h-full w-full" />

        {/* Touch overlay — always active when streaming */}
        {running && stream.status === "streaming" && (
          <div
            className="absolute inset-0 z-20 cursor-crosshair"
            onPointerDown={onPointerDown}
            onPointerUp={onPointerUp}
          />
        )}

        {/* Non-streaming overlays */}
        {stream.status !== "streaming" && (
          <div className="pointer-events-none absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-black">
            {!running ? (
              <>
                <MonitorOff className="h-10 w-10 text-fg-dim" />
                <p className="font-mono text-xs text-fg-dim">powered off</p>
              </>
            ) : stream.status === "connecting" ? (
              <Loader2 className="h-10 w-10 animate-spin text-accent/60" />
            ) : stream.status === "unsupported" ? (
              <>
                <AlertCircle className="h-10 w-10 text-warn" />
                <p className="font-mono text-xs text-warn">WebCodecs not supported</p>
                <p className="max-w-[70%] text-center font-mono text-[10px] text-fg-dim/70">
                  use Chrome or Edge for hardware-decoded display streaming
                </p>
              </>
            ) : stream.status === "error" ? (
              <>
                <MonitorOff className="h-10 w-10 text-error" />
                <p className="font-mono text-xs text-error">{stream.error || "stream error"}</p>
                <p className="max-w-[70%] text-center font-mono text-[10px] text-fg-dim/70">
                  reconnecting automatically...
                </p>
              </>
            ) : (
              <>
                <MonitorOff className="h-10 w-10 text-fg-dim" />
                <p className="font-mono text-xs text-fg-dim">no signal</p>
                <p className="max-w-[70%] text-center font-mono text-[10px] text-fg-dim/70">
                  reconnecting automatically...
                </p>
              </>
            )}
          </div>
        )}
      </DeviceFrame>
    </div>
  );
}
