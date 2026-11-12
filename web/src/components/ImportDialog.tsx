import { AlertTriangle, FileUp, Loader2 } from "lucide-react";
import { useState } from "react";
import { importApi } from "../api";
import { CredentialIcon, FolderIcon, KINDS, KindIcon } from "../kinds";
import type { ImportPlan, Kind } from "../types";
import { Modal } from "./Modal";

export function ImportDialog({ onDone, onClose }: { onDone: (rootId: string, msg: string) => Promise<void>; onClose: () => void }) {
  const [file, setFile] = useState<File | null>(null);
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [folder, setFolder] = useState("Import RDM");
  const [sshDefault, setSshDefault] = useState("");
  const [rdpDefault, setRdpDefault] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const pick = async (f: File | null) => {
    setFile(f);
    setPlan(null);
    setError("");
    if (!f) return;
    setBusy(true);
    try {
      const pl = await importApi.preview(f);
      setPlan(pl);
      // Suggestions : la clé SSH pour le SSH, un compte avec domaine pour le RDP.
      setSshDefault(pl.credentials.find((c) => /cl[ée]|ssh|key/i.test(c.name))?.name ?? "");
      setRdpDefault(pl.credentials.find((c) => c.domain)?.name ?? "");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const apply = async () => {
    if (!file) return;
    setBusy(true);
    setError("");
    try {
      const r = await importApi.apply(file, folder, { ssh: sshDefault, rdp: rdpDefault });
      await onDone(r.rootId, `Import terminé : ${r.entries} entrée(s), ${r.credentials} identifiant(s), ${r.folders} dossier(s)`);
    } catch (e) {
      setError((e as Error).message);
      setBusy(false);
    }
  };

  const counts = plan?.entries.reduce<Partial<Record<Kind, number>>>((m, e) => ({ ...m, [e.kind]: (m[e.kind] ?? 0) + 1 }), {}) ?? {};

  return (
    <Modal
      title="Importer depuis Remote Desktop Manager"
      icon={<FileUp size={16} />}
      onClose={onClose}
      width={900}
      height={640}
      footer={
        <>
          {error && <span className="form-error">{error}</span>}
          <div className="spacer" />
          <button className="btn" onClick={onClose}>
            Annuler
          </button>
          <button className="btn primary" onClick={apply} disabled={!plan || busy || !plan.entries.length}>
            {busy && plan ? <Loader2 size={14} className="spin" /> : null} Importer
          </button>
        </>
      }
    >
      <div className="import">
        <p className="muted" style={{ marginTop: 0 }}>
          Dans RDM : <b>Fichier → Exporter → Exporter toutes les entrées</b> au format <b>Remote Desktop Manager (.rdm)</b>, sans protection par mot de passe.
          Rien n'est créé avant d'avoir cliqué sur « Importer ».
        </p>
        <div className="field-row">
          <label className="field grow">
            <span>Fichier d'export (.rdm)</span>
            <input type="file" accept=".rdm,.xml" onChange={(e) => pick(e.target.files?.[0] ?? null)} />
          </label>
          <label className="field" style={{ width: 260 }}>
            <span>Dossier de destination (créé)</span>
            <input value={folder} onChange={(e) => setFolder(e.target.value)} />
          </label>
        </div>
        {busy && !plan && (
          <div className="muted">
            <Loader2 size={14} className="spin" /> Analyse du fichier…
          </div>
        )}
        {plan && (
          <>
            <div className="stats" style={{ marginTop: 0 }}>
              <div className="stat">
                <FolderIcon size={20} />
                <div>
                  <div className="stat-n">{plan.folders.length}</div>
                  <div className="stat-l">Dossiers</div>
                </div>
              </div>
              {(Object.keys(counts) as Kind[]).map((k) => (
                <div className="stat" key={k}>
                  <KindIcon kind={k} size={20} />
                  <div>
                    <div className="stat-n">{counts[k]}</div>
                    <div className="stat-l">{KINDS[k].label}</div>
                  </div>
                </div>
              ))}
              <div className="stat">
                <CredentialIcon size={20} />
                <div>
                  <div className="stat-n">{plan.credentials.length}</div>
                  <div className="stat-l">Identifiants</div>
                </div>
              </div>
            </div>
            {plan.credentials.length > 0 && (
              <div className="field-row">
                {(
                  [
                    ["SSH", sshDefault, setSshDefault],
                    ["RDP", rdpDefault, setRdpDefault],
                  ] as const
                ).map(([label, value, setter]) => (
                  <label className="field grow" key={label}>
                    <span>Identifiant pour les entrées {label} sans identifiant</span>
                    <select value={value} onChange={(e) => setter(e.target.value)}>
                      <option value="">Aucun</option>
                      {plan.credentials.map((c) => (
                        <option key={c.name} value={c.name}>
                          {c.name}
                        </option>
                      ))}
                    </select>
                  </label>
                ))}
              </div>
            )}
            {plan.warnings.map((w) => (
              <div key={w} className="import-warn">
                <AlertTriangle size={14} /> {w}
              </div>
            ))}
            <div className="import-table">
              <table>
                <thead>
                  <tr>
                    <th>Entrée</th>
                    <th>Dossier</th>
                    <th>Cible</th>
                    <th>Identifiant</th>
                  </tr>
                </thead>
                <tbody>
                  {plan.entries.map((e, i) => (
                    <tr key={i}>
                      <td>
                        <span className="cell-icon">
                          <KindIcon kind={e.kind} size={14} /> {e.name}
                        </span>
                      </td>
                      <td className="muted">{(e.path ?? []).join(" \\ ") || "(racine)"}</td>
                      <td className="mono small">{e.kind === "web" ? e.url : `${e.host}:${e.port}`}</td>
                      <td className="muted">{e.credential || e.username || "—"}</td>
                    </tr>
                  ))}
                  {plan.credentials.map((c, i) => (
                    <tr key={"c" + i}>
                      <td>
                        <span className="cell-icon">
                          <CredentialIcon size={14} /> {c.name}
                        </span>
                      </td>
                      <td className="muted">Coffre</td>
                      <td className="mono small">{c.domain ? `${c.domain}\\${c.username}` : c.username}</td>
                      <td className="muted">{c.hasPassword ? "mot de passe importé" : c.encryptedSecret ? "mot de passe à ressaisir" : "—"}</td>
                    </tr>
                  ))}
                  {plan.skipped.map((s, i) => (
                    <tr key={"s" + i} className="skipped">
                      <td>{s.name}</td>
                      <td className="muted">{s.type}</td>
                      <td colSpan={2} className="muted">
                        Ignorée : {s.reason}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>
    </Modal>
  );
}
