# Recorded CLI output (fixtures)

Real output captured on 2026-10-09 from the official CLIs by task P1-001, then sanitized: identifiers are replaced by deterministic fake ones, working directories by `/work`, and account fields (email, organization) and lists of installed tools, plugins and skills are removed. No credentials or tokens were ever read or stored.

| File | Source | Used to test |
| --- | --- | --- |
| `claude-stream.jsonl` | `claude -p "..." --output-format stream-json --verbose --include-partial-messages` (Claude Code 2.1.295) | event parsing of a normal turn |
| `claude-interrupted.jsonl` | same command, ended with SIGINT | the final `result` with `terminal_reason: aborted_streaming` |
| `claude-logged-out.jsonl` | same command with a brand-new, logged-out `CLAUDE_CONFIG_DIR` (assistant line carries `error: authentication_failed`) | the needs_login path |
| `claude-auth-status-logged-in.json`, `claude-auth-status-logged-out.json` | `claude auth status` | login detection |
| `codex-failed-model.jsonl` | `codex exec --json` (codex-cli 0.160.0) on an account whose token had expired and whose default model was rejected | error events and the failed-turn path |
| `codex-login-status-*.txt` | `codex login status` | login detection |

Missing on purpose: a successful `codex exec --json` turn. The recording account could not complete a turn (expired token). Record it after `codex login` and add it here; the Codex adapter tests that need it are written against the documented item types until then.
