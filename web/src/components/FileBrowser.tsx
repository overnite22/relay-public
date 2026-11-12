import Guacamole from "guacamole-common-js";
import { ArrowUp, Download, File, Folder, Loader2, RefreshCw, Upload, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

/* eslint-disable @typescript-eslint/no-explicit-any */

const INDEX = "application/vnd.glyptodon.guacamole.stream-index+json";
const SUCCESS = 0x0000;

interface Item {
  path: string;
  name: string;
  dir: boolean;
}

interface Transfer {
  id: number;
  name: string;
  dir: "up" | "down";
  done: number;
  total: number;
  state: "run" | "ok" | "err";
  error?: string;
}

const basename = (p: string) => p.replace(/\/+$/, "").split("/").pop() || "/";
const parent = (p: string) => {
  const t = p.replace(/\/+$/, "");
  const i = t.lastIndexOf("/");
  return i <= 0 ? "/" : t.slice(0, i);
};
const join = (dir: string, name: string) => (dir.endsWith("/") ? dir : dir + "/") + name;

function fmtSize(n: number) {
  if (n < 1024) return `${n} o`;
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(0)} Ko`;
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} Mo`;
  return `${(n / 1024 ** 3).toFixed(2)} Go`;
}

/**
 * Accuse réception de chaque bloc reçu : guacd n'envoie le bloc suivant
 * (listing ou téléchargement) qu'après cet accusé.
 */
export function ackEachBlob(stream: any) {
  const handler = stream.onblob;
  stream.onblob = (data: string) => {
    handler?.(data);
    stream.sendAck("OK", SUCCESS);
  };
}

/** Navigateur de fichiers SFTP d'une session SSH (objet « filesystem » de guacd). */
export function FileBrowser({
  fs,
  title = "Fichiers (SFTP)",
  hint = "Glissez des fichiers ici pour les envoyer dans le dossier affiché.",
  onClose,
  onToast,
}: {
  fs: any;
  title?: string;
  hint?: string;
  onClose: () => void;
  onToast: (m: string) => void;
}) {
  const [path, setPath] = useState("/");
  const [items, setItems] = useState<Item[] | null>(null);
  const [error, setError] = useState("");
  const [transfers, setTransfers] = useState<Transfer[]>([]);
  const [drag, setDrag] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const seq = useRef(0);

  const update = (id: number, t: Partial<Transfer>) => setTransfers((all) => all.map((x) => (x.id === id ? { ...x, ...t } : x)));

  const list = useCallback(
    (dir: string) => {
      setItems(null);
      setError("");
      fs.requestInputStream(dir, (stream: any, mimetype: string) => {
        if (mimetype !== INDEX) {
          stream.sendAck("Type inattendu", 0x0100);
          setError("Ce chemin n'est pas un dossier.");
          setItems([]);
          return;
        }
        const reader = new Guacamole.JSONReader(stream);
        ackEachBlob(stream);
        reader.onend = () => {
          const index = (reader.getJSON() ?? {}) as Record<string, string>;
          const out = Object.entries(index).map(([p, m]) => ({ path: p, name: basename(p), dir: m === INDEX }));
          out.sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name, "fr", { numeric: true }) : a.dir ? -1 : 1));
          setItems(out);
          setPath(dir);
        };
        stream.sendAck("Prêt", SUCCESS);
      });
    },
    [fs],
  );

  useEffect(() => list("/"), [list]);

  const download = (it: Item) => {
    const id = ++seq.current;
    setTransfers((t) => [...t, { id, name: it.name, dir: "down", done: 0, total: 0, state: "run" }]);
    fs.requestInputStream(it.path, (stream: any, mimetype: string) => {
      const reader = new Guacamole.BlobReader(stream, mimetype);
      ackEachBlob(stream);
      reader.onprogress = (len: number) => update(id, { done: reader.getLength?.() ?? len });
      reader.onend = () => {
        const url = URL.createObjectURL(reader.getBlob());
        const a = document.createElement("a");
        a.href = url;
        a.download = it.name;
        a.click();
        window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
        update(id, { state: "ok" });
      };
      stream.sendAck("Prêt", SUCCESS);
    });
  };

  const upload = (files: FileList | File[]) => {
    const dir = path;
    for (const file of Array.from(files)) {
      const id = ++seq.current;
      setTransfers((t) => [...t, { id, name: file.name, dir: "up", done: 0, total: file.size, state: "run" }]);
      const stream = fs.createOutputStream(file.type || "application/octet-stream", join(dir, file.name));
      const writer = new Guacamole.BlobWriter(stream);
      writer.onprogress = (_blob: Blob, offset: number) => update(id, { done: offset });
      writer.onerror = (_blob: Blob, _offset: number, status: { message?: string }) =>
        update(id, { state: "err", error: status?.message || "refusé par le serveur" });
      writer.oncomplete = () => {
        writer.sendEnd();
        update(id, { state: "ok", done: file.size });
        if (dir === path) list(dir);
      };
      writer.onack = (status: { isError?: () => boolean; message?: string }) => {
        if (status?.isError?.()) update(id, { state: "err", error: status.message || "refusé par le serveur" });
      };
      writer.sendBlob(file);
    }
    onToast(`Envoi vers ${dir}`);
  };

  const crumbs = path === "/" ? [] : path.split("/").filter(Boolean);

  return (
    <aside
      className={`files ${drag ? "drag" : ""}`}
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes("Files")) {
          e.preventDefault();
          setDrag(true);
        }
      }}
      onDragLeave={() => setDrag(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDrag(false);
        if (e.dataTransfer.files.length) upload(e.dataTransfer.files);
      }}
    >
      <div className="files-head">
        <span className="files-title">{title}</span>
        <button className="icon-btn" title="Dossier parent" onClick={() => list(parent(path))} disabled={path === "/"}>
          <ArrowUp size={14} />
        </button>
        <button className="icon-btn" title="Actualiser" onClick={() => list(path)}>
          <RefreshCw size={14} />
        </button>
        <button className="icon-btn" title="Envoyer des fichiers ici" onClick={() => inputRef.current?.click()}>
          <Upload size={14} />
        </button>
        <button className="icon-btn" title="Fermer" onClick={onClose}>
          <X size={14} />
        </button>
        <input
          ref={inputRef}
          type="file"
          multiple
          hidden
          onChange={(e) => {
            if (e.target.files?.length) upload(e.target.files);
            e.target.value = "";
          }}
        />
      </div>
      <div className="files-path">
        <button onClick={() => list("/")}>/</button>
        {crumbs.map((c, i) => (
          <button key={i} onClick={() => list("/" + crumbs.slice(0, i + 1).join("/"))}>
            {c}/
          </button>
        ))}
      </div>
      <div className="files-list">
        {items === null && (
          <div className="tree-empty">
            <Loader2 size={16} className="spin" />
          </div>
        )}
        {error && <div className="form-error small" style={{ padding: 8 }}>{error}</div>}
        {items?.length === 0 && !error && <div className="tree-empty">Dossier vide</div>}
        {items?.map((it) => (
          <div
            key={it.path}
            className="file-row"
            onDoubleClick={() => (it.dir ? list(it.path) : download(it))}
            title={it.dir ? "Ouvrir" : "Double-clic pour télécharger"}
          >
            {it.dir ? <Folder size={15} color="#e3b341" /> : <File size={15} color="var(--text-muted)" />}
            <span className="file-name" onClick={() => it.dir && list(it.path)}>
              {it.name}
            </span>
            {!it.dir && (
              <button className="icon-btn" title="Télécharger" onClick={() => download(it)}>
                <Download size={13} />
              </button>
            )}
          </div>
        ))}
      </div>
      {transfers.length > 0 && (
        <div className="files-transfers">
          {transfers.slice(-6).map((t) => (
            <div key={t.id} className={`transfer t-${t.state}`} title={t.error}>
              {t.dir === "up" ? <Upload size={12} /> : <Download size={12} />}
              <span className="file-name">{t.name}</span>
              <span className="muted small">
                {t.state === "err" ? "échec" : t.state === "ok" ? "terminé" : t.total ? `${Math.round((t.done / t.total) * 100)} %` : fmtSize(t.done)}
              </span>
            </div>
          ))}
          <button className="btn small" onClick={() => setTransfers((t) => t.filter((x) => x.state === "run"))}>
            Effacer la liste
          </button>
        </div>
      )}
      <div className="files-hint muted small">{hint}</div>
    </aside>
  );
}
