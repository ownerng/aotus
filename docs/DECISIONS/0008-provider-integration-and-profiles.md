# 0008 - Aotus is an interface over the official CLIs; subscriptions are profiles

Status: accepted
Date: 2026-10-09

## Context

Aotus does not replace any provider's CLI or app. It is a polished interface that lets a person create agents ("employees") and talk to them while the official CLIs do the work in the background, on the person's own machine and with the person's own subscriptions. `docs/research/provider-terms.md` (read 2026-10-09) shows that providers differ: some welcome third-party frontends (OpenAI Codex app-server, xAI headless/ACP), Anthropic allows the end user to sign in to the unmodified Claude Code but forbids third parties from routing requests through Free, Pro or Max credentials, and the status of headless and SDK use on a subscription is ambiguous and has changed several times in 2026.

## Decision

1. **One employee session = one independent official CLI process**, launched by the daemon from the binary the user installed (found on PATH or chosen by the user), supervised in the background. We never bundle, patch or replace a CLI, never alter its headers or User-Agent, never route its traffic, and never read, copy or log its credential or session files. Login is done in the CLI itself.
2. **Integration modes per provider**, as capability flags, each with a `terms_checked_at` date:
   - `structured`: the CLI's own headless or protocol mode with streamed JSON (Claude Code `-p` with stream-json, Codex `exec --json` or `app-server`, Grok `-p` or ACP). Best UI.
   - `terminal`: the official interactive terminal UI hosted in a pseudo-terminal and rendered in the app. It is literally the official client used by the user, which is the lowest-risk mode where `structured` is ambiguous.
   - `api`: an OpenAI-compatible endpoint with the user's own API key, kept in the operating system credential store.
   P1-001 verifies which modes each CLI really supports; `docs/research/provider-terms.md` decides which are enabled by default.
3. **Profiles manage several subscriptions.** A profile is provider + binary path + an isolated configuration directory (using the CLI's own mechanism, for example `CLAUDE_CONFIG_DIR` for Claude Code and `CODEX_HOME` for Codex, to be verified in P1-001) + mode + terms flags. One person can have several profiles (for example a personal and a work account of their own). Each employee is bound to exactly one profile chosen by the user.
4. **No pooling or rotation.** The daemon never switches an employee to another profile automatically to get around rate limits, and a profile is never shared between different people. Limits are shown to the user, who decides.
5. **The environment is scrubbed.** A child process gets only an explicit allow-list of environment variables.
6. **Supervision lives in `internal/proc`:** own process group (Job Object on Windows), output ring buffer, kill of the whole tree on cancel, restart policy with back-off, sessions survive when the UI disconnects, and sessions are resumed after a daemon restart through the CLI's own resume feature where it exists.

## Consequences

- A provider can be switched off by flipping a flag, with no change to the rest of the system.
- Claude Code support depends on the mode chosen in the terms decision; written confirmation from Anthropic is an open item.
- The `terminal` mode needs a pseudo-terminal (ConPTY on Windows). Adding a PTY library requires its own ADR.
