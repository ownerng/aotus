# 0012 - API keys live in the operating system credential store (go-keyring)

Status: accepted
Date: 2026-10-09

## Context

The API fallback (an OpenAI-compatible endpoint with the user's own key, ADR 0008) needs a place for the key that is not the database, a file or a log. Our rule is no cgo in our code.

## Decision

Use `github.com/zalando/go-keyring` (MIT). It is pure Go: it calls the `security` tool on macOS, the Windows Credential Manager API on Windows, and the Secret Service over D-Bus on Linux (GNOME Keyring, KDE Wallet). Only `internal/credstore` imports it, and only the daemon's composition root uses `credstore`: providers receive a `KeySource` function that returns a key for a reference, so they cannot store one. A profile holds a reference (`profile/<id>/api-key`), never the key.

## Consequences

- On a Linux machine without a Secret Service running (a bare server, some minimal desktops), storing a key fails; the error says so and the API mode stays unavailable there. A file fallback is deliberately not offered.
- Tests use the library's in-memory mock and never touch the real store.
- go-keyring pulls `github.com/godbus/dbus/v5` as a dependency.
