# Relay Desktop (Windows)

Native Windows client for Relay, written in Rust with a [Slint](https://slint.dev)
interface (no web view). Shipped as an MSI.

## Data sources

Like RDM data sources, several can be declared and switched from the ribbon:

- **Relay server**: inventory, vault, folder credential inheritance and notes
  come from a Relay server, authenticated with a **device token** created in
  Relay web → Administration → Appareils. The token is stored in the Windows
  Credential Manager, never in the settings file. HTTPS is required (except
  loopback). If an SSO proxy protects Relay, it must let `/api/` requests with
  a device token through.
- **Local database**: a single SQLite file, no server needed. Credential
  secrets are encrypted with AES-256-GCM using a key derived from the master
  password (Argon2id, 64 MiB, 3 passes). Inventory (names, hosts, notes) is
  stored in clear, like an RDM local data source.

Settings: `%APPDATA%\Relay\settings.json` (source list only).

## Sessions

Current version: RDP through `mstsc` (credential passed as a session-only
`TERMSRV/<host>` entry removed after 30 s, never overwriting an existing one),
SSH through the Windows OpenSSH client, web consoles in the default browser.
Embedded RDP/SSH tabs are the next steps (see `PLAN.md`).

Keyboard: ↑/↓ move, →/← expand/collapse, Enter opens, F2 edits, Del deletes.

## Build

```sh
cargo test -p relay-desktop-core                       # any platform
cargo build --release -p relay-desktop                 # Windows (MSVC)
wix build app/wix/Relay.wxs -arch x64 -d Version=… -d ExePath=…   # MSI, WiX 5
```

CI (`.github/workflows/desktop.yml`) builds the MSI on `windows-latest`.
