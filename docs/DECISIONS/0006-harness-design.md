# 0006 - The harness is a Go CLI over JSON files in the repository

Status: accepted
Date: 2026-10-08

## Context

Any model must be able to learn the architecture, the phases, the current work and the definition of done from the repository alone, and a task must not be marked done without evidence.

## Decision

- Project state is data in `harness/*.json` (phases, requirements, tasks, architecture rules), versioned with the code. Status lives in the task files; the current phase is derived from gates. `docs/STATUS.md` is generated from them.
- `cmd/crew` (library in `internal/harness`, standard library only) is the only way to change state: `start`, `done`, `block`, `gate`. `done` runs the task's checks plus the quality gates.
- Checks are machine-verifiable: named Go tests that must exist and pass, files with required content, or commands.
- One task in progress at a time; only the current phase can be worked on; lint rejects hand edits that break these rules and a stale `docs/STATUS.md`.
- Architecture rules (`harness/architecture.json`) are checked from the real import graph.
- `AGENTS.md` is the single entry point for every model; `CLAUDE.md` imports it.

## Consequences

- Models get the same instructions regardless of vendor, and progress cannot be faked by editing a status field.
- JSON has no comments; explanations live in task descriptions and docs.
- Task checks are only as good as their test names: reviewers must read new tasks like they read code.
