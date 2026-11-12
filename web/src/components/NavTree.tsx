import { ChevronDown, ChevronRight, Database, Star } from "lucide-react";
import { useMemo, useState } from "react";
import { endpoint, FolderIcon, KindIcon } from "../kinds";
import { ConnectionIndicator } from "./ConnectionIndicator";
import type { ConnectionTest, Entry, Folder, Selection, Tree } from "../types";

interface Props {
  tree: Tree;
  connectionStatus: Record<string, ConnectionTest>;
  vaultName: string;
  selection: Selection;
  expanded: Set<string>;
  search: string;
  mode: "nav" | "fav" | "recent";
  recent: string[];
  onSelect: (s: Selection) => void;
  onToggle: (folderId: string) => void;
  onOpen: (e: Entry) => void;
  onContextMenu: (s: Selection, x: number, y: number) => void;
  onMove: (item: { type: "entry" | "folder"; id: string }, folderId: string | null) => void;
}

type Drag = { type: "entry" | "folder"; id: string };

export function NavTree(p: Props) {
  const [dropTarget, setDropTarget] = useState<string | null>(null);

  const children = useMemo(() => {
    const folders = new Map<string | null, Folder[]>();
    const entries = new Map<string | null, Entry[]>();
    for (const f of p.tree.folders) {
      const k = f.parentId ?? null;
      folders.set(k, [...(folders.get(k) ?? []), f]);
    }
    for (const e of p.tree.entries) {
      const k = e.folderId ?? null;
      entries.set(k, [...(entries.get(k) ?? []), e]);
    }
    return { folders, entries };
  }, [p.tree]);

  const isSel = (s: Selection) =>
    s.type === p.selection.type && (s.type === "root" || (p.selection.type !== "root" && s.id === p.selection.id));

  const dragProps = (item: Drag) => ({
    draggable: true,
    onDragStart: (ev: React.DragEvent) => {
      ev.dataTransfer.setData("application/x-relay", JSON.stringify(item));
      ev.dataTransfer.effectAllowed = "move";
    },
  });

  const dropProps = (folderId: string | null) => ({
    onDragOver: (ev: React.DragEvent) => {
      if (!ev.dataTransfer.types.includes("application/x-relay")) return;
      ev.preventDefault();
      setDropTarget(folderId ?? "__root");
    },
    onDragLeave: () => setDropTarget(null),
    onDrop: (ev: React.DragEvent) => {
      ev.preventDefault();
      setDropTarget(null);
      try {
        const item = JSON.parse(ev.dataTransfer.getData("application/x-relay")) as Drag;
        if (item.type === "folder" && item.id === folderId) return;
        p.onMove(item, folderId);
      } catch {
        /* glisser externe */
      }
    },
  });

  const entryRow = (e: Entry, depth: number, showPath = false) => {
    const sel: Selection = { type: "entry", id: e.id };
    return (
      <div
        key={e.id}
        className={`tree-row ${isSel(sel) ? "selected" : ""}`}
        style={{ paddingLeft: 8 + depth * 16 + 16 }}
        onClick={() => p.onSelect(sel)}
        onDoubleClick={() => p.onOpen(e)}
        onContextMenu={(ev) => {
          ev.preventDefault();
          p.onSelect(sel);
          p.onContextMenu(sel, ev.clientX, ev.clientY);
        }}
        title={endpoint(e)}
        {...dragProps({ type: "entry", id: e.id })}
      >
        <KindIcon kind={e.kind} />
        <span className="tree-label">{e.name}</span>
        {showPath && <span className="tree-sub">{endpoint(e)}</span>}
        {e.favorite && p.mode === "nav" && <Star size={12} className="tree-fav" />}
        <ConnectionIndicator entry={e} results={p.connectionStatus} />
      </div>
    );
  };

  const folderRows = (parent: string | null, depth: number): React.ReactNode[] => {
    const out: React.ReactNode[] = [];
    for (const f of children.folders.get(parent) ?? []) {
      const open = p.expanded.has(f.id);
      const sel: Selection = { type: "folder", id: f.id };
      const count = countEntries(f.id);
      out.push(
        <div
          key={f.id}
          className={`tree-row folder ${isSel(sel) ? "selected" : ""} ${dropTarget === f.id ? "drop" : ""}`}
          style={{ paddingLeft: 8 + depth * 16 }}
          onClick={() => p.onSelect(sel)}
          onDoubleClick={() => p.onToggle(f.id)}
          onContextMenu={(ev) => {
            ev.preventDefault();
            p.onSelect(sel);
            p.onContextMenu(sel, ev.clientX, ev.clientY);
          }}
          {...dragProps({ type: "folder", id: f.id })}
          {...dropProps(f.id)}
        >
          <span
            className="tree-chevron"
            onClick={(ev) => {
              ev.stopPropagation();
              p.onToggle(f.id);
            }}
          >
            {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
          </span>
          <FolderIcon open={open} />
          <span className="tree-label">{f.name}</span>
          <span className="tree-count">{count}</span>
        </div>,
      );
      if (open) {
        out.push(...folderRows(f.id, depth + 1));
        for (const e of children.entries.get(f.id) ?? []) out.push(entryRow(e, depth + 1));
      }
    }
    return out;
  };

  const countEntries = (folderId: string): number =>
    (children.entries.get(folderId)?.length ?? 0) +
    (children.folders.get(folderId) ?? []).reduce((n, f) => n + countEntries(f.id), 0);

  const q = p.search.trim().toLowerCase();
  let body: React.ReactNode;
  if (q) {
    const hits = p.tree.entries.filter((e) =>
      [e.name, e.host, e.url, e.username, e.notes].some((v) => v?.toLowerCase().includes(q)),
    );
    body = hits.length ? hits.map((e) => entryRow(e, 0, true)) : <div className="tree-empty">Aucun résultat</div>;
  } else if (p.mode === "fav") {
    const favs = p.tree.entries.filter((e) => e.favorite);
    body = favs.length ? (
      favs.map((e) => entryRow(e, 0, true))
    ) : (
      <div className="tree-empty">Aucun favori. Ajoutez-en depuis le ruban ou le tableau de bord.</div>
    );
  } else if (p.mode === "recent") {
    const byId = new Map(p.tree.entries.map((e) => [e.id, e]));
    const rec = p.recent.map((id) => byId.get(id)).filter((e): e is Entry => !!e);
    body = rec.length ? rec.map((e) => entryRow(e, 0, true)) : <div className="tree-empty">Aucune session récente.</div>;
  } else {
    const rootSel: Selection = { type: "root" };
    body = (
      <>
        <div
          className={`tree-row root ${isSel(rootSel) ? "selected" : ""} ${dropTarget === "__root" ? "drop" : ""}`}
          onClick={() => p.onSelect(rootSel)}
          onContextMenu={(ev) => {
            ev.preventDefault();
            p.onSelect(rootSel);
            p.onContextMenu(rootSel, ev.clientX, ev.clientY);
          }}
          {...dropProps(null)}
        >
          <Database size={16} color="#58a6ff" />
          <span className="tree-label">{p.vaultName}</span>
          <span className="tree-count">{p.tree.entries.length}</span>
        </div>
        {folderRows(null, 1)}
        {(children.entries.get(null) ?? []).map((e) => entryRow(e, 1))}
      </>
    );
  }
  return <div className="tree">{body}</div>;
}
