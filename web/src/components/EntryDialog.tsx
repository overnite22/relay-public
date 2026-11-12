import { FileText, KeyRound, Settings2, SlidersHorizontal } from "lucide-react";
import { useState } from "react";
import { CredentialIcon, KINDS, KindIcon } from "../kinds";
import type { Credential, EntryInput, Kind, Tree } from "../types";
import { inheritedCredential } from "../credentials";
import { pathOf } from "./Dashboard";
import { MarkdownNotes } from "./MarkdownNotes";
import { Modal } from "./Modal";

/** Choix du type lors de la création, façon « Ajouter une entrée ». */
export function KindPicker({ onPick, onClose }: { onPick: (k: Kind) => void; onClose: () => void }) {
  return (
    <Modal title="Nouvelle entrée" onClose={onClose} width={520}>
      <p className="muted" style={{ marginTop: 0 }}>
        Choisissez le type d'entrée à créer.
      </p>
      <div className="kind-grid">
        {(Object.keys(KINDS) as Kind[]).filter((k) => k !== "pve").map((k) => (
          <button key={k} className="kind-tile" onClick={() => onPick(k)} autoFocus={k === "rdp"}>
            <KindIcon kind={k} size={30} />
            <span className="kind-name">{KINDS[k].label}</span>
            <span className="kind-desc">{KINDS[k].long}</span>
          </button>
        ))}
      </div>
    </Modal>
  );
}

type Section = "general" | "connection" | "credentials" | "notes";

interface Props {
  initial: EntryInput;
  isNew: boolean;
  initialSection?: Section;
  tree: Tree;
  credentials: Credential[];
  vaultName: string;
  onSave: (e: EntryInput) => Promise<void>;
  onClose: () => void;
  onManageCredentials: () => void;
}

const RDP_LAYOUTS: [string, string][] = [
  ["fr-fr-azerty", "Français (AZERTY)"],
  ["fr-be-azerty", "Français (Belgique)"],
  ["fr-ch-qwertz", "Français (Suisse)"],
  ["en-us-qwerty", "Anglais (États-Unis)"],
  ["en-gb-qwerty", "Anglais (Royaume-Uni)"],
  ["de-de-qwertz", "Allemand"],
  ["es-es-qwerty", "Espagnol"],
  ["it-it-qwerty", "Italien"],
  ["failsafe", "Unicode (secours)"],
];

const SSH_SCHEMES: [string, string][] = [
  ["gray-black", "Gris sur noir"],
  ["green-black", "Vert sur noir"],
  ["white-black", "Blanc sur noir"],
  ["black-white", "Noir sur blanc"],
];

export function EntryDialog(p: Props) {
  const [e, setE] = useState<EntryInput>(p.initial);
  const [section, setSection] = useState<Section>(p.initialSection ?? "general");
  const [error, setError] = useState("");
  const [preview, setPreview] = useState(false);
  const [saving, setSaving] = useState(false);
  const k = KINDS[e.kind];
  const set = <K extends keyof EntryInput>(key: K, v: EntryInput[K]) => setE((cur) => ({ ...cur, [key]: v }));
  const inherited = inheritedCredential(p.tree, e.folderId, e.kind);
  const inheritedName = p.credentials.find((credential) => credential.id === inherited.id)?.name;
  const st = e.settings as Record<string, unknown>;
  const setS = (key: string, v: unknown) => setE((cur) => ({ ...cur, settings: { ...cur.settings, [key]: v } }));

  const folders = [...p.tree.folders]
    .map((f) => ({ id: f.id, path: pathOf(p.tree, f.id) }))
    .sort((a, b) => a.path.localeCompare(b.path, "fr"));

  const sections: { id: Section; label: string; Icon: typeof Settings2 }[] = [
    { id: "general", label: "Général", Icon: Settings2 },
    ...(e.kind !== "web" && e.kind !== "pve" ? [{ id: "connection" as Section, label: `Paramètres ${k.label}`, Icon: SlidersHorizontal }] : []),
    { id: "credentials", label: "Identifiants", Icon: KeyRound },
    { id: "notes", label: "Notes", Icon: FileText },
  ];

  const save = async () => {
    setError("");
    setSaving(true);
    try {
      await p.onSave(e);
    } catch (err) {
      setError((err as Error).message);
      setSaving(false);
    }
  };

  return (
    <Modal
      title={`${p.isNew ? "Nouvelle entrée" : "Propriétés"} — ${k.long}`}
      icon={<KindIcon kind={e.kind} />}
      onClose={p.onClose}
      width={780}
      height={560}
      footer={
        <>
          {error && <span className="form-error">{error}</span>}
          <div className="spacer" />
          <button className="btn" onClick={p.onClose}>
            Annuler
          </button>
          <button className="btn primary" onClick={save} disabled={saving}>
            OK
          </button>
        </>
      }
    >
      <form
        className="props-dialog"
        onSubmit={(ev) => {
          ev.preventDefault();
          save();
        }}
      >
        <nav className="props-nav">
          {sections.map((s) => (
            <button type="button" key={s.id} className={section === s.id ? "active" : ""} onClick={() => setSection(s.id)}>
              <s.Icon size={15} /> {s.label}
            </button>
          ))}
        </nav>
        <div className="props-pane">
          {section === "general" && (
            <>
              <h3>Général</h3>
              <label className="field">
                <span>Nom</span>
                <input value={e.name} onChange={(ev) => set("name", ev.target.value)} autoFocus required placeholder="ex. pve1" />
              </label>
              <label className="field">
                <span>Dossier</span>
                <select value={e.folderId ?? ""} onChange={(ev) => set("folderId", ev.target.value || null)}>
                  <option value="">{p.vaultName} (racine)</option>
                  {folders.map((f) => (
                    <option key={f.id} value={f.id}>
                      {f.path}
                    </option>
                  ))}
                </select>
              </label>
              {e.kind === "pve" ? (
                <p className="muted">
                  Console Proxmox {String(st.type ?? "").toUpperCase()} {String(st.vmid ?? "")} sur le nœud {String(st.node ?? "")}, gérée par la
                  synchronisation.
                </p>
              ) : e.kind === "web" ? (
                <label className="field">
                  <span>URL</span>
                  <input value={e.url} onChange={(ev) => set("url", ev.target.value)} placeholder="https://idrac.lab.local" />
                </label>
              ) : (
                <div className="field-row">
                  <label className="field grow">
                    <span>Hôte</span>
                    <input value={e.host} onChange={(ev) => set("host", ev.target.value)} placeholder="192.0.2.10 ou srv.lab.local" />
                  </label>
                  <label className="field" style={{ width: 110 }}>
                    <span>Port</span>
                    <input
                      type="number"
                      min={1}
                      max={65535}
                      value={e.port || ""}
                      placeholder={String(k.defaultPort)}
                      onChange={(ev) => set("port", Number(ev.target.value) || 0)}
                    />
                  </label>
                </div>
              )}
              <label className="check">
                <input type="checkbox" checked={e.favorite} onChange={(ev) => set("favorite", ev.target.checked)} />
                Afficher dans les favoris
              </label>
            </>
          )}

          {section === "connection" && e.kind === "rdp" && (
            <>
              <h3>Paramètres RDP</h3>
              <label className="field">
                <span>Sécurité</span>
                <select value={(st.security as string) ?? "any"} onChange={(ev) => setS("security", ev.target.value)}>
                  <option value="any">Négociée (recommandé)</option>
                  <option value="nla">NLA (Network Level Authentication)</option>
                  <option value="tls">TLS</option>
                  <option value="rdp">RDP standard</option>
                  <option value="vmconnect">Hyper-V (vmconnect)</option>
                </select>
              </label>
              <label className="field">
                <span>Disposition du clavier distant</span>
                <select value={(st.keyboardLayout as string) ?? "fr-fr-azerty"} onChange={(ev) => setS("keyboardLayout", ev.target.value)}>
                  {RDP_LAYOUTS.map(([v, l]) => (
                    <option key={v} value={v}>
                      {l}
                    </option>
                  ))}
                </select>
              </label>
              <label className="check">
                <input type="checkbox" checked={st.ignoreCert === true} onChange={(ev) => setS("ignoreCert", ev.target.checked)} />
                Accepter les certificats non approuvés (auto-signés)
              </label>
              <label className="check">
                <input type="checkbox" checked={!!st.console} onChange={(ev) => setS("console", ev.target.checked)} />
                Session console (administration)
              </label>
              <label className="check">
                <input type="checkbox" checked={!st.disableAudio} onChange={(ev) => setS("disableAudio", !ev.target.checked)} />
                Lire le son de la machine distante
              </label>
              <label className="check">
                <input type="checkbox" checked={!!st.wallpaper} onChange={(ev) => setS("wallpaper", ev.target.checked)} />
                Afficher le fond d'écran
              </label>
              <label className="check">
                <input type="checkbox" checked={!st.disableCopy} onChange={(ev) => setS("disableCopy", !ev.target.checked)} />
                Autoriser la copie depuis la session
              </label>
              <label className="check">
                <input type="checkbox" checked={!st.disablePaste} onChange={(ev) => setS("disablePaste", !ev.target.checked)} />
                Autoriser le collage vers la session
              </label>
              <label className="check">
                <input type="checkbox" checked={st.drive !== false} onChange={(ev) => setS("drive", ev.target.checked)} />
                Lecteur « Relay » dans Windows : bouton « Fichiers » pour échanger des fichiers
              </label>
            </>
          )}

          {section === "connection" && e.kind === "ssh" && (
            <>
              <h3>Paramètres SSH</h3>
              <div className="field-row">
                <label className="field grow">
                  <span>Police</span>
                  <input value={(st.fontName as string) ?? ""} placeholder="monospace" onChange={(ev) => setS("fontName", ev.target.value)} />
                </label>
                <label className="field" style={{ width: 110 }}>
                  <span>Taille</span>
                  <input
                    type="number"
                    min={6}
                    max={48}
                    value={(st.fontSize as number) || ""}
                    placeholder="12"
                    onChange={(ev) => setS("fontSize", Number(ev.target.value) || 0)}
                  />
                </label>
              </div>
              <label className="field">
                <span>Couleurs</span>
                <select value={(st.colorScheme as string) ?? "gray-black"} onChange={(ev) => setS("colorScheme", ev.target.value)}>
                  {SSH_SCHEMES.map(([v, l]) => (
                    <option key={v} value={v}>
                      {l}
                    </option>
                  ))}
                </select>
              </label>
              <label className="field">
                <span>Historique (lignes)</span>
                <input
                  type="number"
                  min={0}
                  value={(st.scrollback as number) || ""}
                  placeholder="1000"
                  onChange={(ev) => setS("scrollback", Number(ev.target.value) || 0)}
                />
              </label>
              <label className="check">
                <input type="checkbox" checked={st.sftp !== false} onChange={(ev) => setS("sftp", ev.target.checked)} />
                Transfert de fichiers (SFTP) : bouton « Fichiers » dans la session
              </label>
              {st.sftp !== false && (
                <label className="field">
                  <span>Dossier de départ du navigateur de fichiers</span>
                  <input value={(st.sftpRoot as string) ?? ""} placeholder="/" onChange={(ev) => setS("sftpRoot", ev.target.value)} />
                </label>
              )}
            </>
          )}

          {section === "connection" && e.kind === "vnc" && (
            <>
              <h3>Paramètres VNC</h3>
              <label className="field">
                <span>Profondeur de couleur</span>
                <select value={String((st.colorDepth as number) ?? "")} onChange={(ev) => setS("colorDepth", Number(ev.target.value) || 0)}>
                  <option value="">Automatique</option>
                  <option value="8">256 couleurs</option>
                  <option value="16">16 bits</option>
                  <option value="24">24 bits</option>
                  <option value="32">32 bits</option>
                </select>
              </label>
              <label className="check">
                <input type="checkbox" checked={!!st.readOnly} onChange={(ev) => setS("readOnly", ev.target.checked)} />
                Lecture seule (observer sans contrôler)
              </label>
              <label className="check">
                <input type="checkbox" checked={st.cursor === "remote"} onChange={(ev) => setS("cursor", ev.target.checked ? "remote" : "")} />
                Curseur dessiné par le serveur
              </label>
            </>
          )}

          {section === "credentials" && (
            <>
              <h3>Identifiants</h3>
              <label className="field">
                <span>Identifiant du coffre</span>
                <div className="field-row">
                  <select
                    className="grow"
                    aria-label="Identifiant du coffre"
                    value={e.credentialId ?? (st.credentialMode === "none" ? "__none" : "")}
                    onChange={(event) => {
                      const value = event.target.value;
                      setE((current) => ({
                        ...current,
                        credentialId: value && value !== "__none" ? value : null,
                        settings: { ...current.settings, credentialMode: value === "__none" ? "none" : "inherit" },
                      }));
                    }}
                  >
                    <option value="">Hériter du dossier{inheritedName ? ` — ${inheritedName}` : ""}</option>
                    <option value="__none">Aucun identifiant</option>
                    {p.credentials.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.name}
                        {c.username ? ` (${c.domain ? c.domain + "\\" : ""}${c.username})` : ""}
                      </option>
                    ))}
                  </select>
                  <button type="button" className="btn" onClick={p.onManageCredentials}>
                    <CredentialIcon size={14} /> Gérer le coffre…
                  </button>
                </div>
              </label>
              <label className="field">
                <span>Nom d'utilisateur (remplace celui de l'identifiant)</span>
                <input value={e.username} onChange={(ev) => set("username", ev.target.value)} placeholder="facultatif" autoComplete="off" />
              </label>
              <p className="muted small">
                Les mots de passe, clés et codes TOTP sont stockés chiffrés dans le coffre et ne sont jamais envoyés au navigateur
                pour ouvrir une session : c'est le serveur Relay qui s'authentifie.
              </p>
            </>
          )}

          {section === "notes" && (
            <>
              <h3>Notes Markdown</h3>
              <div className="notes-mode" role="group" aria-label="Mode des notes">
                <button className={`btn ${!preview ? "primary" : ""}`} type="button" aria-pressed={!preview} onClick={() => setPreview(false)}>Écrire</button>
                <button className={`btn ${preview ? "primary" : ""}`} type="button" aria-pressed={preview} onClick={() => setPreview(true)}>Aperçu</button>
              </div>
              {preview ? (
                <div className="notes-preview">{e.notes ? <MarkdownNotes text={e.notes} /> : <p className="muted">Aucune note.</p>}</div>
              ) : (
                <textarea className="notes-input" aria-label="Notes Markdown" value={e.notes} onChange={(ev) => set("notes", ev.target.value)} placeholder="Documentation, procédures, contacts…" />
              )}
            </>
          )}
        </div>
        <button type="submit" hidden />
      </form>
    </Modal>
  );
}
