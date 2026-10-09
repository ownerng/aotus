# Quickstart

From download to a working employee, on your own PC. Aotus runs the official CLIs of the subscriptions you already pay for; it never asks for, reads or copies your credentials.

## 1. What you need

- **At least one official CLI installed and signed in** (you can sign in from inside Aotus):
  - Claude Code: `claude` (Claude Pro or Max)
  - Codex CLI: `codex` (ChatGPT Plus, Pro or Team)
- **Windows 10/11**: the WebView2 runtime (already present on Windows 11 and recent Windows 10).
- **macOS**: nothing else.
- **Linux**: WebKitGTK 4.1 and GTK 3 (`webkit2gtk4.1` and `gtk3` on Fedora; `libwebkit2gtk-4.1-0` and `libgtk-3-0` on Debian and Ubuntu).

## 2. Install

Download the package for your system from the releases page, unpack it anywhere and keep the files together (`aotusd`, `aotus-desktop`, `aotus`). Nothing else is installed or changed.

## 3. Open it

Run `aotus-desktop`. It finds the daemon, or starts `aotusd` in the background if none is running. Closing the window quits the app but leaves the daemon, and with it your employees, working.

1. **Subscriptions → Link a subscription.** Choose Claude Code or Codex CLI, give it a name, and press *Link*. Press *Check* to see the CLI's version and whether it is signed in. If it is not, press *Log in*: you get the official CLI's own login in a terminal, in a folder private to this profile.
2. **New employee.** Give it a name, a role, instructions and a subscription.
3. **Chat or Terminal.** Chat streams the answer and lets you stop it. Terminal shows the official CLI's own screen running in the daemon; close the tab and it keeps running.
4. **Approvals.** When an employee wants to do something that needs your permission, it waits here until you allow or deny it.

You can also drive everything without the window: `aotus status`, `aotus profiles`, `aotus chat <employee> <prompt>` (see `aotus` without arguments).

## 4. Build from source

Needs Go (version in `go.mod`), Node 22 or newer, and for the desktop app a C compiler and the WebView libraries:

| System | Install |
| --- | --- |
| Fedora | `sudo dnf install gcc pkgconf-pkg-config gtk3-devel webkit2gtk4.1-devel` |
| Debian, Ubuntu | `sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev` |
| macOS | `xcode-select --install` |
| Windows | Go, Node and the WebView2 runtime; no C compiler is expected to be needed, but this is not yet verified (see `docs/research/windows-verification.md`) |

```
go install -tags gtk3 github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28   # Linux; drop "-tags gtk3" elsewhere
make build      # aotusd and aotus into bin/ (no cgo)
make desktop    # the window into bin/
```

The daemon and the CLI build with `CGO_ENABLED=0` for every platform; only the window needs cgo.

## 5. Where things are

Your data lives in `~/.aotus` (or `$AOTUS_HOME`): the database, one folder per subscription profile (the CLI's own configuration and login), one folder per employee, and the token that lets the app talk to the daemon. API keys go to the operating system's credential store, never to these files.
