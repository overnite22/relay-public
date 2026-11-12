import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import {AuthGate} from "./components/AuthGate";
import { ErrorBoundary } from "./components/ErrorBoundary";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ErrorBoundary label="Relay a rencontré une erreur">
      <AuthGate><App /></AuthGate>
    </ErrorBoundary>
  </StrictMode>,
);

if ("serviceWorker" in navigator && import.meta.env.PROD) {
  const register = () => {
    navigator.serviceWorker.register("/sw.js", { updateViaCache: "none" }).catch(() => {
      // The browser menu can still offer installation.
    });
  };
  if (document.readyState === "complete") register();
  else window.addEventListener("load", register, { once: true });
}
