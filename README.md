# Relay

Self-hosted remote connection manager with an RDM-style ribbon, folder tree and session tabs.
SSH, RDP and VNC run in the browser through Apache Guacamole. Proxmox consoles, NetBox/Proxmox
inventory sync, an encrypted credential vault, file transfer, notes and recordings are included.

Relay is intended for a trusted homelab or administration team. Authorized users share the
inventory, credential vault and administrative actions. It is not a multi-tenant platform.
Workspaces and recordings are private to their owner, except that explicitly configured
instance backup administrators can export the entire instance.

## Install with Docker Compose

Prerequisites: Docker Engine with Compose v2, an OpenID Connect provider, and an HTTPS reverse
proxy. Entra ID, Keycloak and Authentik can provide OIDC. No application build is required.

1. Download `compose.yaml` and `.env.example` from a tagged release.
2. Copy `.env.example` to `.env`, set its fields, and protect it with `chmod 600 .env`.
3. Generate the cookie secret with `openssl rand -base64 32`.
4. Register `https://relay.example.com/oauth2/callback` as an OIDC redirect URI, replacing the
   example origin with your actual public URL.
5. Run:

   ```sh
   docker compose pull
   docker compose up -d
   ```

6. Point the HTTPS reverse proxy at `127.0.0.1:4180` on the Docker host, forwarding WebSockets,
   `Host` and `X-Forwarded-Proto`. A containerized proxy can instead join the Compose backend
   network and forward to `sso:4180`.

The SSO proxy is the only published service and binds to loopback. Relay verifies every API
request itself, including mobile bearer tokens. Named volumes are initialized with the required
owners. A master key is generated in the data volume on first boot. Retain that volume and
export encrypted backups before updates.

`RELAY_ALLOWED_USERS` is the comma-separated list of allowed emails.
`RELAY_ADMIN_USERS` grants full-instance backup/restore access to SSO administrators.
A paired device cannot export or restore instance backups, even if its owner is an admin.
Never publish guacd or use development authentication on an untrusted network.

## Existing SSO

You may reuse an existing oauth2-proxy with Relay and guacd. Set
`RELAY_OAUTH2_USERINFO_URL` to its `/oauth2/userinfo` endpoint and join the required network.
Trusted-header authentication also requires an explicit `RELAY_TRUSTED_PROXIES` CIDR.

For an externally managed proxy, preserve the PWA metadata exception in
`deploy/nginx-relay-pwa.conf`. Pass `/api/` through to Relay for mobile authentication;
Relay enforces authentication on every API route.

## Features

- SSH, RDP, VNC and Proxmox consoles in tabs, plus quick connections.
- Reusable AES-256-GCM encrypted credentials and per-protocol folder inheritance.
- SFTP, per-user redirected RDP drive, clipboard and fullscreen.
- NetBox/Proxmox sync, RDM import preview and duplicate merging.
- Parallel SSH commands with bounded output, timeout and cancellation.
- Markdown notes, attachments, private workspaces and recordings.
- TCP availability checks, filters and scoped retests.
- Password-encrypted instance backups, including completed recordings.
- SSO, revocable mobile pairing and installable PWA.

## Trust and data protection

SSH host keys use trust on first use, then are pinned for interactive sessions and commands.
Verify the first connection on a trusted network. Changed keys block subsequent connections.
RDP certificate validation is enabled by default for new entries; explicit existing settings
are retained. Self-signed lab endpoints require an explicit opt-out.

The vault encrypts credentials with a master key. Notes, attachments, command output and
recordings are ordinary files/data in the protected volume. Use host disk encryption when
needed. Instance backups are separately encrypted with their archive password. Losing that
password makes an archive unrecoverable.

Restore replaces instance data and requires closing sessions and waiting for commands/syncs.
The archive limit is 512 MiB uncompressed. Backup administrators can access all private captures,
workspaces, pairings and credentials included in the archive.

RDP transfer directories now use a SHA-256 identity namespace. Legacy files must be moved by an
operator after checking that the old directory name was not shared by multiple identities.

## Update and rollback

Pin `RELAY_IMAGE` to a version for reproducible updates. Export an encrypted backup, then run
`docker compose pull && docker compose up -d`. Keep the previous image and data backup for
rollback. Never delete named volumes during updates.

## Build and develop

```sh
cd web && npm ci && npm run build && cd ..
go test ./...
docker compose -f compose.yaml -f compose.build.yaml build relay
```

Development-only authentication must listen on loopback:

```sh
RELAY_AUTH_MODE=dev RELAY_DEV_USER=developer@example.test RELAY_LISTEN=127.0.0.1:8080 go run ./cmd/relay
```

Requires Go 1.27.4 or newer and Node.js 22. Release images target amd64 and arm64, with SBOM and
build provenance. The runtime image runs as nonroot, has no shell, and includes dependency
license notices under `/licenses`.

## License

Apache-2.0. See `LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES.md`.
Relay is independent of Devolutions and Remote Desktop Manager.
