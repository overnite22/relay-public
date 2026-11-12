import { Copy, Eye, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { api } from "../api";
import { CredentialIcon } from "../kinds";
import type { Credential, CredentialInput } from "../types";
import { copyText } from "./Dashboard";
import { Confirm, Modal } from "./Modal";

type SecretField = "password" | "privateKey" | "passphrase" | "totp";

const SECRET_LABELS: Record<SecretField, string> = {
  password: "Mot de passe",
  privateKey: "Clé privée SSH",
  passphrase: "Phrase secrète de la clé",
  totp: "Secret TOTP (base32 ou otpauth://)",
};

const HAS: Record<SecretField, keyof Credential> = {
  password: "hasPassword",
  privateKey: "hasPrivateKey",
  passphrase: "hasPassphrase",
  totp: "hasTotp",
};

function SecretInput({
  field,
  cred,
  value,
  onChange,
  onToast,
}: {
  field: SecretField;
  cred: Credential | null;
  value: string | undefined;
  onChange: (v: string | undefined) => void;
  onToast: (m: string) => void;
}) {
  const stored = !!cred && !!cred[HAS[field]];
  const editing = value !== undefined;
  const multiline = field === "privateKey";
  return (
    <label className="field">
      <span>{SECRET_LABELS[field]}</span>
      {stored && !editing ? (
        <div className="secret-stored">
          <span className="mono">•••••••• enregistré</span>
          <button
            type="button"
            className="icon-btn"
            title="Afficher"
            onClick={async () => {
              try {
                const { value: v } = await api.reveal(cred!.id, field);
                onChange(v);
              } catch (e) {
                onToast((e as Error).message);
              }
            }}
          >
            <Eye size={14} />
          </button>
          {field !== "privateKey" && (
            <button
              type="button"
              className="icon-btn"
              title="Copier"
              onClick={async () => {
                try {
                  const { value: v } = await api.reveal(cred!.id, field);
                  await copyText(v, onToast, SECRET_LABELS[field], true);
                } catch (e) {
                  onToast((e as Error).message);
                }
              }}
            >
              <Copy size={14} />
            </button>
          )}
          <button type="button" className="btn small" onClick={() => onChange("")}>
            Remplacer
          </button>
        </div>
      ) : multiline ? (
        <textarea
          className="mono key-input"
          value={value ?? ""}
          onChange={(e) => onChange(e.target.value)}
          placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
          spellCheck={false}
        />
      ) : (
        <input
          type={field === "totp" ? "text" : "password"}
          className="mono"
          value={value ?? ""}
          onChange={(e) => onChange(e.target.value)}
          autoComplete="new-password"
          spellCheck={false}
        />
      )}
    </label>
  );
}

export function CredentialsDialog({
  credentials,
  onChanged,
  onClose,
  onToast,
}: {
  credentials: Credential[];
  onChanged: () => Promise<void>;
  onClose: () => void;
  onToast: (m: string) => void;
}) {
  const [selId, setSelId] = useState<string | null>(credentials[0]?.id ?? null);
  const [creating, setCreating] = useState(credentials.length === 0);
  const cred = creating ? null : (credentials.find((c) => c.id === selId) ?? null);
  const blank = (c: Credential | null): CredentialInput => ({
    name: c?.name ?? "",
    username: c?.username ?? "",
    domain: c?.domain ?? "",
    notes: c?.notes ?? "",
  });
  const [form, setForm] = useState<CredentialInput>(blank(cred));
  const [error, setError] = useState("");
  const [confirmDel, setConfirmDel] = useState(false);

  const select = (c: Credential | null, isNew = false) => {
    setCreating(isNew);
    setSelId(c?.id ?? null);
    setForm(blank(c));
    setError("");
  };

  const save = async () => {
    setError("");
    try {
      const saved = await api.saveCredential(form, cred?.id);
      await onChanged();
      onToast(cred ? "Identifiant mis à jour" : "Identifiant ajouté au coffre");
      setCreating(false);
      setSelId(saved.id);
      setForm(blank(saved));
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const remove = async () => {
    if (!cred) return;
    try {
      await api.deleteCredential(cred.id);
      await onChanged();
      select(null, false);
      onToast("Identifiant supprimé");
    } catch (e) {
      onToast((e as Error).message);
    }
  };

  const setSecret = (f: SecretField, v: string | undefined) => setForm((cur) => ({ ...cur, [f]: v }));

  return (
    <Modal
      title="Coffre — Identifiants"
      icon={<CredentialIcon />}
      onClose={onClose}
      width={860}
      height={600}
      footer={
        <>
          {error && <span className="form-error">{error}</span>}
          <div className="spacer" />
          <button className="btn" onClick={onClose}>
            Fermer
          </button>
          {(creating || cred) && (
            <button className="btn primary" onClick={save}>
              Enregistrer
            </button>
          )}
        </>
      }
    >
      <div className="cred-dialog">
        <div className="cred-list">
          <button className="btn small" onClick={() => select(null, true)}>
            <Plus size={14} /> Nouvel identifiant
          </button>
          <div className="cred-items">
            {creating && (
              <div className="cred-item selected">
                <CredentialIcon size={14} /> <em>Nouvel identifiant</em>
              </div>
            )}
            {credentials.map((c) => (
              <div key={c.id} className={`cred-item ${!creating && c.id === selId ? "selected" : ""}`} onClick={() => select(c)}>
                <CredentialIcon size={14} />
                <div>
                  <div>{c.name}</div>
                  <div className="muted small">{c.domain ? `${c.domain}\\${c.username}` : c.username || "—"}</div>
                </div>
              </div>
            ))}
            {!credentials.length && !creating && <div className="tree-empty">Coffre vide</div>}
          </div>
        </div>
        <div className="cred-form">
          {creating || cred ? (
            <form
              key={creating ? "new" : cred?.id}
              onSubmit={(e) => {
                e.preventDefault();
                save();
              }}
            >
              <label className="field">
                <span>Nom</span>
                <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} autoFocus required placeholder="ex. root Proxmox" />
              </label>
              <div className="field-row">
                <label className="field grow">
                  <span>Nom d'utilisateur</span>
                  <input value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} autoComplete="off" />
                </label>
                <label className="field" style={{ width: 180 }}>
                  <span>Domaine</span>
                  <input value={form.domain} onChange={(e) => setForm({ ...form, domain: e.target.value })} placeholder="facultatif" />
                </label>
              </div>
              {(Object.keys(SECRET_LABELS) as SecretField[]).map((f) => (
                <SecretInput key={f} field={f} cred={cred} value={form[f]} onChange={(v) => setSecret(f, v)} onToast={onToast} />
              ))}
              <label className="field">
                <span>Notes</span>
                <textarea value={form.notes} onChange={(e) => setForm({ ...form, notes: e.target.value })} rows={3} />
              </label>
              {cred && (
                <button type="button" className="btn danger small" onClick={() => setConfirmDel(true)}>
                  <Trash2 size={14} /> Supprimer cet identifiant
                </button>
              )}
              <button type="submit" hidden />
            </form>
          ) : (
            <div className="empty-state">
              <p>Sélectionnez un identifiant ou créez-en un.</p>
            </div>
          )}
        </div>
      </div>
      {confirmDel && cred && (
        <Confirm
          title="Supprimer l'identifiant"
          message={
            <>
              Supprimer <b>{cred.name}</b> du coffre ? Les entrées qui l'utilisent n'auront plus d'identifiant.
            </>
          }
          danger
          confirmLabel="Supprimer"
          onConfirm={remove}
          onClose={() => setConfirmDel(false)}
        />
      )}
    </Modal>
  );
}
