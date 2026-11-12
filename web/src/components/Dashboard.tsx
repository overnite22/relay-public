import { Activity, Copy, ExternalLink, KeyRound, Pencil, Play, Plus, ShieldCheck, Star, Timer, User } from "lucide-react";
import { useEffect, useState } from "react";
import { canTestConnection, ConnectionIndicator } from "./ConnectionIndicator";
import { EntryAttachments } from "./EntryAttachments";
import { MarkdownNotes } from "./MarkdownNotes";
import { effectiveCredential } from "../credentials";
import { api } from "../api";
import { CredentialIcon, endpoint, FolderIcon, KINDS, KindIcon } from "../kinds";
import type { ConnectionTest, Credential, Entry, Selection, Tree } from "../types";

interface Props {
  tree: Tree;
  connectionStatus: Record<string, ConnectionTest>;
  credentials: Credential[];
  selection: Selection;
  vaultName: string;
  onOpen: (e: Entry) => void;
  onTest: (e: Entry) => void;
  onFolderCredentials: (id:string) => void;
  onEdit: (e: Entry, section?: "notes") => void;
  onToggleFavorite: (e: Entry) => void;
  onSelect: (s: Selection) => void;
  onNewEntry: () => void;
  onToast: (m: string) => void;
}

function fmtDate(s: string) {
  return new Date(s).toLocaleString("fr-FR", { dateStyle: "short", timeStyle: "short" });
}

export async function copyText(text: string, onToast: (m: string) => void, what: string, sensitive = false) {
  try {
    await navigator.clipboard.writeText(text);
    onToast(sensitive ? `${what} copié (effacé du presse-papiers dans 30 s)` : `${what} copié`);
    if (sensitive) {
      window.setTimeout(async () => {
        try {
          if ((await navigator.clipboard.readText()) === text) await navigator.clipboard.writeText("");
        } catch {
          /* le navigateur peut refuser la lecture : on laisse tel quel */
        }
      }, 30_000);
    }
  } catch {
    onToast("Copie refusée par le navigateur (HTTPS requis)");
  }
}

function TotpBox({ cred, onToast }: { cred: Credential; onToast: (m: string) => void }) {
  const [code, setCode] = useState<{ code: string; remaining: number; period: number } | null>(null);
  const [left, setLeft] = useState(0);
  useEffect(() => {
    if (!code) return;
    setLeft(code.remaining);
    const t = window.setInterval(() => setLeft((l) => (l <= 1 ? 0 : l - 1)), 1000);
    return () => window.clearInterval(t);
  }, [code]);
  useEffect(() => {
    if (code && left === 0) api.totp(cred.id).then(setCode).catch(() => setCode(null));
  }, [left, code, cred.id]);
  if (!code)
    return (
      <button className="btn" onClick={() => api.totp(cred.id).then(setCode).catch((e) => onToast(e.message))}>
        <Timer size={14} /> Afficher le code TOTP
      </button>
    );
  return (
    <div className="totp">
      <span className="totp-code">{code.code.replace(/(\d{3})(?=\d)/g, "$1 ")}</span>
      <span className="totp-bar">
        <span style={{ width: `${(left / code.period) * 100}%` }} />
      </span>
      <button className="icon-btn" title="Copier le code" onClick={() => copyText(code.code, onToast, "Code TOTP")}>
        <Copy size={14} />
      </button>
    </div>
  );
}

function EntryDashboard({ entry, p }: { entry: Entry; p: Props }) {
  const resolved=effectiveCredential(p.tree,entry);
  const cred = p.credentials.find((c) => c.id === resolved.id);
  const folderPath = pathOf(p.tree, entry.folderId);
  const k = KINDS[entry.kind];
  const username = entry.username || cred?.username || "";

  const copySecret = async (field: "password" | "passphrase") => {
    if (!cred) return;
    try {
      const { value } = await api.reveal(cred.id, field);
      await copyText(value, p.onToast, field === "password" ? "Mot de passe" : "Phrase secrète", true);
    } catch (e) {
      p.onToast((e as Error).message);
    }
  };

  const rows: [string, React.ReactNode][] = [
    ["Type", k.long],
    entry.kind === "web" ? ["URL", entry.url] : entry.kind === "pve" ? ["Invité", endpoint(entry)] : ["Hôte", entry.host],
    ...(entry.kind !== "web" && entry.kind !== "pve" ? ([["Port", String(entry.port)]] as [string, string][]) : []),
    ["Nom d'utilisateur", username || <em className="muted">aucun</em>],
    ["Identifiant", cred ? cred.name+(resolved.folder?` (hérité de ${resolved.folder.name})`:"") : <em className="muted">aucun</em>],
    ["Dossier", folderPath || p.vaultName],
    ["Modifiée le", fmtDate(entry.updatedAt)],
    ...(entry.sourceId ? ([["Origine", "Synchronisée automatiquement"]] as [string, string][]) : []),
  ];

  return (
    <div className="dash">
      <div className="dash-header" style={{ borderLeftColor: k.color }}>
        <div className="dash-icon" style={{ background: k.color + "22" }}>
          <KindIcon kind={entry.kind} size={34} />
        </div>
        <div className="dash-title">
          <h1>{entry.name}</h1>
          <div className="dash-sub">
            {k.label} · {endpoint(entry)} <ConnectionIndicator entry={entry} results={p.connectionStatus} />
          </div>
        </div>
        <div className="dash-actions">
          <button className="btn primary big" onClick={() => p.onOpen(entry)}>
            {entry.kind === "web" ? <ExternalLink size={16} /> : <Play size={16} />}
            {entry.kind === "web" ? "Ouvrir le site" : "Ouvrir la session"}
          </button>
          {canTestConnection(entry) && (
            <button className="btn" onClick={() => p.onTest(entry)}><Activity size={14} /> Tester</button>
          )}
          <button className="btn" onClick={() => p.onEdit(entry)}>
            <Pencil size={14} /> Modifier
          </button>
          <button className={`btn ${entry.favorite ? "fav-on" : ""}`} onClick={() => p.onToggleFavorite(entry)}>
            <Star size={14} /> {entry.favorite ? "Favori" : "Ajouter aux favoris"}
          </button>
        </div>
      </div>

      <div className="dash-grid">
        <section className="card">
          <h2>Vue d'ensemble</h2>
          <table className="props">
            <tbody>
              {rows.map(([k2, v]) => (
                <tr key={k2}>
                  <th>{k2}</th>
                  <td>{v}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>

        <section className="card">
          <h2>Identifiants</h2>
          {!cred && !username && <p className="muted">Aucun identifiant lié. Modifiez l'entrée pour en choisir un dans le coffre.</p>}
          {(cred || username) && (
            <div className="cred-actions">
              {username && (
                <div className="cred-line">
                  <User size={14} />
                  <span className="mono">{cred?.domain ? `${cred.domain}\\${username}` : username}</span>
                  <button className="icon-btn" title="Copier le nom d'utilisateur" onClick={() => copyText(username, p.onToast, "Nom d'utilisateur")}>
                    <Copy size={14} />
                  </button>
                </div>
              )}
              {cred?.hasPassword && (
                <div className="cred-line">
                  <KeyRound size={14} />
                  <span className="mono">••••••••••</span>
                  <button className="icon-btn" title="Copier le mot de passe" onClick={() => copySecret("password")}>
                    <Copy size={14} />
                  </button>
                </div>
              )}
              {cred?.hasPrivateKey && (
                <div className="cred-line">
                  <ShieldCheck size={14} />
                  <span>Clé privée SSH enregistrée</span>
                </div>
              )}
              {cred?.hasTotp && <TotpBox key={cred.id} cred={cred} onToast={p.onToast} />}
            </div>
          )}
          {cred && (
            <div className="cred-source">
              <CredentialIcon size={13} /> Coffre : {cred.name}
            </div>
          )}
        </section>

        <section className="card wide">
          <div className="documentation-header">
            <h2>Notes</h2>
            <button className="btn" onClick={() => p.onEdit(entry, "notes")}><Pencil size={14} /> Modifier les notes</button>
          </div>
          {entry.notes ? <MarkdownNotes text={entry.notes} /> : <p className="muted">Aucune note.</p>}
        </section>
        <EntryAttachments key={entry.id} entryId={entry.id} />
      </div>
    </div>
  );
}

export function pathOf(tree: Tree, folderId: string | null): string {
  const parts: string[] = [];
  let cur = folderId;
  const byId = new Map(tree.folders.map((f) => [f.id, f]));
  for (let i = 0; cur && i < 100; i++) {
    const f = byId.get(cur);
    if (!f) break;
    parts.unshift(f.name);
    cur = f.parentId;
  }
  return parts.join(" \\ ");
}

function FolderDashboard({ folderId, p }: { folderId: string | null; p: Props }) {
  const folders = p.tree.folders.filter((f) => (f.parentId ?? null) === folderId);
  const entries = p.tree.entries.filter((e) => (e.folderId ?? null) === folderId);
  const name = folderId ? p.tree.folders.find((f) => f.id === folderId)?.name : p.vaultName;
  const byKind = p.tree.entries.reduce<Record<string, number>>((m, e) => ({ ...m, [e.kind]: (m[e.kind] ?? 0) + 1 }), {});
  return (
    <div className="dash">
      <div className="dash-header" style={{ borderLeftColor: "#e3b341" }}>
        <div className="dash-icon" style={{ background: "#e3b34122" }}>
          <FolderIcon open size={34} />
        </div>
        <div className="dash-title">
          <h1>{name}</h1>
          <div className="dash-sub">
            {folderId ? pathOf(p.tree, folderId) : "Coffre"} · {folders.length} dossier(s), {entries.length} entrée(s)
          </div>
        </div>
        <div className="dash-actions">
          {folderId&&<button className="btn" onClick={()=>p.onFolderCredentials(folderId)}><KeyRound size={14}/>Identifiants du dossier</button>}
          <button className="btn primary" onClick={p.onNewEntry}>
            <Plus size={15} /> Nouvelle entrée
          </button>
        </div>
      </div>
      {!folderId && (
        <div className="stats">
          {(Object.keys(KINDS) as (keyof typeof KINDS)[]).map((k) => (
            <div className="stat" key={k}>
              <KindIcon kind={k} size={20} />
              <div>
                <div className="stat-n">{byKind[k] ?? 0}</div>
                <div className="stat-l">{KINDS[k].label}</div>
              </div>
            </div>
          ))}
          <div className="stat">
            <CredentialIcon size={20} />
            <div>
              <div className="stat-n">{p.credentials.length}</div>
              <div className="stat-l">Identifiants</div>
            </div>
          </div>
        </div>
      )}
      <div className="tiles">
        {folders.map((f) => (
          <button key={f.id} className="tile" onDoubleClick={() => p.onSelect({ type: "folder", id: f.id })} onClick={() => p.onSelect({ type: "folder", id: f.id })}>
            <FolderIcon size={28} />
            <span className="tile-name">{f.name}</span>
            <span className="tile-sub">Dossier</span>
          </button>
        ))}
        {entries.map((e) => (
          <button key={e.id} className="tile" onClick={() => p.onSelect({ type: "entry", id: e.id })} onDoubleClick={() => p.onOpen(e)}>
            <KindIcon kind={e.kind} size={28} />
            <span className="tile-name">{e.name}</span>
            <span className="tile-sub">{endpoint(e)} <ConnectionIndicator entry={e} results={p.connectionStatus} /></span>
          </button>
        ))}
        {!folders.length && !entries.length && (
          <div className="empty-state">
            <p>Ce dossier est vide.</p>
            <button className="btn primary" onClick={p.onNewEntry}>
              <Plus size={15} /> Créer une entrée
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

export function Dashboard(p: Props) {
  if (p.selection.type === "entry") {
    const id = p.selection.id;
    const e = p.tree.entries.find((x) => x.id === id);
    if (e) return <EntryDashboard entry={e} p={p} />;
  }
  if (p.selection.type === "folder") {
    const id = p.selection.id;
    if (p.tree.folders.some((f) => f.id === id)) return <FolderDashboard folderId={id} p={p} />;
  }
  return <FolderDashboard folderId={null} p={p} />;
}
