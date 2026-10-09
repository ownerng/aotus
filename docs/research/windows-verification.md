# Windows verification

Aotus is developed on Linux. The Windows-specific code was first written and cross-compiled only, then run on the `windows-latest` runner of `.github/workflows/ci.yml` (Windows Server 2025, build 10.0.26100). Results are in the table at the end. What is still **not** verified on Windows: the real `claude` and `codex` CLIs, the desktop window (it builds and its tests pass, but nobody has looked at it), and Windows 10.

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
| Pseudo-console | `internal/proc/pty_windows.go` (ConPTY, Job Object, suspended start) | `TestPTYWindows*` in `pty_windows_test.go`, the terminal tests of `internal/api`, `internal/provider`, `internal/orchestrator` | Needs Windows 10 1809+. Passing: output, input, resize, tree kill. Ctrl+C is the byte 0x03 through the console input; whether it ends a given real CLI is still to be checked with `claude` and `codex`. |
| Single instance lock | `internal/lifecycle` (`LockFileEx`) | `internal/lifecycle` tests | |
| Autostart | `internal/lifecycle` (registry `Run` key) | `TestAutostart*` (windows) | |
| Desktop app | `cmd/aotus-desktop` with WebView2 | `go test -tags desktop ./cmd/aotus-desktop` and a manual run | Whether Wails v3 needs a C compiler on Windows (not known). |
| Real CLIs | `claude`, `codex` on Windows | manual: link, log in from the app, chat | Their Windows install paths (`.cmd` shims) and how `proc` resolves them. |

## Results

| Date | Windows version | Runner or machine | Command | Result |
| --- | --- | --- | --- | --- |
| 2026-10-09 | Windows Server 2025 Datacenter, 10.0.26100 | GitHub `windows-latest` | `go test -count=1 ./...` ([run 37974496236](https://github.com/ownerng/aotus/actions/runs/37974496236)) | all packages ok, including `internal/proc` (ConPTY, Job Object, detached start), `internal/lifecycle` (lock, autostart), `internal/api`, `internal/provider`, `internal/orchestrator`, `internal/bench` |
| 2026-10-09 | same | same | `go build -tags desktop` and `go test -tags desktop ./cmd/aotus-desktop` (job "desktop app on windows-latest") | passes; no C compiler step was needed beyond the runner's own |

### What the first runs found (all fixed)

- **ConPTY output went to our stdout.** Without `STARTF_USESTDHANDLES` in the startup info (with no handles given), the program inherits the parent's standard handles and never writes to the pseudo-console. Found by running a known-good library on the same runner and diffing.
- **The single-instance lock hid its own PID.** Windows file locks are mandatory, so locking byte 0 stopped the second daemon from reading the PID the first one wrote. The lock now sits at offset 1 GiB.
- Smaller: line endings (`.gitattributes` forces LF), `%q` doubling backslashes in an error test, a console-size helper for the fake terminal program.
