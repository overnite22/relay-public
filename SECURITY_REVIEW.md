# Public release review — 2026-11-12

This is a maintainer security review and automated-check record, not an independent
penetration-test certification. No audit can establish that an application has no vulnerabilities.

## Scope and trust model

The review covered the Go runtime, authentication, API routing, credential encryption,
backups/restores, recordings/workspaces, imports, attachments, SSH execution and Guacamole
bridges. Frontend handling of notes, links and downloads, Docker packaging and release
workflows were also inspected. Two independent read-only static reviews were compared.

Authorized users are trusted administrators of the shared inventory and vault. They can
connect to configured network targets, reveal shared credentials and run SSH commands.
This product does not isolate mutually untrusted tenants. Instance backup administrators
are explicitly privileged to access all archived user data. Device bearer tokens are not
permitted to export or restore backups.

## Corrections included in the first public release

- Full-instance backup and restore now require an explicit SSO administrator allowlist.
  All mobile bearer tokens are denied, including administrator-owned devices. This also
  closes an archive-based bypass of the device-pairing authorization boundary.
- Redirected RDP drives use a collision-resistant hash of the canonical user identity.
- Interactive SSH sessions verify and pin the host key before credentials reach guacd;
  guacd must verify that same key during its own connection. Changed keys fail closed.
- New RDP connections validate the server certificate by default.
- RDM imports bound XML nodes, connection counts, folder counts and logical path depth;
  text accumulation avoids repeated copying.
- The Docker context is an allowlist. Runtime data, transfer files and private exports
  cannot be included by a broad source-copy instruction.
- Go was upgraded to 1.27.4 to address a reachable net/http advisory. Cryptography
  dependencies were updated. Release actions are pinned to commit SHAs.

## Privacy and release packaging

The public repository starts from a new root commit. Private development history is not
published. Personal infrastructure names, addresses and accounts were replaced with
reserved example values. No deployment environment, data volume, RDM export, capture or
credential file is included in the public source or Docker build context.

## Validation

- Formatting, go vet and the full Go test suite.
- Production web build and npm dependency audit.
- govulncheck of reachable Go dependencies.
- Gitleaks review of the private baseline history and the clean public export.
- Trivy HIGH/CRITICAL vulnerability scans of the runtime image, guacd 1.6.0 and
  oauth2-proxy 7.15.6: none detected at the time of review.
- Fresh-volume Compose startup with a disposable OIDC discovery service; unauthenticated
  API rejection, login redirect, public PWA metadata and volume initialization checks.
- Browser session through real guacd against a disposable multi-key SSH server.
- Regression tests for backup permissions, drive identity separation, changed SSH keys
  and excessive RDM paths.

The Codex Security MCP scanner could not start because its bundled Python helper was
unavailable. Its automated repository scan did not run. Manual reviews and the other
checks above were completed separately; they are not a substitute for that tool's report.

## Operational limitations

SSH uses trust on first use: the initial contact must occur on a trusted network. Notes,
attachments, command output and recordings are not encrypted individually at rest;
protect the data volume and use disk encryption where appropriate. Vault encryption
cannot protect against a compromised application host holding the master key.

The provided installation requires a configured OIDC provider and an HTTPS reverse proxy.
Only the SSO proxy binds a host port, on loopback. Never expose guacd or development auth.
Real OIDC-provider configuration and downstream SSH/RDP endpoints remain deployment-specific.

Follow SECURITY.md for private vulnerability reports and keep Relay and sidecar images updated.
