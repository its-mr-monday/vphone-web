import { useRef, useEffect, useState, useCallback } from "react";
import { screenWebSocketURL } from "../api/client";

export interface DisplayStreamOptions {
  vmId: string;
  enabled: boolean;
  fps?: number;
  bitrate?: number;
  maxWidth?: number;
  maxHeight?: number;
}

export interface DisplayStreamState {
  status: "connecting" | "streaming" | "paused" | "disconnected" | "error" | "unsupported";
  canvasRef: React.RefObject<HTMLCanvasElement>;
  sourceWidth: number;
  sourceHeight: number;
  encodedWidth: number;
  encodedHeight: number;
  scale: number;
  codec: string;
  measuredFps: number;
  error?: string;
}

interface FrameHeader {
  id: number;
  status: "frame" | "unchanged" | "dropped";
  session?: number;
  width: number;
  height: number;
  sourceWidth: number;
  sourceHeight: number;
  scale: number;
  key?: boolean;
  record?: string;
  error?: { code: string; message: string };
}

function parseFrame(data: ArrayBuffer): { header: FrameHeader; payload: Uint8Array } {
  const view = new DataView(data);
  const headerLen = view.getUint32(0, true);
  const headerBytes = new Uint8Array(data, 4, headerLen);
  const header = JSON.parse(new TextDecoder().decode(headerBytes)) as FrameHeader;
  const payload = new Uint8Array(data, 4 + headerLen);
  return { header, payload };
}

function makeDecoderConfig(codec: string, header: FrameHeader): VideoDecoderConfig {
  const description = Uint8Array.from(atob(header.record!), c => c.charCodeAt(0));
  return {
    codec: codec === "hevc" ? "hev1.1.6.L120.90" : "avc1.64001F",
    codedWidth: header.width,
    codedHeight: header.height,
    description: description.buffer,
  };
}

async function detectCodec(): Promise<string | null> {
  if (typeof VideoDecoder === "undefined") return null;
  try {
    const hevc = await VideoDecoder.isConfigSupported({
      codec: "hev1.1.6.L120.90",
      codedWidth: 1280,
      codedHeight: 720,
    });
    if (hevc.supported) return "hevc";
  } catch { /* not supported */ }
  try {
    const avc = await VideoDecoder.isConfigSupported({
      codec: "avc1.64001F",
      codedWidth: 1280,
      codedHeight: 720,
    });
    if (avc.supported) return "h264";
  } catch { /* not supported */ }
  return null;
}

export function useDisplayStream(opts: DisplayStreamOptions): DisplayStreamState {
  const { vmId, enabled, fps = 30, bitrate = 12_000_000, maxWidth = 1500, maxHeight = 3000 } = opts;

  const canvasRef = useRef<HTMLCanvasElement>(null!);
  const wsRef = useRef<WebSocket | null>(null);
  const decoderRef = useRef<VideoDecoder | null>(null);
  const sessionRef = useRef<number>(0);
  const requestIdRef = useRef(0);
  const pendingRef = useRef(0);
  const codecRef = useRef<string>("");
  const activeRef = useRef(true);
  const reconnectTimer = useRef<number>(0);
  const keyFrameTimer = useRef<number>(0);
  const pollTimer = useRef<number>(0);
  const fpsCountRef = useRef(0);
  const fpsIntervalRef = useRef<number>(0);

  const [status, setStatus] = useState<DisplayStreamState["status"]>("disconnected");
  const [sourceWidth, setSourceWidth] = useState(0);
  const [sourceHeight, setSourceHeight] = useState(0);
  const [encodedWidth, setEncodedWidth] = useState(0);
  const [encodedHeight, setEncodedHeight] = useState(0);
  const [scale, setScale] = useState(1);
  const [codec, setCodec] = useState("");
  const [measuredFps, setMeasuredFps] = useState(0);
  const [error, setError] = useState<string | undefined>();

  const MAX_IN_FLIGHT = 8;

  const sendRequest = useCallback((ws: WebSocket, key: boolean) => {
    if (ws.readyState !== WebSocket.OPEN) return;
    if (pendingRef.current >= MAX_IN_FLIGHT) return;
    pendingRef.current += 1;
    requestIdRef.current += 1;
    const msg = {
      id: requestIdRef.current,
      codec: codecRef.current,
      box: [maxWidth, maxHeight],
      fps,
      bitrate,
      key,
    };
    ws.send(JSON.stringify(msg));
  }, [fps, bitrate, maxWidth, maxHeight]);

  const fillPipeline = useCallback((ws: WebSocket) => {
    while (pendingRef.current < MAX_IN_FLIGHT && ws.readyState === WebSocket.OPEN) {
      sendRequest(ws, false);
    }
  }, [sendRequest]);

  const schedulePoll = useCallback((ws: WebSocket) => {
    if (pollTimer.current) return;
    pollTimer.current = window.setTimeout(() => {
      pollTimer.current = 0;
      if (ws.readyState === WebSocket.OPEN) {
        sendRequest(ws, false);
      }
    }, Math.ceil(1000 / fps));
  }, [sendRequest, fps]);

  useEffect(() => {
    if (!enabled) {
      setStatus("disconnected");
      return;
    }

    let cancelled = false;
    let ws: WebSocket | null = null;
    let decoder: VideoDecoder | null = null;

    const cleanup = () => {
      cancelled = true;
      if (reconnectTimer.current) window.clearTimeout(reconnectTimer.current);
      if (keyFrameTimer.current) window.clearInterval(keyFrameTimer.current);
      if (fpsIntervalRef.current) window.clearInterval(fpsIntervalRef.current);
      if (pollTimer.current) window.clearTimeout(pollTimer.current);
      reconnectTimer.current = 0;
      keyFrameTimer.current = 0;
      fpsIntervalRef.current = 0;
      pollTimer.current = 0;
      if (ws) {
        try { ws.close(); } catch { /* ignore */ }
        wsRef.current = null;
      }
      if (decoder && decoder.state !== "closed") {
        try { decoder.close(); } catch { /* ignore */ }
        decoderRef.current = null;
      }
    };

    async function start() {
      const detectedCodec = await detectCodec();
      if (cancelled) return;
      if (!detectedCodec) {
        setStatus("unsupported");
        setError("WebCodecs not supported in this browser");
        return;
      }
      codecRef.current = detectedCodec;
      setCodec(detectedCodec);
      connect();
    }

    function createDecoder(): VideoDecoder {
      const d = new VideoDecoder({
        output: (frame: VideoFrame) => {
          const canvas = canvasRef.current;
          if (canvas) {
            canvas.width = frame.displayWidth;
            canvas.height = frame.displayHeight;
            const ctx = canvas.getContext("2d");
            if (ctx) {
              ctx.drawImage(frame, 0, 0, canvas.width, canvas.height);
            }
          }
          frame.close();
          fpsCountRef.current += 1;
        },
        error: (e: DOMException) => {
          setError(e.message);
          setStatus("error");
        },
      });
      decoderRef.current = d;
      return d;
    }

    function connect() {
      if (cancelled) return;
      setStatus("connecting");
      setError(undefined);
      requestIdRef.current = 0;
      pendingRef.current = 0;
      sessionRef.current = 0;

      ws = new WebSocket(screenWebSocketURL(vmId));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      decoder = createDecoder();

      ws.onopen = () => {
        if (cancelled) return;
        sendRequest(ws!, true);
        fillPipeline(ws!);

        keyFrameTimer.current = window.setInterval(() => {
          if (ws && ws.readyState === WebSocket.OPEN) {
            sendRequest(ws, true);
          }
        }, 10000);

        fpsCountRef.current = 0;
        fpsIntervalRef.current = window.setInterval(() => {
          setMeasuredFps(fpsCountRef.current);
          fpsCountRef.current = 0;
        }, 1000);
      };

      ws.onmessage = (ev) => {
        if (cancelled) return;
        if (!(ev.data instanceof ArrayBuffer)) return;

        const { header, payload } = parseFrame(ev.data);

        pendingRef.current = Math.max(0, pendingRef.current - 1);

        if (header.error) {
          pendingRef.current = 0;
          setError(header.error.message || header.error.code || "unknown error");
          setStatus("error");
          return;
        }

        if (header.status === "unchanged" || header.status === "dropped") {
          activeRef.current = false;
          schedulePoll(ws!);
          return;
        }

        // Update dimensions
        if (header.sourceWidth) setSourceWidth(header.sourceWidth);
        if (header.sourceHeight) setSourceHeight(header.sourceHeight);
        if (header.width) setEncodedWidth(header.width);
        if (header.height) setEncodedHeight(header.height);
        if (header.scale) setScale(header.scale);

        // Session change — flush and reconfigure
        if (header.session != null && header.session !== sessionRef.current) {
          sessionRef.current = header.session;
          if (decoder && decoder.state === "configured") {
            try { decoder.flush(); } catch { /* ignore */ }
          }
        }

        // Key frame with record — configure decoder
        if (header.key && header.record && decoder && decoder.state !== "closed") {
          try {
            const config = makeDecoderConfig(codecRef.current, header);
            decoder.configure(config);
          } catch (e) {
            setError(e instanceof Error ? e.message : "decoder config failed");
            setStatus("error");
            return;
          }
        }

        // Decode the frame
        if (payload.length > 0 && decoder && decoder.state === "configured") {
          try {
            const chunk = new EncodedVideoChunk({
              type: header.key ? "key" : "delta",
              timestamp: performance.now() * 1000,
              data: payload,
            });
            decoder.decode(chunk);
            setStatus("streaming");
          } catch (e) {
            setError(e instanceof Error ? e.message : "decode error");
          }
        }

        // Got a real frame — switch to active mode and fill pipeline for throughput
        if (!activeRef.current) {
          activeRef.current = true;
          if (pollTimer.current) {
            window.clearTimeout(pollTimer.current);
            pollTimer.current = 0;
          }
        }
        fillPipeline(ws!);
      };

      ws.onclose = () => {
        if (cancelled) return;
        wsRef.current = null;
        if (keyFrameTimer.current) window.clearInterval(keyFrameTimer.current);
        if (fpsIntervalRef.current) window.clearInterval(fpsIntervalRef.current);
        keyFrameTimer.current = 0;
        fpsIntervalRef.current = 0;
        setStatus("disconnected");
        setMeasuredFps(0);
        reconnectTimer.current = window.setTimeout(connect, 3000);
      };

      ws.onerror = () => {
        if (ws) ws.close();
      };
    }

    start();
    return cleanup;
  }, [vmId, enabled, sendRequest, fillPipeline, schedulePoll]);

  return {
    status,
    canvasRef,
    sourceWidth,
    sourceHeight,
    encodedWidth,
    encodedHeight,
    scale,
    codec,
    measuredFps,
    error,
  };
}
