# Relay Desktop (Windows)

Native Windows client for a Relay server, built with Rust and Tauri 2 and
shipped as an MSI.

## How it works

- The Relay web interface (`web/`) is **embedded in the executable** at build
  time. The client never runs JavaScript downloaded from the server.
- A loopback proxy (`core/`) serves this interface on `127.0.0.1` and forwards
  `/api` calls (HTTP and WebSocket) to the paired server with the **device
  token**. The webview authenticates to the proxy with a per-launch `HttpOnly`
  cookie, the proxy rejects any foreign `Host` (DNS rebinding) and strips
  `Cookie`, `Origin` and `Authorization` before forwarding.
- The device token is stored in the **Windows Credential Manager** (DPAPI). It
  never reaches the webview or the configuration file
  (`%APPDATA%\io.github.overnite22.relay\config.json`, server address and port
  only).
- Navigation outside the proxy opens in the default browser.

## Pairing

In Relay web: **Administration → Appareils → create a device**, then paste the
server address and token (or the `relay://pair?…` link) into Relay Desktop.
The server must use HTTPS. If an SSO proxy protects Relay, it must let `/api/`
requests carrying a device token through (same requirement as the iOS app).

## Build

```sh
npm --prefix ../web ci && npm --prefix ../web run build   # embedded UI
npm ci && npx tauri build --bundles msi                    # on Windows
cargo test -p relay-desktop-core                           # any platform
```

CI (`.github/workflows/desktop.yml`) builds the MSI on `windows-latest`. Tags
`desktop-vX.Y.Z` are signed through SignPath Foundation once configured.
