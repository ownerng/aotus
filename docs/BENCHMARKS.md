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

## N5: ten employees on a small server

Requirement N5: ten employees working at the same time on a 2 vCPU / 4 GB server must not degrade the interface.

### What the test measures (`TestTenParallelEmployeesStayResponsive`)

The real `aotusd` binary runs as its own process, limited to **two CPUs with `taskset -c 0,1` where the system allows it** (the limit is inherited by everything the daemon starts, so the employees' programs share those two CPUs too). Ten employees work against it, all scripted so that the model's time is out of the picture:

- four **structured** employees, each running one busy turn after another (a text event every 5 ms);
- one **probe** employee that streams a time-stamped answer four times;
- five **terminal** employees hosting an interactive program that prints a line every 20 ms, each watched by a client, as a window would.

While they all work, a client does what a window does all the time (`GET /status` and `GET /employees` every 50 ms) and the test checks, with the bounds below. A bound that real use shows to be wrong is changed here with the evidence, not quietly in the test.

| Measure | Bound | Measured (3 runs, linux/amd64, 8-core desktop limited to 2 CPUs) |
| --- | --- | --- |
| Interface: `GET status` + `GET employees` while all ten work | p95 under 100 ms, max under 500 ms | p95 2.4 to 2.7 ms, max 3.2 to 9.4 ms |
| Start of a turn (from `Send` to the turn being started) | max under 500 ms | max 12.7 to 26.3 ms over 12 turns |
| Stream latency under load (N3 must still hold) | p95 under 100 ms | p95 1.1 ms over 400 events |
| Daemon memory under load | under 100 MB (N4) | 20.3 to 21.3 MB |
| Daemon CPU under load | (reported) | about 40% of one CPU |
| Memory of the ten scripted employees' programs | (reported) | about 92 MB more (the fake CLI is the Go test binary; real CLIs weigh far more) |

### What this does not tell you

- **The real CLIs are not in it.** Claude Code and Codex CLI are Node and Rust programs that use hundreds of megabytes each when they work. Ten of them busy at once will use more memory than a 4 GB server has. What the test proves is that **the daemon adds almost nothing**: the interface does not slow down, and nothing the daemon does grows with the number of employees.
- **The default is four turns at once.** `aotusd --max-turns N` (default 4) is how many employees' *turns* run at the same time; more wait in line, in the order they were sent. Terminal programs are not limited, because they are long-lived sessions. The test runs with `--max-turns 10`. With the default, a fifth simultaneous turn waits for one of the first four to end (found by this test: turns started up to 1 s late when five employees worked with a limit of four). Raise it to what the server's memory allows once you know how much one of your CLIs uses (`aotusd --service install --max-turns 6`, or edit the unit).
- Latency over a real tailnet is Tailscale's, not the daemon's.

### On a real VPS (procedure; waiting for results)

1. A 2 vCPU / 4 GB server set up as in `docs/VPS.md`, with the daemon as a service and `--max-turns` at the value you want to test.
2. The scripted test, on the server itself (needs Go): `go test -count=1 -v -run TestTenParallel ./internal/bench`. Expected: the same bounds.
3. With the real CLIs: link your subscription, create ten employees (five structured, five terminals), give each a real task, and while they work:
   - from your PC: `aotus --connection vps status` repeatedly (or watch the window): the answers should stay quick;
   - on the server: `ps -eo rss,comm --sort=-rss | head -20` for each CLI's memory, `free -m` for the total, and `systemctl --user show aotusd -p MemoryCurrent -p CPUUsageNSec`.
4. Write down: the CLI versions, the tasks, the number of employees working at once, the memory at its highest, and whether the window felt slow. Add it below.

**Result of a real VPS run:** not run yet. This is the part of N5 that still needs a server.

