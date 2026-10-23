import { test } from "node:test";
import assert from "node:assert/strict";
import { applyEvent, endTurn } from "./chat.ts";
import type { ChatItem } from "./types.ts";

// started is what the chat holds right after Send: the prompt and an empty,
// pending answer.
function started(turnId: string): ChatItem[] {
  return [
    { key: turnId + ":u", turnId, kind: "user", text: "ping" },
    { key: turnId + ":a", turnId, kind: "assistant", text: "", pending: true },
  ];
}

test("a turn stopped before any text shows Stopped and no empty answer", () => {
  const out = endTurn(started("t1"), "t1", "canceled");
  assert.deepEqual(out.map((c) => [c.kind, c.text]), [["user", "ping"], ["status", "Stopped"]]);
  assert.ok(out.every((c) => !c.pending));
});

test("a turn stopped mid-answer keeps the text and says it was stopped", () => {
  const items = started("t1");
  applyEvent(items, "t1", { kind: "text", text: "po" } as any);
  const out = endTurn(items, "t1", "canceled");
  assert.deepEqual(out.map((c) => [c.kind, c.text]), [["user", "ping"], ["assistant", "po"], ["status", "Stopped"]]);
});

test("a completed turn gets no status line", () => {
  const items = started("t1");
  applyEvent(items, "t1", { kind: "text", text: "pong" } as any);
  const out = endTurn(items, "t1", "completed");
  assert.deepEqual(out.map((c) => [c.kind, c.text, !!c.pending]), [["user", "ping", false], ["assistant", "pong", false]]);
});

test("an interrupted turn says the daemon stopped", () => {
  const out = endTurn(started("t1"), "t1", "interrupted");
  assert.match(out[out.length - 1].text, /daemon stopped/);
});

test("ending a turn twice adds nothing, and other turns are untouched", () => {
  const items = [...started("t0"), ...started("t1")];
  const once = endTurn(items, "t0", "canceled");
  const twice = endTurn(once, "t0", "canceled");
  assert.deepEqual(twice, once);
  assert.deepEqual(once.map((c) => c.turnId + ":" + c.kind), ["t0:user", "t0:status", "t1:user", "t1:assistant"]);
  assert.equal(once[3].pending, true);
});
