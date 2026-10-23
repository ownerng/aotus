import { test } from "node:test";
import assert from "node:assert/strict";
import { defaultMode, modeChoices, needsNotice } from "./profiles.ts";

test("each provider gets the default the terms decision sets", () => {
  assert.equal(defaultMode("codex"), "structured");
  assert.equal(defaultMode("claude"), "terminal");
  assert.equal(defaultMode("openai-api"), "api");
});

test("CLI subscriptions offer chat and terminal, default first", () => {
  assert.deepEqual(modeChoices("codex").map((c) => c.value), ["structured", "terminal"]);
  assert.deepEqual(modeChoices("claude").map((c) => c.value), ["terminal", "structured"]);
  assert.deepEqual(modeChoices("openai-api").map((c) => c.value), ["api"]);
});

test("only Claude's structured mode is behind the notice", () => {
  assert.equal(needsNotice("claude", "structured"), true);
  assert.equal(needsNotice("claude", "terminal"), false);
  assert.equal(needsNotice("codex", "structured"), false);
});
