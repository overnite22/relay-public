import {
  Activity,
  ChevronsDownUp,
  ChevronsUpDown,
  Copy,
  CopyX,
  CircleDot,
  Film,
  Smartphone,
  ExternalLink,
  FileUp,
  FolderPlus,
  FolderOpen,
  HardDrive,
  History,
  Info,
  KeyRound,
  LayoutDashboard,
  Moon,
  Pencil,
  Play,
  PlugZap,
  Plus,
  RefreshCw,
  Search,
  Star,
  Sun,
  Trash2,
  X,
  Zap,
 Users,
 LogOut,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { api, entryInput, localAuthApi } from "./api";
import { AvailabilityView } from "./components/AvailabilityView";
import {UsersDialog,PasswordDialog} from "./components/UsersDialog";
import { BackupDialog } from "./components/BackupDialog";
import { WorkspacesDialog } from "./components/WorkspacesDialog";
import { FolderCredentialsDialog } from "./components/FolderCredentialsDialog";
import { InstallPWA } from "./components/InstallPWA";
import { AuditDialog } from "./components/AuditDialog";
import { DevicesDialog } from "./components/DevicesDialog";
import { canTestConnection } from "./components/ConnectionIndicator";
import { ConnectionTestDialog } from "./components/ConnectionTestDialog";
import { CommandView } from "./components/CommandView";
import { CredentialsDialog } from "./components/CredentialsDialog";
import { Dashboard, pathOf } from "./components/Dashboard";
import { DuplicatesDialog } from "./components/DuplicatesDialog";
import { EntryDialog, KindPicker } from "./components/EntryDialog";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { ImportDialog } from "./components/ImportDialog";
import { PveSessionView } from "./components/PveSessionView";
import { SourcesDialog } from "./components/SourcesDialog";
import { Confirm, Modal, Prompt } from "./components/Modal";
import { NavTree } from "./components/NavTree";
import { Ribbon, type RibbonTab } from "./components/Ribbon";
import { RecordingsDialog } from "./components/RecordingsDialog";
import { QuickConnectDialog } from "./components/QuickConnectDialog";
import { SessionView } from "./components/SessionView";
import { FOLDER_COLOR, KINDS, KindIcon } from "./kinds";
import type { ConnectionTest, Credential, Entry, EntryInput, Kind, QuickTarget, Selection, SessionTab, Tree, Workspace } from "./types";

const VAULT_NAME = "Homelab";
const canRecord = (entry: Entry) => ["ssh", "rdp", "vnc"].includes(entry.kind);

type Dialog =
  | { type: "kind"; folderId: string | null }
  | { type: "entry"; initial: EntryInput; id?: string; section?: "notes" }
  | { type: "folder"; parentId: string | null; id?: string; name?: string }
  | { type: "credentials" }
  | { type: "audit" }
  | { type: "backups" }
 | {type:"users"}
 | {type:"password"}
  | { type: "workspaces" }
  | { type: "folder-credentials"; id:string }
  | { type: "recordings" }
  | { type: "devices" }
  | { type: "connection-test"; entry: Entry }
  | { type: "quick"; initial?: QuickTarget; replaceKey?: string }
  | { type: "sources" }
  | { type: "duplicates" }
  | { type: "import" }
  | { type: "about" }
  | { type: "confirm"; title: string; message: React.ReactNode; action: () => void; label: string };

interface Menu {
  x: number;
  y: number;
  sel: Selection;
}

function loadSet(key: string): Set<string> {
  try {
    return new Set(JSON.parse(localStorage.getItem(key) ?? "[]"));
  } catch {
    return new Set();
  }
}

export default function App() {
  const [tree, setTree] = useState<Tree>({ folders: [], entries: [] });
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [me, setMe] = useState<{ user: string; version: string; canManageBackups: boolean; localAuth:boolean; isAdmin:boolean } | null>(null);
  const [connectionStatus, setConnectionStatus] = useState<Record<string, ConnectionTest>>({});
  const [fatal, setFatal] = useState("");
  const [selection, setSelection] = useState<Selection>({ type: "root" });
  const [expanded, setExpanded] = useState<Set<string>>(() => loadSet("relay.expanded"));
  const [recent, setRecent] = useState<string[]>(() => [...loadSet("relay.recent")]);
  const [search, setSearch] = useState("");
  const [navMode, setNavMode] = useState<"nav" | "fav" | "recent">("nav");
  const [tabs, setTabs] = useState<SessionTab[]>([]);
  const [activeTab, setActiveTab] = useState("dashboard");
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [menu, setMenu] = useState<Menu | null>(null);
  const [toasts, setToasts] = useState<{ id: number; msg: string }[]>([]);
  const [theme, setTheme] = useState<"dark" | "light">(() => (localStorage.getItem("relay.theme") as "dark" | "light") ?? "dark");

  const toast = useCallback((msg: string) => {
    const id = Date.now() + Math.random();
    setToasts((t) => [...t, { id, msg }]);
    window.setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 3500);
  }, []);

  const reload = useCallback(async () => {
    const [t, c] = await Promise.all([api.tree(), api.credentials()]);
    setTree(t);
    setCredentials(c);
  }, []);

  useEffect(() => {
    api
      .me()
      .then(setMe)
      .then(reload)
      .catch((e) => setFatal(e.message));
  }, [reload]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem("relay.theme", theme);
  }, [theme]);
  useEffect(() => localStorage.setItem("relay.expanded", JSON.stringify([...expanded])), [expanded]);
  useEffect(() => localStorage.setItem("relay.recent", JSON.stringify(recent)), [recent]);
  useEffect(() => {
    const close = () => setMenu(null);
    window.addEventListener("click", close);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("blur", close);
    };
  }, []);

  useEffect(() => {
    if (!me) return;
    const controller = new AbortController();
    let timer = 0;
    const poll = async () => {
      try {
        const next = await api.connectionStatus(controller.signal);
        if (!controller.signal.aborted) setConnectionStatus(next);
      } catch {
        if (!controller.signal.aborted) setConnectionStatus({});
      } finally {
        if (!controller.signal.aborted) timer = window.setTimeout(poll, 15_000);
      }
    };
    void poll();
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [me]);

  const updateConnectionStatus = useCallback((result: ConnectionTest) => {
    setConnectionStatus((current) => ({ ...current, [result.address]: result }));
  }, []);

  const selectedEntry = selection.type === "entry" ? tree.entries.find((e) => e.id === selection.id) : undefined;
  const selectedFolderId =
    selection.type === "folder" ? selection.id : selection.type === "entry" ? (selectedEntry?.folderId ?? null) : null;

  const select = (s: Selection) => {
    setSelection(s);
    setActiveTab("dashboard");
  };

  const toggle = (id: string) =>
    setExpanded((cur) => {
      const n = new Set(cur);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });

  // Déplie les dossiers parents d'une entrée sélectionnée (recherche, favoris…).
  const reveal = (folderId: string | null) =>
    setExpanded((cur) => {
      const n = new Set(cur);
      const byId = new Map(tree.folders.map((f) => [f.id, f]));
      for (let id = folderId, i = 0; id && i < 100; i++) {
        n.add(id);
        id = byId.get(id)?.parentId ?? null;
      }
      return n;
    });

  const open = (e: Entry, record = false) => {
    setRecent((r) => [e.id, ...r.filter((x) => x !== e.id)].slice(0, 15));
    if (e.kind === "web") {
      window.open(e.url, "_blank", "noopener,noreferrer");
      return;
    }
    const key = `${e.id}-${Date.now()}`;
    setTabs((t) => [...t, { key, entryId: e.id, name: e.name, kind: e.kind, record }]);
    setActiveTab(key);
  };

  const openQuick = (target: QuickTarget, token: string, replaceKey?: string) => {
    const key = crypto.randomUUID();
    const name = target.port ? `${target.host} · ${target.port}` : target.host;
    const tab: SessionTab = { key, entryId: "", name, kind: target.kind, record: target.record, quick: { target, token } };
    setTabs((current) => {
      if (!replaceKey || !current.some((item) => item.key === replaceKey)) return [...current, tab];
      return current.map((item) => item.key === replaceKey ? tab : item);
    });
    setActiveTab(key);
    setDialog(null);
  };

  // Entrées SSH visées : l'entrée, ou tout le contenu SSH d'un dossier (récursif).
  const sshTargets = (s: Selection): string[] => {
    if (s.type === "entry") {
      const e = tree.entries.find((x) => x.id === s.id);
      return e?.kind === "ssh" ? [e.id] : [];
    }
    const under = new Set<string | null>([s.type === "folder" ? s.id : null]);
    if (s.type === "root") return tree.entries.filter((e) => e.kind === "ssh").map((e) => e.id);
    let grew = true;
    while (grew) {
      grew = false;
      for (const f of tree.folders) if (under.has(f.parentId) && !under.has(f.id)) (under.add(f.id), (grew = true));
    }
    return tree.entries.filter((e) => e.kind === "ssh" && under.has(e.folderId)).map((e) => e.id);
  };

  const openCommand = (targets: string[]) => {
    const key = `cmd-${Date.now()}`;
    setTabs((t) => [...t, { key, entryId: "", name: "Commande", kind: "ssh", tool: "command", targets }]);
    setActiveTab(key);
  };

  const openAvailability = (folderId: string | null = null) => {
    const tab: SessionTab = { key: "availability", entryId: "", name: "Disponibilité", kind: "ssh", tool: "availability", scopeFolderId: folderId };
    setTabs((current) => current.some((item) => item.key === tab.key)
      ? current.map((item) => item.key === tab.key ? tab : item)
      : [...current, tab]);
    setActiveTab(tab.key);
  };

  const openWorkspace = (workspace: Workspace) => {
    const byID = new Map(tree.entries.map((entry) => [entry.id, entry]));
    const entries = workspace.entryIds
      .map((id) => byID.get(id))
      .filter((entry): entry is Entry => !!entry && entry.kind !== "web");
    if (!entries.length) {
      toast("Aucune entrée de cet espace n’est disponible.");
      return;
    }
    const next = entries.map((entry) => ({
      key: crypto.randomUUID(), entryId: entry.id, name: entry.name, kind: entry.kind,
    }));
    setTabs((current) => [...current, ...next]);
    setActiveTab(next[0].key);
    setDialog(null);
    if (entries.length !== workspace.entryIds.length) toast("Les entrées supprimées ont été ignorées.");
  };

  const closeTab = (key: string) => {
    setTabs((t) => {
      const idx = t.findIndex((x) => x.key === key);
      const next = t.filter((x) => x.key !== key);
      if (activeTab === key) setActiveTab(next[Math.max(0, idx - 1)]?.key ?? "dashboard");
      return next;
    });
  };

  const newEntry = (kind: Kind, folderId: string | null) =>
    setDialog({
      type: "entry",
      initial: {
        folderId,
        name: "",
        kind,
        host: "",
        port: 0,
        url: "",
        username: "",
        credentialId: null,
        settings: {},
        notes: "",
        favorite: false,
      },
    });

  const edit = (e: Entry, section?: "notes") => setDialog({ type: "entry", initial: entryInput(e), id: e.id, section });

  const saveEntry = async (input: EntryInput, id?: string) => {
    const saved = await api.saveEntry(input, id);
    await reload();
    setDialog(null);
    reveal(saved.folderId);
    select({ type: "entry", id: saved.id });
    toast(id ? "Entrée enregistrée" : "Entrée créée");
  };

  const duplicate = async (e: Entry) => {
    try {
      const saved = await api.saveEntry({ ...entryInput(e), name: `${e.name} (copie)`, favorite: false });
      await reload();
      select({ type: "entry", id: saved.id });
      toast("Entrée dupliquée");
    } catch (err) {
      toast((err as Error).message);
    }
  };

  const toggleFavorite = async (e: Entry) => {
    try {
      await api.saveEntry({ ...entryInput(e), favorite: !e.favorite }, e.id);
      await reload();
    } catch (err) {
      toast((err as Error).message);
    }
  };

  const askDelete = (s: Selection) => {
    if (s.type === "entry") {
      const e = tree.entries.find((x) => x.id === s.id);
      if (!e) return;
      setDialog({
        type: "confirm",
        title: "Supprimer l'entrée",
        message: (
          <>
            Supprimer l'entrée <b>{e.name}</b> ?
          </>
        ),
        label: "Supprimer",
        action: async () => {
          try {
            await api.deleteEntry(e.id);
            await reload();
            select({ type: "root" });
            toast("Entrée supprimée");
          } catch (err) {
            toast((err as Error).message);
          }
        },
      });
    } else if (s.type === "folder") {
      const f = tree.folders.find((x) => x.id === s.id);
      if (!f) return;
      setDialog({
        type: "confirm",
        title: "Supprimer le dossier",
        message: (
          <>
            Supprimer le dossier <b>{f.name}</b> ainsi que <b>tous ses sous-dossiers et entrées</b> ? Cette action est
            définitive.
          </>
        ),
        label: "Supprimer",
        action: async () => {
          try {
            await api.deleteFolder(f.id);
            await reload();
            select({ type: "root" });
            toast("Dossier supprimé");
          } catch (err) {
            toast((err as Error).message);
          }
        },
      });
    }
  };

  const move = async (item: { type: "entry" | "folder"; id: string }, folderId: string | null) => {
    try {
      if (item.type === "entry") {
        const e = tree.entries.find((x) => x.id === item.id);
        if (!e || (e.folderId ?? null) === folderId) return;
        await api.saveEntry({ ...entryInput(e), folderId }, e.id);
      } else {
        const f = tree.folders.find((x) => x.id === item.id);
        if (!f || (f.parentId ?? null) === folderId) return;
        await api.saveFolder({ name: f.name, parentId: folderId }, f.id);
      }
      await reload();
      if (folderId) reveal(folderId);
    } catch (err) {
      toast((err as Error).message);
    }
  };

  const renameFolder = (id: string) => {
    const f = tree.folders.find((x) => x.id === id);
    if (f) setDialog({ type: "folder", parentId: f.parentId, id: f.id, name: f.name });
  };

  const ribbon: RibbonTab[] = useMemo(
    () => [
      {
        id: "home",
        label: "Accueil",
        groups: [
          {
            label: "Session",
            actions: [
              {
                id: "open",
                label: selectedEntry?.kind === "web" ? "Ouvrir le site" : "Ouvrir la session",
                Icon: selectedEntry?.kind === "web" ? ExternalLink : Play,
                color: "#3fb950",
                large: true,
                disabled: !selectedEntry,
                onClick: () => selectedEntry && open(selectedEntry),
              },
              {
                id: "record", label: "Ouvrir et enregistrer", Icon: CircleDot, color: "#f85149",
                disabled: !selectedEntry || !canRecord(selectedEntry),
                onClick: () => selectedEntry && open(selectedEntry, true),
              },
              { id: "test-connection", label: "Tester la connexion", Icon: Activity, color: "#58a6ff",
                disabled: !selectedEntry || !canTestConnection(selectedEntry),
                onClick: () => selectedEntry && setDialog({ type: "connection-test", entry: selectedEntry }) },
              { id: "quick", label: "Connexion rapide", Icon: PlugZap, color: "#e3b341", onClick: () => setDialog({ type: "quick" }) },
              { id: "workspaces", label: "Espaces de travail", Icon: FolderOpen, onClick: () => setDialog({type:"workspaces"}) },
            ],
          },
          {
            label: "Ajouter",
            actions: [
              { id: "new", label: "Nouvelle entrée", Icon: Plus, color: "#58a6ff", large: true, onClick: () => setDialog({ type: "kind", folderId: selectedFolderId }) },
              { id: "folder", label: "Nouveau dossier", Icon: FolderPlus, color: FOLDER_COLOR, onClick: () => setDialog({ type: "folder", parentId: selectedFolderId }) },
              ...(Object.keys(KINDS) as Kind[]).slice(0, 2).map((k) => ({
                id: "new-" + k,
                label: `Entrée ${KINDS[k].label}`,
                Icon: KINDS[k].Icon,
                color: KINDS[k].color,
                onClick: () => newEntry(k, selectedFolderId),
              })),
            ],
          },
          {
            label: "Édition",
            actions: [
              {
                id: "edit",
                label: "Modifier",
                Icon: Pencil,
                large: true,
                disabled: selection.type === "root",
                onClick: () => {
                  if (selectedEntry) edit(selectedEntry);
                  else if (selection.type === "folder") renameFolder(selection.id);
                },
              },
              { id: "dup", label: "Dupliquer", Icon: Copy, disabled: !selectedEntry, onClick: () => selectedEntry && duplicate(selectedEntry) },
              { id: "del", label: "Supprimer", Icon: Trash2, color: "#f85149", disabled: selection.type === "root", onClick: () => askDelete(selection) },
              {
                id: "fav",
                label: selectedEntry?.favorite ? "Retirer des favoris" : "Ajouter aux favoris",
                Icon: Star,
                color: "#e3b341",
                disabled: !selectedEntry,
                onClick: () => selectedEntry && toggleFavorite(selectedEntry),
              },
            ],
          },
          {
            label: "Commandes",
            actions: [
              {
                id: "cmd",
                label: "Commande SSH",
                Icon: Zap,
                color: "#e3b341",
                large: true,
                onClick: () => openCommand(selection.type === "root" ? [] : sshTargets(selection)),
              },
            ],
          },
          {
            label: "Coffre",
            actions: [{ id: "creds", label: "Identifiants", Icon: KeyRound, color: "#d29922", large: true, onClick: () => setDialog({ type: "credentials" }) }],
          },
          {
            label: "Importer",
            actions: [{ id: "import", label: "Importer depuis RDM", Icon: FileUp, color: "#58a6ff", large: true, onClick: () => setDialog({ type: "import" }) }],
          },
          {
            label: "Données",
            actions: [
              { id: "availability", label: "Disponibilité", Icon: Activity, color: "#3fb950", large: true, onClick: () => openAvailability(selectedFolderId) },
              { id: "reload", label: "Actualiser", Icon: RefreshCw, onClick: () => reload().then(() => toast("Données actualisées")) },
            ],
          },
        ],
      },
      {
        id: "view",
        label: "Affichage",
        groups: [
          {
            label: "Thème",
            actions: [
              { id: "dark", label: "Sombre", Icon: Moon, large: true, active: theme === "dark", onClick: () => setTheme("dark") },
              { id: "light", label: "Clair", Icon: Sun, large: true, active: theme === "light", onClick: () => setTheme("light") },
            ],
          },
          {
            label: "Navigation",
            actions: [
              { id: "expand", label: "Tout développer", Icon: ChevronsUpDown, onClick: () => setExpanded(new Set(tree.folders.map((f) => f.id))) },
              { id: "collapse", label: "Tout réduire", Icon: ChevronsDownUp, onClick: () => setExpanded(new Set()) },
              { id: "dash", label: "Tableau de bord", Icon: LayoutDashboard, onClick: () => setActiveTab("dashboard") },
            ],
          },
        ],
      },
      {
        id: "admin",
        label: "Administration",
        groups: [
          {
            label: "Suivi",
            actions: [
              { id: "audit", label: "Journal d'activité", Icon: History, color: "#58a6ff", large: true, onClick: () => setDialog({ type: "audit" }) },
              { id: "recordings", label: "Enregistrements", Icon: Film, color: "#58a6ff", large: true, onClick: () => setDialog({ type: "recordings" }) },
            ],
          },
          {
            label: "Mobile",
            actions: [{ id: "devices", label: "Appareils", Icon: Smartphone, color: "#3fb950", large: true, onClick: () => setDialog({ type: "devices" }) }],
          },
          {
            label: "Synchronisation",
            actions: [{ id: "sources", label: "Sources de synchro", Icon: RefreshCw, color: "#e57000", large: true, onClick: () => setDialog({ type: "sources" }) }],
          },
          {
            label: "Maintenance",
            actions: [
              { id: "dups", label: "Doublons", Icon: CopyX, color: "#d29922", large: true, onClick: () => setDialog({ type: "duplicates" }) },
              ...(me?.localAuth&&me.isAdmin?[{id:"users",label:"Comptes locaux",Icon:Users,large:true,onClick:()=>setDialog({type:"users" as const})}]:[]),
              ...(me?.canManageBackups ? [{ id: "backups", label: "Sauvegarde", Icon: HardDrive, color: "#58a6ff", large: true, onClick: () => setDialog({type:"backups" as const}) }] : []),
            ],
          },
          {
            label: "Aide",
            actions: [{ id: "about", label: "À propos", Icon: Info, large: true, onClick: () => setDialog({ type: "about" }) }],
          },
        ],
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [selection, selectedEntry, selectedFolderId, theme, tree, me],
  );

  if (fatal) {
    return (
      <div className="fatal">
        <h1>Relay</h1>
        <p>{fatal}</p>
        <button className="btn primary" onClick={() => location.reload()}>
          Recharger
        </button>
      </div>
    );
  }

  const menuItems = (s: Selection) => {
    const e = s.type === "entry" ? tree.entries.find((x) => x.id === s.id) : undefined;
    const folderId = s.type === "folder" ? s.id : s.type === "entry" ? (e?.folderId ?? null) : null;
    const items: ({ label: string; Icon: typeof Play; run: () => void; danger?: boolean } | "sep")[] = [];
    if (e) {
      items.push({ label: e.kind === "web" ? "Ouvrir le site" : "Ouvrir la session", Icon: e.kind === "web" ? ExternalLink : Play, run: () => open(e) });
      if (canTestConnection(e)) items.push({ label: "Tester la connexion", Icon: Activity, run: () => setDialog({ type: "connection-test", entry: e }) });
      if (canRecord(e)) items.push({ label: "Ouvrir et enregistrer", Icon: CircleDot, run: () => open(e, true) });
      items.push({ label: "Modifier…", Icon: Pencil, run: () => edit(e) });
      items.push({ label: "Dupliquer", Icon: Copy, run: () => duplicate(e) });
      items.push({ label: e.favorite ? "Retirer des favoris" : "Ajouter aux favoris", Icon: Star, run: () => toggleFavorite(e) });
      items.push("sep");
    }
    if(s.type==="folder") items.push({label:"Identifiants du dossier",Icon:KeyRound,run:()=>setDialog({type:"folder-credentials",id:s.id})});
    if (s.type === "folder" || s.type === "root") {
      items.push({ label: "Voir la disponibilité", Icon: Activity, run: () => openAvailability(s.type === "folder" ? s.id : null) });
      items.push("sep");
    }
    const targets = sshTargets(s);
    if (targets.length) {
      items.push({ label: targets.length > 1 ? `Exécuter une commande (${targets.length} serveurs SSH)…` : "Exécuter une commande…", Icon: Zap, run: () => openCommand(targets) });
      items.push("sep");
    }
    items.push({ label: "Nouvelle entrée…", Icon: Plus, run: () => setDialog({ type: "kind", folderId }) });
    items.push({ label: "Nouveau dossier…", Icon: FolderPlus, run: () => setDialog({ type: "folder", parentId: folderId }) });
    if (s.type === "folder") items.push({ label: "Renommer…", Icon: Pencil, run: () => renameFolder(s.id) });
    if (s.type !== "root") {
      items.push("sep");
      items.push({ label: "Supprimer", Icon: Trash2, run: () => askDelete(s), danger: true });
    }
    return items;
  };

  const activeSession = tabs.find((t) => t.key === activeTab);

  return (
    <div className="app">
      <header className="titlebar">
        <img src="/favicon.svg" alt="" className="logo" />
        <span className="app-name">Relay</span>
        <span className="title-sep">—</span>
        <span className="title-vault">{VAULT_NAME}</span>
        <span className="title-context">
          {activeSession ? activeSession.name : selectedEntry ? selectedEntry.name : ""}
        </span>
        <div className="spacer" />
        <InstallPWA />
        <span className="title-user">{me?.user}</span>
 {me?.localAuth&&<><button className="icon-btn" title="Changer mon mot de passe" onClick={()=>setDialog({type:"password"})}><KeyRound size={15}/></button><button className="icon-btn" title="Se déconnecter" onClick={()=>{void localAuthApi.logout().then(()=>location.reload()).catch((e:Error)=>toast(e.message))}}><LogOut size={15}/></button></>}
      </header>

      <Ribbon tabs={ribbon} />

      <div className="workspace">
        <aside className="nav">
          <div className="nav-header">
            <span>Navigation</span>
          </div>
          <div className="nav-vault">
            <KeyRound size={14} color="#58a6ff" />
            <span>Coffre : {VAULT_NAME}</span>
          </div>
          <div className="nav-search">
            <Search size={14} />
            <input placeholder="Rechercher (nom, hôte, notes)…" value={search} onChange={(e) => setSearch(e.target.value)} />
            {search && (
              <button className="icon-btn" onClick={() => setSearch("")} title="Effacer">
                <X size={13} />
              </button>
            )}
          </div>
          <NavTree
            tree={tree}
            connectionStatus={connectionStatus}
            vaultName={VAULT_NAME}
            selection={selection}
            expanded={expanded}
            search={search}
            mode={navMode}
            recent={recent}
            onSelect={(s) => {
              select(s);
              if (s.type === "entry" && (search || navMode !== "nav")) {
                const e = tree.entries.find((x) => x.id === s.id);
                if (e) reveal(e.folderId);
              }
            }}
            onToggle={toggle}
            onOpen={open}
            onContextMenu={(sel, x, y) => setMenu({ sel, x, y })}
            onMove={move}
          />
          <div className="nav-tabs">
            {(
              [
                ["nav", "Navigation"],
                ["fav", "Favoris"],
                ["recent", "Récents"],
              ] as const
            ).map(([id, label]) => (
              <button key={id} className={navMode === id ? "active" : ""} onClick={() => setNavMode(id)}>
                {label}
              </button>
            ))}
          </div>
        </aside>

        <main className="content">
          <div className="doc-tabs">
            <button className={`doc-tab ${activeTab === "dashboard" ? "active" : ""}`} onClick={() => setActiveTab("dashboard")}>
              <LayoutDashboard size={14} /> Tableau de bord
            </button>
            {tabs.map((t) => (
              <div
                key={t.key}
                className={`doc-tab ${activeTab === t.key ? "active" : ""}`}
                onClick={() => setActiveTab(t.key)}
                onAuxClick={(e) => e.button === 1 && closeTab(t.key)}
                title={t.name}
              >
                {t.tool === "command" ? <Zap size={14} color="#e3b341" /> : t.tool === "availability" ? <Activity size={14} color="#3fb950" /> : <KindIcon kind={t.kind} size={14} />}
                <span>{t.name}</span>
                <button
                  className="tab-close"
                  onClick={(e) => {
                    e.stopPropagation();
                    closeTab(t.key);
                  }}
                  title="Fermer la session"
                >
                  <X size={12} />
                </button>
              </div>
            ))}
          </div>
          <div className="doc-body">
            <div style={{ display: activeTab === "dashboard" ? "block" : "none", height: "100%", overflow: "auto" }}>
              <Dashboard
                tree={tree}
                connectionStatus={connectionStatus}
                credentials={credentials}
                selection={selection}
                vaultName={VAULT_NAME}
                onOpen={open}
                onEdit={edit}
                onFolderCredentials={(id)=>setDialog({type:"folder-credentials",id})}
                onTest={(entry) => setDialog({ type: "connection-test", entry })}
                onToggleFavorite={toggleFavorite}
                onSelect={(s) => {
                  select(s);
                  if (s.type === "folder") reveal(s.id);
                }}
                onNewEntry={() => setDialog({ type: "kind", folderId: selectedFolderId })}
                onToast={toast}
              />
            </div>
            {tabs.map((t) =>
              t.tool === "availability" ? (
                <AvailabilityView key={t.key} tab={t} active={activeTab === t.key} tree={tree} results={connectionStatus}
                  onResult={updateConnectionStatus} onOpen={open} onTest={(entry) => setDialog({ type: "connection-test", entry })} />
              ) : t.tool === "command" ? (
                <CommandView key={t.key} tab={t} active={activeTab === t.key} tree={tree} onToast={toast} />
              ) : t.kind === "pve" ? (
                <PveSessionView key={t.key} tab={t} active={activeTab === t.key} onToast={toast} />
              ) : (
                <SessionView key={t.key} tab={t} active={activeTab === t.key} onToast={toast}
                  onReconnect={t.quick ? () => setDialog({ type: "quick", initial: t.quick?.target, replaceKey: t.key }) : undefined} />
              ),
            )}
          </div>
        </main>
      </div>

      <footer className="statusbar">
        <span>{me ? `Connecté : ${me.user}` : "Connexion…"}</span>
        <span className="sb-sep" />
        <span>
          {tree.entries.length} entrée(s) · {tree.folders.length} dossier(s) · {credentials.length} identifiant(s)
        </span>
        <span className="sb-sep" />
        <span>{tabs.filter((t) => !t.tool).length} session(s) ouverte(s)</span>
        <div className="spacer" />
        {selection.type !== "root" && <span className="muted">{pathOf(tree, selectedFolderId) || VAULT_NAME}</span>}
        <span className="sb-sep" />
        <span>Relay {me?.version}</span>
      </footer>

      {menu && (
        <div className="ctx-menu" style={{ left: menu.x, top: menu.y }} onClick={(e) => e.stopPropagation()}>
          {menuItems(menu.sel).map((it, i) =>
            it === "sep" ? (
              <div key={i} className="ctx-sep" />
            ) : (
              <button
                key={i}
                className={it.danger ? "danger" : ""}
                onClick={() => {
                  setMenu(null);
                  it.run();
                }}
              >
                <it.Icon size={14} /> {it.label}
              </button>
            ),
          )}
        </div>
      )}

      {dialog?.type==="users"&&me&&<UsersDialog current={me.user} onClose={()=>setDialog(null)}/>}
 {dialog?.type==="password"&&<PasswordDialog onClose={()=>setDialog(null)}/>}
 {dialog?.type==="backups"&&<BackupDialog onClose={()=>setDialog(null)}/>}
      {dialog?.type==="workspaces"&&<WorkspacesDialog tree={tree} currentIds={tabs.filter(t=>!t.tool&&!t.quick).map(t=>t.entryId)} onOpen={openWorkspace} onClose={()=>setDialog(null)}/>}
      {dialog?.type==="folder-credentials"&&tree.folders.find(f=>f.id===dialog.id)&&<FolderCredentialsDialog folder={tree.folders.find(f=>f.id===dialog.id)!} tree={tree} credentials={credentials} onSaved={reload} onClose={()=>setDialog(null)}/>}
      {dialog?.type === "connection-test" && (
        <ConnectionTestDialog key={dialog.entry.id} entry={dialog.entry} onClose={() => setDialog(null)} onResult={updateConnectionStatus}
          onOpen={() => { open(dialog.entry); setDialog(null); }} />
      )}
      {dialog?.type === "quick" && (
        <QuickConnectDialog credentials={credentials} initial={dialog.initial} onClose={() => setDialog(null)}
          onConnect={(target, token) => openQuick(target, token, dialog.replaceKey)} />
      )}
      {dialog?.type === "kind" && (
        <KindPicker
          onClose={() => setDialog(null)}
          onPick={(k) => newEntry(k, dialog.folderId)}
        />
      )}
      {dialog?.type === "entry" && (
        <EntryDialog
          key={dialog.id ?? "new"}
          initial={dialog.initial}
          isNew={!dialog.id}
          initialSection={dialog.section}
          tree={tree}
          credentials={credentials}
          vaultName={VAULT_NAME}
          onSave={(e) => saveEntry(e, dialog.id)}
          onClose={() => setDialog(null)}
          onManageCredentials={() => setDialog({ type: "credentials" })}
        />
      )}
      {dialog?.type === "folder" && (
        <Prompt
          title={dialog.id ? "Renommer le dossier" : "Nouveau dossier"}
          label="Nom du dossier"
          initial={dialog.name}
          onClose={() => setDialog(null)}
          onSubmit={async (name) => {
            try {
              const f = await api.saveFolder({ name, parentId: dialog.parentId }, dialog.id);
              await reload();
              setDialog(null);
              if (dialog.parentId) reveal(dialog.parentId);
              select({ type: "folder", id: f.id });
            } catch (err) {
              toast((err as Error).message);
            }
          }}
        />
      )}
      {dialog?.type === "credentials" && (
        <CredentialsDialog
          credentials={credentials}
          onChanged={reload}
          onClose={() => setDialog(null)}
          onToast={toast}
        />
      )}
      {dialog?.type === "recordings" && <RecordingsDialog onClose={() => setDialog(null)} />}
      {dialog?.type === "devices" && <DevicesDialog onClose={() => setDialog(null)} onToast={toast} />}
      {dialog?.type === "audit" && <AuditDialog onClose={() => setDialog(null)} />}
      {dialog?.type === "duplicates" && (
        <ErrorBoundary label="La fenêtre des doublons a rencontré une erreur" onReset={() => setDialog(null)}>
          <DuplicatesDialog
            tree={tree}
            credentials={credentials}
            onChanged={reload}
            onSelect={(id) => {
              const e = tree.entries.find((x) => x.id === id);
              if (e) reveal(e.folderId);
              select({ type: "entry", id });
              setDialog(null);
            }}
            onClose={() => setDialog(null)}
            onToast={toast}
          />
        </ErrorBoundary>
      )}
      {dialog?.type === "sources" && (
        <SourcesDialog credentials={credentials} onSynced={reload} onClose={() => setDialog(null)} onToast={toast} />
      )}
      {dialog?.type === "import" && (
        <ErrorBoundary label="La fenêtre d'import a rencontré une erreur" onReset={() => setDialog(null)}>
          <ImportDialog
          onClose={() => setDialog(null)}
          onDone={async (rootId, msg) => {
            await reload();
            setDialog(null);
            select({ type: "folder", id: rootId });
            toast(msg);
          }}
          />
        </ErrorBoundary>
      )}
      {dialog?.type === "confirm" && (
        <Confirm title={dialog.title} message={dialog.message} danger confirmLabel={dialog.label} onConfirm={dialog.action} onClose={() => setDialog(null)} />
      )}
      {dialog?.type === "about" && (
        <Modal title="À propos de Relay" onClose={() => setDialog(null)} width={460} icon={<Info size={16} />}>
          <div className="about">
            <img src="/favicon.svg" alt="" width={56} />
            <div>
              <h2>Relay {me?.version}</h2>
              <p>Gestionnaire de connexions à distance pour le homelab : RDP, SSH, VNC et accès web, avec coffre d'identifiants chiffré.</p>
              <p className="muted small">Les sessions passent par guacd (Apache Guacamole, licence Apache 2.0).</p>
            </div>
          </div>
        </Modal>
      )}

      <div className="toasts">
        {toasts.map((t) => (
          <div key={t.id} className="toast">
            {t.msg}
          </div>
        ))}
      </div>
    </div>
  );
}
