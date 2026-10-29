import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type VM } from "../../api/client";
import { Loader2, MonitorOff, Radio, ZoomIn, ZoomOut, AlertCircle, Tv, Download } from "lucide-react";
import { DeviceFrame } from "./DeviceFrame";
import { useDisplayStream } from "../../hooks/useDisplayStream";
import { VMDisplayVNC } from "./VMDisplayVNC";

// USB HID keyboard page (0x07) usage codes for common keys.
const KEY_MAP: Record<string, number> = {
  KeyA: 0x04, KeyB: 0x05, KeyC: 0x06, KeyD: 0x07, KeyE: 0x08, KeyF: 0x09,
  KeyG: 0x0a, KeyH: 0x0b, KeyI: 0x0c, KeyJ: 0x0d, KeyK: 0x0e, KeyL: 0x0f,
  KeyM: 0x10, KeyN: 0x11, KeyO: 0x12, KeyP: 0x13, KeyQ: 0x14, KeyR: 0x15,
  KeyS: 0x16, KeyT: 0x17, KeyU: 0x18, KeyV: 0x19, KeyW: 0x1a, KeyX: 0x1b,
  KeyY: 0x1c, KeyZ: 0x1d,
  Digit1: 0x1e, Digit2: 0x1f, Digit3: 0x20, Digit4: 0x21, Digit5: 0x22,
  Digit6: 0x23, Digit7: 0x24, Digit8: 0x25, Digit9: 0x26, Digit0: 0x27,
  Enter: 0x28, Escape: 0x29, Backspace: 0x2a, Tab: 0x2b, Space: 0x2c,
  Minus: 0x2d, Equal: 0x2e, BracketLeft: 0x2f, BracketRight: 0x30,
  Backslash: 0x31, Semicolon: 0x33, Quote: 0x34, Backquote: 0x35,
  Comma: 0x36, Period: 0x37, Slash: 0x38,
  ArrowRight: 0x4f, ArrowLeft: 0x50, ArrowDown: 0x51, ArrowUp: 0x52,
  Delete: 0x4c, Home: 0x4a, End: 0x4d, PageUp: 0x4b, PageDown: 0x4e,
  F1: 0x3a, F2: 0x3b, F3: 0x3c, F4: 0x3d, F5: 0x3e, F6: 0x3f,
  F7: 0x40, F8: 0x41, F9: 0x42, F10: 0x43, F11: 0x44, F12: 0x45,
};

// HID modifier usage codes (page 0x07).
const MOD_MAP: Record<string, number> = {
  ShiftLeft: 0xe1, ShiftRight: 0xe5,
  ControlLeft: 0xe0, ControlRight: 0xe4,
  AltLeft: 0xe2, AltRight: 0xe6,
  MetaLeft: 0xe3, MetaRight: 0xe7,
};

export function VMDisplay({ vm }: { vm: VM }) {
  const running = vm.status === "RUNNING";
  const qc = useQueryClient();

  const { data: vncStatus } = useQuery({
    queryKey: ["vnc-server", vm.id],
    queryFn: () => api.vncServerStatus(vm.id),
    enabled: running,
    refetchInterval: (q) => {
      const d = q.state.data;
      if (d?.installed && d?.running) return false;
      return 5000;
    },
  });

  const installVNC = useMutation({
    mutationFn: () => api.vncServerInstall(vm.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["vnc-server", vm.id] });
    },
  });

  // Use VNC mode when TrollVNC is running.
  const useVNC = vncStatus?.installed && vncStatus?.running;

  if (useVNC) {
    return <VMDisplayVNC vm={vm} vncPort={vncStatus.port} />;
  }

  // Show install banner when not installed and VM is running.
  const showInstallBanner = running && vncStatus && !vncStatus.installed && !installVNC.isPending;
  const installing = installVNC.isPending;

  return (
    <div className="flex h-full flex-col gap-2">
      {(showInstallBanner || installing) && (
        <div className="flex items-center gap-3 rounded-md border border-border bg-surface/50 px-4 py-2">
          <Tv className="h-4 w-4 shrink-0 text-accent" />
          <div className="flex-1">
            <p className="font-mono text-xs text-fg">
              {installing ? "Installing VNC server..." : "Install VNC server for native display input"}
            </p>
            <p className="font-mono text-[10px] text-fg-dim">
              {installing
                ? "Pushing TrollVNC to the guest..."
                : "Scroll, keyboard, and clipboard work natively over VNC"}
            </p>
          </div>
          {!installing && (
            <button
              onClick={() => installVNC.mutate()}
              className="flex items-center gap-1.5 rounded bg-accent/10 px-3 py-1.5 font-mono text-xs text-accent transition-colors hover:bg-accent/20"
            >
              <Download className="h-3 w-3" />
              Install
            </button>
          )}
          {installing && <Loader2 className="h-4 w-4 animate-spin text-accent" />}
        </div>
      )}
      {installVNC.isError && (
        <p className="font-mono text-xs text-red-400 px-1">
          {(installVNC.error as Error)?.message || "VNC install failed"}
        </p>
      )}
      <div className="min-h-0 flex-1">
        <VMDisplayWebCodecs vm={vm} />
      </div>
    </div>
  );
}

function VMDisplayWebCodecs({ vm }: { vm: VM }) {
  const running = vm.status === "RUNNING";
  const [scale, setScale] = useState(1);
  const [focused, setFocused] = useState(false);
  const downPoint = useRef<{ x: number; y: number; t: number } | null>(null);
  const scrollTimer = useRef<number | undefined>(undefined);
  const scrolling = useRef(false);
  const overlayRef = useRef<HTMLDivElement>(null);

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
      const pointX = nx * (stream.sourceWidth / 3);
      const pointY = ny * (stream.sourceHeight / 3);
      return { x: pointX, y: pointY };
    },
    [stream.sourceWidth, stream.sourceHeight],
  );

  const onPointerDown = (e: React.PointerEvent) => {
    const p = toVMCoords(e.clientX, e.clientY);
    if (p) downPoint.current = { x: p.x, y: p.y, t: Date.now() };
    // Focus the overlay for keyboard events.
    overlayRef.current?.focus();
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

  // Attach a non-passive wheel listener so we can preventDefault (React's
  // onWheel is passive and can't prevent the page from scrolling).
  useEffect(() => {
    const el = overlayRef.current;
    if (!el || !running) return;

    const handler = (e: WheelEvent) => {
      e.preventDefault();
      e.stopPropagation();
      const canvas = stream.canvasRef.current;
      if (!canvas) return;
      const rect = canvas.getBoundingClientRect();
      if (rect.width === 0 || rect.height === 0) return;
      const nx = (e.clientX - rect.left) / rect.width;
      const ny = (e.clientY - rect.top) / rect.height;
      if (nx < 0 || nx > 1 || ny < 0 || ny > 1) return;

      if (!scrolling.current) {
        scrolling.current = true;
        api.scroll(vm.id, "began", 0, 0, nx, ny).catch(() => {});
      }

      const dy = -e.deltaY * 0.5;
      const dx = -e.deltaX * 0.5;
      api.scroll(vm.id, "changed", dx, dy).catch(() => {});

      if (scrollTimer.current) window.clearTimeout(scrollTimer.current);
      scrollTimer.current = window.setTimeout(() => {
        scrolling.current = false;
        api.scroll(vm.id, "ended", 0, 0).catch(() => {});
      }, 150);
    };

    el.addEventListener("wheel", handler, { passive: false });
    return () => el.removeEventListener("wheel", handler);
  }, [vm.id, running, stream.status, stream.canvasRef]);

  // Track which keys are currently held so we don't re-send on browser repeat.
  const heldKeys = useRef(new Set<string>());

  // Keyboard: use HID long_press mode so the VM handles key repeat natively.
  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (!focused) return;

      // Intercept Cmd/Ctrl+V for paste.
      if ((e.metaKey || e.ctrlKey) && e.code === "KeyV") {
        e.preventDefault();
        navigator.clipboard.readText().then((text) => {
          if (text) api.pasteText(vm.id, text).catch(() => {});
        }).catch(() => {});
        return;
      }

      // Intercept Cmd/Ctrl+C for copy from VM clipboard.
      if ((e.metaKey || e.ctrlKey) && e.code === "KeyC") {
        e.preventDefault();
        api.rpc(vm.id, "clipboard.get", {}).then((result) => {
          const text = (result as Record<string, unknown>)?.text as string;
          if (text) navigator.clipboard.writeText(text).catch(() => {});
        }).catch(() => {});
        return;
      }

      e.preventDefault();

      // Skip browser-generated repeat events — the VM handles repeat itself.
      if (e.repeat) return;

      const code = e.code;
      const usage = MOD_MAP[code] ?? KEY_MAP[code];
      if (!usage) return;

      heldKeys.current.add(code);
      api.hidHold(vm.id, 0x07, usage, true).catch(() => {});
    },
    [vm.id, focused],
  );

  const onKeyUp = useCallback(
    (e: React.KeyboardEvent) => {
      if (!focused) return;
      e.preventDefault();

      const code = e.code;
      heldKeys.current.delete(code);
      const usage = MOD_MAP[code] ?? KEY_MAP[code];
      if (!usage) return;

      api.hidHold(vm.id, 0x07, usage, false).catch(() => {});
    },
    [vm.id, focused],
  );

  // Release all held keys when the overlay loses focus.
  const onBlurRelease = useCallback(() => {
    setFocused(false);
    for (const code of heldKeys.current) {
      const usage = MOD_MAP[code] ?? KEY_MAP[code];
      if (usage) api.hidHold(vm.id, 0x07, usage, false).catch(() => {});
    }
    heldKeys.current.clear();
  }, [vm.id]);

  // Clean up scroll timer on unmount.
  useEffect(() => {
    return () => {
      if (scrollTimer.current) window.clearTimeout(scrollTimer.current);
    };
  }, []);

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
        {focused && (
          <span className="ml-1 text-accent">⌨</span>
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

        {/* Touch/scroll/keyboard overlay — captures all input when streaming */}
        {running && stream.status === "streaming" && (
          <div
            ref={overlayRef}
            tabIndex={0}
            className={`absolute inset-0 z-20 outline-none ${focused ? "cursor-text ring-1 ring-accent/30" : "cursor-crosshair"}`}
            onPointerDown={onPointerDown}
            onPointerUp={onPointerUp}
            onKeyDown={onKeyDown}
            onKeyUp={onKeyUp}
            onFocus={() => setFocused(true)}
            onBlur={onBlurRelease}
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
