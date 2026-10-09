# Conventions

Short rules so every contribution looks the same, whoever (or whatever) writes it.

## Go

- Go version: the one in `go.mod`. Format with `gofmt`; the gate fails on unformatted files.
- Standard library first. Allowed without discussion: nothing outside the standard library. Every other dependency needs an ADR (what, why, license, size, alternatives) and must be pure Go (no cgo).
- Packages are small and named for what they own. No `util`, `common` or `helpers` packages.
- Exported identifiers have doc comments that start with the name. Every package has a doc comment that states its responsibility and what it may import.
- Accept interfaces, return structs. Define an interface where it is consumed, not where it is implemented.
- Pass `context.Context` as the first argument to anything that blocks, does I/O or can be cancelled. Never store a context in a struct.
- Errors: wrap with `fmt.Errorf("doing x: %w", err)`; compare with `errors.Is/As`; never ignore an error silently. User-facing errors say what happened and what to do next.
- Logging: `log/slog`, structured, never log secrets, prompts or file contents. No `fmt.Println` in library code.
- No global mutable state, no `init()` side effects. Wire dependencies in `cmd/aotusd`.
- Concurrency: every goroutine has an owner and a way to stop. Channels are closed by the sender. Prefer `errgroup`-style patterns written with the standard library.
- Platform code goes in `*_linux.go`, `*_darwin.go`, `*_windows.go` with build tags, and every platform-specific function has the same signature in all of them. Never use `runtime.GOOS` branches for large differences.
- File paths: `filepath` for OS paths, `path` for URLs. Never build paths with string concatenation.

## Tests

- Tests are the acceptance criteria. A task lists the test names it requires; create exactly those names.
- Table-driven tests with `t.Run` subtests. `t.Parallel()` where safe. `t.TempDir()` for files. No sleeping to wait for things: use channels, contexts and deterministic fakes.
- Unit tests never use the network, the real home directory, the real clock or a real subscription. Use the fake provider (a small test program that emits scripted output) and fixtures in `testdata/`.
- Adapters must pass the shared provider contract suite. Tests against the real installed CLIs live behind the build tag `realcli` (`make integration`); they use brand-new, logged-out profiles so they never touch the owner's login or quota.
- Security properties get their own tests (for example, "foreign WebSocket origin is rejected", "employee cannot leave its folder").
- Performance budgets are tests in `internal/bench`.
- Run `go test -race ./...` before finishing anything concurrent (`crew verify --full` does it when a C compiler is available; CI always does).

## Commits and changes

- One task per change set. Commit message: `P1-006: turns stream events and cancel immediately` (task ID, imperative, what and why).
- Update docs in the same change: `docs/ARCHITECTURE.md` for design, `docs/API.md` for the API, an ADR for a decision.
- Do not commit generated binaries, data directories, tokens or `.env` files.

## Documents

- Write plain, specific sentences. Numbers with units. No marketing words.
- Decisions go in `docs/DECISIONS/NNNN-title.md` using `docs/DECISIONS/TEMPLATE.md`. Superseded decisions stay, marked `Status: superseded by NNNN`.
- Research goes in `docs/research/` and always records the date it was done and the source URLs.
- `docs/STATUS.md` is generated. Run `go run ./cmd/crew report`; never edit it by hand.

## Harness files

- `harness/tasks/phase-N.json`: tasks of one phase. Next free ID is `P<phase>-<three digits>`. Each task needs a description, acceptance criteria and at least one check. Prefer `pkg` + `tests` checks that name the tests; use `file` + `contains` for documents; use `cmd` only when nothing else fits.
- Never edit `status`, `completed_at` or `gate_passed_at` by hand: use `crew start`, `crew done`, `crew block`, `crew gate`.
- `harness/architecture.json` changes go with the matching change in `docs/ARCHITECTURE.md` and an explanation in the commit.
