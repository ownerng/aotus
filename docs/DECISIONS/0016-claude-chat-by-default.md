# 0016 - Claude Code runs as a chat by default, behind its notice

Status: accepted (amends the provider-terms decision of 2026-10-09 and ADR 0008, decision 2)
Date: 2026-10-23

## Context

`docs/research/provider-terms.md` (Decision, 2026-10-09) made Claude Code default to `terminal` mode, the official interactive screen in a pseudo-terminal, with `structured` mode (`claude -p`) available per profile behind a notice, because Anthropic's terms are ambiguous about automated use of a subscription through `-p`.

In use, that default meant the app's chat did not work with Claude, the subscription most people link first. A new Claude teammate was terminal-only. Before its session started, the app did not know its mode and showed a composer, and a message sent from it was typed into the terminal. If Claude Code was showing its folder-trust prompt, the message landed in the prompt and the teammate looked blocked (reproduced 2026-10-23). The owner, who made the 2026-10-09 decision, now chooses the chat as the default.

## Decision

Claude Code profiles default to `structured` mode, the chat, like Codex CLI. The notice stays and must be accepted: the link form shows the notice's exact text (served by the daemon at `GET /notices`), and the person ticks that they read and accept it before linking. A Claude teammate whose notice is not accepted shows that text in its chat with an accept button, instead of failing. `terminal` mode stays available for anyone who prefers the official screen or wants to avoid the question the notice raises.

## Consequences

- The chat works with Claude out of the box. Under `-p` the folder-trust prompt does not appear (`docs/research/cli-contracts.md`), so the blocking case is gone for the default.
- The person accepts, knowingly and once per profile, the risk the notice describes: Anthropic may treat automated `-p` use of a Free, Pro or Max subscription differently from its own apps, and billing for this mode has changed before. Aotus keeps the acceptance on the profile, and it is shown on the profile. This is the owner's call, and it is recorded here.
- In terminal mode the app never shows a composer. It tells the person, once, that Claude Code asks whether to trust the teammate's own folder. An employee's mode is known before its session starts (from its profile).
- Revisit when Anthropic confirms or forbids `-p` use on a subscription in writing (an open item in `provider-terms.md`).
