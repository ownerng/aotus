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

Not measured yet: recorded when the desktop app (P1-017) exists.
