import { CheckCircle2, Loader2, Plus, RefreshCw, Trash2, Unplug, XCircle } from "lucide-react";
import { useEffect, useState } from "react";
import { duplicatesApi, sourcesApi } from "../api";
import type { Credential, Source, SourceInput, SourceKind } from "../types";
import { Confirm, Modal } from "./Modal";

const KIND_LABEL: Record<SourceKind, string> = { proxmox: "Proxmox VE", netbox: "NetBox" };

function blank(kind: SourceKind): SourceInput {
  return {
    kind,
    name: kind === "proxmox" ? "Proxmox" : "NetBox",
    url: kind === "proxmox" ? "https://192.0.2.10:8006" : "https://netbox.example.com",
    tokenId: kind === "proxmox" ? "relay@pve!relay" : "",
    verifyTls: kind === "netbox",
    options: {},
    intervalMin: 60,
  };
}

function toInput(s: Source): SourceInput {
  return { kind: s.kind, name: s.name, url: s.url, tokenId: s.tokenId, verifyTls: s.verifyTls, options: s.options ?? {}, intervalMin: s.intervalMin };
}

export function SourcesDialog({
  credentials,
  onSynced,
  onClose,
  onToast,
}: {
  credentials: Credential[];
  onSynced: () => Promise<void>;
  onClose: () => void;
  onToast: (m: string) => void;
}) {
  const [sources, setSources] = useState<Source[]>([]);
  const [selId, setSelId] = useState<string | null>(null);
  const [form, setForm] = useState<SourceInput | null>(null);
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState<"" | "save" | "test" | "sync">("");
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null);
  const [error, setError] = useState("");
  const [confirmDel, setConfirmDel] = useState(false);

  const load = async () => {
    const list = await sourcesApi.list();
    setSources(list);
    return list;
  };
  useEffect(() => {
    load().then((l) => {
      if (l[0]) select(l[0]);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const sel = sources.find((s) => s.id === selId) ?? null;
  const select = (s: Source | null, kind?: SourceKind) => {
    setSelId(s?.id ?? null);
    setForm(s ? toInput(s) : kind ? blank(kind) : null);
    setSecret("");
    setResult(null);
    setError("");
  };

  const save = async (): Promise<Source | null> => {
    if (!form) return null;
    setBusy("save");
    setError("");
    try {
      const saved = await sourcesApi.save({ ...form, ...(secret ? { secret } : {}) }, sel?.id);
      await load();
      setSelId(saved.id);
      setSecret("");
      return saved;
    } catch (e) {
      setError((e as Error).message);
      return null;
    } finally {
      setBusy("");
    }
  };

  const run = async (what: "test" | "sync") => {
    const s = await save();
    if (!s) return;
    setBusy(what);
    setResult(null);
    try {
      const r = what === "test" ? await sourcesApi.test(s.id) : await sourcesApi.sync(s.id);
      setResult(r);
      if (what === "sync") {
        await load();
        await onSynced();
        if (r.ok) onToast("Synchronisation terminée");
      }
    } catch (e) {
      setResult({ ok: false, message: (e as Error).message });
    } finally {
      setBusy("");
    }
  };

  const set = <K extends keyof SourceInput>(k: K, v: SourceInput[K]) => setForm((f) => (f ? { ...f, [k]: v } : f));
  const setOpt = (k: keyof SourceInput["options"], v: unknown) => setForm((f) => (f ? { ...f, options: { ...f.options, [k]: v } } : f));
  const optOn = (v: boolean | undefined) => v !== false;

  return (
    <Modal
      title="Sources de synchronisation"
      icon={<RefreshCw size={16} />}
      onClose={onClose}
      width={920}
      height={640}
      footer={
        <>
          {error && <span className="form-error">{error}</span>}
          <div className="spacer" />
          <button className="btn" onClick={onClose}>
            Fermer
          </button>
          {form && (
            <>
              <button className="btn" onClick={() => run("test")} disabled={!!busy}>
                {busy === "test" ? <Loader2 size={14} className="spin" /> : <Unplug size={14} />} Tester
              </button>
              <button className="btn" onClick={() => run("sync")} disabled={!!busy}>
                {busy === "sync" ? <Loader2 size={14} className="spin" /> : <RefreshCw size={14} />} Synchroniser maintenant
              </button>
              <button className="btn primary" onClick={save} disabled={!!busy}>
                Enregistrer
              </button>
            </>
          )}
        </>
      }
    >
      <div className="cred-dialog">
        <div className="cred-list">
          <div style={{ display: "flex", gap: 6 }}>
            <button className="btn small" onClick={() => select(null, "proxmox")}>
              <Plus size={14} /> Proxmox
            </button>
            <button className="btn small" onClick={() => select(null, "netbox")}>
              <Plus size={14} /> NetBox
            </button>
          </div>
          <div className="cred-items">
            {!selId && form && (
              <div className="cred-item selected">
                <em>Nouvelle source {KIND_LABEL[form.kind]}</em>
              </div>
            )}
            {sources.map((s) => (
              <div key={s.id} className={`cred-item ${s.id === selId ? "selected" : ""}`} onClick={() => select(s)}>
                <div style={{ minWidth: 0 }}>
                  <div>{s.name}</div>
                  <div className="muted small">
                    {KIND_LABEL[s.kind]} · {s.lastSyncAt ? new Date(s.lastSyncAt).toLocaleString("fr-FR", { dateStyle: "short", timeStyle: "short" }) : "jamais synchronisée"}
                  </div>
                  {s.lastStatus && <div className={`small ${s.lastStatus.startsWith("Échec") ? "form-error" : "muted"}`}>{s.lastStatus}</div>}
                </div>
              </div>
            ))}
            {!sources.length && !form && <div className="tree-empty">Aucune source. Ajoutez un cluster Proxmox ou votre NetBox.</div>}
          </div>
        </div>
        <div className="cred-form">
          {form ? (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                save();
              }}
            >
              <h3 style={{ marginTop: 0 }}>{KIND_LABEL[form.kind]}</h3>
              <div className="field-row">
                <label className="field grow">
                  <span>Nom (dossier créé dans l'arborescence)</span>
                  <input value={form.name} onChange={(e) => set("name", e.target.value)} required />
                </label>
                <label className="field" style={{ width: 200 }}>
                  <span>Synchronisation automatique</span>
                  <select value={form.intervalMin} onChange={(e) => set("intervalMin", Number(e.target.value))}>
                    <option value={0}>Manuelle</option>
                    <option value={15}>Toutes les 15 min</option>
                    <option value={60}>Toutes les heures</option>
                    <option value={360}>Toutes les 6 h</option>
                    <option value={1440}>Une fois par jour</option>
                  </select>
                </label>
              </div>
              <label className="field">
                <span>URL</span>
                <input value={form.url} onChange={(e) => set("url", e.target.value)} required />
              </label>
              {form.kind === "proxmox" && (
                <label className="field">
                  <span>Identifiant du jeton d'API (utilisateur@royaume!jeton)</span>
                  <input className="mono" value={form.tokenId} onChange={(e) => set("tokenId", e.target.value)} placeholder="relay@pve!relay" />
                </label>
              )}
              <label className="field">
                <span>{form.kind === "proxmox" ? "Secret du jeton (UUID)" : "Jeton d'API NetBox"}</span>
                <input
                  className="mono"
                  type="password"
                  value={secret}
                  onChange={(e) => setSecret(e.target.value)}
                  placeholder={sel?.hasSecret ? "•••••••• enregistré (laisser vide pour conserver)" : ""}
                  autoComplete="new-password"
                />
              </label>
              <label className="check">
                <input type="checkbox" checked={form.verifyTls} onChange={(e) => set("verifyTls", e.target.checked)} />
                Vérifier le certificat TLS (décochez pour un certificat auto-signé)
              </label>

              <h3>Entrées créées</h3>
              {form.kind === "proxmox" ? (
                <>
                  <p className="muted small" style={{ marginTop: 0 }}>
                    Une console (noVNC) par VM et conteneur, toujours. En option :
                  </p>
                  <label className="check">
                    <input type="checkbox" checked={optOn(form.options.nodeEntries)} onChange={(e) => setOpt("nodeEntries", e.target.checked)} />
                    Nœuds : entrée SSH + interface web (:8006)
                  </label>
                  <label className="check">
                    <input type="checkbox" checked={optOn(form.options.guestEntries)} onChange={(e) => setOpt("guestEntries", e.target.checked)} />
                    Invités en marche : entrée SSH (ou RDP pour Windows) avec l'IP détectée
                  </label>
                </>
              ) : (
                <>
                  <label className="check">
                    <input type="checkbox" checked={optOn(form.options.devices)} onChange={(e) => setOpt("devices", e.target.checked)} />
                    Équipements (classés par site)
                  </label>
                  <label className="check">
                    <input type="checkbox" checked={optOn(form.options.vms)} onChange={(e) => setOpt("vms", e.target.checked)} />
                    Machines virtuelles (classées par cluster)
                  </label>
                  <p className="muted small">Seuls les objets ayant une IP principale sont importés ; plateforme « Windows » → RDP, sinon SSH.</p>
                </>
              )}
              <div className="field-row">
                <label className="field grow">
                  <span>Identifiant par défaut des entrées SSH</span>
                  <select value={form.options.sshCredentialId ?? ""} onChange={(e) => setOpt("sshCredentialId", e.target.value || undefined)}>
                    <option value="">Aucun</option>
                    {credentials.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="field grow">
                  <span>Identifiant par défaut des entrées RDP</span>
                  <select value={form.options.rdpCredentialId ?? ""} onChange={(e) => setOpt("rdpCredentialId", e.target.value || undefined)}>
                    <option value="">Aucun</option>
                    {credentials.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.name}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <p className="muted small">
                Les entrées synchronisées gardent vos modifications (dossier, identifiant, notes, favoris, port). Une entrée qui disparaît de la
                source est supprimée.
              </p>
              {result && (
                <div className={`sync-result ${result.ok ? "ok" : "ko"}`}>
                  {result.ok ? <CheckCircle2 size={16} /> : <XCircle size={16} />} {result.message}
                </div>
              )}
              {sel && (
                <button
                  type="button"
                  className="btn small"
                  style={{ marginTop: 12, marginRight: 8 }}
                  title="Les entrées synchronisées que vous avez supprimées ou fusionnées ne sont pas recréées ; ce bouton les réautorise."
                  onClick={async () => {
                    try {
                      const r = await duplicatesApi.clearExclusions(sel.id);
                      setResult({ ok: true, message: r.message });
                    } catch (e) {
                      setResult({ ok: false, message: (e as Error).message });
                    }
                  }}
                >
                  <RefreshCw size={14} /> Réautoriser les entrées supprimées
                </button>
              )}
              {sel && (
                <button type="button" className="btn danger small" style={{ marginTop: 12 }} onClick={() => setConfirmDel(true)}>
                  <Trash2 size={14} /> Supprimer la source
                </button>
              )}
              <button type="submit" hidden />
            </form>
          ) : (
            <div className="empty-state">
              <p>Sélectionnez une source ou ajoutez-en une.</p>
            </div>
          )}
        </div>
      </div>
      {confirmDel && sel && (
        <Confirm
          title="Supprimer la source"
          message={
            <>
              Supprimer la source <b>{sel.name}</b> ? Les entrées déjà créées sont conservées mais ne seront plus mises à jour (les consoles Proxmox ne
              fonctionneront plus).
            </>
          }
          danger
          confirmLabel="Supprimer"
          onConfirm={async () => {
            await sourcesApi.remove(sel.id);
            const l = await load();
            select(l[0] ?? null);
          }}
          onClose={() => setConfirmDel(false)}
        />
      )}
    </Modal>
  );
}
