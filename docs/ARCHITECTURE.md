# Architecture

Status: design of record. Update this file in the same change that alters the design. The enforceable part of it lives in `harness/architecture.json` and is checked by `crew arch`. Decisions behind it are in `docs/DECISIONS/` (especially 0001, 0002, 0007, 0008, 0009).

## What Aotus is

A polished interface for working in the agentic era. The person creates **employees** (agents with a name, role, memory and a job). Each employee works through the **official CLI** of a provider (Claude Code, Codex CLI...), running as an independent background process on the person's own computer with their own subscription. Aotus does not replace any CLI: it launches them, supervises them and gives them a good interface.

## Shape

```
 desktop app (Wails)            daemon  aotusd  (Go, headless, one static binary)                official CLIs
 -------------------            --------------------------------------------------              -------------
 window + tray        --HTTP/WS-> internal/api          loopback only, token, origin checks
 (cmd/aotus-desktop)             internal/orchestrator  employees, sessions, turns, cancel
 aotus CLI (dev/headless)  --->  internal/permissions   approvals + audit log
                                 internal/store         SQLite (WAL): employees, history, memory
                                 internal/provider      adapters + profiles (subscriptions)
                                 internal/workspace     per-employee folder + scrubbed env
                                 internal/proc          supervised child processes (process tree)
                                 internal/lifecycle     single instance, discovery file, autostart
                                 internal/netaccess     bind rules, local token   (tsnet in phase 2)
                                          |
                                          v  starts, supervises, kills the whole tree
                                 one process per employee session  ------>  claude / codex / grok ...
                                                                            (the user's own login)
```

Key ideas:

1. **The daemon is the product.** It has no window and is the only long-lived process. The desktop app is a client: closing it never stops the agents. The app finds a running daemon through the discovery file or starts one.
2. **One employee session = one independent CLI process**, in its own process group (Job Object on Windows), with a bounded output buffer.
3. **Subscriptions are profiles.** A profile = provider + CLI binary + isolated configuration directory + integration mode + terms flags. A person may have several profiles. Each employee is bound to one profile chosen by the user. There is no automatic rotation or pooling.
4. **The API is the only contract** between the daemon and every client (`docs/API.md`).

## Packages and dependency rules

An arrow means "may import". Anything not listed is forbidden. `harness/architecture.json` is the source of truth; if this table and the JSON disagree, fix the JSON first and then this table.

| Package | Responsibility | May import |
| --- | --- | --- |
| `internal/version` | build version | - |
| `internal/proc` | start/stop child processes, process-tree kill, line streaming, ring buffer, PTY (if adopted) | version |
| `internal/store` | SQLite (WAL, pure-Go driver), migrations, data dir layout, memory | version |
| `internal/workspace` | per-employee folder, path-escape protection, scrubbed environment, trash | version |
| `internal/lifecycle` | single-instance lock, discovery file, autostart per OS | version |
| `internal/netaccess` | bind rules, local token (tsnet in phase 2) | version |
| `internal/provider` | provider adapters, profiles, login detection, normalized events | proc, version |
| `internal/permissions` | action classification, approvals, audit log | store, version |
| `internal/orchestrator` | employees, sessions, turns, streaming, cancel, parallelism, restart policy | provider, proc, workspace, permissions, store, version |
| `internal/api` | HTTP + WebSocket, authentication, origin validation | orchestrator, permissions, store, netaccess, version |
| `internal/client` | Go client of the API | version |
| `internal/bench` | performance budgets as tests | anything |
| `cmd/aotusd` | daemon, composition root | anything |
| `cmd/aotus` | small CLI client for development and headless use | client, version |
| `cmd/aotus-desktop` | Wails desktop app (build tag `desktop`) | client, version |
| `cmd/crew` / `internal/harness` | project tooling, standard library only | - |

Restricted imports: `os/exec` only in `proc`, `harness`, `bench`. `import "C"` nowhere in our code: the daemon and libraries are cgo-free; the only cgo is inside the Wails dependency, reached through `cmd/aotus-desktop`, which is behind the `desktop` build tag.

## Core concepts

- **Profile**: how to reach one subscription. Provider kind, CLI binary path, isolated config directory (the CLI's own mechanism, such as `CLAUDE_CONFIG_DIR` or `CODEX_HOME`, verified in P1-001), integration mode, terms flags and `terms_checked_at`. Holds no secrets: logins live inside the CLI's own configuration directory and are never read by Aotus. API keys, if used, live in the operating system credential store.
- **Integration mode** (per profile, see ADR 0008): `structured` (the CLI's headless/protocol mode with streamed JSON), `terminal` (the official interactive UI hosted in a pseudo-terminal), `api` (OpenAI-compatible endpoint with the user's key).
- **Employee**: name, role, system prompt, profile, workspace folder, permissions, memory. Persistent in the store.
- **Session**: a running CLI process for an employee. Survives UI disconnects. Resumed after a daemon restart through the CLI's own resume feature where available.
- **Turn**: one request to an employee until it is done. Cancelable at any time.
- **Event** (structured mode): `TextDelta`, `ToolRequest`, `ToolResult`, `Error`, `Done`, normalized across providers. Events never contain credentials or environment variables.
- **Action**: something an employee wants to do that the permissions layer classifies (run command, write outside the folder, network, read outside the folder). Sensitive ones need approval.

## Provider interface (finalized in P1-004)

- `Detect(ctx, Profile)` - CLI installed, version, supported modes, login status; never reads credential files.
- `Start(ctx, SessionRequest) (Session, error)` - starts the CLI process through `proc`.
- `Session.Events() <-chan Event`, `Session.Send(...)`, `Session.Cancel()`, `Session.Close()`.

## Concurrency model

- One goroutine supervises each session; it owns the child process and its pipes.
- Events fan out to subscribers through per-subscriber buffered channels. A subscriber that cannot keep up is dropped or coalesced; it never blocks the producer or other employees.
- A configurable concurrency limit queues extra turns.
- Every blocking call takes a `context.Context`; cancel kills the whole process tree on every OS.
- No global mutable state. Dependencies are passed in from `cmd/aotusd`.

## Data directory

One folder per user, default `~/.aotus` (platform equivalent on Windows). Owner-only permissions.

```
~/.aotus/
  aotus.db           SQLite, WAL mode (employees, profiles, history, memory index, audit log)
  daemon.json        discovery file: address, pid, started_at (owner-only)
  token              local API token (owner-only)
  profiles/<id>/     isolated configuration directory of one subscription profile
  employees/<name>/  working folder and Markdown notes of each employee
  trash/             deleted employee folders (recoverable)
```

## Phases and where each piece lands

- **Phase 1 (MVP, personal PC):** everything above except tsnet. Loopback only.
- **Phase 2 (remote):** `tsnet` in `netaccess`, Tailscale identity, daemon on a VPS, desktop app connecting remotely, web client served by the daemon.
- **Phase 3:** OS sandbox (containers), delegation between employees, schedules and triggers, employee browser, signed skills.

## What is not decided yet

Tracked as ADRs when decided: SQLite driver (P1-003), PTY library and `terminal` mode details (P1-001, P1-006), Wails v2 vs v3 and frontend framework (P1-016), skill signing (phase 3).
