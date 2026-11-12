import type { Attachment, AuditEvent, CommandRun, ConnectionTest, Credential, CredentialInput, Entry, EntryInput, Folder, ImportPlan, QuickTarget, Recording, SavedCommand, Source, SourceInput, Tree, Workspace } from "./types";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function req<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { "X-Relay": "1" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const resp = await fetch(path, {
    method,
    headers,
    signal,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
  });
  if (resp.status === 204) return undefined as T;
  const text = await resp.text();
  let data: unknown = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    /* réponse non JSON (ex. page de connexion SSO) */
  }
  if (!resp.ok) {
    let msg = (data as { error?: string } | undefined)?.error ?? `Erreur ${resp.status}`;
    if (resp.status === 401) msg = "Session SSO expirée : rechargez la page pour vous reconnecter.";
    throw new ApiError(resp.status, msg);
  }
  return data as T;
}

export const api = {
  me: () => req<{ user: string; version: string; canManageBackups: boolean }>("GET", "/api/me"),
  tree: () => req<Tree>("GET", "/api/tree"),

  saveFolder: (f: { name: string; parentId: string | null }, id?: string) =>
    id ? req<Folder>("PUT", `/api/folders/${id}`, f) : req<Folder>("POST", "/api/folders", f),
  folderCredentials: (id: string, credentials: Partial<Record<string,string|null>>) => req<void>("PUT", `/api/folders/${encodeURIComponent(id)}/credentials`, {credentials}),
  deleteFolder: (id: string) => req<void>("DELETE", `/api/folders/${id}`),

  saveEntry: (e: EntryInput, id?: string) =>
    id ? req<Entry>("PUT", `/api/entries/${id}`, e) : req<Entry>("POST", "/api/entries", e),
  deleteEntry: (id: string) => req<void>("DELETE", `/api/entries/${id}`),
  connect: (id: string, size: { width: number; height: number; dpi: number; timezone: string; record?: boolean }) =>
    req<{ token: string; protocol: string; password?: string }>("POST", `/api/entries/${id}/connect`, size),

  connectionStatus: (signal: AbortSignal) => req<Record<string, ConnectionTest>>("GET", "/api/connection-status", undefined, signal),

  testConnection: (id: string, signal: AbortSignal) =>
    req<ConnectionTest>("POST", `/api/entries/${encodeURIComponent(id)}/test-connection`, undefined, signal),

  quickConnect: (input: QuickTarget & { password: string; width: number; height: number; dpi: number; timezone: string }) =>
    req<{ token: string; protocol: string }>("POST", "/api/quick-connect", input),

  credentials: () => req<Credential[]>("GET", "/api/credentials"),
  saveCredential: (c: CredentialInput, id?: string) =>
    id ? req<Credential>("PUT", `/api/credentials/${id}`, c) : req<Credential>("POST", "/api/credentials", c),
  deleteCredential: (id: string) => req<void>("DELETE", `/api/credentials/${id}`),
  reveal: (id: string, field: "password" | "privateKey" | "passphrase" | "totp") =>
    req<{ value: string }>("POST", `/api/credentials/${id}/reveal`, { field }),
  totp: (id: string) => req<{ code: string; remaining: number; period: number }>("GET", `/api/credentials/${id}/totp`),

  audit: (limit = 300) => req<AuditEvent[]>("GET", `/api/audit?limit=${limit}`),
};

export function entryInput(e: Entry): EntryInput {
  const { id: _id, createdAt: _c, updatedAt: _u, sourceId: _s, externalId: _x, ...rest } = e;
  return rest;
}

export const sourcesApi = {
  list: () => req<Source[]>("GET", "/api/sources"),
  save: (s: SourceInput, id?: string) => (id ? req<Source>("PUT", `/api/sources/${id}`, s) : req<Source>("POST", "/api/sources", s)),
  remove: (id: string) => req<void>("DELETE", `/api/sources/${id}`),
  test: (id: string) => req<{ ok: boolean; message: string }>("POST", `/api/sources/${id}/test`, {}),
  sync: (id: string) => req<{ ok: boolean; message: string }>("POST", `/api/sources/${id}/sync`, {}),
};

async function rawPost<T>(path: string, body: Blob | string, contentType = "application/xml"): Promise<T> {
  const resp = await fetch(path, { method: "POST", headers: { "X-Relay": "1", "Content-Type": contentType }, body, credentials: "same-origin" });
  const data = await resp.json().catch(() => undefined);
  if (!resp.ok) throw new ApiError(resp.status, (data as { error?: string } | undefined)?.error ?? `Erreur ${resp.status}`);
  return data as T;
}

export const importApi = {
  preview: (file: Blob) => rawPost<ImportPlan>("/api/import/rdm/preview", file),
  apply: (file: Blob, folder: string, defaults: { ssh: string; rdp: string }) =>
    rawPost<{ folders: number; entries: number; credentials: number; rootId: string }>(
      `/api/import/rdm/apply?${new URLSearchParams({ folder, sshDefault: defaults.ssh, rdpDefault: defaults.rdp })}`,
      file,
    ),
};

export interface DuplicateGroup {
  key: string;
  label: string;
  entries: Entry[];
}

export const duplicatesApi = {
  list: () => req<DuplicateGroup[]>("GET", "/api/duplicates"),
  merge: (keepId: string, ids: string[]) => req<Entry>("POST", `/api/entries/${keepId}/merge`, { ids }),
  clearExclusions: (sourceId: string) => req<{ message: string }>("POST", `/api/sources/${sourceId}/clear-exclusions`, {}),
};

export const commandsApi = {
  run: (body: { entryIds: string[]; command: string; sudo: boolean; timeoutSec: number; concurrency: number }) =>
    req<CommandRun>("POST", "/api/commands/run", body),
  get: (id: string) => req<CommandRun>("GET", `/api/commands/runs/${id}`),
  cancel: (id: string) => req<void>("POST", `/api/commands/runs/${id}/cancel`, {}),
  history: () => req<CommandRun[]>("GET", "/api/commands/runs?limit=50"),
  saved: () => req<SavedCommand[]>("GET", "/api/commands/saved"),
  save: (c: { name: string; command: string; sudo: boolean }, id?: string) =>
    id ? req<SavedCommand>("PUT", `/api/commands/saved/${id}`, c) : req<SavedCommand>("POST", "/api/commands/saved", c),
  removeSaved: (id: string) => req<void>("DELETE", `/api/commands/saved/${id}`),
  forgetHost: (address: string) => req<void>("POST", "/api/known-hosts/forget", { address }),
};

export const recordingsApi = {
  list: () => req<Recording[]>("GET", "/api/recordings"),
  remove: (id: string) => req<void>("DELETE", `/api/recordings/${encodeURIComponent(id)}`),
  read: async (id: string, signal: AbortSignal) => {
    const response = await fetch(`/api/recordings/${encodeURIComponent(id)}`, { signal, cache: "no-store" });
    if (!response.ok || response.headers.get("Content-Type") !== "application/octet-stream" || !response.body) {
      throw new Error("Impossible de charger cet enregistrement.");
    }
    return response.body;
  },
};

const attachmentsPath = (entryId: string) => `/api/entries/${encodeURIComponent(entryId)}/attachments`;
const attachmentPath = (entryId: string, id: string) => `${attachmentsPath(entryId)}/${encodeURIComponent(id)}`;

export const attachmentsApi = {
  list: (entryId: string) => req<Attachment[]>("GET", attachmentsPath(entryId)),
  add: (entryId: string, file: File) => rawPost<Attachment>(
    `${attachmentsPath(entryId)}?${new URLSearchParams({ name: file.name })}`, file, "application/octet-stream",
  ),
  downloadURL: attachmentPath,
  remove: (entryId: string, id: string) => req<void>("DELETE", attachmentPath(entryId, id)),
};

export interface Device {
  id: string;
  user: string;
  name: string;
  createdAt: string;
  lastUsedAt: string | null;
  lastIp: string;
}

export const devicesApi = {
  list: () => req<Device[]>("GET", "/api/devices"),
  create: (name: string) => req<{ device: Device; token: string; server: string; pairing: string }>("POST", "/api/devices", { name }),
  remove: (id: string) => req<void>("DELETE", `/api/devices/${encodeURIComponent(id)}`),
};

export const workspacesApi = {
  list: () => req<Workspace[]>("GET", "/api/workspaces"),
  save: (item: Workspace) =>
    req<Workspace>(item.id ? "PUT" : "POST", item.id ? `/api/workspaces/${encodeURIComponent(item.id)}` : "/api/workspaces", {
      name: item.name,
      entryIds: item.entryIds,
    }),
  remove: (id: string) => req<void>("DELETE", `/api/workspaces/${encodeURIComponent(id)}`),
};

async function backupResponse(response: Response) {
  if (!response.ok) {
    const data = await response.json().catch(() => null);
    throw new ApiError(response.status, data?.error ?? `Erreur ${response.status}`);
  }
  if (response.redirected) throw new Error("Session SSO expirée, rechargez la page.");
  return response;
}
export const backupsApi = {
  export: async (password: string) => {
    const response = await backupResponse(
      await fetch("/api/backups/export", {
        method: "POST",
        headers: { "X-Relay": "1", "Content-Type": "application/json" },
        body: JSON.stringify({ password }),
      }),
    );
    if (response.headers.get("Content-Type") !== "application/octet-stream") throw new Error("Réponse de sauvegarde invalide.");
    return response.blob();
  },
  restore: async (file: File, password: string) => {
    if (file.size > 513 * 1024 * 1024) throw new Error("Archive trop volumineuse (512 Mio maximum).");
    const encoded = btoa(Array.from(new TextEncoder().encode(password), (b) => String.fromCharCode(b)).join(""));
    await backupResponse(
      await fetch("/api/backups/restore", {
        method: "POST",
        headers: { "X-Relay": "1", "X-Backup-Password": encoded, "Content-Type": "application/octet-stream" },
        body: file,
      }),
    );
  },
};
