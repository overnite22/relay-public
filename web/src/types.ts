export type Kind = "rdp" | "ssh" | "vnc" | "web" | "pve";

export interface Folder {
  credentials: Partial<Record<Kind,string|null>>;
  id: string;
  parentId: string | null;
  name: string;
  createdAt: string;
  updatedAt: string;
}

export interface Entry {
  id: string;
  folderId: string | null;
  name: string;
  kind: Kind;
  host: string;
  port: number;
  url: string;
  username: string;
  credentialId: string | null;
  settings: Record<string, unknown>;
  notes: string;
  favorite: boolean;
  sourceId: string | null;
  externalId: string;
  createdAt: string;
  updatedAt: string;
}

export type EntryInput = Omit<Entry, "id" | "createdAt" | "updatedAt" | "sourceId" | "externalId">;

export interface Credential {
  id: string;
  name: string;
  username: string;
  domain: string;
  notes: string;
  hasPassword: boolean;
  hasPrivateKey: boolean;
  hasPassphrase: boolean;
  hasTotp: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface CredentialInput {
  name: string;
  username: string;
  domain: string;
  notes: string;
  password?: string;
  privateKey?: string;
  passphrase?: string;
  totp?: string;
}

export interface AuditEvent {
  id: number;
  at: string;
  user: string;
  action: string;
  target: string;
  detail: string;
}

export interface Tree {
  folders: Folder[];
  entries: Entry[];
}

export type Selection = { type: "folder"; id: string } | { type: "entry"; id: string } | { type: "root" };

export interface QuickTarget {
  kind: "ssh" | "rdp";
  host: string;
  port: number;
  username: string;
  domain: string;
  credentialId: string | null;
  record: boolean;
}

export interface SessionTab {
  key: string;
  entryId: string;
  name: string;
  kind: Kind;
  /** Onglet outil (pas une session) : exécution de commande sur plusieurs serveurs. */
  tool?: "command" | "availability";
  scopeFolderId?: string | null;
  record?: boolean;
  quick?: { target: QuickTarget; token: string };
  targets?: string[];
}

export type SourceKind = "proxmox" | "netbox";

export interface SourceOptions {
  sshCredentialId?: string;
  rdpCredentialId?: string;
  guestEntries?: boolean;
  nodeEntries?: boolean;
  devices?: boolean;
  vms?: boolean;
}

export interface Source {
  id: string;
  kind: SourceKind;
  name: string;
  url: string;
  tokenId: string;
  hasSecret: boolean;
  verifyTls: boolean;
  folderId: string | null;
  options: SourceOptions;
  intervalMin: number;
  lastSyncAt: string | null;
  lastStatus: string;
}

export interface SourceInput {
  kind: SourceKind;
  name: string;
  url: string;
  tokenId: string;
  secret?: string;
  verifyTls: boolean;
  options: SourceOptions;
  intervalMin: number;
}

export interface ImportPlan {
  folders: { path: string[] }[];
  entries: { name: string; kind: Kind; host: string; port: number; url: string; username: string; path: string[]; credential: string; inlineSecret: boolean; rdmType: string }[];
  credentials: { name: string; username: string; domain: string; hasPassword: boolean; encryptedSecret: boolean; hasPrivateKey: boolean }[];
  skipped: { name: string; type: string; reason: string }[];
  warnings: string[];
}

export type RunStatus = "pending" | "running" | "ok" | "failed" | "error" | "canceled";

export interface RunResult {
  entryId: string;
  name: string;
  address: string;
  status: RunStatus;
  exitCode: number | null;
  output: string;
  truncated: boolean;
  error: string;
  durationMs: number;
  fingerprint: string;
  newHostKey: boolean;
}

export interface CommandRun {
  id: string;
  at: string;
  finishedAt: string | null;
  user: string;
  command: string;
  sudo: boolean;
  targets: number;
  ok: number;
  failed: number;
  results?: RunResult[];
}

export interface SavedCommand {
  id: string;
  name: string;
  command: string;
  sudo: boolean;
}

export interface Recording {
  id: string;
  name: string;
  kind: Kind;
  at: string;
  size: number;
  limited: boolean;
}

export interface Attachment {
  id: string;
  name: string;
  size: number;
  createdAt: string;
}

export interface ConnectionTest {
  address: string;
  ip?: string;
  status: "reachable" | "refused" | "timeout" | "dns_error" | "unreachable" | "error";
  durationMs: number;
  checkedAt: string;
}

export interface Workspace { id: string; name: string; entryIds: string[]; }
