# Relay

Self-hosted remote connection manager with an RDM-style ribbon, folder tree and session tabs.
SSH, RDP and Proxmox consoles run in the browser. An encrypted credential vault, file transfer,
inventory sync, notes and recordings are included.

**Local accounts work out of the box. SSO is optional.** Relay is for a trusted homelab or
administration team sharing an inventory and vault. It does not isolate mutually untrusted tenants.

## Install with Docker Compose

Requires Docker Engine and Compose v2. No identity provider or application build is required.

1. Download `compose.yaml` and `.env.example` from a tagged release.
2. Copy `.env.example` to `.env`, then run:

   ```sh
   docker compose pull
   docker compose up -d
   docker compose exec relay /relay setup-token
   ```

3. Open `http://localhost:8080` on the Docker host. Enter the one-time code and create the first
   administrator with a password of at least 12 characters. The code file is removed after setup.
4. Administration → **Comptes locaux** creates users, assigns administrators, resets passwords
   and disables accounts. Users can change their own password and sign out from the title bar.

For a remote Docker host, use an SSH port forward for initial access, or configure an HTTPS
reverse proxy. Set `RELAY_PUBLIC_ORIGIN=https://relay.example.com` and forward WebSockets,
`Host` and `X-Forwarded-Proto` to `127.0.0.1:8080`. The published port binds to loopback by default.
HTTP session cookies are allowed only for a configured loopback origin. Use HTTPS for remote access.

Named volumes are initialized with the required ownership. The data volume contains the generated
vault master key and SQLite database. Keep it: losing the master key makes the vault unreadable.

## Local account security

- Argon2id password hashes, random salts and a minimum 12-byte password.
- Random browser sessions, stored as hashes, expiring after 12 hours; HttpOnly/SameSite cookies
  and Secure cookies for HTTPS. Signing out revokes the browser session on the server.
- Password changes, account disabling and role changes revoke browser sessions and paired devices.
- Per-account login throttling and bounded concurrent password verification. Incorrect and
  throttled logins return the same error and perform the same Argon2 work.
- The last active administrator cannot be disabled or demoted.
- Account creation, role updates and mobile pairing recheck the current session inside the database
  mutation, including requests that were already in flight when an account was revoked.

All allowed users can use the shared vault and administer configured machines. Administrators
also manage accounts and instance content backups. A paired mobile device cannot manage accounts,
pair other devices, or export/restore backups, including when its owner is an administrator.
There is no public self-registration. Keep at least two administrator accounts for recovery.

## Optional SSO

Use `compose.sso.yaml` with Compose **2.24.4 or newer**. Copy `.env.sso.example` to `.env`, set
an OIDC issuer/client ID/client secret, explicit `RELAY_ALLOWED_USERS` and `RELAY_ADMIN_USERS`,
and generate `OAUTH2_PROXY_COOKIE_SECRET` with `openssl rand -base64 32`. Protect `.env` with
`chmod 600 .env`. Register `${RELAY_PUBLIC_ORIGIN}/oauth2/callback` with the provider.

```sh
docker compose -f compose.yaml -f compose.sso.yaml pull
docker compose -f compose.yaml -f compose.sso.yaml up -d
```

Entra ID, Keycloak or Authentik can provide OIDC. An HTTPS proxy should forward to
`127.0.0.1:4180`. In this mode Relay validates the SSO session via oauth2-proxy; the local login
screen and local account management are disabled. SSO and local authentication are alternative
installation modes. Switching modes does not delete existing accounts or inventory.

For an existing oauth2-proxy, set `RELAY_AUTH_MODE=userinfo` and `RELAY_OAUTH2_USERINFO_URL`
and connect Relay to its network. Trusted-header authentication also requires explicit
`RELAY_TRUSTED_PROXIES` CIDRs. Preserve the public PWA metadata routes in
`deploy/nginx-relay-pwa.conf`, and pass `/api/` through for mobile bearer authentication.
Relay authenticates every API request itself.

## Features

- SSH, RDP, VNC and Proxmox consoles in tabs, plus quick connections.
- Reusable AES-256-GCM encrypted credentials and per-protocol folder inheritance.
- SFTP, per-user redirected RDP drive, clipboard and fullscreen.
- NetBox/Proxmox sync, RDM import preview and duplicate merging.
- Parallel SSH commands with bounded output, timeout and cancellation.
- Markdown notes, attachments, private workspaces and recordings.
- TCP availability checks, filters and scoped retests.
- Password-encrypted content backups, including completed recordings.
- Local accounts or SSO, revocable mobile pairing and installable PWA.

## Trust and data protection

SSH uses trust on first use and pins server keys for interactive sessions and commands.
Verify the first contact on a trusted network. Changed keys block subsequent connections.
The command runner and guacd use the same algorithm preference when a host offers several keys.
After importing older data, an existing pin using a different algorithm may require manual
fingerprint verification before resetting that pin. New RDP entries validate certificates by
default; explicit existing overrides are retained.

Notes, attachments, command output and recordings are ordinary files/data in the protected volume.
Use disk encryption when needed. Vault encryption cannot protect against compromise of the host
that holds its master key. Backup administrators can export every user's private captures/workspaces.

Content backups are separately password-encrypted. Restore replaces inventory, vault and completed
recordings, and requires closing sessions and waiting for commands/syncs. The archive limit is
512 MiB uncompressed. **Local accounts, browser sessions and mobile pairings are installation
access policy: content restore preserves them.** Legacy archives' device tokens are ignored.
Use a protected, consistent host-volume backup to preserve account policy for disaster recovery.

RDP transfer directories use a SHA-256 identity namespace. Legacy files must be moved by an
operator after checking that the old directory name was not shared by multiple identities.
Never expose guacd or development authentication to an untrusted network.

## Images and updates

Relay runs as nonroot in a distroless image. The supplied guacd image builds Apache Guacamole
1.6.0 from checksum-verified source on a maintained Alpine base with system FreeRDP 3 libraries
and an included Apache PostConnect compatibility backport (GUACAMOLE-2328).
The older official guacd image's obsolete base is not used by the recommended installation.
Both images target amd64 and arm64 and include SBOM/build provenance and license notices.

Pin `RELAY_IMAGE` to a release version for reproducible updates. Export a content backup and keep
a protected host-volume backup, then run `docker compose pull && docker compose up -d`. Keep
previous image digests and backups for rollback. Never delete named volumes during updates.

## Build and develop

Requires Go 1.27.4 or newer and Node.js 22:

```sh
cd web && npm ci && npm run build && cd ..
go test ./...
docker compose -f compose.yaml -f compose.build.yaml build
```

Development-only authentication must bind to loopback:

```sh
RELAY_AUTH_MODE=dev RELAY_DEV_USER=developer@example.test RELAY_LISTEN=127.0.0.1:8080 go run ./cmd/relay
```

See `SECURITY_REVIEW.md` for review coverage and limitations, and `SECURITY.md` for private reports.
Apache-2.0 license; see `LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES.md`.
Relay is independent of Devolutions and Remote Desktop Manager.
