# 0007 - The MVP client is a desktop app built with Wails; the daemon stays independent

Status: accepted
Date: 2026-10-09

## Context

The owner wants a polished desktop experience for working with agents on a personal PC, on Windows, macOS and Linux, and does not have the capacity for mobile apps yet. The earlier plan (terminal client plus web tab in the MVP) is replaced by a desktop app. The constraint that agents must keep working when the window is closed does not change.

Facts checked on 2026-10-09 (https://v3.wails.io/blog/wails-v3-beta/ and the Wails releases page): Wails v2 is the stable line; Wails v3 is in beta (v3.0.0-beta.28 published on 2026-10-05) and adds system tray and multiple windows; both use the operating system WebView (WebView2 on Windows, WebKit on macOS, WebKitGTK on Linux), not a bundled Chromium. On Linux, v3 defaults to GTK4 with WebKitGTK 6.0 and has a legacy GTK3 / WebKit2GTK 4.1 build tag. Wails needs cgo on macOS and Linux.

## Decision

1. The desktop app (`cmd/aotus-desktop`) is a **client** of the daemon. It never embeds the daemon: on start it looks for a running `aotusd` (discovery file plus local token) and starts one if needed, and closing the window leaves the daemon and its agents running. A system tray entry reopens the window.
2. **cgo rule:** the daemon and every library package stay cgo-free and cross-compile with `CGO_ENABLED=0`. Only `cmd/aotus-desktop` may need cgo. It sits behind the build tag `desktop`, so `go build ./...` and the cross-compilation gates ignore it, and CI builds it natively on each operating system.
3. **Wails v2 or v3, and the frontend framework (Svelte or Preact, TypeScript), are decided by the spike in task P1-016** against these criteria: system tray, behavior on the Linux distributions we target, stability, memory of the window, and effort to upgrade later. The result is recorded in `docs/DECISIONS/0010-desktop-stack.md`. Node is a build-time tool only.
4. The frontend talks to the daemon only through the documented API (`docs/API.md`), either directly or through the Go side of the desktop app. It keeps memory low: virtualized message lists, history loaded on demand, incremental streaming.
5. A tiny command line client `aotus` (status, list, start, stop) exists for development, tests and headless use. It is not a product surface.
6. **Mobile is out of scope.** The web client served by the daemon moves to the remote-access phase.

## Consequences

- The "UI is an optional client" principle of ADR 0002 is intact; its terminal and web clients are no longer MVP.
- Wails and its frontend toolchain are new dependencies, accepted by this record. Native build prerequisites per OS must be documented (on Fedora: `webkit2gtk4.1-devel` and a C compiler; Xcode command line tools on macOS; the WebView2 runtime on Windows).
- If Wails fails the spike, the fallback is a Go-native toolkit or a web client served by the daemon; the client contract does not change.
