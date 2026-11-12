declare module "@novnc/novnc" {
  // Types minimaux de noVNC (pas de types officiels).
  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, url: string, options?: { credentials?: { password?: string }; wsProtocols?: string[]; shared?: boolean });
    scaleViewport: boolean;
    resizeSession: boolean;
    clipViewport: boolean;
    viewOnly: boolean;
    background: string;
    disconnect(): void;
    sendCtrlAltDel(): void;
    clipboardPasteFrom(text: string): void;
    focus(): void;
    sendCredentials(creds: { password: string }): void;
  }
}
