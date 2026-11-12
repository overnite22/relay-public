import { KeyRound } from "lucide-react";
import { useState } from "react";
import { api } from "../api";
import { inheritedCredential } from "../credentials";
import { KINDS } from "../kinds";
import type { Credential, Folder, Kind, Tree } from "../types";
import { Modal } from "./Modal";

export function FolderCredentialsDialog({
  folder,
  tree,
  credentials,
  onSaved,
  onClose,
}: {
  folder: Folder;
  tree: Tree;
  credentials: Credential[];
  onSaved: () => Promise<void>;
  onClose: () => void;
}) {
  const [values, setValues] = useState(folder.credentials ?? {});
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    setError("");
    try {
      await api.folderCredentials(folder.id, values);
      await onSaved();
      onClose();
    } catch (err) {
      setError((err as Error).message);
      setBusy(false);
    }
  };
  return (
    <Modal
      title={`Identifiants du dossier — ${folder.name}`}
      icon={<KeyRound size={16} />}
      onClose={onClose}
      width={540}
      footer={
        <>
          <span className="form-error">{error}</span>
          <div className="spacer" />
          <button className="btn" onClick={onClose}>
            Annuler
          </button>
          <button className="btn primary" onClick={save} disabled={busy}>
            Enregistrer
          </button>
        </>
      }
    >
      <p className="muted">Appliqués aux entrées sans identifiant explicite, y compris dans les sous-dossiers.</p>
      {(["ssh", "rdp", "vnc", "web"] as Kind[]).map((kind) => {
        const inherited = inheritedCredential(tree, folder.parentId, kind);
        const inheritedName = credentials.find((c) => c.id === inherited.id)?.name;
        const value = Object.hasOwn(values, kind) ? (values[kind] ?? "__none") : "";
        return (
          <label className="field" key={kind}>
            <span>{KINDS[kind].label}</span>
            <select
              aria-label={`Identifiant ${KINDS[kind].label}`}
              value={value}
              onChange={(event) => {
                const next = { ...values };
                if (!event.target.value) delete next[kind];
                else next[kind] = event.target.value === "__none" ? null : event.target.value;
                setValues(next);
              }}
            >
              <option value="">Hériter du parent{inheritedName ? ` — ${inheritedName}` : ""}</option>
              <option value="__none">Aucun identifiant</option>
              {credentials.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>
        );
      })}
    </Modal>
  );
}
