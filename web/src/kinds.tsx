import { Boxes, Folder, FolderOpen, Globe, KeyRound, Monitor, ScreenShare, SquareTerminal } from "lucide-react";
import type { Kind } from "./types";

export const KINDS: Record<Kind, { label: string; long: string; color: string; Icon: typeof Monitor; defaultPort: number }> = {
  rdp: { label: "RDP", long: "Bureau à distance Microsoft (RDP)", color: "#3b8eea", Icon: Monitor, defaultPort: 3389 },
  ssh: { label: "SSH", long: "Terminal SSH", color: "#3fb950", Icon: SquareTerminal, defaultPort: 22 },
  vnc: { label: "VNC", long: "VNC", color: "#a371f7", Icon: ScreenShare, defaultPort: 5900 },
  web: { label: "Web", long: "Site web / interface d'administration", color: "#f0883e", Icon: Globe, defaultPort: 0 },
  pve: { label: "Console PVE", long: "Console Proxmox VE (noVNC)", color: "#e57000", Icon: Boxes, defaultPort: 0 },
};

export const FOLDER_COLOR = "#e3b341";
export const CREDENTIAL_COLOR = "#d29922";

export function KindIcon({ kind, size = 16 }: { kind: Kind; size?: number }) {
  const k = KINDS[kind];
  return <k.Icon size={size} color={k.color} strokeWidth={2} aria-label={k.label} />;
}

export function FolderIcon({ open = false, size = 16 }: { open?: boolean; size?: number }) {
  const I = open ? FolderOpen : Folder;
  return <I size={size} color={FOLDER_COLOR} fill={FOLDER_COLOR + "33"} strokeWidth={2} />;
}

export function CredentialIcon({ size = 16 }: { size?: number }) {
  return <KeyRound size={size} color={CREDENTIAL_COLOR} strokeWidth={2} />;
}

export function endpoint(e: { kind: Kind; host: string; port: number; url: string; settings?: Record<string, unknown> }) {
  if (e.kind === "web") return e.url;
  if (e.kind === "pve") {
    const s = e.settings ?? {};
    return `${String(s.type ?? "").toUpperCase()} ${s.vmid ?? ""} · ${s.node ?? ""}`;
  }
  const def = KINDS[e.kind].defaultPort;
  return e.port && e.port !== def ? `${e.host}:${e.port}` : e.host;
}
