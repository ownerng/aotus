// Package provider adapts the official CLIs (Claude Code, Codex CLI, ...) and
// OpenAI-compatible APIs to one Provider interface, and manages profiles: one
// profile per subscription, each with its own isolated configuration
// directory. It never reads, copies or forwards subscription tokens, cookies
// or credential files, and never rotates profiles automatically.
//
// It starts processes only through internal/proc.
package provider
