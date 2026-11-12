import { Activity, Ban, Loader2, Play, RefreshCw, Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import { KindIcon, KINDS } from "../kinds";
import type { ConnectionTest, Entry, SessionTab, Tree } from "../types";
import { pathOf } from "./Dashboard";
import { canTestConnection, connectionAddress, ConnectionIndicator, connectionStatusLabels } from "./ConnectionIndicator";

type StateFilter = "all" | "reachable" | "failed" | "unknown";
type SortColumn = "state" | "name" | "folder" | "address";

function statusOf(result: ConnectionTest | undefined): Exclude<StateFilter, "all"> {
  if (!result) return "unknown";
  return result.status === "reachable" ? "reachable" : "failed";
}

const stateOrder = { failed: 0, unknown: 1, reachable: 2 };

export function AvailabilityView({ tab, active, tree, results, onResult, onOpen, onTest }: {
  tab: SessionTab;
  active: boolean;
  tree: Tree;
  results: Record<string, ConnectionTest>;
  onResult: (result: ConnectionTest) => void;
  onOpen: (entry: Entry) => void;
  onTest: (entry: Entry) => void;
}) {
  const [folderId, setFolderId] = useState(tab.scopeFolderId ?? "");
  const [protocol, setProtocol] = useState("");
  const [filter, setFilter] = useState<StateFilter>("all");
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [sort, setSort] = useState<{ column: SortColumn; ascending: boolean }>({ column: "state", ascending: true });
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);
  const [error, setError] = useState("");
  const pending = useRef<AbortController | null>(null);

  useEffect(() => {
    setFolderId(tab.scopeFolderId ?? "");
  }, [tab]);
  useEffect(() => () => pending.current?.abort(), []);

  const folders = useMemo(() => tree.folders.map((folder) => ({ id: folder.id, path: pathOf(tree, folder.id) }))
    .sort((a, b) => a.path.localeCompare(b.path, "fr")), [tree]);

  // Le périmètre d'un dossier inclut ses sous-dossiers.
  const scope = useMemo(() => {
    if (!folderId) return null;
    const ids = new Set([folderId]);
    let grew = true;
    while (grew) {
      grew = false;
      for (const folder of tree.folders) {
        if (folder.parentId && ids.has(folder.parentId) && !ids.has(folder.id)) {
          ids.add(folder.id);
          grew = true;
        }
      }
    }
    return ids;
  }, [tree, folderId]);

  const query = search.trim().toLocaleLowerCase("fr");
  const entries = tree.entries.filter((entry) => canTestConnection(entry)
    && (!scope || (entry.folderId !== null && scope.has(entry.folderId)))
    && (!protocol || entry.kind === protocol)
    && (!query || [entry.name, entry.host].some((value) => value.toLocaleLowerCase("fr").includes(query))));
  const counts = { all: entries.length, failed: 0, unknown: 0, reachable: 0 };
  for (const entry of entries) counts[statusOf(results[connectionAddress(entry)])]++;

  const rows = entries.filter((entry) => filter === "all" || statusOf(results[connectionAddress(entry)]) === filter);
  rows.sort((a, b) => {
    let comparison: number;
    switch (sort.column) {
      case "name":
        comparison = a.name.localeCompare(b.name, "fr", { numeric: true });
        break;
      case "folder":
        comparison = pathOf(tree, a.folderId).localeCompare(pathOf(tree, b.folderId), "fr");
        break;
      case "address":
        comparison = connectionAddress(a).localeCompare(connectionAddress(b), "fr", { numeric: true });
        break;
      default:
        comparison = stateOrder[statusOf(results[connectionAddress(a)])] - stateOrder[statusOf(results[connectionAddress(b)])];
    }
    if (comparison === 0) comparison = a.name.localeCompare(b.name, "fr", { numeric: true });
    return sort.ascending ? comparison : -comparison;
  });
  const current = rows.find((entry) => entry.id === selected);
  const running = progress !== null;

  const retest = async () => {
    if (pending.current || !rows.length) return;
    // Plusieurs fiches peuvent viser le même port, qui est testé une seule fois.
    const targets = [...new Map(rows.map((entry) => [connectionAddress(entry), entry])).values()];
    const controller = new AbortController();
    pending.current = controller;
    setError("");
    setProgress({ done: 0, total: targets.length });
    let next = 0;
    let failures = 0;
    const worker = async () => {
      while (!controller.signal.aborted && next < targets.length) {
        const target = targets[next++];
        try {
          const result = await api.testConnection(target.id, controller.signal);
          if (!controller.signal.aborted) onResult(result);
        } catch {
          if (!controller.signal.aborted) failures++;
        } finally {
          if (!controller.signal.aborted) setProgress((current) => current ? { ...current, done: current.done + 1 } : null);
        }
      }
    };
    try {
      await Promise.all(Array.from({ length: Math.min(6, targets.length) }, worker));
      if (!controller.signal.aborted && failures) {
        setError(`${failures} test(s) n’ont pas pu être effectués. Réessayez.`);
      }
    } finally {
      if (pending.current === controller) {
        pending.current = null;
        setProgress(null);
      }
    }
  };

  const changeSort = (column: SortColumn) => setSort((current) => ({
    column, ascending: current.column === column ? !current.ascending : true,
  }));
  const sortHeader = (column: SortColumn, label: string) => (
    <th aria-sort={sort.column === column ? (sort.ascending ? "ascending" : "descending") : "none"}>
      <button type="button" onClick={() => changeSort(column)}>{label}{sort.column === column ? (sort.ascending ? " ↑" : " ↓") : ""}</button>
    </th>
  );

  return (
    <div className="availability" style={{ display: active ? "flex" : "none" }}>
      <div className="cmd-toolbar">
        <Activity size={16} />
        <label className="tb-check">Dossier
          <select className="tb-select availability-folder" value={folderId} disabled={running}
            onChange={(event) => setFolderId(event.target.value)} aria-label="Dossier à surveiller">
            <option value="">Tous les dossiers</option>
            {folderId && !folders.some((folder) => folder.id === folderId) && <option value={folderId}>Dossier supprimé</option>}
            {folders.map((folder) => <option key={folder.id} value={folder.id}>{folder.path}</option>)}
          </select>
        </label>
        <label className="tb-check">Protocole
          <select className="tb-select" value={protocol} disabled={running} onChange={(event) => setProtocol(event.target.value)}
            aria-label="Protocole à surveiller">
            <option value="">Tous</option>
            {(["ssh", "rdp", "vnc"] as const).map((kind) => <option key={kind} value={kind}>{KINDS[kind].label}</option>)}
          </select>
        </label>
        <div className="nav-search availability-search"><Search size={14} />
          <input placeholder="Filtrer par nom ou hôte…" value={search} disabled={running}
            onChange={(event) => setSearch(event.target.value)} aria-label="Filtrer les entrées" />
        </div>
        <button type="button" className="tb" disabled={running || !rows.length} onClick={retest}>
          <RefreshCw size={14} /> Retester les entrées affichées
        </button>
        {running && <button type="button" className="tb" onClick={() => pending.current?.abort()}><Ban size={14} /> Arrêter</button>}
        <div className="spacer" />
        <button type="button" className="tb" disabled={!current} onClick={() => current && onTest(current)}>Détail du test</button>
        <button type="button" className="tb" disabled={!current} onClick={() => current && onOpen(current)}><Play size={14} /> Ouvrir</button>
      </div>

      <div className="availability-summary" role="group" aria-label="Filtrer par disponibilité">
        {([
          ["all", "Toutes"], ["failed", "Inaccessibles"], ["reachable", "Accessibles"], ["unknown", "En attente"],
        ] as [StateFilter, string][]).map(([state, label]) => (
          <button type="button" key={state} className={`availability-count ${state} ${filter === state ? "selected" : ""}`}
            disabled={running} aria-pressed={filter === state} onClick={() => setFilter(state)}>
            <span>{label}</span><strong>{counts[state]}</strong>
          </button>
        ))}
      </div>
      <div className="availability-note">
        Ports TCP vérifiés depuis Relay toutes les minutes.
        {progress && <span role="status"><Loader2 size={13} className="spin" /> {progress.done}/{progress.total} adresses testées</span>}
      </div>
      {error && <p className="form-error availability-error" role="alert">{error}</p>}

      <div className="cmd-grid availability-grid">
        <table aria-label="Disponibilité des entrées">
          <thead><tr>
            {sortHeader("state", "État")}
            {sortHeader("name", "Entrée")}
            <th>Protocole</th>
            {sortHeader("folder", "Dossier")}
            {sortHeader("address", "Adresse")}
            <th>IP contactée</th><th>Durée</th><th>Dernier test</th>
          </tr></thead>
          <tbody>
            {rows.map((entry) => {
              const result = results[connectionAddress(entry)];
              return (
                <tr key={entry.id} className={selected === entry.id ? "selected" : ""}
                  aria-selected={selected === entry.id} tabIndex={0} onClick={() => setSelected(entry.id)}
                  onDoubleClick={() => onOpen(entry)} onKeyDown={(event) => {
                    if (event.key === "Enter") onOpen(entry);
                    else if (event.key === " ") { event.preventDefault(); setSelected(entry.id); }
                  }}>
                  <td><span className="availability-state"><ConnectionIndicator entry={entry} results={results} />
                    {result ? connectionStatusLabels[result.status] : "En attente"}</span></td>
                  <td><span className="cell-icon"><KindIcon kind={entry.kind} />{entry.name}</span></td>
                  <td>{KINDS[entry.kind].label}</td>
                  <td>{pathOf(tree, entry.folderId) || "Racine"}</td>
                  <td className="mono">{connectionAddress(entry)}</td>
                  <td className="mono">{result?.ip ?? "—"}</td>
                  <td>{result ? `${result.durationMs || "< 1"} ms` : "—"}</td>
                  <td>{result ? new Date(result.checkedAt).toLocaleTimeString("fr-FR") : "—"}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {!rows.length && <div className="tree-empty">{entries.length ? "Aucune entrée pour cet état." : "Aucune entrée dans ce périmètre."}</div>}
      </div>
    </div>
  );
}
