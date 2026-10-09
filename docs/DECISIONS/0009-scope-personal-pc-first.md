# 0009 - Scope: personal PC first; VPS and Tailscale next; OS sandbox later

Status: accepted
Date: 2026-10-09

## Context

The first users are individuals on their own computers. The VPS story (daemon on a server, client elsewhere over Tailscale) is valuable but doubles the problem (remote auth, identity, headless login of CLIs, resource limits). Container sandboxes also conflict with the CLIs' logins living in the user's profile.

## Decision

- **Phase 1 (MVP): one personal PC.** The daemon listens on loopback only, with a local token. Employees are isolated by workspace (own folder, scrubbed environment, supervised process tree) plus each CLI's own permission system and our approval layer.
- **Phase 2: remote access.** The same daemon runs on a VPS and the desktop app connects over the user's tailnet through `tsnet` with Tailscale identity. Remote requirements (S4, F18, N5) belong to this phase.
- **Phase 3 and later:** OS-level sandboxing with containers, delegation between employees, schedules, browser, signed skills.
- **Never in this plan:** mobile apps (the owner has no capacity yet).

## Consequences

- `internal/netaccess` has no tsnet code in phase 1, but its API is designed for it.
- S3 is reworded for the MVP to "workspace isolation"; a stronger sandbox is requirement S5 in phase 3. This is a conscious reduction of the previous plan and can be revisited.
