import { Download, HardDrive, Upload } from "lucide-react";
import { useState } from "react";
import { backupsApi } from "../api";
import { Modal } from "./Modal";

export function BackupDialog({ onClose }: { onClose: () => void }) {
  const [password, setPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const run = async (restore: boolean) => {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      if (restore && file) {
        await backupsApi.restore(file, password);
        location.reload();
      } else {
        const blob = await backupsApi.export(password);
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = `relay-${new Date().toISOString().slice(0, 10)}.relay`;
        a.click();
        window.setTimeout(() => URL.revokeObjectURL(url), 30_000);
        setMessage("Sauvegarde téléchargée. Conservez son mot de passe pour la restauration.");
      }
    } catch (err) {
      setError((err as Error).message);
    }
    setBusy(false);
  };
  return (
    <Modal title="Sauvegarde et restauration" icon={<HardDrive size={16} />} onClose={busy ? () => {} : onClose} width={640}>
      <p>
        Archive chiffrée comprenant l’inventaire, le coffre, les notes, les pièces jointes, les sources, les espaces, l’historique et les
        enregistrements.
      </p>
      <label className="field">
        <span>Mot de passe de l’archive</span>
        <input type="password" value={password} autoComplete="new-password" disabled={busy} onChange={(e) => setPassword(e.target.value)} />
      </label>
      <section className="backup-section">
        <h3>Créer une sauvegarde</h3>
        <label className="field">
          <span>Confirmer le mot de passe</span>
          <input type="password" value={repeat} disabled={busy} onChange={(e) => setRepeat(e.target.value)} />
        </label>
        <button className="btn primary" disabled={busy || !password || password !== repeat} onClick={() => run(false)}>
          <Download size={14} />
          Télécharger la sauvegarde
        </button>
        <p className="muted small">512 Mio maximum avant compression. Le mot de passe protège aussi les secrets du coffre.</p>
      </section>
      <section className="backup-section">
        <h3>Restaurer une sauvegarde</h3>
        <p>
          La restauration remplace les données de Relay pour tous les utilisateurs. Fermez les sessions et attendez la fin des commandes ou
          synchronisations.
        </p>
        <label className="field">
          <span>Archive Relay</span>
          <input type="file" accept=".relay" disabled={busy} onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
        </label>
        <label className="field">
          <span>Tapez RESTAURER pour confirmer</span>
          <input value={confirmation} disabled={busy} onChange={(e) => setConfirmation(e.target.value)} />
        </label>
        <button className="btn danger" disabled={busy || !file || !password || confirmation !== "RESTAURER"} onClick={() => run(true)}>
          <Upload size={14} />
          Restaurer
        </button>
      </section>
      {busy && <p role="status">Opération en cours…</p>}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {message && <p role="status">{message}</p>}
    </Modal>
  );
}
