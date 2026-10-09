# 0011 - Pseudo-terminals on Linux and macOS through creack/pty

Status: accepted
Date: 2026-10-09

## Context

The `terminal` mode (ADR 0008) runs the official interactive UI of Claude Code or Codex CLI inside a pseudo-terminal owned by the daemon, so that it keeps running when no window is open. Go's standard library has no pseudo-terminal support. Our rule is no cgo in our code.

## Decision

On Linux and macOS use `github.com/creack/pty` (MIT, pure Go, no cgo). It opens the pseudo-terminal pair, makes the child a session leader with the terminal as its controlling terminal (so its process group ID is its PID and the whole tree can be signalled), and resizes the terminal. It is used only inside `internal/proc`, behind a small backend interface (`ptyBackend`) so the supervision, replay buffer and fan-out are shared with the Windows backend.

On Windows the same interface is implemented with ConPTY in task P1-021, with no third-party library unless that task proves one necessary (a new ADR would record it).

## Consequences

- `proc.StartPTY` is the only way a program gets a terminal, which keeps process spawning in one package.
- The library is small and widely used; its Unix-only restriction is why Windows has its own backend.
