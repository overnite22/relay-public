import {
  AlertTriangle,
  Ban,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Clock,
  Copy,
  History,
  KeyRound,
  Loader2,
  Play,
  RotateCcw,
  Save,
  Square,
  Trash2,
  XCircle,
  Zap,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { commandsApi } from "../api";
import { FolderIcon, KindIcon } from "../kinds";
import type { CommandRun, Entry, RunResult, RunStatus, SavedCommand, SessionTab, Tree } from "../types";
import { copyText, pathOf } from "./Dashboard";
import { Prompt } from "./Modal";

const STATUS: Record<RunStatus, { label: string; Icon: typeof Clock; color: string }> = {
  pending: { label: "En attente", Icon: Clock, color: "var(--text-muted)" },
  running: { label: "En cours", Icon: Loader2, color: "var(--accent-hover)" },
  ok: { label: "Réussi", Icon: CheckCircle2, color: "var(--ok)" },
  failed: { label: "Échec", Icon: XCircle, color: "#f0883e" },
  error: { label: "Erreur", Icon: AlertTriangle, color: "var(--danger)" },
  canceled: { label: "Arrêté", Icon: Ban, color: "var(--text-muted)" },
};

const fmtMs = (ms: number) => (ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`);

interface FolderNode {
  id: string | null;
  name: string;
  folders: FolderNode[];
  entries: Entry[];
}

/** Arbre des seuls dossiers contenant des entrées SSH. */
function buildTargetTree(tree: Tree): FolderNode {
  const root: FolderNode = { id: null, name: "", folders: [], entries: [] };
  const nodes = new Map<string | null, FolderNode>([[null, root]]);
  for (const f of tree.folders) nodes.set(f.id, { id: f.id, name: f.name, folders: [], entries: [] });
  for (const f of tree.folders) nodes.get(f.parentId ?? null)?.folders.push(nodes.get(f.id)!);
  for (const e of tree.entries) if (e.kind === "ssh") (nodes.get(e.folderId ?? null) ?? root).entries.push(e);
  const prune = (n: FolderNode): boolean => {
    n.folders = n.folders.filter(prune).sort((a, b) => a.name.localeCompare(b.name, "fr"));
    n.entries.sort((a, b) => a.name.localeCompare(b.name, "fr", { numeric: true }));
    return n.entries.length > 0 || n.folders.length > 0;
  };
  prune(root);
  return root;
}

const allEntries = (n: FolderNode): Entry[] => [...n.entries, ...n.folders.flatMap(allEntries)];

export function CommandView({ tab, active, tree, onToast }: { tab: SessionTab; active: boolean; tree: Tree; onToast: (m: string) => void }) {
  const [selected, setSelected] = useState<Set<string>>(() => new Set(tab.targets ?? []));
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [filter, setFilter] = useState("");
  const [command, setCommand] = useState("");
  const [sudo, setSudo] = useState(false);
  const [timeoutSec, setTimeoutSec] = useState(60);
  const [concurrency, setConcurrency] = useState(10);
  const [run, setRun] = useState<CommandRun | null>(null);
  const [current, setCurrent] = useState<string | null>(null);
  const [saved, setSaved] = useState<SavedCommand[]>([]);
  const [history, setHistory] = useState<CommandRun[] | null>(null);
  const [savePrompt, setSavePrompt] = useState(false);
  const [starting, setStarting] = useState(false);
  const generation = useRef(0);
  const launching = useRef(false);
  const [error, setError] = useState("");
  const poll = useRef(0);

  const targetTree = useMemo(() => buildTargetTree(tree), [tree]);
  const sshById = useMemo(() => new Map(tree.entries.filter((e) => e.kind === "ssh").map((e) => [e.id, e])), [tree]);
  const running = starting || (!!run && !run.finishedAt);

  useEffect(() => {
    commandsApi.saved().then(setSaved).catch(() => {});
    return () => { generation.current++; window.clearTimeout(poll.current); };
  }, []);

  const follow = useCallback((id: string) => {
    window.clearTimeout(poll.current);
    const token = ++generation.current;
    const tick = async () => {
      try {
        const r = await commandsApi.get(id);
        if (token !== generation.current) return;
        setRun(r);
        setError("");
        if (!r.finishedAt) poll.current = window.setTimeout(tick, 700);
        else onToast(`Commande terminée : ${r.ok} réussie(s), ${r.failed} en échec`);
      } catch (e) {
        if (token !== generation.current) return;
        setError((e as Error).message);
        poll.current = window.setTimeout(tick, 3000);
      }
    };
    tick();
  }, [onToast]);

  const execute = async (ids: string[], text = command, useSudo = sudo) => {
    if (launching.current || running) return;
    setError("");
    if (!text.trim()) return setError("Saisissez une commande.");
    if (!ids.length) return setError("Cochez au moins un serveur dans la liste des cibles.");
    launching.current = true;
    setStarting(true);
    try {
      const r = await commandsApi.run({ entryIds: ids, command: text, sudo: useSudo, timeoutSec, concurrency });
      setRun(r);
      setCurrent(r.results?.[0]?.entryId ?? null);
      follow(r.id);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      launching.current = false;
      setStarting(false);
    }
  };

  const toggle = (ids: string[], on: boolean) =>
    setSelected((cur) => {
      const n = new Set(cur);
      ids.forEach((id) => (on ? n.add(id) : n.delete(id)));
      return n;
    });

  const q = filter.trim().toLowerCase();
  const match = (e: Entry) => !q || e.name.toLowerCase().includes(q) || e.host.toLowerCase().includes(q);

  const renderFolder = (n: FolderNode, depth: number): React.ReactNode => {
    const ids = allEntries(n).filter(match).map((e) => e.id);
    if (!ids.length) return null;
    const count = ids.filter((id) => selected.has(id)).length;
    const key = n.id ?? "__root";
    const open = !collapsed.has(key) || !!q;
    return (
      <div key={key}>
        {n.id !== null && (
          <div className="tree-row folder" style={{ paddingLeft: 6 + depth * 14 }}>
            <span
              className="tree-chevron"
              onClick={() =>
                setCollapsed((c) => {
                  const s = new Set(c);
                  if (s.has(key)) s.delete(key);
                  else s.add(key);
                  return s;
                })
              }
            >
              {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
            </span>
            <input
              type="checkbox"
              checked={count === ids.length}
              ref={(el) => {
                if (el) el.indeterminate = count > 0 && count < ids.length;
              }}
              onChange={(e) => toggle(ids, e.target.checked)}
            />
            <FolderIcon open={open} />
            <span className="tree-label">{n.name}</span>
            <span className="tree-count">
              {count}/{ids.length}
            </span>
          </div>
        )}
        {open && (
          <>
            {n.folders.map((f) => renderFolder(f, n.id === null ? depth : depth + 1))}
            {n.entries.filter(match).map((e) => (
              <label key={e.id} className="tree-row" style={{ paddingLeft: 6 + (n.id === null ? depth : depth + 1) * 14 + 14 }} title={e.host}>
                <input type="checkbox" checked={selected.has(e.id)} onChange={(ev) => toggle([e.id], ev.target.checked)} />
                <KindIcon kind="ssh" />
                <span className="tree-label">{e.name}</span>
                <span className="tree-sub">{e.host}</span>
              </label>
            ))}
          </>
        )}
      </div>
    );
  };

  const results: RunResult[] = run?.results ?? [];
  const cur = results.find((r) => r.entryId === current) ?? results[0];
  const done = results.filter((r) => !["pending", "running"].includes(r.status)).length;
  const failedIds = results.filter((r) => r.status !== "ok").map((r) => r.entryId);

  const copyAll = () =>
    copyText(
      results
        .map((r) => `### ${r.name} (${r.address}) — ${STATUS[r.status].label}${r.exitCode != null ? `, code ${r.exitCode}` : ""}\n${r.error || r.output}`)
        .join("\n\n"),
      onToast,
      "Sorties",
    );

  return (
    <div className="cmd" style={{ display: active ? "flex" : "none" }}>
      <div className="cmd-toolbar">
        <button className="tb primary-tb" onClick={() => execute([...selected])} disabled={running} title="Exécuter (Ctrl+Entrée)">
          <Play size={15} /> Exécuter
        </button>
        <button className="tb" onClick={() => run && commandsApi.cancel(run.id).catch((e) => setError(e.message))} disabled={!running}>
          <Square size={14} /> Arrêter
        </button>
        <button className="tb" onClick={() => run && execute(failedIds, run.command, run.sudo)} disabled={running || !run || !failedIds.length} title="Relancer sur les serveurs en échec">
          <RotateCcw size={14} /> Relancer les échecs
        </button>
        <span className="tb-sep" />
        <select
          className="tb-select"
          value=""
          onChange={(e) => {
            const c = saved.find((x) => x.id === e.target.value);
            if (c) {
              setCommand(c.command);
              setSudo(c.sudo);
            }
          }}
          title="Commandes enregistrées"
        >
          <option value="">Bibliothèque ({saved.length})…</option>
          {saved.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
        <button className="tb" onClick={() => command.trim() && setSavePrompt(true)} disabled={!command.trim()} title="Enregistrer dans la bibliothèque">
          <Save size={14} />
        </button>
        {saved.some((c) => c.command === command) && (
          <button
            className="tb"
            title="Retirer de la bibliothèque"
            onClick={async () => {
              const c = saved.find((x) => x.command === command)!;
              await commandsApi.removeSaved(c.id);
              setSaved(await commandsApi.saved());
            }}
          >
            <Trash2 size={14} />
          </button>
        )}
        <span className="tb-sep" />
        <label className="tb-check" title="Exécuter avec sudo, avec le mot de passe de l'identifiant">
          <input type="checkbox" checked={sudo} onChange={(e) => setSudo(e.target.checked)} /> sudo
        </label>
        <label className="tb-check">
          Délai
          <select className="tb-select" value={timeoutSec} onChange={(e) => setTimeoutSec(Number(e.target.value))}>
            {[15, 60, 300, 900, 3600].map((s) => (
              <option key={s} value={s}>
                {s < 60 ? `${s} s` : `${s / 60} min`}
              </option>
            ))}
          </select>
        </label>
        <label className="tb-check">
          En parallèle
          <select className="tb-select" value={concurrency} onChange={(e) => setConcurrency(Number(e.target.value))}>
            {[1, 5, 10, 20, 50].map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        <div className="spacer" />
        <div className="tb-menu">
          <button className="tb" disabled={running} onClick={() => (history ? setHistory(null) : commandsApi.history().then(setHistory).catch((e) => setError(e.message)))}>
            <History size={14} /> Historique
          </button>
          {history && (
            <div className="ctx-menu tb-dropdown">
              {!history.length && <div className="tree-empty">Aucune exécution</div>}
              {history.map((h) => (
                <button
                  key={h.id}
                  onClick={async () => {
                    setHistory(null);
                    window.clearTimeout(poll.current);
                    generation.current++;
                    const r = await commandsApi.get(h.id);
                    setRun(r);
                    setCommand(r.command);
                    setSudo(r.sudo);
                    setSelected(new Set((r.results ?? []).map((x) => x.entryId)));
                    if (!r.finishedAt) follow(r.id);
                  }}
                >
                  <span className="muted small nowrap">{new Date(h.at).toLocaleString("fr-FR", { dateStyle: "short", timeStyle: "short" })}</span>
                  <span className="mono hist-cmd">{h.command}</span>
                  <span className="small nowrap" style={{ color: h.failed ? "#f0883e" : "var(--ok)" }}>
                    {h.ok}/{h.targets}
                  </span>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      <div className="cmd-body">
        <aside className="cmd-targets">
          <div className="cmd-pane-title">
            <span>Cibles SSH</span>
            <span className="muted small">{selected.size} sélectionnée(s)</span>
          </div>
          <div className="nav-search" style={{ margin: 6 }}>
            <input placeholder="Filtrer…" value={filter} onChange={(e) => setFilter(e.target.value)} />
          </div>
          <div className="cmd-targets-actions">
            <button className="link-btn small" onClick={() => toggle(allEntries(targetTree).filter(match).map((e) => e.id), true)}>
              Tout cocher
            </button>
            <button className="link-btn small" onClick={() => setSelected(new Set())}>
              Tout décocher
            </button>
          </div>
          <div className="tree">{sshById.size ? renderFolder(targetTree, 0) : <div className="tree-empty">Aucune entrée SSH.</div>}</div>
        </aside>

        <main className="cmd-main">
          <div className="cmd-editor">
            <div className="cmd-pane-title">
              <span>
                <Zap size={13} color="#e3b341" /> Commande
              </span>
              <span className="muted small">Ctrl+Entrée pour exécuter · interprétée par le shell distant</span>
            </div>
            <textarea
              className="mono"
              value={command}
              spellCheck={false}
              placeholder={"ex. uptime\napt list --upgradable 2>/dev/null | tail -n +2 | wc -l\ndf -h /"}
              onChange={(e) => setCommand(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.ctrlKey || e.metaKey) && !running) {
                  e.preventDefault();
                  execute([...selected]);
                }
              }}
            />
            {error && <div className="form-error small" style={{ padding: "4px 8px" }}>{error}</div>}
          </div>

          <div className="cmd-results">
            <div className="cmd-pane-title">
              <span>Résultats</span>
              {run && (
                <span className="muted small">
                  {done}/{results.length} terminé(s) · {results.filter((r) => r.status === "ok").length} réussi(s) ·{" "}
                  {results.filter((r) => ["failed", "error"].includes(r.status)).length} en échec
                  {running && <Loader2 size={12} className="spin" style={{ marginLeft: 6, verticalAlign: -2 }} />}
                </span>
              )}
              <div className="spacer" />
              {run && (
                <button className="icon-btn" title="Copier toutes les sorties" onClick={copyAll}>
                  <Copy size={14} />
                </button>
              )}
            </div>
            {run ? (
              <div className="cmd-grid">
                <table>
                  <thead>
                    <tr>
                      <th style={{ width: 110 }}>État</th>
                      <th>Serveur</th>
                      <th>Adresse</th>
                      <th style={{ width: 60 }}>Code</th>
                      <th style={{ width: 80 }}>Durée</th>
                      <th>Détail</th>
                    </tr>
                  </thead>
                  <tbody>
                    {results.map((r) => {
                      const s = STATUS[r.status];
                      const e = sshById.get(r.entryId);
                      return (
                        <tr key={r.entryId} className={cur?.entryId === r.entryId ? "selected" : ""} onClick={() => setCurrent(r.entryId)}>
                          <td>
                            <span className="cell-icon" style={{ color: s.color }}>
                              <s.Icon size={14} className={r.status === "running" ? "spin" : ""} /> {s.label}
                            </span>
                          </td>
                          <td>
                            {r.name}
                            {e && <span className="muted small"> · {pathOf(tree, e.folderId)}</span>}
                          </td>
                          <td className="mono small">{r.address}</td>
                          <td className="mono">{r.exitCode ?? ""}</td>
                          <td className="muted small">{r.durationMs ? fmtMs(r.durationMs) : ""}</td>
                          <td className="small" style={{ color: r.error ? "var(--danger)" : "var(--text-muted)" }}>
                            {r.error || firstLine(r.output)}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className="empty-state">
                <p>Cochez des serveurs, saisissez une commande et cliquez sur « Exécuter ».</p>
              </div>
            )}
          </div>

          {cur && (
            <div className="cmd-output">
              <div className="cmd-pane-title">
                <span>
                  Sortie : <b>{cur.name}</b>
                  {cur.exitCode != null && <span className="muted"> (code {cur.exitCode})</span>}
                </span>
                <div className="spacer" />
                {cur.newHostKey && (
                  <span className="muted small" title={cur.fingerprint}>
                    <KeyRound size={12} /> nouvelle clé d'hôte mémorisée
                  </span>
                )}
                {cur.error.includes("clé d'hôte modifiée") && (
                  <button
                    className="btn small danger"
                    title="À faire seulement si le serveur a été réinstallé ou sa clé régénérée"
                    onClick={async () => {
                      await commandsApi.forgetHost(cur.address);
                      onToast("Clé oubliée : la prochaine connexion mémorisera la nouvelle");
                    }}
                  >
                    <KeyRound size={12} /> Oublier la clé
                  </button>
                )}
                <button className="icon-btn" title="Copier la sortie" onClick={() => copyText(cur.output, onToast, "Sortie")}>
                  <Copy size={14} />
                </button>
              </div>
              <pre className="mono">
                {cur.error ? `⚠ ${cur.error}\n` : ""}
                {cur.output}
                {cur.truncated ? "\n… (sortie tronquée à 512 Ko)" : ""}
                {!cur.output && !cur.error && (cur.status === "ok" ? "(aucune sortie)" : "")}
              </pre>
            </div>
          )}
        </main>
      </div>
      {savePrompt && (
        <Prompt
          title="Enregistrer la commande"
          label="Nom dans la bibliothèque"
          initial={command.split("\n")[0].slice(0, 40)}
          onClose={() => setSavePrompt(false)}
          onSubmit={async (name) => {
            try {
              await commandsApi.save({ name, command, sudo });
              setSaved(await commandsApi.saved());
              setSavePrompt(false);
              onToast("Commande enregistrée");
            } catch (e) {
              onToast((e as Error).message);
            }
          }}
        />
      )}
    </div>
  );
}

function firstLine(s: string) {
  const l = s.trim().split("\n")[0] ?? "";
  return l.length > 90 ? l.slice(0, 90) + "…" : l;
}
