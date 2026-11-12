import { Download, Share } from "lucide-react";
import { useEffect, useState } from "react";
import { Modal } from "./Modal";

interface InstallPrompt extends Event {
  prompt(): Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed" }>;
}

function standalone() {
  return window.matchMedia("(display-mode: standalone)").matches
    || (navigator as Navigator & { standalone?: boolean }).standalone === true;
}

export function InstallPWA() {
  const [prompt, setPrompt] = useState<InstallPrompt | null>(null);
  const [installed, setInstalled] = useState(standalone);
  const [help, setHelp] = useState(false);
  const [busy, setBusy] = useState(false);
  const ios = /iPhone|iPad|iPod/.test(navigator.userAgent)
    || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);

  useEffect(() => {
    const available = (event: Event) => {
      event.preventDefault();
      setPrompt(event as InstallPrompt);
    };
    const done = () => { setInstalled(true); setPrompt(null); setHelp(false); };
    const mode = window.matchMedia("(display-mode: standalone)");
    const modeChanged = () => setInstalled(standalone());
    window.addEventListener("beforeinstallprompt", available);
    window.addEventListener("appinstalled", done);
    mode.addEventListener("change", modeChanged);
    return () => {
      window.removeEventListener("beforeinstallprompt", available);
      window.removeEventListener("appinstalled", done);
      mode.removeEventListener("change", modeChanged);
    };
  }, []);

  const install = async () => {
    if (!prompt) { setHelp(true); return; }
    setBusy(true);
    try {
      await prompt.prompt();
      await prompt.userChoice;
    } catch {
      setHelp(true);
    } finally {
      setPrompt(null);
      setBusy(false);
    }
  };

  if (installed) return null;
  return <>
    <button type="button" className="install-pwa" disabled={busy} onClick={install} title="Installer Relay sur cet appareil">
      <Download size={14} /> Installer Relay
    </button>
    {help && <Modal title="Installer Relay" onClose={() => setHelp(false)} width={460} icon={<Download size={16} />}>
      {ios ? (
        <ol className="pwa-instructions">
          <li>Ouvrez Relay dans Safari.</li>
          <li>Touchez <Share size={15} aria-hidden="true" /> <strong>Partager</strong> dans le menu du navigateur.</li>
          <li>Choisissez <strong>Sur l’écran d’accueil</strong>, puis <strong>Ajouter</strong>.</li>
        </ol>
      ) : (
        <p>Ouvrez le menu de votre navigateur et choisissez <strong>Installer Relay</strong> ou <strong>Installer cette page en tant qu’application</strong>. L’option dépend du navigateur utilisé.</p>
      )}
    </Modal>}
  </>;
}
