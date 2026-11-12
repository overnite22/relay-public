import type { Entry, Folder, Kind, Tree } from "./types";

export function inheritedCredential(tree: Tree, folderId: string | null, kind: Kind): { id: string | null; folder?: Folder } {
  const folders = new Map(tree.folders.map((folder) => [folder.id, folder]));
  const seen = new Set<string>();
  while (folderId && !seen.has(folderId)) {
    seen.add(folderId);
    const folder = folders.get(folderId);
    if (!folder) break;
    if (Object.hasOwn(folder.credentials ?? {}, kind)) return { id: folder.credentials[kind] ?? null, folder };
    folderId = folder.parentId;
  }
  return { id: null };
}

export function effectiveCredential(tree: Tree, entry: Pick<Entry, "credentialId" | "settings" | "folderId" | "kind">) {
  if (entry.credentialId) return { id: entry.credentialId };
  if (entry.settings.credentialMode === "none" || entry.kind === "pve") return { id: null };
  return inheritedCredential(tree, entry.folderId, entry.kind);
}
