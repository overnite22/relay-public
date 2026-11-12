import RFB from "@novnc/novnc";
import { ClipboardPaste, Expand, Keyboard, Loader2, Plug, RotateCw, Unplug } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { KindIcon } from "../kinds";
import type { SessionTab } from "../types";

type State = "connecting" | "connected" | "disconnected" | "error";

/** Console Proxmox VE : noVNC relié au WebSocket VNC de Proxmox via Relay. */
export function PveSessionView({ tab, active, onToast }: { tab: SessionTab; active: boolean; onToast: (m: string) => void }) {
  const screenRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<RFB | null>(null);
  const [state, setState] = useState<State>("connecting");
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const screen = screenRef.current;
    if (!screen) return;
    let disposed = false;
    setState("connecting");
    setError("");
    api
      .connect(tab.entryId, { width: 0, height: 0, dpi: 96, timezone: "" })
      .then(({ token, password }) => {
        if (disposed) return;
        const proto = location.protocol === "https:" ? "wss:" : "ws:";
        const rfb = new RFB(screen, `${proto}//${location.host}/api/vnc-tunnel?token=${encodeURIComponent(token)}`, {
          credentials: { password: password ?? "" },
          wsProtocols: ["binary"],
        });
        rfb.scaleViewport = true;
        rfb.background = "#000";
        rfb.addEventListener("connect", () => !disposed && setState("connected"));
        rfb.addEventListener("disconnect", (e: Event) => {
          if (disposed) return;
          const clean = (e as CustomEvent<{ clean: boolean }>).detail?.clean;
          if (clean) setState("disconnected");
          else {
            setError("Connexion à la console interrompue");
            setState("error");
          }
        });
        rfb.addEventListener("securityfailure", (e: Event) => {
          if (disposed) return;
          setError("Authentification VNC refusée : " + ((e as CustomEvent<{ reason?: string }>).detail?.reason ?? ""));
          setState("error");
        });
        rfb.addEventListener("clipboard", (e: Event) => {
          const text = (e as CustomEvent<{ text: string }>).detail?.text;
          if (text) navigator.clipboard?.writeText(text).catch(() => {});
        });
        rfbRef.current = rfb;
      })
      .catch((e: Error) => {
        if (disposed) return;
        setError(e.message);
        setState("error");
      });
    return () => {
      disposed = true;
      rfbRef.current?.disconnect();
      rfbRef.current = null;
      screen.replaceChildren();
    };
  }, [tab.entryId, attempt]);

  useEffect(() => {
    if (active && state === "connected") rfbRef.current?.focus();
  }, [active, state]);

  const paste = async () => {
    try {
      const text = await navigator.clipboard.readText();
      rfbRef.current?.clipboardPasteFrom(text);
      onToast("Presse-papiers envoyé (selon l'OS invité, collez-le ensuite)");
    } catch {
      onToast("Lecture du presse-papiers refusée par le navigateur");
    }
  };

  return (
    <div className="session" style={{ display: active ? "flex" : "none" }}>
      <div className="session-toolbar">
        <KindIcon kind="pve" />
        <span className="session-title">{tab.name}</span>
        <span className={`session-state s-${state}`}>
          {state === "connecting" && "Connexion…"}
          {state === "connected" && "Connecté"}
          {state === "disconnected" && "Déconnecté"}
          {state === "error" && "Erreur"}
        </span>
        <div className="spacer" />
        <button className="tb" title="Envoyer Ctrl+Alt+Suppr" onClick={() => rfbRef.current?.sendCtrlAltDel()}>
          <Keyboard size={15} /> Ctrl+Alt+Suppr
        </button>
        <button className="tb" title="Envoyer le presse-papiers local" onClick={paste}>
          <ClipboardPaste size={15} /> Coller
        </button>
        <button className="tb" title="Plein écran" onClick={() => screenRef.current?.parentElement?.parentElement?.requestFullscreen?.()}>
          <Expand size={15} />
        </button>
        <button className="tb" title="Reconnecter" onClick={() => setAttempt((a) => a + 1)}>
          <RotateCw size={15} />
        </button>
        <button className="tb" title="Déconnecter" onClick={() => rfbRef.current?.disconnect()}>
          <Unplug size={15} />
        </button>
      </div>
      <div className="session-host">
        <div className="novnc-screen" ref={screenRef} />
        {state !== "connected" && (
          <div className="session-overlay">
            {state === "connecting" ? (
              <>
                <Loader2 className="spin" size={28} />
                <div>Ouverture de la console {tab.name}…</div>
              </>
            ) : (
              <>
                <Unplug size={28} />
                <div>{state === "error" ? error : "La console est fermée."}</div>
                <button className="btn primary" onClick={() => setAttempt((a) => a + 1)}>
                  <Plug size={15} /> Reconnecter
                </button>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
