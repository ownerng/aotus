# CLI contracts: what the official CLIs really do

Observed on 2026-10-09 on Linux (Fedora 44) with **Claude Code 2.1.295** and **Codex CLI 0.160.0** (`codex-cli 0.160.0`), both installed by the owner and logged in. Method: `--help`, `--version`, isolated configuration directories, and a handful of tiny real prompts (about 8 seconds and under USD 0.05 of equivalent API cost for Claude Code). No credential or session file was opened; account fields (email, organization) that some commands print were discarded. Fixtures recorded from this session are in `internal/provider/testdata/`.

Tags: **[verified]** observed here. **[unverified]** not observed; taken from help text or prior knowledge and must be confirmed before relying on it. Behavior on macOS and Windows was **not** tested.

Terms-of-use status of these routes is in `docs/research/provider-terms.md`; integration modes are defined in ADR 0008.

## 1. Summary

| | Claude Code | Codex CLI |
| --- | --- | --- |
| Detect and version | `claude --version` prints `2.1.295 (Claude Code)` [verified] | `codex --version` prints `codex-cli 0.160.0` [verified] |
| Login status command | `claude auth status` prints JSON on stdout; exit code is 0 even when logged out [verified] | `codex login status` prints `Logged in using ChatGPT` or `Not logged in` on stderr; exit 1 when logged out [verified] |
| Isolate configuration | `CLAUDE_CONFIG_DIR=<dir>` [verified] | `CODEX_HOME=<dir>` [verified] |
| Structured (headless) mode | `claude -p ... --output-format stream-json --verbose` [verified] | `codex exec --json ...` [verified for the failure path only] |
| Resume a session | `--resume <session_id>` keeps the same session id [verified] | `codex exec resume <id>` or `--last` [unverified] |
| Cancel a turn | SIGINT: final `result` event, then exit [verified] | not tested [unverified] |
| Host the interactive UI in a pseudo-terminal | works, exits on two Ctrl+C [verified] | works, ended by SIGINT [verified] |
| Minimum version tested | 2.1.295 | 0.160.0 |

## 2. Claude Code

### 2.1 Detection and login status

- Find the binary on `PATH` (here `~/.local/bin/claude`) or use the path the user configured. `claude --version` prints the version.
- `claude auth status` prints JSON and exits 0 in both states. Keys seen: `loggedIn` (bool), `authMethod` (`claude.ai` or `none`), `apiProvider`, `analyticsDisabled`, `projectsDirectory`, `configDirectory`, `email`, `orgId`, `orgName`, `subscriptionType` (for example `pro`). **The adapter must read only `loggedIn`, `authMethod` and `subscriptionType`, and must never log or store the rest** (it contains personal data). Fixtures: `claude-auth-status-logged-in.json`, `claude-auth-status-logged-out.json`.
- `claude auth login` and `claude auth logout` exist. Login is done by the user, in the CLI's own flow, never by Aotus.

### 2.2 Isolating the config directory

`CLAUDE_CONFIG_DIR=<dir> claude auth status` on an empty directory returns `loggedIn: false`; the CLI created its own files there (`.claude.json`, `.claude.json.lock`, `backups/`). That confirms a profile can have its own config directory with its own login and its own session transcripts. [verified]

### 2.3 Structured mode (headless)

Command used: `claude -p "<prompt>" --output-format stream-json --verbose --include-partial-messages --max-turns 1`, with stdin redirected from `/dev/null`.

- **Always give the CLI an explicit stdin.** Without it, it waits 3 seconds and prints `Warning: no stdin data received in 3s, proceeding without it...` to stderr. For a text prompt, close stdin right after starting the process. [verified]
- Output is one JSON object per line. Event types seen in a normal turn, in order [verified]:

| `type` / `subtype` | Meaning | Fields that matter |
| --- | --- | --- |
| `system` / `hook_started`, `hook_response` | The user's own hooks ran. Appear only when the user has hooks configured | `hook_name`, `outcome` |
| `system` / `init` | Session start | `session_id`, `model`, `permissionMode`, `claude_code_version`, `tools`, `mcp_servers`, `cwd`, `apiKeySource` |
| `system` / `status` | Progress (`requesting`) | `status` |
| `stream_event` with `event.type` = `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop` | Token streaming. Text arrives in `content_block_delta` with `delta.type` = `text_delta` and `delta.text` | `event`, `session_id`, `ttft_ms` |
| `assistant` | The complete assistant message so far | `message.content`, `message.usage` |
| `rate_limit_event` | Plan limit state | `rate_limit_info.status`, `resetsAt`, `rateLimitType` (`five_hour`), `unifiedWindows` (`five_hour`, `seven_day`) |
| `result` / `success` | Final event | `result`, `is_error`, `stop_reason`, `terminal_reason` (`completed`), `session_id`, `total_cost_usd`, `usage`, `num_turns` |

- The adapter must **ignore unknown event types and unknown fields**; hook events and many fields depend on the user's configuration and the CLI version.
- `rate_limit_event` is useful: it lets the UI show plan limits without guessing.
- Other relevant flags from `--help` [verified as listed, not all exercised]: `--input-format stream-json` (realtime streaming input, with `--print`), `--replay-user-messages`, `--permission-mode` (`acceptEdits`, `auto`, `bypassPermissions`, `manual`, `dontAsk`, `plan`), `--permission-prompts host|none` (who answers permission prompts under `--print`), `--allowedTools`, `--disallowedTools`, `--add-dir`, `--append-system-prompt`, `--system-prompt`, `--model`, `--mcp-config`, `--settings`, `--bare` (skips hooks and other config; API key only), `--session-id <uuid>`, `--max-turns`, `--restricted`.
- Under `-p` the workspace trust dialog is skipped; run it only in folders the employee is allowed to use.

### 2.4 Resume

`claude -p "..." --resume <session_id> --output-format json` returned the answer with the **same** `session_id`, `is_error: false` and `terminal_reason: completed`. `--continue` (most recent) and `--fork-session` also exist [help text; unverified]. [resume verified]

### 2.5 Cancel

Sending SIGINT to `claude -p` mid-turn made it write a final `result` event with `subtype: error_during_execution`, `is_error: true`, `terminal_reason: aborted_streaming` and end (the shell showed the `timeout` wrapper's code 124, so the CLI's own exit code was not captured: **unverified**). Fixture: `claude-interrupted.jsonl`. This matches `proc.Process.Interrupt()` (SIGINT to the process group) on Unix; Windows has no equivalent and needs the stdin protocol or a hard cancel.

### 2.6 Interactive UI in a pseudo-terminal

Started under a pseudo-terminal (40 rows by 120 columns, `TERM=xterm-256color`), it drew its interface (97 escape sequences in 7 seconds, beginning with the workspace trust prompt) and ended within a second of two Ctrl+C. This is the basis of the `terminal` mode, which is the default for Claude Code (owner's decision, 2026-10-09). [verified]

### 2.7 Other facts

- Claude Code has its own background sessions: `claude --bg`, `claude agents`, `claude attach <id>`, `claude logs <id>`, `claude respawn`. Aotus supervises its own processes instead, but these exist and could matter later. [help text; unverified]
- Wall time of a one-word turn: about 8.5 s, mostly start-up and the user's hooks.

## 3. Codex CLI

### 3.1 Detection and login status

- `codex --version` prints `codex-cli 0.160.0`.
- `codex login status` prints a line of text: `Logged in using ChatGPT` or `Not logged in`. **It writes it to stderr, not stdout** (stdout is empty), and exits 0 when logged in and 1 when logged out (verified 2026-10-09 while testing the adapter against the real CLI). When `CODEX_HOME` points under a temporary directory it also prints a warning about "helper binaries" first, so read both streams and take the last line that is a status.
- **The login status can be wrong.** On the recording machine it said `Logged in using ChatGPT` while every request failed with `401 ... Your authentication token has expired. Please try refreshing it.` (printed on stderr by `codex_models_manager`). The adapter must treat that error as "needs login" and not trust `login status` alone. `codex doctor` has an `auth` section (it reports the storage mode `File` and whether tokens are stored; it does not validate them). [verified]

### 3.2 Isolating the config directory

`CODEX_HOME=<dir> codex login status` on an empty directory prints `Not logged in`. Codex refuses to create helper binaries when its home is under a temporary directory, so **profile directories must live under the data directory, never under `/tmp`**. Credentials are kept by Codex in a file inside its home (storage mode `File`, as `codex doctor` reports); Aotus never opens it. [verified]

### 3.3 Structured mode

`codex exec --json --skip-git-repo-check -s read-only "<prompt>" < /dev/null` writes JSON lines on stdout [verified for the failure path]:

```
{"type":"thread.started","thread_id":"..."}
{"type":"turn.started"}
{"type":"error","message":"{...status 400 ... model not supported with a ChatGPT account...}"}
{"type":"turn.failed","error":{"message":"..."}}
```

and the process exits with code 1. A model-metadata warning arrives as `{"type":"item.completed","item":{"id":"item_0","type":"error","message":"..."}}`. Fixture: `codex-failed-model.jsonl`.

- The default model of the recording account (and `gpt-5-codex`, `gpt-5`) was rejected with `The '<model>' model is not supported when using Codex with a ChatGPT account`. The adapter must show this text to the user and let the profile choose a model.
- Without stdin redirected, Codex prints `Reading additional input from stdin...`. Close stdin.
- **Success events were not observed.** Expected, from Codex's public documentation and not verified here: `item.started` and `item.completed` with item types such as `agent_message`, `reasoning`, `command_execution`, `file_change`, `mcp_tool_call`, `web_search`, and `turn.completed` with token usage. The adapter's success-path tests must wait for a recording made after `codex login`.
- Useful flags: `-s read-only|workspace-write|danger-full-access`, `-C <dir>`, `--add-dir`, `-m <model>`, `--ephemeral` (do not persist the session), `--ignore-user-config`, `--output-schema <file>`, `-o <file>` (last message), `-c key=value` (config overrides), `--skip-git-repo-check`. [help text]

### 3.4 Resume, fork and the app server

- `codex exec resume [SESSION_ID|--last] [PROMPT]` resumes a session; `codex resume` and `codex fork` do it interactively. [help text; unverified]
- `codex app-server` is **experimental**: a protocol server with `daemon` and `proxy` subcommands and generators for its schema (`generate-json-schema`, `generate-ts`); `codex agents` browses sessions on a "shared local app-server daemon". It is the richer route (approvals, threads) but experimental, so the MVP uses `exec --json` and revisits it in a later phase. [help text; unverified]

### 3.5 Cancel and pseudo-terminal

- Cancel was not tested. Expect SIGINT to stop a turn; confirm when a successful turn can be recorded. [unverified]
- The interactive UI runs under a pseudo-terminal (2,880 bytes and 352 escape sequences in 7 seconds, with the start-up hint "To get started, describe a task...") and was ended by SIGINT after Ctrl+C. [verified]

## 4. What the adapters must do (consequences)

1. Start the official binary with an **exact environment** (`proc.Spec.Env`) that includes the profile's `CLAUDE_CONFIG_DIR` or `CODEX_HOME`, `HOME` (or the platform equivalent), `PATH` and the allow-listed variables, nothing else.
2. In structured mode, **close stdin** immediately for text prompts, and set `Dir` to the employee workspace.
3. Parse JSON lines **tolerantly**: unknown types and fields are ignored; malformed lines are reported as a diagnostic event, never a crash.
4. Login detection runs the CLI's own status command with the profile's environment, reads only the fields listed above, and treats authentication errors in a turn (`401`, "token has expired") as "needs login".
5. Cancel with SIGINT first (Unix), wait for the final event, then use `Process.Cancel()` for the tree.
6. Surface `rate_limit_event` data (Claude Code) and model errors (Codex) to the user as they are.
7. Minimum versions: the adapters declare **minimum version** 2.1.295 for Claude Code and 0.160.0 for Codex CLI until older versions are tested; older ones produce a distinct "unsupported version" error.

## 5. Open items

- Record a successful `codex exec --json` turn after the owner runs `codex login`, add it to the fixtures and extend the Codex parser tests.
- Capture the real exit code of `claude -p` after SIGINT and the behavior of Codex on cancel.
- Exercise `claude --input-format stream-json` (multi-turn input over stdin) and `codex exec resume`.
- Repeat the isolation and login-status checks on macOS and Windows (environment variable names and file locations may differ).
