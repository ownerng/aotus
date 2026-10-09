# 0002 - The UI is an optional client, never part of the daemon

Status: accepted, partly superseded by 0007 (the MVP clients are the desktop app and a tiny CLI; the web client moves to the remote-access phase)
Date: 2026-10-08

## Context

A web UI is a browser, and a browser costs memory however optimized the code is. Embedding a UI toolkit or Chromium in the daemon would make that cost permanent and would force a VPS to carry it.

## Decision

The daemon is headless and is the only process that always runs. Clients are separate, optional and disposable, and talk to the daemon only through the documented API (`docs/API.md`):

1. Terminal client (`cmd/aotus`): lightest, works over SSH. Phase 1.
2. Web client: a light page served by the daemon, opened in the browser the user already has. No bundled Chromium. Virtualized lists, on-demand history, incremental streaming. Phase 1.
3. Native app (system WebView via Wails/Tauri, or a Go toolkit such as Gio/Fyne): optional, phase 3, decided after a one-week spike that measures memory and text quality.

## Consequences

- Closing a client frees its memory; employees keep running.
- Never ship Electron or a private Chromium.
- Employee browsers (phase 2) are the real memory cost: launched on demand, headless, isolated profile, closed when idle, globally capped.
- `crew arch` enforces that clients do not import daemon internals.
