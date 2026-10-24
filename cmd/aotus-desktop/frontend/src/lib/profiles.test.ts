import { test } from "node:test";
import assert from "node:assert/strict";
import { defaultLogin, defaultMode, loginChoices, modeChoices, needsNotice } from "./profiles.ts";

test("each provider gets the default ADR 0016 sets: the chat", () => {
  assert.equal(defaultMode("codex"), "structured");
  assert.equal(defaultMode("claude"), "structured");
  assert.equal(defaultMode("openai-api"), "api");
});

test("CLI subscriptions offer chat and terminal, default first", () => {
  assert.deepEqual(modeChoices("codex").map((c) => c.value), ["structured", "terminal"]);
  assert.deepEqual(modeChoices("claude").map((c) => c.value), ["structured", "terminal"]);
  assert.deepEqual(modeChoices("openai-api").map((c) => c.value), ["api"]);
});

test("only Claude's structured mode is behind the notice", () => {
  assert.equal(needsNotice("claude", "structured"), true);
  assert.equal(needsNotice("claude", "terminal"), false);
  assert.equal(needsNotice("codex", "structured"), false);
});

test("a CLI subscription uses this computer's sign-in by default", () => {
  assert.equal(defaultLogin("claude"), "computer");
  assert.equal(defaultLogin("codex"), "computer");
  assert.equal(defaultLogin("openai-api"), "");
  const c = loginChoices("this computer");
  assert.deepEqual(c.map((x) => x.value), ["computer", "separate"]);
  assert.match(c[0].label, /already on this computer/);
  assert.match(loginChoices("vps").at(0)!.label, /already on vps/);
});
