# 0010 - Desktop stack: Wails v3 with a Svelte frontend; the app exits when its window closes

Status: accepted
Date: 2026-10-09

## Context

ADR 0007 chose a Wails desktop app as a client of the daemon and left the Wails version and the frontend framework to a spike. The owner then chose Wails v3 and Svelte. The spike (`docs/research/desktop-spike.md`) built a v3 app on Fedora 44, ran it, and measured it: an empty window costs about 250 MB PSS (about 120 MB real), and keeping the app alive in a tray with no window still costs about 170 MB, against 15 MB for the whole daemon.

## Decision

1. **Wails v3**, pinned to `v3.0.0-beta.28` in `go.mod` until a stable release. It is a beta: upgrades are deliberate, one at a time, with the test suite and a manual run. It was chosen for its built-in system tray and multiple windows. On Linux it is built with the build tag `gtk3` (WebKit2GTK 4.1) today; moving to GTK 4 and WebKitGTK 6.0 is a separate task that must happen before Wails v3.1 removes the tag.
2. **Frontend: Svelte 5 + TypeScript + Vite**, from the official Wails template, with xterm.js for the terminal view. Long lists are virtualized and history is loaded on demand. Bundle size is not a reason for Svelte (Preact is smaller), and not a problem either.
3. **The JavaScript never talks to the daemon.** The Go side of the app uses `internal/client` (it holds the token and the connection) and exposes typed methods and events to the frontend through Wails bindings. The token never enters the WebView, and the daemon's Host and Origin rules are never in play.
4. **Memory by design.** Closing the window quits the desktop app by default; only the 15 MB daemon stays. A setting "Keep in the tray" (off by default, with its cost stated in the UI: about 170 MB) keeps the app resident. Approvals that arrive while the app is closed wait in the daemon and are denied after 15 minutes; desktop notifications are requirement F14 (phase 4).
5. **Native build prerequisites** are documented in `docs/QUICKSTART.md` and `docs/research/desktop-spike.md`.

## Consequences

- The Wails and Svelte dependencies are accepted by this record. The app lives in `cmd/aotus-desktop` behind the build tag `desktop`, so the daemon and every library still build with `CGO_ENABLED=0`.
- Starting the app takes the cost of a WebView (about a second); the design accepts it in exchange for freeing the memory when the window closes.
- Wails v2 was not tested. If v3 stops being viable, v2 or a web client served by the daemon (phase 2) are the fallbacks; the API contract does not change.
- If the WebView memory is judged too heavy after measuring the real app (P1-017), a native toolkit is the alternative to study, at the price of text rendering work.
