import { PlugZap } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { api } from "../api";
import type { Credential, QuickTarget } from "../types";
import { Modal } from "./Modal";

const defaults: QuickTarget = { kind: "ssh", host: "", port: 0, username: "", domain: "", credentialId: null, record: false };

export function QuickConnectDialog({ credentials, initial, onConnect, onClose }: {
  credentials: Credential[];
  initial?: QuickTarget;
  onConnect: (target: QuickTarget, token: string) => void;
  onClose: () => void;
}) {
  const [target, setTarget] = useState(initial ?? defaults);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const password = useRef<HTMLInputElement>(null);
  const active = useRef(false);

  useEffect(() => {
    active.current = true;
    return () => { active.current = false; };
  }, []);

  const change = <K extends keyof QuickTarget>(key: K, value: QuickTarget[K]) => {
    setTarget((current) => ({ ...current, [key]: value }));
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    const connection = { ...target, host: target.host.trim(), username: target.username.trim() };
    try {
      const { token } = await api.quickConnect({
        ...connection,
        password: target.credentialId ? "" : password.current?.value ?? "",
        width: Math.max(640, window.innerWidth - 300),
        height: Math.max(480, window.innerHeight - 240),
        dpi: 96,
        timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      });
      if (active.current) onConnect(connection, token);
    } catch (err) {
      if (active.current) {
        setError((err as Error).message);
        setBusy(false);
      }
    } finally {
      if (password.current) password.current.value = "";
    }
  };

  return (
    <Modal title="Connexion rapide" icon={<PlugZap size={16} />} width={520} onClose={onClose}>
      <form className="quick-connect-form" onSubmit={submit}>
        <label className="field">
          <span>Protocole</span>
          <select aria-label="Protocole" value={target.kind} onChange={(event) => {
            const kind = event.target.value === "rdp" ? "rdp" : "ssh";
            setTarget((current) => ({ ...current, kind, port: 0 }));
          }}>
            <option value="ssh">SSH</option>
            <option value="rdp">RDP</option>
          </select>
        </label>
        <div className="quick-address">
          <label className="field">
            <span>Hôte</span>
            <input autoFocus required value={target.host} placeholder="srv-lab ou 192.0.2.20"
              onChange={(event) => change("host", event.target.value)} />
          </label>
          <label className="field">
            <span>Port</span>
            <input type="number" min={1} max={65535} value={target.port || ""}
              placeholder={target.kind === "ssh" ? "22" : "3389"}
              onChange={(event) => change("port", Number(event.target.value))} />
          </label>
        </div>
        <label className="field">
          <span>Identifiant du coffre</span>
          <select aria-label="Identifiant du coffre" value={target.credentialId ?? ""} onChange={(event) => change("credentialId", event.target.value || null)}>
            <option value="">Saisie manuelle</option>
            {credentials.map((credential) => <option key={credential.id} value={credential.id}>{credential.name}</option>)}
          </select>
        </label>
        <label className="field">
          <span>Nom d’utilisateur</span>
          <input value={target.username} onChange={(event) => change("username", event.target.value)}
            placeholder={target.credentialId ? "Utilisateur du coffre par défaut" : undefined} autoComplete="off" />
        </label>
        {!target.credentialId && (
          <>
            {target.kind === "rdp" && (
              <label className="field">
                <span>Domaine (facultatif)</span>
                <input value={target.domain} onChange={(event) => change("domain", event.target.value)} />
              </label>
            )}
            <label className="field">
              <span>Mot de passe</span>
              <input ref={password} type="password" autoComplete="off" />
            </label>
          </>
        )}
        <label className="quick-record">
          <input type="checkbox" checked={target.record} onChange={(event) => change("record", event.target.checked)} />
          Enregistrer la session
        </label>
        {error && <div className="form-error" role="alert">{error}</div>}
        <div className="quick-actions">
          <button className="btn" type="button" onClick={onClose}>Annuler</button>
          <button className="btn primary" type="submit" disabled={busy}><PlugZap size={15} /> Ouvrir la session</button>
        </div>
      </form>
    </Modal>
  );
}
