# 0001 - The daemon is written in Go

Status: accepted
Date: 2026-10-08

## Context

The product is a long-running daemon that supervises many child processes (provider CLIs, sandboxes, browsers), streams their output, stores state in SQLite and must run on Linux, macOS and Windows as one downloadable file. The user asked whether Rust is needed for maximum performance.

Each turn waits seconds for the user's subscription (network and model); the orchestrator spends milliseconds. Language speed is not the bottleneck. What matters is memory per employee (dominated by browsers and sandboxes, not our code), startup time, streaming smoothness and how many child processes we manage at once.

## Decision

The core is written in Go: one static binary, no cgo, cross-compiled to linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and windows/amd64.

## Consequences

- Cross-compilation is one command; goroutines fit "many supervised processes and streams" naturally.
- `tsnet` (Tailscale's Go library) lets the daemon join the user's tailnet by itself, which solves the VPS access story in one library.
- Low memory and fast startup are good enough for the budgets N1-N7; they are enforced by tests, not assumed.
- Rust was considered: lowest memory and best memory safety, but a steeper curve, more work for dependencies and cross-compilation, and no equivalent of `tsnet`. If a heavy local-compute module is ever needed (indexing, embeddings, screen recording), it can be a separate Rust binary the daemon launches like any other process. The API-only contract between daemon and clients keeps the core replaceable.
- Kotlin Multiplatform was considered for the UI: it does not remove the memory cost on desktop (Compose runs on the JVM) and only pays off for native mobile apps. Not chosen.
