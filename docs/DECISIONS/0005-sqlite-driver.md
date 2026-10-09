# 0005 - SQLite through modernc.org/sqlite (pure Go)

Status: accepted
Date: 2026-10-09

## Context

The daemon stores employees, profiles, history, memory and the audit log in SQLite. Our rule is no cgo in our code (ADR 0001) so the daemon cross-compiles to five targets from any machine and ships as one static binary. The driver therefore has to be pure Go.

## Decision

Use `modernc.org/sqlite` (v1.60.1 at the time of writing), a transpilation of the SQLite C source to Go, registered as the `sqlite` driver of `database/sql`. License: BSD-3-Clause. It is widely used and cross-compiles without a C toolchain. It pulls `modernc.org/libc`, `modernc.org/memory`, `modernc.org/mathutil` and a few small utilities as indirect dependencies. Adopting it raised the `go` line of `go.mod` to 1.26.

Pragmas are set in the DSN so every pooled connection gets them: `journal_mode(WAL)`, `foreign_keys(1)`, `busy_timeout(5000)`, `synchronous(NORMAL)`.

## Consequences

- The daemon binary grows (the SQLite engine is inside it); the size budget N6 (under 50 MB) is checked by the budget tests in P1-019. If it is exceeded, revisit.
- It is slower than the C library in some workloads; our workload is small and local, and N1 to N7 are measured.
- Alternatives considered: `github.com/ncruces/go-sqlite3` (SQLite compiled to WebAssembly and run with wazero; also pure Go, a heavier runtime and a smaller user base) and `mattn/go-sqlite3` (cgo, rejected by ADR 0001).
- Full-text search for memory (F5) uses SQLite FTS5; P1-012 verifies that this build includes it.
