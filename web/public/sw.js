// Relay needs the server for sessions and authentication. No application data
// is put in Cache Storage, including API responses or pages returned by SSO.
self.addEventListener("install", (event) => {
  event.waitUntil(self.skipWaiting());
});
self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});
self.addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);
  if (event.request.mode !== "navigate" || url.origin !== self.location.origin) return;
  event.respondWith(fetch(event.request).catch(() => new Response(
    `<!doctype html><html lang="fr"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="theme-color" content="#2b7de9"><title>Relay — Hors connexion</title><style>body{background:#1b1c1f;color:#e4e4e7;font:16px system-ui;display:grid;place-items:center;min-height:95vh;margin:0}main{max-width:420px;padding:24px}a{color:#70b5ff}</style><main><h1>Relay</h1><p>Le serveur est injoignable. Vérifiez votre connexion réseau.</p><a href="/">Réessayer</a></main></html>`,
    { status: 503, headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" } }
  )));
});
