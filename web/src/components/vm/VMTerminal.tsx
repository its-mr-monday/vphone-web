import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { terminalWebSocketURL } from "../../api/client";
import { TerminalSquare } from "lucide-react";

/**
 * VMTerminal renders an xterm.js terminal bridged to the VM's SSH port through
 * the server's WebSocket→TCP proxy. Keystrokes are sent as binary frames and
 * server bytes are written back. The terminal fits its container and re-fits on
 * resize; the socket auto-reconnects if it drops.
 *
 * Note: the proxy is a raw byte bridge to the device SSH port (per the project
 * design). A full interactive shell over raw SSH additionally requires the SSH
 * handshake; this component wires the transport end-to-end so the terminal is
 * live the moment a compatible endpoint answers.
 */
export function VMTerminal({ vmId, active }: { vmId: string; active: boolean }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    if (!active || !containerRef.current) return;

    const term = new Terminal({
      convertEol: true,
      cursorBlink: true,
      fontFamily: '"JetBrains Mono", "SF Mono", Menlo, monospace',
      fontSize: 13,
      theme: {
        background: "#000000",
        foreground: "#e6e6ea",
        cursor: "#00e5ff",
        selectionBackground: "#00e5ff44",
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(containerRef.current);
    fit.fit();
    termRef.current = term;
    fitRef.current = fit;

    let closedByUs = false;
    let attempt = 0;
    let retryTimer: number | undefined;
    const enc = new TextEncoder();

    // Send the terminal dimensions to the server as a JSON control frame so the
    // remote PTY resizes (keystrokes are sent as binary; resize as text JSON).
    const sendResize = () => {
      const ws = wsRef.current;
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ resize: { cols: term.cols, rows: term.rows } }));
      }
    };

    const connect = () => {
      const ws = new WebSocket(terminalWebSocketURL(vmId));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      ws.onopen = () => {
        attempt = 0;
        // Send the current terminal size so the remote PTY matches xterm.
        sendResize();
      };
      ws.onmessage = (ev) => {
        if (ev.data instanceof ArrayBuffer) {
          term.write(new Uint8Array(ev.data));
        } else if (typeof ev.data === "string") {
          term.write(ev.data);
        }
      };
      ws.onclose = () => {
        wsRef.current = null;
        if (closedByUs) return;
        attempt += 1;
        term.writeln(`\x1b[33m[disconnected — reconnecting…]\x1b[0m`);
        retryTimer = window.setTimeout(connect, Math.min(1000 * 2 ** attempt, 8000));
      };
      ws.onerror = () => ws.close();
    };
    connect();

    // Send keystrokes to the backend.
    const dataSub = term.onData((data) => {
      const ws = wsRef.current;
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(enc.encode(data));
      }
    });

    // Re-fit on container resize and notify the remote PTY of the new size.
    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
        sendResize();
      } catch {
        /* container detached */
      }
    });
    ro.observe(containerRef.current);

    return () => {
      closedByUs = true;
      if (retryTimer) window.clearTimeout(retryTimer);
      ro.disconnect();
      dataSub.dispose();
      wsRef.current?.close();
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, [vmId, active]);

  if (!active) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 rounded-md border border-border bg-black">
        <TerminalSquare className="h-10 w-10 text-fg-dim" />
        <p className="font-mono text-xs text-fg-dim">terminal available when the VM is running</p>
      </div>
    );
  }

  return (
    <div className="h-full overflow-hidden rounded-md border border-border bg-black p-2">
      <div ref={containerRef} className="h-full w-full" />
    </div>
  );
}
