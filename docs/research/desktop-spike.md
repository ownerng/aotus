# Desktop stack spike: Wails and the frontend

Done on 2026-10-09 on Fedora 44 (KDE Plasma, Wayland), Go 1.26.8, Node 22.23.1, WebKitGTK 2.54.1 (the `webkit2gtk-4.1` package with GTK 3). The owner chose Wails v3 and Svelte beforehand; the spike checks that the choice works here and measures what it costs. The throwaway code lived outside the repository and is not merged.

## What was built

A Wails v3 app (`v3.0.0-beta.28`, the version on the releases page on 2026-10-05) from the official `svelte` template (Svelte 5, TypeScript, Vite 8), plus a variant with a system tray that closes and reopens its window. Both were run on the real desktop and their memory measured from `/proc/<pid>/smaps_rollup` (PSS, which shares library pages fairly between processes) for the app and every process it started.

## Wails v2 and Wails v3

| | Wails v2 | Wails v3 |
| --- | --- | --- |
| Tested here | No. Only read about (stable line; WebView2 on Windows without cgo; GTK3 and WebKit2GTK 4.0/4.1 on Linux). | Yes, built and run. |
| Release state | Stable | Beta (`v3.0.0-beta.28`); the project says teams already use it in production |
| System tray | Not built in | Built in (`app.SystemTray`); used in the spike |
| Several windows | Not built in | Built in |
| Linux libraries | GTK3, WebKit2GTK | Default GTK 4 and WebKitGTK 6.0 (`webkitgtk-6.0`, `gtk4`: **not installed here**). GTK 3 and WebKit2GTK 4.1 still work with the build tag `gtk3`, a legacy path the project says it removes in v3.1. |
| Go | older versions listed | 1.25 or newer |

**Findings with v3 on this machine**

1. The default Linux build wants GTK 4 and fails with `Package gtk4 was not found`. It builds with `EXTRA_TAGS=gtk3`: `wails3 build EXTRA_TAGS=gtk3` runs `go build -tags production,gtk3 ...`. Installing the CLI also needs the tag: `go install -tags gtk3 github.com/wailsapp/wails/v3/cmd/wails3@latest`.
2. The first build is slow (about 80 s: generating bindings took 48 s and the cgo compile most of the rest); rebuilds with nothing changed take about 1 s.
3. `wails3 generate bindings` prints GTK 4 warnings when run without `-f '-tags gtk3'` but still writes the bindings. The supported flow (`wails3 build`) passes the build flags, and building the frontend by calling `vite` directly fails unless the bindings already exist.
4. The binary is 10.2 MB (9.8 MB for the tray variant) and the Svelte bundle 57.7 kB (21.2 kB compressed), the Wails runtime included.
5. Moving to GTK 4 / WebKitGTK 6.0 before v3.1 means installing `webkitgtk6.0-devel` and `gtk4-devel` on Fedora and dropping the `gtk3` tag; plan it as a separate task.

## Memory: what a Wails window costs

Measured with an empty template window (nothing but the greeting page).

| State | Processes | PSS total | Of which real (anonymous) memory |
| --- | --- | --- | --- |
| Window open | app + `WebKitWebProcess` + `WebKitNetworkProcess` | **246 to 262 MB** | about 120 MB |
| Window closed, app kept alive by the tray | app + `WebKitNetworkProcess` | **170 MB** | about 73 MB |
| Window reopened | same three processes | 262 MB | |
| For comparison: the daemon `aotusd` after creating profiles, an employee and a terminal | one process | **15 MB** | |

Per process with the window open: the app 114 MB (63 MB anonymous), the web process 125 MB (45 MB anonymous, 80 MB library pages), the network process 26 MB. With the window closed the web process disappears, but the app process stays at 142 MB PSS (62 MB anonymous) and the network process at 31 MB: **the GUI toolkit and WebKit libraries stay loaded in the app process even with no window**.

Consequences:

- A WebView window is expensive however small the page: about 250 MB PSS and about 120 MB of real memory, before any chat content. The owner's concern about browser-like memory is confirmed by measurement.
- A resident tray made with Wails costs about 170 MB PSS (73 MB real) all day. That is ten times the daemon. So **the desktop app should exit when its window closes** and leave only the 15 MB daemon running, and the tray should be an opt-in setting ("keep in the tray"), off by default. Approvals that arrive while the app is closed wait in the daemon (they time out as a denial after 15 minutes); desktop notifications that wake the user are the phase 4 requirement F14.
- Closing the window does free its web process (confirmed), so memory is paid only while the window is open.

Not measured: the memory of the window with a long chat (needs the real app; task P1-017 records it in `docs/BENCHMARKS.md`), and native toolkits (Gio, Fyne), which would avoid the WebView but give up polished text and Markdown rendering; the API-only design keeps that door open.

## Svelte and Preact

Same screen in both: a virtualized list of 1000 messages (only the visible rows exist in the page) and an input, built with Vite 8 in production mode. Size of the JavaScript bundle:

| | Raw | Compressed |
| --- | --- | --- |
| Svelte 5 (runes) | 36.7 kB | 14.6 kB |
| Preact 10 | 14.3 kB | 6.0 kB |

Preact is smaller by about 22 kB, which is nothing next to a 120 MB WebView. The memory of the page was not compared (it needs a browser engine; the numbers above are the WebView baseline). Wails ships official templates for Svelte, React, Vue and vanilla, not for Preact. The owner chose Svelte: it is not justified by bundle size, but by the official template, and by a model of reactivity that suits a list updated token by token. Whichever is used, the frontend must virtualize long lists and load history on demand.

## Native build prerequisites (for the quickstart)

- Linux (today's GTK 3 path): `gcc`, `pkg-config`, `webkit2gtk4.1-devel` (Fedora) or `libwebkit2gtk-4.1-dev` (Debian and Ubuntu), `gtk3-devel`, Node 22 or newer for the frontend.
- macOS: Xcode command line tools, Node.
- Windows: Go, Node, and the WebView2 runtime (already present on Windows 11 and recent Windows 10). Windows needs no C compiler for Wails v2; for v3 this was **not verified** here.

## Open items

- Wails v2 was not built; nothing here says v3 is better beyond the tray and windows, which the owner wanted.
- Windows and macOS builds of the v3 app were not tested (no such machine here). The CI matrix of P1-018 will be the first test.
- Re-measure with the real app (P1-017) and record it in `docs/BENCHMARKS.md`.
