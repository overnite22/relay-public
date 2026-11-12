import { CheckCircle2, CopyX, EyeOff, Loader2, Merge, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { duplicatesApi, sourcesApi, type DuplicateGroup } from "../api";
import { endpoint, KindIcon } from "../kinds";
import type { Credential, Source, Tree } from "../types";
import { pathOf } from "./Dashboard";
import { Modal } from "./Modal";

const IGNORED_KEY = "relay.duplicates.ignored";

function loadIgnored(): Set<string> {
  try {
    return new Set(JSON.parse(localStorage.getItem(IGNORED_KEY) ?? "[]"));
  } catch {
    return new Set();
  }
}

/** Signature d'un groupe : un groupe ignoré réapparaît si ses entrées changent. */
const signature = (g: DuplicateGroup) => g.key + "|" + g.entries.map((e) => e.id).sort().join(",");

export function DuplicatesDialog({
  tree,
  credentials,
  onChanged,
  onSelect,
  onClose,
  onToast,
}: {
  tree: Tree;
  credentials: Credential[];
  onChanged: () => Promise<void>;
  onSelect: (entryId: string) => void;
  onClose: () => void;
  onToast: (m: string) => void;
}) {
  const [groups, setGroups] = useState<DuplicateGroup[] | null>(null);
  const [keep, setKeep] = useState<Record<string, string>>({});
  const [ignored, setIgnored] = useState<Set<string>>(loadIgnored);
  const [showIgnored, setShowIgnored] = useState(false);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [sources, setSources] = useState<Source[]>([]);

  const load = async () => {
    setGroups(null);
    setError("");
    try {
      const g = await duplicatesApi.list();
      setGroups(g);
      setKeep(Object.fromEntries(g.map((x) => [x.key, x.entries[0].id])));
    } catch (e) {
      setError((e as Error).message);
      setGroups([]);
    }
  };
  useEffect(() => {
    load();
    sourcesApi.list().then(setSources).catch(() => {});
  }, []);
  useEffect(() => localStorage.setItem(IGNORED_KEY, JSON.stringify([...ignored])), [ignored]);

  const merge = async (g: DuplicateGroup) => {
    const keepId = keep[g.key];
    setBusy(g.key);
    try {
      await duplicatesApi.merge(
        keepId,
        g.entries.map((e) => e.id).filter((id) => id !== keepId),
      );
      await onChanged();
      onToast(`Fusion faite : ${g.entries.length - 1} doublon(s) supprimé(s)`);
      await load();
    } catch (e) {
      onToast((e as Error).message);
    } finally {
      setBusy("");
    }
  };

  const credName = (id: string | null) => credentials.find((c) => c.id === id)?.name;
  const sourceName = (id: string | null) => sources.find((s) => s.id === id)?.name ?? "source supprimée";
  const visible = (groups ?? []).filter((g) => showIgnored || !ignored.has(signature(g)));
  const hiddenCount = (groups ?? []).length - (groups ?? []).filter((g) => !ignored.has(signature(g))).length;

  return (
    <Modal
      title="Doublons"
      icon={<CopyX size={16} />}
      onClose={onClose}
      width={980}
      height={660}
      footer={
        <>
          {error && <span className="form-error">{error}</span>}
          <span className="muted small">
            {groups ? `${visible.length} groupe(s)` : ""}
            {hiddenCount > 0 && (
              <>
                {" · "}
                <button className="link-btn" onClick={() => setShowIgnored((v) => !v)}>
                  {showIgnored ? "masquer" : "afficher"} les {hiddenCount} ignoré(s)
                </button>
              </>
            )}
          </span>
          <div className="spacer" />
          <button className="btn" onClick={load}>
            <RefreshCw size={14} /> Actualiser
          </button>
          <button className="btn" onClick={onClose}>
            Fermer
          </button>
        </>
      }
    >
      <p className="muted" style={{ marginTop: 0 }}>
        Entrées qui visent la même cible (même type, même IP, même port ; les noms DNS sont résolus). Choisissez l'entrée à garder : la fusion lui
        ajoute l'identifiant, l'utilisateur, les notes et le favori des autres, puis supprime ces dernières. Une entrée synchronisée supprimée ne sera
        pas recréée par la synchronisation ; si vous gardez une entrée manuelle, elle devient synchronisée à sa place.
      </p>
      {groups === null && (
        <div className="tree-empty">
          <Loader2 size={18} className="spin" /> Recherche des doublons (résolution DNS)…
        </div>
      )}
      {groups && visible.length === 0 && (
        <div className="empty-state">
          <CheckCircle2 size={28} color="var(--ok)" />
          <p>Aucun doublon.</p>
        </div>
      )}
      <div className="dups">
        {visible.map((g) => (
          <section key={g.key} className={`card dup ${ignored.has(signature(g)) ? "ignored" : ""}`}>
            <div className="dup-head">
              <KindIcon kind={g.entries[0].kind} />
              <span className="mono">{g.label}</span>
              <span className="muted small">{g.entries.length} entrées</span>
              <div className="spacer" />
              <button
                className="btn small"
                onClick={() =>
                  setIgnored((cur) => {
                    const n = new Set(cur);
                    if (n.has(signature(g))) n.delete(signature(g));
                    else n.add(signature(g));
                    return n;
                  })
                }
              >
                <EyeOff size={13} /> {ignored.has(signature(g)) ? "Ne plus ignorer" : "Ignorer"}
              </button>
              <button className="btn small primary" onClick={() => merge(g)} disabled={!!busy}>
                {busy === g.key ? <Loader2 size={13} className="spin" /> : <Merge size={13} />} Fusionner
              </button>
            </div>
            <table className="dup-table">
              <tbody>
                {g.entries.map((e) => (
                  <tr key={e.id} className={keep[g.key] === e.id ? "keep" : "drop"}>
                    <td style={{ width: 70 }}>
                      <label className="check" style={{ margin: 0 }}>
                        <input type="radio" name={g.key} checked={keep[g.key] === e.id} onChange={() => setKeep((k) => ({ ...k, [g.key]: e.id }))} />
                        {keep[g.key] === e.id ? "garder" : ""}
                      </label>
                    </td>
                    <td>
                      <button className="link-btn" onClick={() => onSelect(e.id)} title="Afficher dans l'arborescence">
                        {e.name}
                      </button>
                    </td>
                    <td className="muted">{pathOf(tree, e.folderId) || "(racine)"}</td>
                    <td className="mono small">{endpoint(e)}</td>
                    <td>
                      {e.sourceId ? <span className="badge sync">{sourceName(e.sourceId)}</span> : <span className="badge">manuelle</span>}
                    </td>
                    <td className="muted small">{credName(e.credentialId) ?? (e.username || "—")}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        ))}
      </div>
    </Modal>
  );
}
