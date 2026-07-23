import { useEffect, useRef, useState } from "react";
import { jobLogsWebSocketURL } from "../../api/client";
import { useReconnectingWS } from "../../hooks/useReconnectingWS";

/**
 * JobLog streams a job's combined stdout/stderr over a WebSocket into a
 * terminal-style viewer, auto-scrolling to the tail. When the job finishes the
 * server sends the final buffer and closes. The stream does NOT auto-reconnect
 * on normal close (the job is done); it only reconnects on unexpected drops.
 */
export function JobLog({ jobId, height = "100%" }: { jobId: string; height?: string | number }) {
  const [lines, setLines] = useState<string[]>([]);
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickBottom = useRef(true);

  // Reset when the job changes.
  useEffect(() => setLines([]), [jobId]);

  const { state } = useReconnectingWS(jobLogsWebSocketURL(jobId), {
    reconnect: false,
    onMessage: (ev) => {
      const text = typeof ev.data === "string" ? ev.data : "";
      if (!text) return;
      setLines((prev) => {
        const merged = (prev.join("\n") + text).split("\n");
        // Cap to a sane number of lines.
        return merged.slice(-4000);
      });
    },
  });

  useEffect(() => {
    if (stickBottom.current && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [lines]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  };

  return (
    <div
      className="flex flex-col overflow-hidden rounded-md border border-border bg-black"
      style={{ height }}
    >
      <div className="flex items-center justify-between border-b border-border px-3 py-1.5">
        <span className="font-mono text-[10px] uppercase tracking-widest text-fg-dim">
          job output
        </span>
        <span
          className={`font-mono text-[10px] uppercase ${
            state === "open" ? "text-success" : state === "connecting" ? "text-accent" : "text-fg-dim"
          }`}
        >
          {state === "open" ? "● streaming" : state === "connecting" ? "connecting" : "ended"}
        </span>
      </div>
      <div
        ref={scrollRef}
        onScroll={onScroll}
        className="flex-1 overflow-auto px-3 py-2 font-mono text-[11px] leading-relaxed text-fg-muted"
      >
        {lines.length === 0 ? (
          <span className="text-fg-dim">waiting for output…</span>
        ) : (
          lines.map((line, i) => (
            <div key={i} className="whitespace-pre-wrap break-all">
              {colorize(line)}
            </div>
          ))
        )}
      </div>
    </div>
  );
}

// colorize applies subtle status coloring to common log line shapes.
function colorize(line: string) {
  const l = line.toLowerCase();
  if (l.includes("[ok]") || l.includes("complete") || l.includes(" done"))
    return <span className="text-success">{line}</span>;
  if (l.includes("error") || l.includes("[exit]") || l.includes("failed") || l.includes("[-]"))
    return <span className="text-error">{line}</span>;
  if (l.startsWith("$ ") || l.startsWith("== ") || l.includes("[dfu]"))
    return <span className="text-accent">{line}</span>;
  if (l.includes("warn"))
    return <span className="text-warn">{line}</span>;
  return line;
}
