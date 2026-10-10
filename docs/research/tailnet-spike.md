# Tailnet spike: how the daemon joins the tailnet and learns who is calling

Done on 2026-10-09 on Fedora 44, Go 1.26.8. Question for phase 2 (requirements F18, S4): the daemon runs on a VPS and the desktop app reaches it over Tailscale; only identified, allow-listed people may call it. How does the daemon get on the tailnet, and how does it know who a caller is?

Tailscale is not installed on the development machine and there is no tailnet account here, so everything that can be measured offline was measured, and what needs a real tailnet is a checklist at the end.

## The two options

**A. Embed `tailscale.com/tsnet`.** The daemon is its own tailnet node, in user space (no TUN, no root), with its own state directory and an auth key. `tsnet.Server.Listen` gives a listener on the tailnet only, and `LocalClient().WhoIs(remoteAddr)` gives the caller.

**B. Use the Tailscale that is already on the machine.** The daemon asks the local Tailscale daemon, over its LocalAPI (an HTTP API on a Unix socket, `/var/run/tailscale/tailscaled.sock` on Linux), for the machine's tailnet address, binds a normal TCP listener to that address only, and asks the same API `GET /localapi/v0/whois?addr=<peer ip:port>` who each connection comes from. The two calls we need (`status`, `whois`) are documented in Tailscale's own client as "considered a stable API". They are plain HTTP and JSON, so the daemon can speak them with the standard library and no Tailscale code at all. The program that proves it is `docs/research/tailnet-spike/whois.go` (about 80 lines, standard library only).

## Measurements (offline)

All with `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`. The two numbers that matter are the binary size (budget N6) and the idle memory (budget N4).

| | Daemon today | A: tsnet linked into the daemon | B: LocalAPI over the standard library |
| --- | --- | --- | --- |
| Binary size (budget N6: under 50 MB) | 11.8 MB | **25.8 MB** | 11.8 MB (no new code beyond about 100 lines) |
| Idle memory, feature not used (budget N4: under 100 MB) | 11.8 MB | **21.0 MB** | 11.8 MB |
| Idle memory, tsnet node started but not connected to a control server | | **39.0 MB**, 14 threads (a connected node with peers will use more; not measured) | |
| Time until the daemon answers | about 45 ms | about 54 ms (package initialisation of tsnet's dependencies) | about 45 ms |
| New direct dependency | | `tailscale.com` (BSD-3-Clause) | none |
| Packages linked | 233 | 552 | 233 |
| `go.sum` lines | 85 | 293 | 85 |
| Go version it forces on the project | 1.26.0 | **1.26.6 or newer for v1.102.5; the newest release (v1.104) needs Go 1.27.1.** Tailscale moves its minimum Go with every release, so following it means following Go. | none |

Reproduce: the throwaway modules were built in the scratchpad with `go get tailscale.com@v1.102.5` (a copy of the repository with a file that creates a `tsnet.Server`), `go build`, and `/proc/<pid>/status` for memory; startup is the time until `daemon.json` appears.

## What each option asks of the user

| | A: embedded tsnet | B: system Tailscale |
| --- | --- | --- |
| On the VPS | an auth key (a secret to create, store and rotate); the daemon shows up as a second device | install Tailscale and run `tailscale up` (the VPS usually has it anyway, to be reachable at all) |
| On the user's PC | nothing for the browser-less desktop app if it also embeds tsnet (another 26 MB and another node), or Tailscale | Tailscale, which people who use a tailnet already have |
| Works without root and without a TUN device | yes (containers, locked-down hosts) | needs `tailscaled`, so not in a container without it |
| Windows and macOS servers | works | the LocalAPI is a named pipe on Windows and a local port with a token in the macOS GUI app; out of scope (servers are Linux, see ADR 0014) |
| Who can reach the daemon | only tailnet peers, enforced by the tailnet | the listener is bound to the tailnet address, so only tailnet peers can connect; the tailnet's ACLs still apply |
| Identity of the caller | `WhoIs`: login name, device, tags | the same call, over the socket |

## Identity details both options share

- `WhoIs` returns the user's `LoginName` (an e-mail-like name), display name and the device (`ComputedName`, MagicDNS name, `Tags`). A device that belongs to a tag, not to a person, has no person to put on an allow-list: the daemon refuses it in version 1.
- A device shared in from another tailnet shows up with that other person's login, which is exactly why an allow-list of logins is needed, and why S6 exists: letting someone else's login drive your Claude or Codex login is a use of your subscription by another person.
- `whois` can fail for a peer that just connected; the daemon refuses the connection (a closed door, never a guess) and the client retries.
- The traffic between peers is encrypted by WireGuard; the HTTP inside it needs no TLS of its own. The bearer token is not used on this path: identity replaces it.

## Recommendation

**Option B for phase 2**, with the LocalAPI spoken by a small client in the standard library. It adds no dependency, keeps the binary at 11.8 MB and the memory at 12 MB, needs no auth key, and does not tie the project's Go version to Tailscale's. Option A costs 14 MB of binary, 9 to 27 MB of memory and 200 more `go.sum` lines, and forces upgrades of Go on Tailscale's schedule, for the benefit of running where Tailscale cannot be installed. If that benefit is needed later it can be added behind a build tag (`tsnet`) as an extra listener implementation: the listener and identity interfaces of task P2-002 are the seam.

On the user's PC the desktop app dials the VPS's MagicDNS name over the PC's own Tailscale: plain TCP, no Tailscale code in the app.

## What still needs the owner's tailnet (checklist)

Nothing here blocks the offline work, but the phase gate needs it.

1. On a machine of your tailnet (a VPS or any Linux box): `tailscale up` if it is not up. Check `ls -l /var/run/tailscale/tailscaled.sock`.
2. Check the socket can answer `whois` for an ordinary user (not root): run the next step as the user that will run `aotusd`. If it is refused, note the exact error: the fix is `tailscale set --operator=$USER`, to be written in `docs/VPS.md`.
3. `go run docs/research/tailnet-spike/whois.go` on that machine (copy the file; it needs only Go).
4. From **another** device on the tailnet: `curl http://<name-of-the-machine>:7843/`. Expected: `hello you@example.com from <your device> (tags [])`.
5. From a device that is **not** on the tailnet (a phone on mobile data without Tailscale): the same address must not answer.
6. If you have a second tailnet user (or share a node to one), repeat step 4 from there and confirm the login name is theirs.
7. Paste what you saw here, under "Results", with `tailscale version`.

## Results

Not run yet (needs the owner's tailnet).

## Not measured

- A connected tsnet node with peers (memory above 39 MB).
- LocalAPI on Windows and macOS (out of scope for servers).
- The latency `whois` adds per connection (expected: one local Unix-socket round trip; the daemon asks once per connection, not per request).
