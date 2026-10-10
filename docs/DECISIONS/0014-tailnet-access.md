# 0014 - Tailnet access: the system Tailscale, spoken to with the standard library

Status: accepted
Date: 2026-10-09

## Context

Phase 2 (ADR 0009) puts the daemon on a VPS, reached by the desktop app over Tailscale, with Tailscale identity and an allow-list of users (requirements F18, S4). ADR 0009 assumed `tsnet`. The spike (`docs/research/tailnet-spike.md`) measured it: linking `tailscale.com/tsnet` takes the daemon from 11.8 MB to 25.8 MB and idle memory from 11.8 MB to 21 MB (39 MB with a node started), adds 200 `go.sum` lines, and ties the project's Go version to Tailscale's (the newest release needs Go 1.27.1). All of it fits the budgets N4 and N6, but the same job can be done without the dependency.

## Decision

1. **The daemon uses the Tailscale already running on the machine.** It asks the local Tailscale daemon, over its LocalAPI Unix socket, for the machine's tailnet address (`GET /localapi/v0/status?peers=false`), binds one TCP listener to that address only, and identifies each accepted connection with `GET /localapi/v0/whois?addr=<peer>`. Both calls are documented by Tailscale as stable. They are spoken with `net/http` over a Unix socket: **no new dependency**.
2. **Identity comes only from the listener**, never from a request header. The result of `whois` (login name, device, tags) is attached to the connection and reaches the handlers as a `Caller`. A peer that `whois` cannot identify, or a device owned by a tag instead of a person, is refused in version 1.
3. **The loopback listener keeps the bearer token** and serves the local `aotus` CLI and the desktop app on the same machine. The token is never accepted on the tailnet listener, and tailnet identity is never accepted on loopback.
4. **Servers are Linux.** The LocalAPI is a named pipe on Windows and a token-protected local port in the macOS GUI app; running the daemon as a server there is out of scope for phase 2. The desktop app on any operating system only dials TCP over the PC's own Tailscale and needs no LocalAPI.
5. **Allow-list by login.** Default: the owner's own login. Adding another person's login needs a stored acknowledgement (requirement S6, task P2-003), because sharing a provider subscription between people is against the providers' terms and our rule S2.
6. **tsnet stays a later option**, behind a build tag, as a second implementation of the same listener interface, for hosts where Tailscale cannot be installed (containers). Not built in phase 2.

## Consequences

- N4 (memory) and N6 (binary size) are untouched. `go.mod` keeps its Go version.
- The user installs Tailscale on the VPS and on the PC; most people who use a tailnet have it. No auth key exists, so there is no key to store or leak.
- The LocalAPI is Tailscale's, not ours: its two endpoints are covered by a contract test against recorded responses, and a Tailscale upgrade that changes them shows up as a failing test and a clear error ("cannot ask Tailscale who is calling").
- On Linux the socket may refuse `whois` to a user who is not root or the Tailscale operator; `docs/VPS.md` says how to fix it (`tailscale set --operator`). The owner's checklist in the spike document confirms this on a real tailnet.
- Tailscale ACLs still apply on top of ours: a peer the tailnet does not let reach the port never gets to the allow-list.
- The listener is bound to the tailnet address, so if Tailscale is stopped the daemon's tailnet listener goes away and the daemon says so; it never falls back to another address (S1).
