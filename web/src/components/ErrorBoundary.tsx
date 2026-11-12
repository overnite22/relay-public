import { AlertTriangle } from "lucide-react";
import { Component, type ReactNode } from "react";

/** Affiche une erreur lisible au lieu d'une page vide si un composant plante. */
export class ErrorBoundary extends Component<{ children: ReactNode; label?: string; onReset?: () => void }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error) {
    console.error("Relay :", error);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="crash">
        <AlertTriangle size={28} color="var(--warn)" />
        <h2>{this.props.label ?? "Cette partie de Relay a rencontré une erreur"}</h2>
        <pre className="mono small">{this.state.error.message}</pre>
        <div style={{ display: "flex", gap: 8, justifyContent: "center" }}>
          {this.props.onReset && (
            <button
              className="btn"
              onClick={() => {
                this.setState({ error: null });
                this.props.onReset?.();
              }}
            >
              Fermer
            </button>
          )}
          <button className="btn primary" onClick={() => location.reload()}>
            Recharger la page
          </button>
        </div>
      </div>
    );
  }
}
