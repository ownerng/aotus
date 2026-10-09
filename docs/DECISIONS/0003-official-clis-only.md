# 0003 - Drive official CLIs and APIs only; never touch subscription tokens

Status: accepted
Date: 2026-10-08

## Context

The product value is that users bring the subscriptions they already pay for. Whether a third-party tool may automate a consumer plan depends on each provider's terms, which change. Extracting session tokens or cookies would be fragile, would likely violate terms and would make us a credential-handling risk.

## Decision

- The daemon launches the provider's own official CLI (Claude Code, Codex CLI, ...) which the user installed and logged into. The provider sees its own client.
- We never read, copy, log or forward the CLI's session files, cookies or tokens, and never ask for a provider password.
- For providers or plans where a CLI is not allowed, the user can configure an OpenAI-compatible endpoint with their own API key, stored in the OS credential store.
- Task P0-001 verifies the current terms before any adapter is built. Adapters are isolated behind one Provider interface so one provider can be dropped without touching the rest.

## Consequences

- We depend on each CLI's non-interactive mode and output format: contract tests with recorded fixtures catch changes.
- A provider can change its rules; the fallback is the user's API key or another provider, and an employee keeps its memory when switching.
- Tests assert that adapters never open credential files.
