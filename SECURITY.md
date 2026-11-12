# Security

Report vulnerabilities privately through GitHub Security Advisories for this repository.
Do not put credentials, infrastructure exports or private captures in public issues.

Supported releases: the latest published version. Relay is for a trusted administration team,
not mutually untrusted tenants. All allowed users share the vault and machine-management
capabilities. Backup administrators can access all exported private data. Local accounts and active authentication tokens are installation access policy, preserved during content restore.

Security properties include authentication on API/session routes, same-origin browser controls,
owner-scoped recordings/workspaces, non-delegable account/device management and content backups,
location-bound encryption, pinned SSH keys after first use, bounded imports/uploads and
exclusion of private runtime data from Docker build contexts.

A security review does not establish that software is vulnerability-free. Run behind HTTPS
with local accounts, or SSO with an explicit email allowlist. Never expose guacd directly.
