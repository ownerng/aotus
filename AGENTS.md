# AGENTS.md - start here

This file is the entry point for any developer or AI model working on this repository. Read it fully, then follow the loop below. Do not rely on memory of earlier sessions: the repository is the source of truth.

## What this project is

**Aotus** is an open source platform where a person links the AI subscriptions they already pay for (Claude, ChatGPT/Codex, Grok, Gemini...) and creates "employees": persistent agents with a name, memory, tools and a job. It runs on the user's own PC or on their VPS, reached over Tailscale. We do not resell tokens and we do not host anything.

- Core: a headless daemon `aotusd` in **Go**, one static binary, no cgo, Linux/macOS/Windows. It runs in the background and supervises one independent official-CLI process per employee session.
- UI: a desktop app built with **Wails** (`cmd/aotus-desktop`) that is only a client of the daemon; closing the window never stops the agents. A tiny `aotus` CLI exists for development and headless use. No mobile.
- Providers: Aotus is an **interface over the official CLIs and APIs** (Claude Code, Codex CLI, ...). We never replace, patch or proxy a CLI, never read, copy or forward subscription credentials, and never rotate or pool subscription profiles automatically (ADR 0008).
- Scope now: **one personal PC** (phase 1). VPS + Tailscale (`tsnet`) is phase 2 (ADR 0009).

## Orient yourself (do this every session, in this order)

1. `go run ./cmd/crew status` - current phase, progress, what is in progress, what is next.
2. `go run ./cmd/crew next` - the brief of the task you should work on: description, acceptance criteria, the exact checks that will verify it, and which docs to read first.
3. Read the docs the brief lists, plus:
   - `docs/ARCHITECTURE.md` - how the system is built and the dependency rules
   - `docs/CONVENTIONS.md` - code style, errors, tests, dependencies
   - `docs/SECURITY.md` - non-negotiable security rules
   - `docs/PRD.md` - why we build this and the requirement IDs (F*, S*, N*)
   - `docs/DECISIONS/` - decisions already made; do not re-litigate them without a new ADR
   - `docs/STATUS.md` - generated progress report (do not edit by hand)

## The work loop

```
crew next            # see the task (or continue the one in progress)
crew start <id>      # claim it - one task at a time
... implement, writing the tests named in the task's checks ...
crew verify          # run the quality gates while you work
crew done <id>       # runs the task checks + gates; marks it done only if all pass
```

`crew` is `go run ./cmd/crew` (or `make crew` to build `bin/crew`). Other commands: `show <id>`, `block <id> <reason>`, `gate <phase>`, `lint`, `arch`, `report`.

Rules of the loop:

- **One task in progress at a time.** Finish it or `crew block` it with a reason.
- **Only the current phase.** Tasks of later phases cannot be started. When all tasks of a phase are done, run `crew gate <phase>`.
- **A task is done only when `crew done` says so.** Never edit `status` or `completed_at` in `harness/tasks/*.json` by hand; the CLI does it and keeps `docs/STATUS.md` in sync.
- **Tests are the proof.** A task's checks name the tests that must exist and pass. Write those tests first or alongside the code; do not rename them to make a check pass.
- **Do not weaken a check, a budget or a rule to get green.** If a budget is unrealistic or a rule is wrong, change it in a separate, explicit step: update the PRD/ADR with evidence, then the harness file.
- If the task is unclear or you find missing work, **add a task** (next free ID in that phase's file, with acceptance criteria and checks) instead of doing unplanned work silently. `crew lint` validates the file.

## Non-negotiables

1. **No cgo in our code.** The daemon and every library must cross-compile with `CGO_ENABLED=0` to linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64. The only part that needs cgo is the Wails desktop app, behind the build tag `desktop`, built natively per OS in CI (ADR 0007).
2. **Layering is enforced.** `harness/architecture.json` lists which package may import which. `crew arch` fails on a violation. Process spawning (`os/exec`) is allowed only in `internal/proc` (and project tooling and benchmarks); everything else starts processes through it.
3. **Secrets never touch the database, logs or events.** API keys live in the OS credential store. Subscription credentials and the CLIs' own session files are never read. Child processes get an explicit allow-list environment.
4. **Closed by default.** The daemon listens on loopback only (tailnet in phase 2), and every request is authenticated. See `docs/SECURITY.md`.
5. **Standard library first.** A new dependency needs a short ADR in `docs/DECISIONS/` (what, why, license, size, alternatives).
6. **Performance is a feature.** The budgets N1-N7 in the PRD are tests. Do not add work to the hot path (turn start, streaming) without measuring.
7. **The UI is a client.** The desktop app and the `aotus` CLI talk to the daemon only through the documented API (`docs/API.md`) via `internal/client`.

## Quality gates

`crew verify` runs, in order: `gofmt`, `go vet`, `go build`, `go test`, `golangci-lint` (skipped if not installed locally, required in CI), harness lint, architecture rules. `crew verify --full` adds the race detector, cross-compilation of the daemon to all five targets and `govulncheck`. The desktop app is checked separately with `-tags desktop` on a machine with the native prerequisites (see ADR 0007). CI runs `crew verify --full --strict` on every pull request.

## Repository map

```
cmd/aotusd/             the daemon (composition root)
cmd/aotus/              small CLI client for development and headless use
cmd/aotus-desktop/      Wails desktop app (build tag desktop)
cmd/crew/               harness CLI (project management + validation)
internal/api/           HTTP + WebSocket on loopback, auth, origin checks
internal/orchestrator/  employees, sessions, turns, restart policy
internal/provider/      adapters for official CLIs and APIs, profiles (subscriptions)
internal/proc/          supervised child processes, process-tree kill
internal/workspace/     per-employee folder, scrubbed environment
internal/permissions/   approvals and audit log
internal/store/         SQLite (WAL), migrations, memory
internal/lifecycle/     single instance, discovery file, autostart
internal/netaccess/     bind rules and local token (tsnet in phase 2)
internal/client/        Go client of the daemon API
internal/bench/         performance budgets as tests
internal/harness/       the harness library behind `crew`
harness/                phases, requirements, tasks, architecture rules (JSON)
docs/                   architecture, conventions, security, PRD, ADRs, research
```

## When you finish a session

Leave the repository in a state the next model can pick up: the task is `done` (or `blocked` with a reason in `notes`), `crew verify` passes, and `docs/ARCHITECTURE.md` / `docs/API.md` reflect what you built. Summarize what changed and what is next, in plain words.
