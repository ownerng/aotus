// How a linked subscription runs. Plain TypeScript, so node's own test runner
// can check it (profiles.test.ts) without a browser.

export type Mode = "structured" | "terminal" | "api";

// defaultMode is the provider's default: Codex CLI structured, Claude Code
// terminal (docs/research/provider-terms.md, Decision). The daemon applies
// the same defaults when no mode is sent.
export function defaultMode(kind: string): Mode {
  switch (kind) {
    case "codex": return "structured";
    case "claude": return "terminal";
    default: return "api";
  }
}

// modeChoices are the ways a kind of subscription can run, default first.
export function modeChoices(kind: string): { value: Mode; label: string }[] {
  if (kind !== "claude" && kind !== "codex") return [{ value: "api", label: "API" }];
  const all: { value: Mode; label: string }[] = [
    { value: "structured", label: "Chat: answers stream into the app" },
    { value: "terminal", label: "Terminal: the official CLI's own screen" },
  ];
  const first = defaultMode(kind);
  return [...all.filter((c) => c.value === first), ...all.filter((c) => c.value !== first)];
}

// needsNotice reports whether a mode is behind a notice the user must accept
// (Claude Code's structured mode, `claude -p`).
export function needsNotice(kind: string, mode: string): boolean {
  return kind === "claude" && mode === "structured";
}
