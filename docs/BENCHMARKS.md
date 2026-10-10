# Benchmarks

The performance budgets of the PRD (N1 to N7) are tests in `internal/bench`.
A budget that is exceeded fails the build. Refresh this table with:

```
AOTUS_BENCH_REPORT=/tmp/rows.md go test -count=1 ./internal/bench
```

N2, N3 and N7 are measured against a scripted fake CLI, so subscription time
is excluded. N1, N4 and N6 use the real `aotusd` binary built like a release
(`CGO_ENABLED=0`, `-trimpath`, `-s -w`).

| Measure | Value | Budget | Platform |
|---|---|---|---|
| Daemon binary size (N6) | 11.8 MB | under 50 MB | linux/amd64 |
| Startup to ready (N1) | median 15ms, worst 33ms | under 1s | linux/amd64 |
| Idle RSS (N4) | 13.2 MB | under 100 MB | linux/amd64 |
| Per-turn overhead (N2) | 1.9ms (CLI alone 5ms, through the daemon 7ms) | under 50ms | linux/amd64 |
| Stream latency (N3) | median 700µs, p95 800µs, worst 900µs | p95 under 100ms | linux/amd64 |
| Resume a paused employee (N7) | median 8ms, worst 9ms | under 1s | linux/amd64 |

macOS and Windows rows are added when the benchmarks run on those systems in CI.

## Desktop window memory

Measured on 2026-10-09 on Fedora 44 (KDE Plasma, Wayland), WebKitGTK 2.54.1, the real app (`go build -tags desktop,gtk3`) opened on an employee with a history of 500 turns (1000 messages: prompts and answers), memory as PSS from `/proc/<pid>/smaps_rollup` for the app and the processes it started (the window's web process and network process). The daemon is not included.

| State | App | Web process | Network process | Total PSS |
| --- | --- | --- | --- | --- |
| Opened on the 500-turn employee (latest 20 turns loaded, 8 answers) | 90 MB | 133 MB | 26 MB | **249 MB** |
| Worst case: all 1000 messages loaded into the list | 94 MB | 151 MB | 26 MB | **272 MB** |
| Empty template window (spike, `docs/research/desktop-spike.md`) | | | | 246 to 262 MB |
| For comparison: the daemon `aotusd` with that history | | | | 14 to 19 MB |

What this says:

- The window costs what a WebView costs, about 250 MB, however small the page. The conversation adds little: loading all 1000 messages added about 23 MB because the list only builds the rows near the viewport (`VirtualList.svelte`) and history is loaded in pages of 20 turns, answers of old turns on demand.
- This is why closing the window quits the app (ADR 0010): the employees run in the 15 MB daemon, so the 250 MB is paid only while a window is open. Keeping the app in the tray is an opt-in setting.
- Only Linux was measured. macOS and Windows rows come from CI machines or the owner's machines when P1-018 and P1-021 run there.

## Remote connection

Measured on 2026-10-09 on the same machine, with the window connected to a **remote daemon** (a second process serving the API through the tailnet-style identified listener on loopback; no Tailscale involved, so this measures the window, not the network), on an employee with 500 turns (1000 messages). `AOTUS_HOME` pointed to an empty directory, and no local daemon was started, which is the point of the first test of P2-006.

| State | App | Web process | Network process | Total PSS |
| --- | --- | --- | --- | --- |
| Remote connection, opened on the 500-turn employee (latest 20 turns loaded) | 92 MB | 138 MB | 27 MB | **258 MB** |
| Local connection, same screen (table above) | 90 MB | 133 MB | 26 MB | 249 MB |
| The remote daemon (including the 500 seeded turns) | | | | 17 MB |

The window costs about 9 MB more than against the local daemon (the connection state and the extra screen), nothing that depends on the network. What changes with a real tailnet is latency, which the stream-latency budget (N3) covers on the daemon side and which Tailscale adds to.

