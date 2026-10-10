# Daemon API

The daemon (`aotusd`) serves this API on loopback and, when started with the tailnet option (phase 2, ADR 0014), on its Tailscale address. It is the only contract between the daemon and its clients (the desktop app, the `aotus` command). Everything here is implemented in `internal/api` and covered by its tests.

## Connecting

1. Read the **discovery file** `daemon.json` in the data directory (`~/.aotus`, or `$AOTUS_HOME`): `{"pid", "address", "started_at", "version"}`. `address` is `127.0.0.1:<port>`. The file can be stale after a crash: confirm by connecting.
2. Read the **token** from the file `token` in the same directory (owner-only; 64 hex characters).
3. Send `Authorization: Bearer <token>` with **every** request, WebSocket upgrades included. The token is never accepted in the URL.

Base path: `/api/v1`. Bodies are JSON (`Content-Type: application/json`), at most 1 MiB; unknown fields are rejected.

## Rules every request goes through

| Rule | Result when broken |
| --- | --- |
| The `Host` header must name this computer (`127.0.0.1`, `localhost`, `[::1]`, any port). This stops DNS rebinding. | `403 bad_host` |
| A request carrying an `Origin` header is refused (programs send none; web pages always do). An operator may allow-list origins in the daemon's configuration. | `403 bad_origin` |
| A valid bearer token. | `401 unauthorized` with `WWW-Authenticate: Bearer` |

### Remote callers (tailnet listener)

Remote callers are authenticated by their Tailscale identity instead of the token.

The same API is served to other devices of the owner's tailnet. There the rules differ, and the two ways in never mix:

| Rule | Result when broken |
| --- | --- |
| Identity comes only from the connection: the daemon asks the Tailscale on its machine who the peer is, before the first byte is read. **No header is ever trusted for identity**, and the bearer token is not used (and not accepted) on this path. A device Tailscale cannot tie to a person (for example a tagged server) never gets a connection. | connection closed |
| The `Host` header must be one of the daemon's tailnet names (MagicDNS name, short name or tailnet address). | `403 bad_host` |
| `Origin` is refused as above. | `403 bad_origin` |
| The login must be the owner's or be on the **allow-list**. Refusals are written to the audit log with the caller. | `403 not_allowed` |

The token path is the mirror image: on loopback, identity headers are ignored and the token is required.

Roles: the **owner** (the login recorded when the daemon was set up for remote use, and anyone holding the local token) can do everything. A **guest** (on the allow-list) can read everything, but can use an employee (turns, terminals, approvals, memory writes, pause, delete, create) **only on a profile the owner shared with them**, and cannot manage profiles, subscriptions' logins, remembered permissions or the allow-list: `403 owner_only` and `403 profile_not_shared`.

Every audit row records the **caller**: `login (device)` for a tailnet caller, `local` for the token, empty for what the daemon did on its own. An approval answer is recorded as the person who gave it.

No response carries CORS headers, and `OPTIONS` preflights fail. Responses have `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`.

## Errors

Every failure is `{"error": {"code": "<stable code>", "message": "<for humans>"}}`. Clients switch on `code`.

| Status | Codes |
| --- | --- |
| 400 | `bad_json`, `invalid`, `no_provider`, `unsupported_mode`, `unknown_notice`, `no_login_flow` |
| 401 / 403 | `unauthorized`, `bad_host`, `bad_origin` |
| 404 | `not_found`, `profile_not_found`, `request_not_found` |
| 409 | `name_taken`, `busy` (employee already has a turn), `paused`, `not_terminal`, `not_running`, `no_turn`, `profile_in_use`, `notice_required` |
| 413 | `too_large` |
| 500 / 503 | `internal` (details are in the daemon log, never in the response), `shutting_down`, `no_credential_store` |

## Resources

`{id}` is the ID returned when the resource was created.

### Status

| Method and path | Purpose |
| --- | --- |
| `GET /status` | `{"status":"ok","version":"...","employees":N}` |

### Profiles (subscriptions)

A profile is how to reach one subscription: which official CLI, with its own isolated configuration directory. Profiles hold no secrets.

| Method and path | Purpose |
| --- | --- |
| `GET /profiles` | List. Each: `id, name, kind, binary, mode, model, base_url, accepted_notices, extra_env, terms_checked_at`. |
| `POST /profiles` | Create `{kind, name, binary, mode, model, base_url, extra_env}`. `kind` is `claude`, `codex` or `openai-api`; `mode` is `terminal`, `structured` or `api`. `extra_env` is plain configuration (a proxy); names that look like credentials are refused. `201` with the profile. |
| `DELETE /profiles/{id}` | Delete; `409 profile_in_use` while employees use it. The configuration directory (and the login in it) is left on disk. |
| `GET /profiles/{id}/detect` | What the CLI says: `{installed, version, version_ok, modes, login, detail}`. `login` is `logged_in`, `logged_out` or `unknown`. `detail` is a message to show the user. Never reads credentials. |
| `POST /profiles/{id}/notices` | `{notice}`: the user accepted a notice (e.g. `claude-headless`, required for Claude Code's structured mode). The text must have been shown to them. `204`. |
| `PUT /profiles/{id}/api-key` | `{key}`: store an API key in the operating system's credential store (`openai-api` profiles). `204`. The key is never returned by any endpoint. |
| `DELETE /profiles/{id}/api-key` | Remove it. `204`. |
| `POST /profiles/{id}/login` | `{rows, cols}` (optional): start the CLI's own login flow in a terminal inside the profile. `200 {"running":true}`. |
| `GET /profiles/{id}/login/ws` | WebSocket onto that login terminal (see Terminals). `409 not_running` if none. |

### Employees

| Method and path | Purpose |
| --- | --- |
| `GET /employees` | List. Each: `id, name, role, system_prompt, profile_id, state` (`active`/`paused`), `permission_mode, allowed_tools, created_at`, and live status: `mode, working, turn_id, terminal_running`. |
| `POST /employees` | Create `{name, role, system_prompt, profile_id, permission_mode, allowed_tools}`. `201`. |
| `GET /employees/{id}` | One employee. |
| `DELETE /employees/{id}` | Retire: work stops, the folder goes to the trash, the history and audit log are kept. |
| `POST /employees/{id}/pause`, `/resume` | `204`. Pausing stops the current work. |

### Turns (structured and API modes)

| Method and path | Purpose |
| --- | --- |
| `POST /employees/{id}/turns` | `{prompt}` → `202 {"turn_id":"..."}` at once. Events arrive on the events WebSocket. For an employee in terminal mode the prompt is typed into the running program and `turn_id` is `""`. |
| `POST /employees/{id}/cancel` | End the running (or queued) turn immediately. `204`, or `409 no_turn`. |
| `POST /employees/{id}/interrupt` | Ask politely (Ctrl+C). `204`. |
| `GET /employees/{id}/history?limit=&before=` | Turns, newest first. `before` is an RFC 3339 `started_at`: pass the oldest one you have to load the page behind it. Each: `id, employee_id, prompt, state, error, started_at, ended_at, cost_usd, input_tokens, output_tokens`. States: `queued, running, completed, canceled, failed, interrupted`. |
| `GET /turns/{id}/events` | The stored events of a turn, in order (`seq`, `kind`, ...). Consecutive text is one entry. |
| `GET /employees/{id}/audit` | The employee's audit log, oldest first: `at, kind, action, detail, decision, caller`. |

At most 4 turns run at once across all employees (configurable); the rest wait their turn in line, in the order they were sent.

### Terminals (the official interactive UI)

| Method and path | Purpose |
| --- | --- |
| `POST /employees/{id}/terminal` | `{rows, cols}` (optional): launch the employee's program in a terminal the daemon keeps alive with no window open. `409 not_terminal` if the profile does not run in terminal mode. |
| `DELETE /employees/{id}/terminal` | Stop it on purpose (it will not be restarted or brought back). |
| `GET /employees/{id}/terminal/ws` | WebSocket onto the running terminal. `409 not_running` if it is not running. |

**Terminal WebSocket.** Server to client: binary frames. The first ones are the recent output (replay), so a window opened late can redraw; then live output follows with nothing lost or repeated. Client to server: binary frames are typed input; a text frame `{"type":"resize","rows":N,"cols":N}` resizes. Closing the socket does not stop the program. The server closes with status 1000 when the program ends or the viewer falls too far behind (reattach to continue).

### Events

`GET /events[?employee={id}]` is a WebSocket of text frames, each a JSON object with `type`:

| `type` | Content |
| --- | --- |
| `hello` | Sent once when the stream is open. |
| `update` | `update`: `{seq, kind, employee_id, turn_id, state, detail, event}`. `seq` rises by one per update, so gaps are visible. `kind`: `turn_queued`, `turn_started`, `event`, `turn_ended` (`state` is the turn state), `session` (`state`: `running`, `restarting`, `stopped`, `crashed`; `detail` says why). `event` is `{kind, text, session_id, code, tool_*, limits, reason, ...}` with `kind` one of `session, text, tool_request, tool_result, limits, error, done`. Every turn ends with exactly one `done`. |
| `approval` | `approval`: `{id, employee_id, kind, target}`: an action waits for the user (see Approvals). |

A client that cannot keep up (more than 256 updates behind) is closed with status 1013 ("try again later"): reconnect and reload what it missed from the history. The agents are never slowed down by a viewer.

### Access (who may call remotely; owner only)

| Route | |
| --- | --- |
| `GET /me` | Who the daemon thinks you are: `{method, login, device, role}` (`role` is `owner` or `guest`). Any caller. |
| `GET /access` | `{owner, entries: [{login, added_at, added_by, profiles}], sharing_notice}`. `profiles` are the profile IDs shared with that person. |
| `POST /access` | `{login, acknowledged}`. Adds a person to the allow-list. **`acknowledged` must be `true`**: `400 acknowledgement_required` otherwise, and the error carries the notice to show. The exact notice text is stored with the entry. `409 is_owner` for the owner's own login. Logins are compared case-insensitively. |
| `DELETE /access/{login}` | Removes the person and everything shared with them. |
| `POST /profiles/{id}/shares` | `{login}`. The owner lets that person use the profile (they must be on the list first: `400 not_on_list`). Nothing is shared by default. |
| `DELETE /profiles/{id}/shares/{login}` | Withdraws it. |

Why the acknowledgement: letting another person use your Claude or ChatGPT subscription can break the provider's terms (`docs/research/provider-terms.md`, rule S2). Aotus cannot check it, so the owner says in a stored act that they understand.

### Approvals and remembered decisions

Sensitive actions (run a command, read or write outside the employee's folder, network) are denied unless approved; an unanswered request is denied after 15 minutes. Every decision is written to the audit log first.

| Method and path | Purpose |
| --- | --- |
| `GET /approvals` | Requests waiting for the user: `id, employee_id, kind, target, at`. |
| `POST /approvals/{id}` | `{allow, remember}`: `remember` is `none` (this once), `exact` (this employee, this kind, this target) or `kind` (this employee, every target of this kind). `204`; `404 request_not_found` if it was answered or expired. |
| `GET /employees/{id}/grants` | Remembered decisions: `kind, target, allow` (`target` `*` means every target of the kind). |
| `PUT /employees/{id}/grants` | `{kind, target, allow}`: set one directly. An exact target wins over `*`. |
| `DELETE /employees/{id}/grants?kind=&target=` | Forget one. |

Kinds: `read_inside` and `write_inside` (the employee's own folder, always allowed), `read_outside`, `write_outside`, `run_command`, `network`.

### Memory

| Method and path | Purpose |
| --- | --- |
| `GET /employees/{id}/memory/search?q=&limit=` | Full-text search of this employee's facts and Markdown notes (all words must match; the last may be a prefix). Hand edits to the notes are picked up first. Each hit: `kind` (`fact`/`note`), `ref` (fact ID or note path), `snippet` (matches in `[brackets]`). |
| `GET`, `POST /employees/{id}/memory/facts` | List, or add `{key, body}` (→ `201 {"id"}`). |
| `DELETE /employees/{id}/memory/facts/{fact}` | Forget a fact. |
| `POST /employees/{id}/memory/sync` | Index the notes in the employee's `memory/` folder now: `{indexed, removed, skipped}`. |

Memory is per employee and never crosses to another.
