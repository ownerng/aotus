# 0015 - A profile may use the sign-in already on this computer

Status: accepted (amends ADR 0008, decision 3)
Date: 2026-10-23

## Context

ADR 0008 gives every profile an isolated configuration folder: Aotus starts the CLI with `CLAUDE_CONFIG_DIR` or `CODEX_HOME` pointing under `~/.aotus/profiles/<id>`. That keeps two profiles, or two people, from ever sharing one state. It also means a person who is already signed in to Claude Code or Codex in their own terminal must sign in again inside Aotus, in a terminal, before anything works. The owner found that friction wrong for the main case: one person on their own computer who has already signed in.

Using the existing sign-in does not require Aotus to read anything. The CLI finds its session in its own default folder (`~/.claude`, `~/.codex`, or wherever the person's `CLAUDE_CONFIG_DIR` / `CODEX_HOME` points), exactly as when the person types `claude` or `codex`. The `HOME` variable already reaches the CLI through the allow-listed environment.

## Decision

A Claude Code or Codex profile may use the sign-in already on the computer that runs the daemon (`computer_login`). For such a profile Aotus does not set the CLI's configuration variable, and passes through the daemon's own value of it if the person set one, so the CLI uses the same folder as in their terminal. It is the default when linking from the app; a separate sign-in (ADR 0008's isolated folder) stays available for a second account or for sharing.

Guards:

- **At most one per CLI.** Two profiles of one CLI on the computer's sign-in would share one state. The store has a unique index on it, and the daemon says which profile already has it.
- **It is never shared.** A profile on the owner's own sign-in cannot be shared with a remote guest (`400 own_sign_in`). Sharing needs a separate sign-in, as S6 requires.
- **CLI profiles only.** An API profile has a key in the credential store, not a sign-in.
- **Aotus still never reads, copies or forwards the session** (S2): it only stops redirecting the CLI's folder.

## Consequences

- Linking a subscription the person already uses works at once: Check reports `logged_in`, and the first message runs with no login step. This was verified on 2026-10-23 with Claude Code 2.1.310 and Codex 0.160.0, and with a real Codex turn.
- Employees on this profile see the person's own CLI configuration: `~/.claude/settings.json` (hooks, permission rules, MCP servers), `CLAUDE.md` user memory, and Codex's `config.toml`. That can be useful (their MCP servers just work) or surprising (their hooks run for employees). The link form says so, and a separate sign-in avoids it.
- The employees' sessions appear in the person's own CLI history (`claude --resume`, `codex resume`), and signing out in a terminal signs the employees out too.
- On a VPS, "this computer" is the server: the profile uses the service user's sign-in there.
- Alternatives considered: copying the session files into the profile folder, which would break S2 (never copy credentials); symlinking the folders, which hides the same sharing behind a link that looks isolated; and keeping the re-login, which the owner rejected.
