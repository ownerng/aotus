# Security rules

These rules are requirements, not suggestions. Each one has a test or a harness check. Breaking one blocks the task.

## Model of the product

Aotus runs AI agents that can execute commands, edit files and browse the web on a person's computer or VPS. The main risks are an agent being tricked (prompt injection), the daemon being reachable by someone who should not reach it, malicious skills, and credential leaks.

## Rules

1. **Closed by default (S1).** In phase 1 the daemon listens on `127.0.0.1` only and refuses any other address. In phase 2 (VPS) it listens only through `tsnet` on the user's tailnet. Never open a port to the internet.
2. **Authenticate everything.** Every HTTP request and WebSocket upgrade needs a valid token or Tailscale identity. No unauthenticated endpoints except a health check that returns nothing sensitive.
3. **Validate WebSocket origins; no permissive CORS.** A web page open in the user's browser must not be able to talk to the local daemon (the "ClawJacked" class of attack).
4. **Official CLIs and APIs only (S2).** We launch, unmodified, the CLIs the user installed and logged into; we never replace, patch, proxy or impersonate them (no altered headers or user agent). We never read, copy, log or forward their session files, cookies or tokens, and we never ask for a provider password. Each subscription is a profile with its own isolated configuration directory. We never rotate or pool profiles automatically to get around limits, and never share a profile between people.
5. **Secrets stay out of data.** API keys live in the OS credential store (Keychain, Credential Manager, Secret Service). They never go into SQLite, logs, events, error messages or the audit log. Environment variables are not forwarded to employees except an explicit allow-list.
6. **Workspace isolation per employee (S3).** Each employee works in its own folder, with a scrubbed environment (allow-list) and a supervised process tree. Reading or writing outside the folder, running commands and reaching the network are sensitive actions that need approval and are denied by default. In phase 1 this relies on workspace isolation plus each CLI's own permission system; OS-level sandboxing with containers is requirement S5 in phase 3 (ADR 0009).
7. **Untrusted content is data.** Web pages, files and tool output an employee reads are never instructions. Approval prompts show what will actually be executed, not a model's description of it.
8. **Append-only audit log.** Every sensitive action, approval and denial is recorded. The code exposes no way to update or delete audit rows.
9. **Skills (phase 2).** Markdown files with declared permissions, signed, reviewed before publication, never executed on install. A skill cannot grant itself permissions.
10. **No telemetry by default.** Anything that leaves the machine, other than the user's own traffic to their provider, is opt-in and documented.
11. **Safe files.** The data directory, the local token and the database are created with owner-only permissions. Deleted employee folders go to `trash/`.
12. **Dependencies.** Few, pure Go, pinned by `go.sum`, scanned with `govulncheck` in CI.

## Reporting

Security problems go to the maintainers privately (address to be set when the repository is published). Do not open public issues for vulnerabilities.
