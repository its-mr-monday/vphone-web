import { useEffect, useRef, useCallback, useState } from "react";
import RFB from "@novnc/novnc";
import { portTunnelWebSocketURL, type VM } from "../../api/client";
import { Loader2, MonitorOff, Radio, ZoomIn, ZoomOut } from "lucide-react";
import { DeviceFrame } from "./DeviceFrame";

type ConnectionState = "connecting" | "connected" | "disconnected" | "error";

export function VMDisplayVNC({ vm, vncPort = 5900 }: { vm: VM; vncPort?: number }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<RFB | null>(null);
  const [status, setStatus] = useState<ConnectionState>("connecting");
  const [scale, setScale] = useState(1);
  const reconnectTimer = useRef<number | undefined>(undefined);
  const attempt = useRef(0);

  const zoom = (delta: number) =>
    setScale((s) => Math.min(1.5, Math.max(0.5, +(s + delta).toFixed(2))));

  const connect = useCallback(() => {
    if (!containerRef.current) return;

    // Clean up previous instance.
    if (rfbRef.current) {
      rfbRef.current.disconnect();
      rfbRef.current = null;
    }

    setStatus("connecting");

    const wsUrl = portTunnelWebSocketURL(vm.id, vncPort);
    const rfb = new RFB(containerRef.current, wsUrl, {
      wsProtocols: [],
    });

    rfb.viewOnly = false;
    rfb.scaleViewport = true;
    rfb.resizeSession = false;
    rfb.showDotCursor = false;
    rfb.qualityLevel = 8;
    rfb.compressionLevel = 2;

    // Set the VNC password (TrollVNC default: "alpine").
    rfb.addEventListener("credentialsrequired", () => {
      rfb.sendCredentials({ password: vm.vnc_password || "alpine" });
    });

    rfb.addEventListener("connect", () => {
      setStatus("connected");
      attempt.current = 0;
    });

    rfb.addEventListener("disconnect", (e: CustomEvent) => {
      rfbRef.current = null;
      if (e.detail?.clean) {
        setStatus("disconnected");
      } else {
        setStatus("error");
      }
      // Auto-reconnect with backoff.
      attempt.current += 1;
      const delay = Math.min(1000 * 2 ** attempt.current, 10000);
      reconnectTimer.current = window.setTimeout(connect, delay);
    });

    rfbRef.current = rfb;
  }, [vm.id, vm.vnc_password, vncPort]);

  useEffect(() => {
    connect();
    return () => {
      if (reconnectTimer.current) window.clearTimeout(reconnectTimer.current);
      if (rfbRef.current) {
        rfbRef.current.disconnect();
        rfbRef.current = null;
      }
    };
  }, [connect]);

  return (
    <div className="relative flex h-full w-full items-center justify-center">
      {/* Status badge */}
      <div className="pointer-events-none absolute left-3 top-3 z-10 flex items-center gap-2 rounded-sm border border-border/60 bg-surface/80 px-2 py-1 font-mono text-[10px] uppercase tracking-wider backdrop-blur">
        {status === "connected" ? (
          <>
            <Radio className="h-3 w-3 text-success" />
            <span className="text-success">vnc</span>
          </>
        ) : status === "connecting" ? (
          <>
            <Loader2 className="h-3 w-3 animate-spin text-accent" />
            <span className="text-accent">connecting</span>
          </>
        ) : (
          <>
            <MonitorOff className="h-3 w-3 text-fg-dim" />
            <span className="text-fg-dim">disconnected</span>
          </>
        )}
      </div>

      {/* Zoom controls */}
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

      <DeviceFrame running={true} scale={scale}>
        <div
          ref={containerRef}
          className="h-full w-full [&>div]:!h-full [&>div]:!w-full [&_canvas]:!h-full [&_canvas]:!w-full"
        />
        {status !== "connected" && (
          <div className="pointer-events-none absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-black/80">
            {status === "connecting" ? (
              <Loader2 className="h-10 w-10 animate-spin text-accent/60" />
            ) : (
              <>
                <MonitorOff className="h-10 w-10 text-fg-dim" />
                <p className="font-mono text-xs text-fg-dim">reconnecting...</p>
              </>
            )}
          </div>
        )}
      </DeviceFrame>
    </div>
  );
}
