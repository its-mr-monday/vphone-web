declare module "@novnc/novnc" {
  interface RFBOptions {
    shared?: boolean;
    credentials?: { password?: string; username?: string; target?: string };
    repeaterID?: string;
    wsProtocols?: string[];
  }

  interface RFBCredentials {
    password?: string;
    username?: string;
    target?: string;
  }

  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, urlOrChannel: string | WebSocket, options?: RFBOptions);

    viewOnly: boolean;
    focusOnClick: boolean;
    clipViewport: boolean;
    dragViewport: boolean;
    scaleViewport: boolean;
    resizeSession: boolean;
    showDotCursor: boolean;
    qualityLevel: number;
    compressionLevel: number;

    readonly capabilities: {
      power: boolean;
    };

    disconnect(): void;
    sendCredentials(credentials: RFBCredentials): void;
    sendKey(keysym: number, code: string | null, down?: boolean): void;
    sendCtrlAltDel(): void;
    focus(): void;
    blur(): void;
    machineShutdown(): void;
    machineReboot(): void;
    machineReset(): void;
    clipboardPasteFrom(text: string): void;

    addEventListener(type: string, listener: (e: CustomEvent) => void): void;
    removeEventListener(type: string, listener: (e: CustomEvent) => void): void;
  }
}
