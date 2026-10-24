// How a linked subscription runs. Plain TypeScript, so node's own test runner
// can check it (profiles.test.ts) without a browser.

export type Mode = "structured" | "terminal" | "api";

// defaultMode is the provider's default: the chat (structured) for Claude Code
// and Codex CLI; Claude's chat is behind a notice the person accepts when
// linking (ADR 0016). The daemon applies the same defaults when no mode is sent.
export function defaultMode(kind: string): Mode {
  return kind === "claude" || kind === "codex" ? "structured" : "api";
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

export type Login = "computer" | "separate";

// defaultLogin: a CLI subscription uses the sign-in already on the computer
// that runs Aotus, so a person signed in to Claude Code or Codex does not log
// in again (ADR 0015). An API profile has a key, not a sign-in.
export function defaultLogin(kind: string): Login | "" {
  return kind === "claude" || kind === "codex" ? "computer" : "";
}

// loginChoices names the two ways in, for the computer the window talks to.
export function loginChoices(where: string): { value: Login; label: string }[] {
  return [
    { value: "computer", label: `Use the sign-in already on ${where}` },
    { value: "separate", label: "A separate sign-in, just for Aotus" },
  ];
}
