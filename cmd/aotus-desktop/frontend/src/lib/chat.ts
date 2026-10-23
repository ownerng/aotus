// How the chat folds a turn's events into lines on screen. Plain TypeScript,
// so node's own test runner can check it (chat.test.ts) without a browser.
import type { Event as Ev } from "../../bindings/aotus/internal/client/models";
import type { ChatItem } from "./types";

// applyEvent folds one provider event into the list of chat lines.
export function applyEvent(items: ChatItem[], turnId: string, ev: Ev) {
  const last = items[items.length - 1];
  switch (ev.kind) {
    case "text":
      if (last && last.kind === "assistant" && last.turnId === turnId) {
        items[items.length - 1] = { ...last, text: last.text + ev.text, pending: true };
      } else {
        items.push({ key: `${turnId}:a${items.length}`, turnId, kind: "assistant", text: ev.text, pending: true });
      }
      break;
    case "tool_request":
      items.push({ key: `${turnId}:t${ev.tool_id || items.length}`, turnId, kind: "tool", text: `${ev.tool_name} ${toolArgs(ev)}` });
      break;
    case "tool_result":
      items.push({ key: `${turnId}:r${items.length}`, turnId, kind: "tool", text: (ev.tool_error ? "failed: " : "→ ") + clip(ev.tool_output, 300) });
      break;
    case "error":
      items.push({ key: `${turnId}:e${items.length}`, turnId, kind: "error", text: ev.text });
      break;
    case "done":
      for (let i = 0; i < items.length; i++) if (items[i].turnId === turnId && items[i].pending) items[i] = { ...items[i], pending: false };
      break;
  }
}

export function markPending(items: ChatItem[], turnId: string) {
  const i = items.map((c) => c.turnId + c.kind).lastIndexOf(turnId + "assistant");
  if (i >= 0) items[i] = { ...items[i], pending: true };
  else items.push({ key: turnId + ":a", turnId, kind: "assistant", text: "", pending: true });
}

function toolArgs(ev: Ev): string {
  try {
    return clip(typeof ev.tool_input === "string" ? ev.tool_input : JSON.stringify(ev.tool_input), 200);
  } catch {
    return "";
  }
}

function clip(s: string, n: number): string {
  return s && s.length > n ? s.slice(0, n) + "…" : (s ?? "");
}

// endLabels say how a turn ended when it did not end normally.
const endLabels: Record<string, string> = {
  canceled: "Stopped",
  interrupted: "Interrupted: the daemon stopped while this was running",
};

// endTurn settles a turn that has ended: nothing of it is pending any more, an
// answer that never got any text is dropped, and a stopped or interrupted
// turn says so. Calling it again changes nothing.
export function endTurn(items: ChatItem[], turnId: string, state: string): ChatItem[] {
  const out = items
    .filter((c) => !(c.turnId === turnId && c.kind === "assistant" && !c.text))
    .map((c) => (c.turnId === turnId && c.pending ? { ...c, pending: false } : c));
  const label = endLabels[state];
  if (!label || out.some((c) => c.turnId === turnId && c.kind === "status")) return out;
  const at = out.map((c) => c.turnId).lastIndexOf(turnId);
  if (at < 0) return out;
  out.splice(at + 1, 0, { key: turnId + ":s", turnId, kind: "status", text: label });
  return out;
}
