import { useCallback, useEffect, useRef, useState } from "react";
import RFB from "@novnc/novnc";
import { api, vncWebSocketURL, type VM } from "../../api/client";
import { Loader2, MonitorOff, Wifi, Hand, ZoomIn, ZoomOut } from "lucide-react";
import { DeviceFrame } from "./DeviceFrame";

type ConnState = "connecting" | "connected" | "disconnected";

/**
 * VMDisplay embeds a noVNC RFB session for a running VM through the server's
 * WebSocket→TCP VNC proxy, auto-reconnecting if the socket drops.
 *
 * Input model: by default noVNC handles input natively over RFB — mouse acts as
 * touch and the keyboard types into the guest (this is what a native VNC client
 * does, so keyboard input works). An optional "socket touch" mode instead routes
 * taps/swipes through the vphone.sock control socket (precise coordinates) while
 * making RFB view-only.
 */
export function VMDisplay({ vm }: { vm: VM }) {
  const running = vm.status === "RUNNING";
  const screenRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<RFB | null>(null);
  const [state, setState] = useState<ConnState>("disconnected");
  const [detail, setDetail] = useState("");
  // Default OFF → native RFB input (mouse + keyboard). ON → socket-tap injection.
  const [socketTouch, setSocketTouch] = useState(false);
  const [scale, setScale] = useState(1);
  const [attempt, setAttempt] = useState(0);
  const downPoint = useRef<{ x: number; y: number; t: number } | null>(null);

  useEffect(() => {
    if (!running || !screenRef.current) {
      setState("disconnected");
      return;
    }
    setState("connecting");
    setDetail("");
    const rfb = new RFB(screenRef.current, vncWebSocketURL(vm.id), {
      wsProtocols: ["binary"],
      // Guest TrollVNC requires VNC auth; supply the known password.
      credentials: { password: vm.vnc_password },
    });
    rfb.scaleViewport = true;
    rfb.resizeSession = false;
    rfb.background = "transparent";
    rfb.viewOnly = socketTouch; // false by default → RFB mouse + keyboard active
    rfb.focusOnClick = true;
    rfbRef.current = rfb;

    let retry: number | undefined;
    const onConnect = () => {
      setState("connected");
      try {
        rfb.focus();
      } catch {
        /* older noVNC */
      }
    };
    const onDisconnect = (e: Event) => {
      setState("disconnected");
      const clean = (e as CustomEvent<{ clean: boolean }>).detail?.clean;
      setDetail(clean ? "session ended" : "backend unreachable");
      retry = window.setTimeout(() => setAttempt((a) => a + 1), 3000);
    };
    rfb.addEventListener("connect", onConnect);
    rfb.addEventListener("disconnect", onDisconnect);

    return () => {
      if (retry) window.clearTimeout(retry);
      rfb.removeEventListener("connect", onConnect);
      rfb.removeEventListener("disconnect", onDisconnect);
      try {
        rfb.disconnect();
      } catch {
        /* already gone */
      }
      rfbRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [vm.id, running, attempt]);

  // Keep RFB view-only in sync with the input-mode toggle.
  useEffect(() => {
    if (rfbRef.current) rfbRef.current.viewOnly = socketTouch;
  }, [socketTouch]);

  const zoom = (delta: number) => setScale((s) => Math.min(1.5, Math.max(0.5, +(s + delta).toFixed(2))));

  // Map a browser pointer event to VM pixel coordinates using the live canvas rect.
  const toVMCoords = useCallback(
    (clientX: number, clientY: number): { x: number; y: number } | null => {
      const canvas = screenRef.current?.querySelector("canvas");
      if (!canvas) return null;
      const rect = canvas.getBoundingClientRect();
      if (rect.width === 0 || rect.height === 0) return null;
      const nx = (clientX - rect.left) / rect.width;
      const ny = (clientY - rect.top) / rect.height;
      if (nx < 0 || nx > 1 || ny < 0 || ny > 1) return null;
      return { x: nx * vm.screen_width, y: ny * vm.screen_height };
    },
    [vm.screen_width, vm.screen_height],
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
    if (dist > 30) {
      const ms = Math.min(600, Math.max(120, Date.now() - start.t));
      api.touch(vm.id, { type: "swipe", x: start.x, y: start.y, x2: end.x, y2: end.y, ms }).catch(() => {});
    } else {
      api.touch(vm.id, { type: "tap", x: end.x, y: end.y }).catch(() => {});
    }
  };

  return (
    <div className="relative flex h-full w-full items-center justify-center">
      {/* Status badge */}
      <div className="pointer-events-none absolute left-3 top-3 z-10 flex items-center gap-2 rounded-sm border border-border/60 bg-surface/80 px-2 py-1 font-mono text-[10px] uppercase tracking-wider backdrop-blur">
        {state === "connected" ? (
          <>
            <Wifi className="h-3 w-3 text-success" />
            <span className="text-success">vnc linked</span>
          </>
        ) : state === "connecting" ? (
          <>
            <Loader2 className="h-3 w-3 animate-spin text-accent" />
            <span className="text-accent">connecting</span>
          </>
        ) : (
          <>
            <MonitorOff className="h-3 w-3 text-fg-dim" />
            <span className="text-fg-dim">no signal</span>
          </>
        )}
      </div>

      {/* Top-right controls: input mode + zoom */}
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
          <button
            onClick={() => setSocketTouch((t) => !t)}
            className={`inline-flex items-center gap-1.5 rounded-sm border px-2 py-1 font-mono text-[10px] uppercase tracking-wider backdrop-blur transition-colors ${
              socketTouch
                ? "border-accent/60 bg-accent/10 text-accent"
                : "border-border/60 bg-surface/80 text-fg-dim hover:text-fg"
            }`}
            title={socketTouch ? "Socket-tap injection (RFB view-only)" : "Native RFB input (mouse + keyboard)"}
          >
            <Hand className="h-3 w-3" />
            {socketTouch ? "socket touch" : "rfb input"}
          </button>
        </div>
      )}

      <DeviceFrame running={running} scale={scale}>
        <div ref={screenRef} data-vm-display className="h-full w-full" />

        {/* Socket-touch overlay (only when that mode is enabled). */}
        {running && socketTouch && (
          <div
            className="absolute inset-0 z-20 cursor-crosshair"
            onPointerDown={onPointerDown}
            onPointerUp={onPointerUp}
          />
        )}

        {/* Non-connected overlays */}
        {state !== "connected" && (
          <div className="pointer-events-none absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-black">
            {!running ? (
              <>
                <MonitorOff className="h-10 w-10 text-fg-dim" />
                <p className="font-mono text-xs text-fg-dim">powered off</p>
              </>
            ) : state === "connecting" ? (
              <Loader2 className="h-10 w-10 animate-spin text-accent/60" />
            ) : (
              <>
                <MonitorOff className="h-10 w-10 text-fg-dim" />
                <p className="font-mono text-xs text-fg-dim">{detail || "no signal"}</p>
                <p className="max-w-[70%] text-center font-mono text-[10px] text-fg-dim/70">
                  reconnecting automatically…
                </p>
              </>
            )}
          </div>
        )}
      </DeviceFrame>
    </div>
  );
}
