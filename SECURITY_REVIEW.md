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
are explicitly privileged to access all archived user data. Local users/sessions and paired devices are installation access policy and are preserved by content restore, including when reading legacy archives. Device bearer tokens are not
permitted to export or restore backups.

## Corrections included in the first public release

- Content backup and restore require a local administrator or an explicit SSO administrator allowlist.
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

## Local authentication added before publication

Local accounts are the default installation; SSO is optional. Passwords use fixed-parameter
Argon2id (64 MiB, three iterations, one lane), with random salts. Browser session tokens are
random, hashed in storage, time-limited and revocable. Cookies are HttpOnly/SameSite and
Secure outside explicitly configured loopback HTTP. Initial setup requires a server-held code.

Independent reviews of the local-account changes led to additional corrections:

- Account/role changes, mobile pairing and backup snapshot/restore recheck session validity
  and administrator authority within the database transaction, after reading request bodies.
- Password changes cannot race a previously verified password into a new browser session.
- Login/setup requests do not hold the content-restore lock; their body-read time is bounded.
- Login admission cannot be exhausted with arbitrary unknown email strings; existing accounts
  have separate budgets and expensive password verification has bounded concurrency.
  Throttled and incorrect credentials have the same response and Argon2 workload.
- Content backups do not export or restore mobile authentication tokens. Legacy archives'
  device tokens are ignored, preventing revival of revoked devices.
- The final active local administrator cannot be disabled or demoted. Password or role
  changes and account disabling revoke browser sessions and device tokens.

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
- Trivy HIGH/CRITICAL vulnerability scans of Relay, the rebuilt guacd (Alpine 3.24),
  and optional oauth2-proxy 7.15.6: no high/critical advisories detected.
  The old official guacd base was EOL; an exploratory
  Debian build also exposed unresolved dependency advisories. Neither is recommended.
- Fresh-volume Compose startup with a disposable OIDC discovery service; unauthenticated
  API rejection, login redirect, public PWA metadata and volume initialization checks.
- Browser tests for bootstrap using the documented CLI code, local login/logout,
  account creation, non-admin denial, password changes and a real guacd SSH session.
- RDP desktop/redirected-drive upload-download against disposable xrdp and VNC against
  TigerVNC, using the rebuilt sidecar. These are Linux test endpoints, not Windows.
- Regression tests for backup permissions, drive identity separation, changed SSH keys
  and excessive RDM paths.

The Codex Security MCP scanner could not start because its bundled Python helper was
unavailable. Its automated repository scan did not run. Manual reviews and the other
checks above were completed separately; they are not a substitute for that tool's report.

The maintained sidecar includes a backport of Apache GUACAMOLE-2328 for FreeRDP 3.31+
GDI initialization and installs its custom channel plugins into the system FreeRDP plugin
directory. These changes were exercised by the RDP desktop and file-transfer checks.

## Operational limitations

SSH uses trust on first use: the initial contact must occur on a trusted network. Notes,
attachments, command output and recordings are not encrypted individually at rest;
protect the data volume and use disk encryption where appropriate. Vault encryption
cannot protect against a compromised application host holding the master key.

The local installation needs no identity provider. Remote access requires HTTPS; loopback
HTTP is supported for initial setup. The optional SSO installation requires an OIDC provider
and HTTPS proxy. Host ports bind only to loopback. Never expose guacd or development auth.
Real OIDC-provider configuration and downstream endpoints remain deployment-specific.

Follow SECURITY.md for private vulnerability reports and keep Relay and sidecar images updated.
