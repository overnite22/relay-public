import { Activity, AlertCircle, CheckCircle2, Loader2, Play, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { api } from "../api";
import { endpoint, KindIcon } from "../kinds";
import type { ConnectionTest, Entry } from "../types";
import { connectionStatusLabels } from "./ConnectionIndicator";
import { Modal } from "./Modal";

export function ConnectionTestDialog({ entry, onOpen, onClose, onResult }: {
  entry: Entry;
  onOpen: () => void;
  onClose: () => void;
  onResult: (result: ConnectionTest) => void;
}) {
  const [result, setResult] = useState<ConnectionTest | null>(null);
  const [error, setError] = useState("");
  const [request, setRequest] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    // Attendre le montage avant de lancer le test, y compris sous StrictMode.
    queueMicrotask(() => {
      if (controller.signal.aborted) return;
      api.testConnection(entry.id, controller.signal).then((value) => {
        if (!controller.signal.aborted) {
          setResult(value);
          onResult(value);
        }
      }).catch((err: Error) => {
        if (!controller.signal.aborted) setError(err.message);
      });
    });
    return () => controller.abort();
  }, [request, entry.id, onResult]);

  const retry = () => {
    setResult(null);
    setError("");
    setRequest((value) => value + 1);
  };

  const busy = !result && !error;
  return (
    <Modal title="Tester la connexion" icon={<Activity size={16} />} width={490} onClose={onClose}
      footer={
        <>
          <button type="button" className="btn" onClick={retry} disabled={busy}><RefreshCw size={14} /> Retester</button>
          <div className="spacer" />
          <button type="button" className="btn primary" onClick={onOpen}><Play size={14} /> Ouvrir la session</button>
        </>
      }>
      <div className="connection-target">
        <KindIcon kind={entry.kind} size={22} />
        <div><strong>{entry.name}</strong><div className="muted mono">{endpoint(entry)}</div></div>
      </div>
      <div className="connection-result" role="status">
        {busy ? (
          <div className="connection-status"><Loader2 size={18} className="spin" /> Test en cours…</div>
        ) : error ? (
          <div className="connection-status failed"><AlertCircle size={18} /> {error}</div>
        ) : result && (
          <>
            <div className={`connection-status ${result.status === "reachable" ? "success" : "failed"}`}>
              {result.status === "reachable" ? <CheckCircle2 size={18} /> : <AlertCircle size={18} />}
              {connectionStatusLabels[result.status]}
            </div>
            <table className="props">
              <tbody>
                <tr><th>Adresse testée</th><td className="mono">{result.address}</td></tr>
                {result.ip && <tr><th>IP contactée</th><td className="mono">{result.ip}</td></tr>}
                <tr><th>Temps écoulé</th><td>{result.durationMs === 0 ? "< 1" : result.durationMs} ms</td></tr>
              </tbody>
            </table>
          </>
        )}
      </div>
      <p className="muted small">Test TCP effectué depuis le serveur Relay, sans authentification.</p>
    </Modal>
  );
}
