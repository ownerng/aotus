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

## Remote access threat model

Phase 2 lets the daemon serve its API on the tailnet (`aotusd --tailnet`, ADR 0014, `docs/VPS.md`). This section says who can reach what, what each attacker gets, and which test proves each defense. It was written for P2-009 (2026-10-23); `govulncheck ./...` (and with `-tags desktop,gtk3` for the window) found no vulnerabilities that day.

### What is exposed, to whom

| Way in | Who it serves | Identity comes from |
| --- | --- | --- |
| Loopback listener (`127.0.0.1`) | Programs on this computer that can read the token in the data directory | The bearer token. Whoever holds it is the owner. |
| Tailnet listener (only a Tailscale address, port 7843) | People on the allow-list, and the owner | Tailscale's LocalAPI `WhoIs` on the connection's peer address, looked up when the connection is accepted. |

The two never mix: on the tailnet listener the token is ignored, and on loopback a Tailscale identity cannot be claimed. Every request goes through the same guards, in this order. The Host must be one of this daemon's names (against DNS rebinding). A request with an `Origin` header is refused unless allow-listed (against web pages). Then come the token or the allow-list, and finally the role: owner, or a guest limited to the profiles shared with them. Failed attempts are counted per caller.

### Attackers and what they get

| Attacker | What they can do | What stops the rest | Tests |
| --- | --- | --- | --- |
| **A peer that is not on the tailnet** (the internet, the LAN, the VPS provider's network) | Nothing. Nothing listens on a public or LAN interface, and a connection whose peer Tailscale cannot tie to a person is closed before HTTP is read. | `netaccess.CheckTailnetAddr` binds only Tailscale ranges; the identifying listener drops unknown peers. Tagged devices (no person) are refused. | `TestTailnetListenerNeverBindsElsewhere`, `TestNoOptionListensOnAWildcard`, `TestTailnetListenerRefusesUnidentifiedPeer`, `TestTaggedDeviceIsRefused`, `TestTokenNotAcceptedOnTailnetListener` |
| **A malicious tailnet peer** (another person or device on the same tailnet, not on the allow-list) | Gets `403 not_allowed`, and each refusal is audited with their login and device. After `MaxFailures` (10) refusals in a minute they get `429` and are no longer audited one by one, so they cannot flood the audit log, the logs or the disk. | The allow-list; identity only from the listener, never from a header; per-login failure limit. | `TestTailnetCallerOffAllowListIsRefusedAndAudited`, `TestIdentityHeadersAreIgnored`, `TestFailedAttemptsAreRateLimited` |
| **Someone holding a copy of the local token** on another machine | Nothing remotely: the token is not accepted on the tailnet listener, whatever Host header is sent. | The token only works on loopback. | `TestTokenNotAcceptedOnTailnetListener`, `TestTokenAndIdentityDoNotCrossOver` |
| **A web page in the user's browser** (on the PC, or the tailnet name typed in a browser) | Nothing. Requests with an `Origin` are refused on both listeners. DNS rebinding fails on the Host check. No response carries CORS headers. | Origin and Host rules. | `TestRebindingAndOriginStillRefusedOnTailnet`, `TestRejectsForeignWebSocketOrigin`, `TestRequiresToken` |
| **A stolen tailnet device** of an allow-listed person (laptop left unlocked, phone taken) | Everything that person may do: as a guest, use the employees on the profiles shared with them, read every conversation, memory and the audit log, and answer approvals for those employees. As the owner, everything. | Remove the device in the Tailscale admin console (it loses its identity at once), or `aotus access deny LOGIN`. Every action is audited with the device name, so what it did can be traced. | `TestAuditRecordsTheCaller` |
| **A stolen allow-listed login** (the person's Tailscale/identity-provider account) | The same as a stolen device, from any device the attacker adds to the tailnet. | Tailscale device approval, the identity provider's 2FA, and the allow-list (`aotus access deny`). Out of Aotus' hands, see Accepted risks. | — |
| **A compromised VPS** (shell as the `aotus` user or root) | Everything on it: the CLIs' session files (the subscriptions' logins), the employees' folders, memory, history and the audit log. With root, also the credential store and the token. | Nothing inside Aotus can stop the host's own root. Keep the server patched, SSH keys only, the service user without sudo and with `NoNewPrivileges` (`docs/VPS.md`). Revoke the CLIs' sessions from the providers' sites if it happens. | `TestSystemdUnitHasNoSecrets` (the unit holds no secret) |
| **A prompt-injected employee** | Whatever the service user can do once the user approves it. Commands, files outside the folder and the network ask first (rule 6). | Approvals and workspace isolation now; an OS sandbox is S5 (phase 3). | `internal/permissions` tests |

Secrets stay out of everything a remote caller can reach or cause to be written. With the tailnet enabled, refused and served remote traffic, wrong tokens and a failing API turn leave neither the local token nor an API key in the logs, the audit log, the turn's events or any file of the data directory (`TestNoSecretsInLogsWithTailnetEnabled`).

### Accepted risks

1. **A valid token is never rate limited.** Only failures are counted, so a local process running as another user cannot lock the owner out of their own daemon by sending wrong tokens. Guessing the token is not a practical attack: it is 256 random bits, compared in constant time. After the limit, wrong tokens are answered with 429 at no cost to the daemon.
2. **Rate limiting is per login on the tailnet and per address on loopback, in memory.** A restart forgets the counts, and an attacker with many tailnet logins gets 10 tries per minute with each. Every login needs to be a real person on the owner's tailnet, so this is bounded by the tailnet's own membership.
3. **Guests see more than they can use.** Anyone on the allow-list can read every employee's history, memory and pending approvals, shared profile or not. `docs/VPS.md` and the sharing notice say to add only people trusted with that. Splitting reading by profile is a candidate for phase 5 (F15, roles).
4. **A guest's "always allow" creates a lasting permission.** Answering an approval with `remember: exact` or `kind` stores a grant for that employee, while editing grants directly is owner-only. It is limited to employees on profiles shared with that guest, who could approve each request anyway. The owner sees and can remove it (`GET`/`DELETE /employees/{id}/grants`).
5. **The tailnet is trusted to say who is calling.** If the owner's Tailscale account or a tailnet admin is compromised, an attacker can present an allow-listed identity. Aotus relies on Tailscale's device approval, ACLs and the identity provider's 2FA, and documents them in `docs/VPS.md`.
6. **A compromised VPS is game over for that server's subscriptions.** The CLIs keep their sessions in their own files, which Aotus never reads but cannot protect from the host's root. The only answer is hardening the host and revoking sessions at the provider.
7. **Traffic on the tailnet listener is plain HTTP.** It travels inside WireGuard, which encrypts and authenticates it end to end between the two devices. Adding TLS on top would add certificates to manage and no protection against the attackers above.

## Reporting

Security problems go to the maintainers privately (address to be set when the repository is published). Do not open public issues for vulnerabilities.
