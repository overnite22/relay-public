import Guacamole from "guacamole-common-js";
import { CircleDot, ClipboardPaste, Expand, FolderOpen, Keyboard, Loader2, Plug, RotateCw, Unplug } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api";
import { KindIcon } from "../kinds";
import type { SessionTab } from "../types";
import { ackEachBlob, FileBrowser } from "./FileBrowser";

type State = "connecting" | "connected" | "disconnected" | "error";

/* eslint-disable @typescript-eslint/no-explicit-any */
interface GuacClient {
  connect(data: string): void;
  disconnect(): void;
  getDisplay(): any;
  sendSize(w: number, h: number): void;
  sendKeyEvent(pressed: 0 | 1, keysym: number): void;
  sendMouseState(state: any, applyDisplayScale?: boolean): void;
  createClipboardStream(mimetype: string): any;
  onstatechange: ((s: number) => void) | null;
  onerror: ((status: { code: number; message: string }) => void) | null;
  onclipboard: ((stream: any, mimetype: string) => void) | null;
  onfilesystem: ((object: any, name: string) => void) | null;
  onfile: ((stream: any, mimetype: string, filename: string) => void) | null;
}

const MIN = 100;

export function SessionView({ tab, active, onToast, onReconnect }: { tab: SessionTab; active: boolean; onToast: (m: string) => void; onReconnect?: () => void }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const screenRef = useRef<HTMLDivElement>(null);
  const clientRef = useRef<GuacClient | null>(null);
  const [state, setState] = useState<State>("connecting");
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [fs, setFs] = useState<any>(null);
  const [showFiles, setShowFiles] = useState(false);

  const fit = useCallback(() => {
    const client = clientRef.current;
    const host = hostRef.current;
    if (!client || !host) return;
    const display = client.getDisplay();
    const w = display.getWidth();
    const h = display.getHeight();
    if (!w || !h || host.clientWidth < MIN || host.clientHeight < MIN) return;
    display.scale(Math.min(host.clientWidth / w, host.clientHeight / h, 1));
  }, []);

  // Ouverture de la session.
  useEffect(() => {
    const host = hostRef.current;
    const screen = screenRef.current;
    if (!host || !screen) return;
    let disposed = false;
    setState("connecting");
    setError("");

    const tunnel = new Guacamole.WebSocketTunnel("/api/tunnel");
    const client: GuacClient = new Guacamole.Client(tunnel);
    clientRef.current = client;
    const display = client.getDisplay();
    const el: HTMLElement = display.getElement();
    screen.replaceChildren(el);
    display.onresize = () => fit();

    client.onstatechange = (s: number) => {
      if (disposed) return;
      if (s === 3) setState("connected");
      if (s === 5) setState((cur) => (cur === "error" ? cur : "disconnected"));
    };
    const fail = (status: { message?: string }) => {
      if (disposed) return;
      setError(status?.message || "Connexion interrompue");
      setState("error");
    };
    client.onerror = fail;
    tunnel.onerror = fail;

    setFs(null);
    client.onfilesystem = (object: any) => {
      if (!disposed) setFs(object);
    };
    // Fichier poussé par la session (RDP : copie dans \\tsclient\Relay\Download).
    client.onfile = (stream: any, mimetype: string, filename: string) => {
      const reader = new Guacamole.BlobReader(stream, mimetype);
      ackEachBlob(stream);
      reader.onend = () => {
        const url = URL.createObjectURL(reader.getBlob());
        const a = document.createElement("a");
        a.href = url;
        a.download = filename;
        a.click();
        window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
      };
      stream.sendAck("Prêt", 0);
      onToast(`Téléchargement de `);
    };

    client.onclipboard = (stream: any, mimetype: string) => {
      if (!mimetype.startsWith("text/")) return;
      const reader = new Guacamole.StringReader(stream);
      let data = "";
      reader.ontext = (t: string) => (data += t);
      reader.onend = () => {
        navigator.clipboard?.writeText(data).catch(() => {});
      };
    };

    const mouse = new Guacamole.Mouse(el);
    mouse.onEach(["mousedown", "mousemove", "mouseup"], (e: any) => client.sendMouseState(e.state, true));
    const touch = new Guacamole.Mouse.Touchscreen(el);
    touch.onEach(["mousedown", "mousemove", "mouseup"], (e: any) => client.sendMouseState(e.state, true));

    const keyboard = new Guacamole.Keyboard(host);
    keyboard.onkeydown = (k: number) => {
      client.sendKeyEvent(1, k);
      return false;
    };
    keyboard.onkeyup = (k: number) => client.sendKeyEvent(0, k);

    const width = Math.max(host.clientWidth, 640);
    const height = Math.max(host.clientHeight, 480);
    const prepared = tab.quick
      ? Promise.resolve({ token: tab.quick.token })
      : api.connect(tab.entryId, {
          width,
          height,
          dpi: 96,
          record: tab.record,
          timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
        });
    prepared
      .then(({ token }) => {
        if (!disposed) client.connect("token=" + encodeURIComponent(token));
      })
      .catch((e: Error) => fail({ message: e.message }));

    host.focus();
    return () => {
      disposed = true;
      keyboard.onkeydown = null;
      keyboard.onkeyup = null;
      keyboard.reset?.();
      client.disconnect();
      clientRef.current = null;
    };
  }, [tab.entryId, tab.record, tab.quick, attempt, fit]);

  // Redimensionnement : la session suit la taille de l'onglet.
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    let timer = 0;
    const ro = new ResizeObserver(() => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        if (host.clientWidth < MIN || host.clientHeight < MIN) return;
        clientRef.current?.sendSize(host.clientWidth, host.clientHeight);
        fit();
      }, 250);
    });
    ro.observe(host);
    return () => {
      ro.disconnect();
      window.clearTimeout(timer);
    };
  }, [fit]);

  useEffect(() => {
    if (active && state === "connected") hostRef.current?.focus();
  }, [active, state]);

  const sendKeys = (keys: number[]) => {
    const c = clientRef.current;
    if (!c) return;
    keys.forEach((k) => c.sendKeyEvent(1, k));
    [...keys].reverse().forEach((k) => c.sendKeyEvent(0, k));
    hostRef.current?.focus();
  };

  const paste = async () => {
    const c = clientRef.current;
    if (!c) return;
    try {
      const text = await navigator.clipboard.readText();
      const stream = c.createClipboardStream("text/plain");
      const writer = new Guacamole.StringWriter(stream);
      writer.sendText(text);
      writer.sendEnd();
      onToast("Presse-papiers envoyé à la session");
    } catch {
      onToast("Lecture du presse-papiers refusée par le navigateur");
    }
    hostRef.current?.focus();
  };

  const fullscreen = () => hostRef.current?.parentElement?.requestFullscreen?.();

  return (
    <div className="session" style={{ display: active ? "flex" : "none" }}>
      <div className="session-toolbar">
        <KindIcon kind={tab.kind} />
        <span className="session-title">{tab.name}</span>
        <span className={`session-state s-${state}`}>
          {state === "connecting" && "Connexion…"}
          {state === "connected" && "Connecté"}
          {state === "disconnected" && "Déconnecté"}
          {state === "error" && "Erreur"}
        </span>
        {tab.record && state === "connected" && (
          <span className="session-recording"><CircleDot size={14} /> Enregistrement</span>
        )}
        <div className="spacer" />
        {tab.kind === "rdp" && (
          <button className="tb" title="Envoyer Ctrl+Alt+Suppr" onClick={() => sendKeys([0xffe3, 0xffe9, 0xffff])}>
            <Keyboard size={15} /> Ctrl+Alt+Suppr
          </button>
        )}
        {fs && (
          <button className={`tb ${showFiles ? "on" : ""}`} title="Parcourir et transférer des fichiers (SFTP)" onClick={() => setShowFiles((v) => !v)}>
            <FolderOpen size={15} /> Fichiers
          </button>
        )}
        <button className="tb" title="Envoyer le presse-papiers local à la session" onClick={paste}>
          <ClipboardPaste size={15} /> Coller
        </button>
        <button className="tb" title="Plein écran" onClick={fullscreen}>
          <Expand size={15} />
        </button>
        <button className="tb" title="Reconnecter" onClick={onReconnect ?? (() => setAttempt((a) => a + 1))}>
          <RotateCw size={15} />
        </button>
        <button className="tb" title="Déconnecter" onClick={() => clientRef.current?.disconnect()}>
          <Unplug size={15} />
        </button>
      </div>
      <div className="session-main">
      <div className="session-host" ref={hostRef} tabIndex={0} onMouseDown={() => hostRef.current?.focus()}>
        <div className="session-screen" ref={screenRef} />
        {state !== "connected" && (
          <div className="session-overlay">
            {state === "connecting" && (
              <>
                <Loader2 className="spin" size={28} />
                <div>Ouverture de la session {tab.name}…</div>
              </>
            )}
            {(state === "disconnected" || state === "error") && (
              <>
                <Unplug size={28} />
                <div>{state === "error" ? error : "La session est terminée."}</div>
                <button className="btn primary" onClick={() => setAttempt((a) => a + 1)}>
                  <Plug size={15} /> Reconnecter
                </button>
              </>
            )}
          </div>
        )}
      </div>
      {fs && showFiles && (
        <FileBrowser
          fs={fs}
          title={tab.kind === "rdp" ? "Lecteur « Relay »" : undefined}
          hint={
            tab.kind === "rdp"
              ? "Dans Windows : Ce PC → « Relay ». Glissez des fichiers ici pour les y déposer ; copiez un fichier dans Relay\\Download pour le télécharger."
              : undefined
          }
          onClose={() => setShowFiles(false)}
          onToast={onToast}
        />
      )}
      </div>
    </div>
  );
}
