# 0013 - WebSocket through coder/websocket

Status: accepted
Date: 2026-10-09

## Context

Employees stream events to clients and a terminal session needs bidirectional binary traffic (typed input, resize, screen output). The standard library has no WebSocket support. Our rule is no cgo.

## Decision

Use `github.com/coder/websocket` (ISC license, pure Go, formerly nhooyr.io/websocket): it is context-aware, handles binary and text frames, enforces read limits, and checks the `Origin` header by default. It is used only by `internal/api` (server) and `internal/client` (Go client).

## Consequences

- The daemon still rejects any request that carries a foreign `Origin` before the upgrade, with its own check, so the protection does not depend on the library's defaults.
- `gorilla/websocket` was the alternative: older and widely used but less context-aware. Server-Sent Events were considered for events but cannot carry terminal input.
