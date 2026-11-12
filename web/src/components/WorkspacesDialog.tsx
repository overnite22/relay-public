import { FolderOpen, Plus, Save, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { workspacesApi } from "../api";
import { KindIcon } from "../kinds";
import type { Tree, Workspace } from "../types";
import { pathOf } from "./Dashboard";
import { Modal } from "./Modal";

export function WorkspacesDialog({
  tree,
  currentIds,
  onOpen,
  onClose,
}: {
  tree: Tree;
  currentIds: string[];
  onOpen: (workspace: Workspace) => void;
  onClose: () => void;
}) {
  const [items, setItems] = useState<Workspace[]>([]);
  const [draft, setDraft] = useState<Workspace | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [remove, setRemove] = useState(false);
  const [filter, setFilter] = useState("");
  useEffect(() => {
    workspacesApi
      .list()
      .then(setItems)
      .catch((err) => setError(err.message));
  }, []);
  const save = async () => {
    if (!draft) return;
    setBusy(true);
    setError("");
    try {
      const saved = await workspacesApi.save(draft);
      setItems((current) => [saved, ...current.filter((item) => item.id !== saved.id)]);
      setDraft(saved);
    } catch (err) {
      setError((err as Error).message);
    }
    setBusy(false);
  };
  const destroy = async () => {
    if (!draft?.id) return;
    setBusy(true);
    try {
      await workspacesApi.remove(draft.id);
      setItems(items.filter((item) => item.id !== draft.id));
      setDraft(null);
      setRemove(false);
    } catch (err) {
      setError((err as Error).message);
    }
    setBusy(false);
  };
  const entries = tree.entries.filter((e) => e.kind !== "web" && `${e.name} ${e.host}`.toLowerCase().includes(filter.toLowerCase()));
  return (
    <Modal title="Espaces de travail" icon={<FolderOpen size={16} />} onClose={onClose} width={900}>
      <div className="workspace-toolbar">
        <button
          className="btn"
          disabled={busy}
          onClick={() => {
            setDraft({ id: "", name: "", entryIds: [...new Set(currentIds)] });
            setRemove(false);
            setError("");
          }}
        >
          <Plus size={14} />
          Nouvel espace
        </button>
        <span className="muted small">Chaque utilisateur conserve ses espaces.</span>
      </div>
      <div className="workspace-body">
        <aside className="workspace-list">
          {items.length === 0 && <p className="muted">Aucun espace enregistré.</p>}
          {items.map((item) => (
            <button
              key={item.id}
              className={draft?.id === item.id ? "selected" : ""}
              onClick={() => {
                setDraft({ ...item, entryIds: [...item.entryIds] });
                setRemove(false);
              }}
            >
              <strong>{item.name}</strong>
              <small>{item.entryIds.length} session(s)</small>
            </button>
          ))}
        </aside>
        <div className="workspace-editor">
          {!draft ? (
            <p className="muted">Créez un espace ou sélectionnez-en un.</p>
          ) : (
            <>
              <label className="field">
                <span>Nom de l’espace</span>
                <input value={draft.name} maxLength={100} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
              </label>
              <div className="workspace-toolbar">
                <button className="btn" disabled={busy} onClick={() => setDraft({ ...draft, entryIds: [...new Set(currentIds)] })}>
                  Reprendre les sessions ouvertes
                </button>
                <span className="muted small">{draft.entryIds.length}/50 sélectionnée(s)</span>
              </div>
              {draft.entryIds.some((id) => !tree.entries.some((e) => e.id === id)) && (
                <p className="muted small">
                  Des entrées ont été supprimées. Elles seront ignorées à l’ouverture.
                  <button
                    type="button"
                    className="link-btn"
                    onClick={() =>
                      setDraft({ ...draft, entryIds: draft.entryIds.filter((id) => tree.entries.some((entry) => entry.id === id)) })
                    }
                  >
                    Retirer les entrées supprimées
                  </button>
                </p>
              )}
              <input
                aria-label="Filtrer les sessions"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder="Filtrer les sessions…"
              />
              <div className="workspace-targets">
                {entries.map((entry) => (
                  <label key={entry.id}>
                    <input
                      type="checkbox"
                      checked={draft.entryIds.includes(entry.id)}
                      disabled={!draft.entryIds.includes(entry.id) && draft.entryIds.length >= 50}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          entryIds: event.target.checked ? [...draft.entryIds, entry.id] : draft.entryIds.filter((id) => id !== entry.id),
                        })
                      }
                    />
                    <KindIcon kind={entry.kind} />
                    <span>{entry.name}</span>
                    <small>{pathOf(tree, entry.folderId)}</small>
                  </label>
                ))}
              </div>
              <div className="workspace-toolbar">
                <button className="btn primary" disabled={busy || !draft.name.trim() || !draft.entryIds.length} onClick={save}>
                  <Save size={14} />
                  Enregistrer
                </button>
                <button className="btn" disabled={busy || !draft.entryIds.length} onClick={() => onOpen(draft)}>
                  <FolderOpen size={14} />
                  Ouvrir l’espace
                </button>
                {draft.id && (
                  <button className="btn" disabled={busy} onClick={() => setRemove(true)}>
                    <Trash2 size={14} />
                    Supprimer
                  </button>
                )}
              </div>
              {remove && (
                <div className="workspace-toolbar">
                  Supprimer cet espace ?
                  <button className="btn danger" disabled={busy} onClick={destroy}>
                    Confirmer
                  </button>
                  <button className="btn" onClick={() => setRemove(false)}>
                    Annuler
                  </button>
                </div>
              )}
            </>
          )}
        </div>
      </div>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
    </Modal>
  );
}
