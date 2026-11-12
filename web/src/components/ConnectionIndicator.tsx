import type { ConnectionTest, Entry } from "../types";

export const canTestConnection = (entry: Entry) => entry.kind === "ssh" || entry.kind === "rdp" || entry.kind === "vnc";

export const connectionStatusLabels: Record<ConnectionTest["status"], string> = {
  reachable: "Port accessible",
  refused: "Connexion refusée",
  timeout: "Délai d’attente dépassé",
  dns_error: "Nom d’hôte non résolu",
  unreachable: "Hôte ou réseau inaccessible",
  error: "Impossible d’ouvrir une connexion TCP",
};

export function connectionAddress(entry: Entry) {
  const host = entry.host.replace(/^\[(.*)\]$/, "$1");
  return `${host.includes(":") ? `[${host}]` : host}:${entry.port}`;
}

export function ConnectionIndicator({ entry, results }: { entry: Entry; results: Record<string, ConnectionTest> }) {
  if (!canTestConnection(entry)) return null;
  const result = results[connectionAddress(entry)];
  const state = !result ? "unknown" : result.status === "reachable" ? "reachable" : "failed";
  const label = state === "unknown" ? "Port de session en attente de vérification"
    : state === "reachable" ? "Port de session accessible depuis Relay" : "Port de session inaccessible depuis Relay";
  const detail = result ? `${label} · ${new Date(result.checkedAt).toLocaleTimeString("fr-FR")}` : label;
  return <span className={`connection-indicator ${state}`} role="img" aria-label={label} title={detail} />;
}
