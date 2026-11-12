import { History, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { api } from "../api";
import type { AuditEvent } from "../types";
import { Modal } from "./Modal";

const LABELS: Record<string, string> = {
  "session.open": "Ouverture de session",
  "session.close": "Fin de session",
  "entry.create": "Entrée créée",
  "entry.update": "Entrée modifiée",
  "entry.delete": "Entrée supprimée",
  "folder.create": "Dossier créé",
  "folder.update": "Dossier modifié",
  "folder.delete": "Dossier supprimé",
  "credential.create": "Identifiant créé",
  "credential.update": "Identifiant modifié",
  "credential.delete": "Identifiant supprimé",
  "credential.reveal": "Secret affiché / copié",
  "credential.totp": "Code TOTP affiché",
  "source.create": "Source ajoutée",
  "source.update": "Source modifiée",
  "source.delete": "Source supprimée",
  "source.sync": "Synchronisation",
  "import.rdm": "Import RDM",
  "device.create": "Appareil appairé",
  "device.delete": "Appareil révoqué",
};

export function AuditDialog({ onClose }: { onClose: () => void }) {
  const [events, setEvents] = useState<AuditEvent[] | null>(null);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");
  const load = () =>
    api
      .audit()
      .then(setEvents)
      .catch((e) => setError(e.message));
  useEffect(() => {
    load();
  }, []);
  const q = filter.toLowerCase();
  const rows = (events ?? []).filter(
    (e) => !q || [e.user, e.action, LABELS[e.action], e.target, e.detail].some((v) => v?.toLowerCase().includes(q)),
  );
  return (
    <Modal title="Journal d'activité" icon={<History size={16} />} onClose={onClose} width={900} height={600}>
      <div className="audit-tools">
        <input placeholder="Filtrer…" value={filter} onChange={(e) => setFilter(e.target.value)} />
        <button className="btn small" onClick={load}>
          <RefreshCw size={14} /> Actualiser
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
      <div className="audit-table">
        <table>
          <thead>
            <tr>
              <th>Date</th>
              <th>Utilisateur</th>
              <th>Action</th>
              <th>Cible</th>
              <th>Détail</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((e) => (
              <tr key={e.id} className={e.action.startsWith("credential.reveal") ? "warn" : ""}>
                <td className="nowrap">{new Date(e.at).toLocaleString("fr-FR")}</td>
                <td>{e.user}</td>
                <td>{LABELS[e.action] ?? e.action}</td>
                <td>{e.target}</td>
                <td className="muted">{e.detail}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {events && !rows.length && <div className="tree-empty">Aucun événement</div>}
      </div>
    </Modal>
  );
}
