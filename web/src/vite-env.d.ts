/// <reference types="vite/client" />

// @novnc/novnc ships without bundled type declarations; declare the minimal
// surface we use (the RFB default export and its constructor options/events).
declare module "@novnc/novnc" {
  export interface RFBOptions {
    shared?: boolean;
    credentials?: { username?: string; password?: string; target?: string };
    wsProtocols?: string[];
  }

  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, urlOrDataChannel: string, options?: RFBOptions);
    scaleViewport: boolean;
    resizeSession: boolean;
    viewOnly: boolean;
    focusOnClick: boolean;
    background: string;
    disconnect(): void;
    focus(): void;
    sendCtrlAltDel(): void;
  }
}
