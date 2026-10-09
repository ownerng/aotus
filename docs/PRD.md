# Product requirements (condensed)

The full PRD lives in the team's document: https://claude.ai/code/artifact/bd8838fb-8acf-4d57-a7e5-2726cb833e02 . This file is the condensed version that models read; requirement IDs here are the ones used by `harness/requirements.json`. If the two disagree, the repository wins after the discrepancy is fixed in an explicit change.

## Vision

Aotus is a polished interface for working in the agentic era. Each person links the AI subscriptions they already pay for and creates persistent "employees" (agents with a name, memory and a job). Each employee works through the provider's official CLI, running as an independent background process on the person's own PC. Aotus does not replace any CLI. Remote use on a VPS through Tailscale comes in phase 2. We do not resell tokens and we do not host anything.

Why now: Grok Bot (launched 2026-08-11) validated the category but costs $120-$300/month, locks the user to one provider and lives in the provider's cloud. Open alternatives exist (OpenClaw, OpenMausBot) but OpenClaw has serious security failures (malicious skills, thousands of exposed instances, localhost WebSocket hijacking) and OpenMausBot is not a cross-platform daemon.

## Differentiators, in priority order

1. Security by design (closed by default, sandbox per employee, signed skills).
2. Bring your own subscription: zero marginal cost for the user.
3. No provider lock-in: an employee can switch provider and keep its memory.
4. Local first, remote when needed: same binary on a laptop or a VPS.
5. Perceived speed: instant startup, fluid streaming, many employees with little memory.

## Users

Technical and semi-technical people who already pay for AI subscriptions: independent developers, freelancers and small agencies, self-hosting enthusiasts; small teams later (phase 4).

## Product principles

Closed by default. One binary, nothing else to install to start. Subscriptions are used the way the provider offers them (official CLIs and APIs only). The user's data is theirs (SQLite and Markdown, no telemetry by default). Same product on PC and VPS. Fast, and measured. Everything is a readable file.

## Phases

| Phase | Name | Scope |
| --- | --- | --- |
| 0 | Validate | Provider terms and integration decision, name and license (done: Aotus, Apache-2.0) |
| 1 | MVP desktop | Personal PC: background daemon, Wails desktop app, Claude Code + Codex CLI + OpenAI-compatible API, subscription profiles, workspace isolation |
| 2 | Remote access | Daemon on a VPS, desktop app connects over Tailscale (tsnet) |
| 3 | Collaborate | Delegation, schedules and triggers, employee browser, signed skills, OS sandbox, switch profile keeping memory |
| 4 | Learn | Learn workflows from recordings, cost panel, notifications |
| 5 | Teams | Multi-user, roles, shared memory |

Out of scope for the MVP: VPS and Tailscale (phase 2), mobile apps (no capacity yet), web client, learning from recordings, skill marketplace, multi-user, OS-level sandbox, local models as a focus (an OpenAI-compatible endpoint can point to one).

## Requirements

Functional (phase 1): F1 link one or more subscriptions per provider as profiles, using the official CLI with an isolated configuration directory; F2 create employees; F3 chat with streaming, history and immediate cancel; F4 approvals for sensitive actions; F5 persistent searchable memory; F6 each employee is an independent supervised CLI process in the background, in parallel, surviving window close; F16 Wails desktop app (profiles, employees, chat, approvals, tray); F17 daemon lifecycle on a PC (single instance, autostart, discovery, graceful shutdown).
Functional (later): F18 remote use on a VPS over Tailscale (phase 2); F7 delegation, F8 schedules/triggers, F9 isolated browser, F10 signed skills, F11 switch profile keeping memory (phase 3); F12 learn by recording, F13 cost panel, F14 notifications (phase 4); F15 multi-user (phase 5).

Security: S1 loopback only with authentication (tailnet in phase 2, S4); S2 official CLIs and APIs only, launched unmodified, never touch credentials, never rotate or pool profiles; S3 workspace isolation per employee (OS sandbox is S5, phase 3).

Performance budgets (proposed targets, enforced by task P1-019 once measured): N1 startup under 1 s; N2 daemon adds under 50 ms per turn; N3 stream delay under 100 ms; N4 idle memory under 100 MB; N6 binary under 50 MB; N7 resume under 1 s; N5 (phase 2) 10 parallel employees on a 2 vCPU / 4 GB VPS. N8: Windows 10/11, macOS Intel/Apple Silicon and Linux built and tested in CI, the daemon also for Linux ARM64.

## Biggest risk

Provider terms of use. Whether a third-party tool may automate a consumer subscription depends on each provider's rules, which change (see docs/research/provider-terms.md, 2026-10-09: Anthropic allows the user's own unmodified Claude Code login but forbids third parties routing requests through Free/Pro/Max credentials, and headless/SDK use on a subscription is ambiguous). Mitigations: verify terms first (task P0-001); only drive official CLIs the user logged into, unmodified; per-provider capability flags with a terms_checked_at date; the user's own API key as a fallback; never depend on one provider; seek written confirmation from Anthropic. See `docs/DECISIONS/0003-official-clis-only.md`.

## Success metrics (first 6 months after the MVP, to be defined)

Time from download to first working employee; users with an active employee at 7 and 30 days; stars, forks and external contributors; security incidents reported and time to fix.

## Open questions

Which providers ship in the MVP (proposal: Claude Code and Codex CLI); definitive name and domain; license (Apache-2.0 or MIT); whether a hosted or paid offering exists later.
