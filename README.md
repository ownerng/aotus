# Aotus

A polished interface for working in the agentic era: turn the AI subscriptions you already pay for into persistent "employees" that work in the background on your own PC through the official CLIs (Claude Code, Codex CLI...). Background Go daemon plus a Wails desktop app for Windows, macOS and Linux. VPS and Tailscale come next. Apache-2.0.

Status: pre-MVP. See `docs/STATUS.md`.

## For contributors and AI models

Read `AGENTS.md`. The short version:

```
go run ./cmd/crew status     # where the project is
go run ./cmd/crew next       # what to build now, with acceptance criteria
go run ./cmd/crew start <id>
go run ./cmd/crew verify     # quality gates
go run ./cmd/crew done <id>  # only succeeds if the task's checks pass
```

Requires Go (version in `go.mod`). Optional: `golangci-lint` and `govulncheck` (CI runs both).

## Documents

`docs/PRD.md` product - `docs/ARCHITECTURE.md` design and dependency rules - `docs/CONVENTIONS.md` code and tests - `docs/SECURITY.md` security rules - `docs/DECISIONS/` ADRs - `docs/research/` findings.
