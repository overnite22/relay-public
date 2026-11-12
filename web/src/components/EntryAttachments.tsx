import { Download, Paperclip, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { attachmentsApi } from "../api";
import type { Attachment } from "../types";

const maxBytes = 10 * 1024 * 1024;

function fileSize(bytes: number) {
  if (bytes < 1024) return `${bytes} o`;
  if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} Kio`;
  return `${(bytes / (1024 * 1024)).toLocaleString("fr-FR", { maximumFractionDigits: 1 })} Mio`;
}

export function EntryAttachments({ entryId }: { entryId: string }) {
  const input = useRef<HTMLInputElement>(null);
  const [items, setItems] = useState<Attachment[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState<string | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    attachmentsApi.list(entryId).then((items) => {
      if (active) setItems(items);
    }).catch((err: Error) => {
      if (active) setError(err.message);
    });
    return () => { active = false; };
  }, [entryId]);

  const add = async (file: File) => {
    if (file.size > maxBytes) {
      setError("Le fichier dépasse 10 Mio.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const item = await attachmentsApi.add(entryId, file);
      setItems((current) => [item, ...(current ?? [])]);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const remove = async (item: Attachment) => {
    setBusy(true);
    setError("");
    try {
      await attachmentsApi.remove(entryId, item.id);
      setItems((current) => current?.filter(({ id }) => id !== item.id) ?? []);
      setConfirm(null);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="card wide">
      <div className="documentation-header">
        <h2>Pièces jointes</h2>
        <button type="button" className="btn" disabled={busy || items === null} onClick={() => input.current?.click()}>
          <Paperclip size={14} /> Joindre un fichier
        </button>
        <input ref={input} type="file" hidden aria-label="Fichier à joindre" onChange={(event) => {
          const file = event.currentTarget.files?.[0];
          event.currentTarget.value = "";
          if (file) void add(file);
        }} />
      </div>
      {error && <p className="form-error" role="alert">{error}</p>}
      {items === null && !error && <p className="muted">Chargement…</p>}
      {items?.length === 0 && <p className="muted">Aucune pièce jointe. Limite de 10 Mio par fichier.</p>}
      {items && items.length > 0 && (
        <ul className="entry-attachments">
          {items.map((item) => (
            <li key={item.id}>
              <Paperclip size={14} />
              <span className="attachment-name" title={item.name}>{item.name}</span>
              <span className="muted">{fileSize(item.size)}</span>
              {confirm === item.id ? (
                <>
                  <button type="button" className="btn danger" disabled={busy} onClick={() => remove(item)}>Confirmer la suppression</button>
                  <button type="button" className="btn" disabled={busy} onClick={() => setConfirm(null)}>Annuler</button>
                </>
              ) : (
                <>
                  <a className="icon-btn" href={attachmentsApi.downloadURL(entryId, item.id)} download={item.name}
                    aria-label={`Télécharger ${item.name}`} title="Télécharger"><Download size={14} /></a>
                  <button type="button" className="icon-btn" disabled={busy} aria-label={`Supprimer ${item.name}`}
                    title="Supprimer" onClick={() => setConfirm(item.id)}><Trash2 size={14} /></button>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
