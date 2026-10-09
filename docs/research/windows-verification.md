# Windows verification

Aotus is developed on Linux. Everything Windows-specific below was written and cross-compiled (`GOOS=windows go vet` and `go test -c`) but **has not been run on Windows**. Task P1-021 stays open until the table at the end holds real results from a Windows 10/11 machine or the `windows-latest` runner of `.github/workflows/ci.yml` (job `test`, which runs `go test ./...`).

## What to run

```
go test -count=1 -v ./internal/proc ./internal/lifecycle ./internal/netaccess ./internal/workspace
go test -count=1 -tags "desktop" ./cmd/aotus-desktop      # after the frontend is built (see docs/QUICKSTART.md)
go test -count=1 ./internal/bench                          # fills the Windows rows of docs/BENCHMARKS.md
```

Then paste the output and `winver` (or `systeminfo | findstr /B /C:"OS"`) in the table.

## What is Windows-only and unverified

| Area | Code | Test that exercises it | Risk to watch |
| --- | --- | --- | --- |
| Process tree kill | `internal/proc/proc_windows.go` (Job Object, kill-on-close) | `TestCancelKillsWholeProcessTree` and friends in `proc_test.go` | A grandchild started in the first instant is assigned late (documented limitation). |
| Detached start | `internal/proc/detached_windows.go` (`DETACHED_PROCESS`, new process group) | `TestStartDetachedStartsAndReleases` | The daemon must survive the app that started it. |
| Pseudo-console | `internal/proc/pty_windows.go` (ConPTY, Job Object, suspended start) | `TestPTYWindows*` in `pty_windows_test.go` | Needs Windows 10 1809+. The output pipe closes only when the pseudo-console is closed, so the end of a program is seen about 500 ms late. Ctrl+C is the byte 0x03 through the console input; whether it ends a given CLI is to be checked with the real `claude` and `codex`. |
| Single instance lock | `internal/lifecycle` (`LockFileEx`) | `internal/lifecycle` tests | |
| Autostart | `internal/lifecycle` (registry `Run` key) | `TestAutostart*` (windows) | |
| Desktop app | `cmd/aotus-desktop` with WebView2 | `go test -tags desktop ./cmd/aotus-desktop` and a manual run | Whether Wails v3 needs a C compiler on Windows (not known). |
| Real CLIs | `claude`, `codex` on Windows | manual: link, log in from the app, chat | Their Windows install paths (`.cmd` shims) and how `proc` resolves them. |

## Results

| Date | Windows version | Runner or machine | Command | Result |
| --- | --- | --- | --- | --- |
| | | | | not run yet |
