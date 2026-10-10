# Running Aotus on a VPS

From a blank Linux server to an employee working there, and your desktop app talking to it over Tailscale. Your employees run on the server, so they keep working when your PC is off.

**Who this is for.** Someone who is comfortable with an SSH session and a tailnet. If you only use one PC, you do not need any of this: `docs/QUICKSTART.md` is enough.

**What it is not.** It is not hosting: you rent the server, the official CLIs run on it under your own subscriptions, and nothing goes through us. It is not a sandbox either (see "What is not protected").

## How it fits together

```
your PC (desktop app)  --Tailscale-->  VPS: aotusd (listens on the tailnet address only)
                                              |-- claude, codex   (the official CLIs, signed in on the VPS)
                                              `-- employees' folders, memory, history
```

- `aotusd` on the VPS listens on its **tailnet address only**, never on a public one. Anything that is not a device of your tailnet cannot even open a connection.
- It asks the Tailscale on the same machine who is calling. A device must belong to a person, and that person must be you (the **owner**) or be on the **allow-list**. There is no password and no token to steal on this path.
- Tailscale encrypts the traffic; the API inside it needs no extra TLS.

## 1. The server

- A Linux server with systemd (Debian 12 or Ubuntu 22.04 and newer are what this guide uses). **Recommended: 2 vCPU and 4 GB of RAM** for ten employees; the daemon itself needs about 15 MB, the official CLIs are what use memory. The measured behaviour of ten employees on two CPUs is in `docs/BENCHMARKS.md` (N5).
- Create an ordinary user for Aotus and use it for everything below. Give it **no sudo**: employees run commands as this user, so what it cannot do, they cannot do.

```
sudo adduser --disabled-password --gecos "" aotus
```

(Administrative steps below say `sudo`; run them from your admin account, and the rest as `aotus`, for example `sudo -iu aotus`.)

## 2. Tailscale on the server

Install Tailscale as its documentation says for your distribution, then join your tailnet:

```
sudo tailscale up
```

Aotus asks the local Tailscale who is calling through its local socket, `/var/run/tailscale/tailscaled.sock`. Check that the user `aotus` can use it:

```
sudo -iu aotus tailscale status        # must work without sudo
```

If Tailscale refuses the user, make that user the Tailscale operator and try again:

```
sudo tailscale set --operator=aotus
```

(This is the expected fix; it has not been confirmed on a real server yet, see "What is verified".)

Note the machine's MagicDNS name (`tailscale status` or the admin console), for example `vps.tail1234.ts.net`. Tailscale ACLs still apply on top of ours: if your tailnet policy does not let your PC reach port 7843 of the server, it will not connect.

## 3. The official CLIs, on the server

Install the CLIs you use (Claude Code, Codex CLI) **as the `aotus` user**, following each vendor's instructions, and check:

```
claude --version
codex --version
```

The service puts `~/.local/bin`, `~/.npm-global/bin`, `~/.bun/bin` and `~/go/bin` of that user on the PATH. If you install them somewhere else, either put a link in `~/.local/bin` or choose the binary when you link the subscription in the app.

**Do not copy login files from your PC.** You sign in on the server (step 6).

## 4. Aotus on the server

Download the VPS package for your architecture from the releases page (`linux-amd64` or `linux-arm64`) and put `aotusd` and `aotus` where the `aotus` user finds them:

```
mkdir -p ~/.local/bin
tar xzf aotus-daemon-*-linux-*.tar.gz
cp aotus/aotusd aotus/aotus ~/.local/bin/
aotusd --version
```

Install the service, as the `aotus` user (no root needed):

```
aotusd --service install --tailnet --owner you@example.com
```

`--owner` is your **tailnet login** (the one you sign in to Tailscale with). If you leave it out, the owner is the person the server's Tailscale node belongs to; a tagged node has no person, so there you must pass `--owner`.

This writes `~/.config/systemd/user/aotusd.service`, enables and starts it, and turns on "linger" so that the service **starts at boot with nobody logged in** and survives you closing your SSH session. If turning on linger needs an administrator, the command tells you the line to run:

```
sudo loginctl enable-linger aotus
```

Useful commands (as `aotus`):

```
aotusd --service status
systemctl --user status aotusd
journalctl --user -u aotusd -f          # the log; it never contains secrets
aotusd --service uninstall              # removes the service, keeps your data
```

`aotusd` runs up to **four employees' turns at once** and queues the rest (terminal programs are not counted). Each official CLI at work can use a few hundred MB, so on a small server keep it low; raise it when you know how much yours uses: `aotusd --service install --max-turns 6 --tailnet ...`. `docs/BENCHMARKS.md` (N5) has the numbers and the procedure to measure your own.

The unit restarts the daemon if it crashes (after 5 s), gives it 30 s to stop its employees cleanly, and then ends anything left in its control group. It sets `NoNewPrivileges`, so an employee cannot gain privileges with `sudo` or a setuid program. To limit memory for the daemon and everything it starts, install with `--service` and edit the unit (`MemoryMax=3G`) or install it as a system unit, below.

### As a system service instead

If you prefer a unit that belongs to the system (root installs it, it runs as `aotus`, and it adds extra hardening):

```
sudo aotusd --service install --service-scope system --service-user aotus --data-dir /home/aotus/.aotus --tailnet --owner you@example.com
```

## 5. Connect from your PC

On your PC, with Tailscale running:

```
aotus connection add vps vps.tail1234.ts.net:7843
aotus connection test vps         # prints who you are there, e.g. "you@example.com ... role owner"
```

In the desktop app, open **Connections**, add the server (name and `host:port`), press **Test**, then **Use**. The window then works against the server. Closing the window never stops the employees; closing the app on the server side is not possible from the window.

If the test fails, the message says what to do: "cannot reach the tailnet" (Tailscale is not running on your PC or the name is wrong), "the daemon did not answer" (is `aotusd --tailnet` running on the server?), "not on the allow-list" (ask the owner).

## 6. Sign the CLIs in, on the server

In the app, with the server in use: **Subscriptions → Link a subscription** (Claude Code or Codex CLI), then **Log in**. That opens the **login terminal**: the CLI's own sign-in running on the server, in a folder private to that profile. You type there what the CLI asks for; where it prints a link, open it in your PC's browser and paste back what it gives you.

Nothing is copied from your PC. The login stays on the server, in `~/.aotus/profiles/<profile>/`, which is why the server must be protected like a password manager (see "What is not protected").

## 7. Who may connect: the allow-list

By default **only you** (the owner) can use the daemon. To let another person see and work with it:

```
aotus --connection vps access allow them@example.com --acknowledge
```

Run it **without** `--acknowledge` first: it prints the notice you are agreeing to. The point of the notice is the providers' terms: a Claude or ChatGPT subscription is licensed to the person who pays for it, and letting someone else use it can get the account limited or closed. Aotus cannot check that for you.

A person on the list can **see** everything but can use an employee only on a subscription profile you **share** with them, one by one:

```
aotus --connection vps access share PROFILE-ID them@example.com
aotus --connection vps access list
aotus --connection vps access deny them@example.com     # removes them and everything shared
```

Every action records who did it (`login (device)`); `GET /employees/{id}/audit` shows it.

## 8. Updating

Download the new package, copy `aotusd` and `aotus` over the old ones, and restart:

```
systemctl --user restart aotusd
```

Terminal sessions that were running come back after the restart; turns that were in flight are marked interrupted.

## 9. Backup

Everything is in the data directory (`~/.aotus` or the `--data-dir` you gave):

| What | Where | Note |
| --- | --- | --- |
| History, employees, memory index, allow-list, audit log | `aotus.db` (+ `-wal`, `-shm` files) | one SQLite database |
| Subscription logins of the CLIs | `profiles/<id>/` | **secrets**; encrypt the backup |
| Employees' working folders and notes | `employees/<name>/` | your employees' files |
| The local token | `token` | only for loopback; not needed on the tailnet |

Simple and safe backup: stop the service, copy, start it again.

```
systemctl --user stop aotusd
tar czf aotus-backup-$(date +%F).tar.gz -C ~ .aotus
systemctl --user start aotusd
```

Restoring is the reverse: stop the service, unpack into the same place, start it. The backup contains your subscription logins: keep it as protected as the server.

## What is not protected

Read this before you put real accounts on a server.

- **A compromised VPS exposes the logins stored on it.** The CLIs' session files are on the server, and so is everything your employees touch. If someone gets a shell as the `aotus` user, or root, they have your subscriptions' sessions, the employees' files, memory and history. Keep the server patched, use SSH keys only, and consider a firewall that closes everything except what Tailscale needs.
- **Employees run commands as the `aotus` user.** Aotus asks you before they read or write outside their folder, run commands or use the network, but this is workspace isolation and the official CLIs' own permission systems, **not an operating-system sandbox**. A tricked employee (prompt injection) can do whatever that user can do. That is why the user has no sudo and the service sets `NoNewPrivileges`. OS-level sandboxing is planned (S5, phase 3).
- **Anyone on the allow-list can read every conversation and the audit log**, shared profile or not. Add only people you trust with that.
- **Your tailnet is part of the security.** Anyone who controls a device of yours, or your Tailscale login, controls your access. Use device approval and ACLs on the tailnet.
- **A revoked person keeps an open connection until it closes.** Identity is checked when a connection is opened. After `access deny`, restart the daemon if you need open connections gone at once.

## Out of scope

- Other init systems than systemd, and servers that are not Linux (Windows, macOS). The desktop app and CLI run on all three; the **server** is Linux.
- Containers where Tailscale cannot be installed. A built-in tailnet node (tsnet) is a possible later addition (ADR 0014).
- Several people sharing one server with their own separate logins and data. Phase 2 is one owner with an allow-list.

## What is verified

- The unit files (user and system) pass `systemd-analyze verify`, and the user unit was run on a real systemd (Fedora 44): it starts, restarts after `kill -9`, stops cleanly (the discovery file is removed), and logs to the journal.
- The installer's commands are tested against a recorder, not against a real boot.
- **Not yet verified, waiting for a real server and tailnet:** starting at boot with nobody logged in (linger), the system-scope unit, Tailscale accepting the `whois` question from a non-root user (`--operator`), the real CLIs signing in through the login terminal on a server, and the N5 numbers on a 2 vCPU / 4 GB machine. When you run these, add what you saw below.

### Results from a real server

Not run yet.
