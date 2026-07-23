import { useEffect, useRef, useState } from "react";

export type WSState = "connecting" | "open" | "closed";

interface Options {
  binaryType?: BinaryType;
  onMessage?: (ev: MessageEvent) => void;
  onOpen?: (ws: WebSocket) => void;
  /** Reconnect automatically on unexpected close. Default true. */
  reconnect?: boolean;
}

/**
 * useReconnectingWS opens a WebSocket to url and reconnects with backoff on
 * unexpected drops. Returns the connection state and the live socket ref.
 * Passing url=null closes and holds. Handlers are kept in refs so changing them
 * does not tear down the socket.
 */
export function useReconnectingWS(url: string | null, opts: Options = {}) {
  const [state, setState] = useState<WSState>("closed");
  const wsRef = useRef<WebSocket | null>(null);
  const handlers = useRef(opts);
  handlers.current = opts;

  useEffect(() => {
    if (!url) {
      setState("closed");
      return;
    }
    let closedByUs = false;
    let attempt = 0;
    let timer: number | undefined;

    const connect = () => {
      setState("connecting");
      const ws = new WebSocket(url);
      ws.binaryType = handlers.current.binaryType ?? "blob";
      wsRef.current = ws;

      ws.onopen = () => {
        attempt = 0;
        setState("open");
        handlers.current.onOpen?.(ws);
      };
      ws.onmessage = (ev) => handlers.current.onMessage?.(ev);
      ws.onclose = () => {
        setState("closed");
        wsRef.current = null;
        if (closedByUs || handlers.current.reconnect === false) return;
        attempt += 1;
        const delay = Math.min(1000 * 2 ** attempt, 10000);
        timer = window.setTimeout(connect, delay);
      };
      ws.onerror = () => ws.close();
    };

    connect();
    return () => {
      closedByUs = true;
      if (timer) window.clearTimeout(timer);
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [url]);

  return { state, wsRef };
}
