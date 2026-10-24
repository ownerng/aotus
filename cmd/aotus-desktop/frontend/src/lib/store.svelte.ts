import { Events } from "@wailsio/runtime";
import { Backend, Shell } from "../../bindings/aotus/cmd/aotus-desktop";
import type { DaemonState, Settings } from "../../bindings/aotus/cmd/aotus-desktop";
import type { Approval, Employee, Profile, Turn, Update, Event as Ev } from "../../bindings/aotus/internal/client/models";
import type { ChatItem } from "./types";
import { applyEvent, endTurn, markPending } from "./chat";

export type View = "chat" | "terminal" | "profiles" | "new-employee" | "approvals" | "settings" | "connections";

const FIRST_PAGE = 20;
const EAGER_TURNS = 8; // answers loaded right away; older ones on demand

class AppState {
  daemon = $state<DaemonState | null>(null);
  error = $state("");
  employees = $state<Employee[]>([]);
  profiles = $state<Profile[]>([]);
  approvals = $state<Approval[]>([]);
  // notices: id -> the exact text a person accepts (loaded with the rest, once connected)
  notices = $state<Record<string, string>>({});
  settings = $state<Settings>({ keep_in_tray: false } as Settings);
  version = $state("");

  view = $state<View>("profiles");
  selected = $state<string>("");
  // Only the selected employee's conversation is kept in memory.
  chat = $state<ChatItem[]>([]);
  oldest = $state(""); // started_at of the oldest turn on screen
  hasOlder = $state(false);

  get current(): Employee | undefined {
    return this.employees.find((e) => e.id === this.selected);
  }

  async start() {
    this.version = await Shell.Version();
    this.settings = await Shell.GetSettings();
    Events.On("daemon", (e) => (this.daemon = e.data));
    Events.On("update", (e) => this.onUpdate(e.data as Update));
    Events.On("approval", (e) => this.onApproval(e.data as Approval));
    // After a dropped link or a slow stream, catch up. What is on screen is not
    // cleared first.
    Events.On("resync", () => this.refreshAll());
    try {
      this.daemon = await Backend.Connect();
      await this.refreshAll();
      if (this.employees.length) this.open(this.employees[0].id);
    } catch (err) {
      this.error = String(err);
    }
  }

  async refreshAll() {
    try {
      const [emps, profs, apps, notes] = await Promise.all([
        Backend.Employees(), Backend.Profiles(), Backend.Approvals(), Backend.Notices(),
      ]);
      [this.employees, this.profiles, this.approvals] = [emps, profs, apps] as [Employee[], Profile[], Approval[]];
      this.notices = Object.fromEntries((notes ?? []).map((n) => [n.id, n.text]));
      if (this.selected) await this.loadChat(this.selected);
    } catch (err) {
      this.error = String(err);
    }
  }

  async refreshEmployees() {
    this.employees = (await Backend.Employees()) as Employee[];
  }

  onApproval(a: Approval | null) {
    if (a && !this.approvals.some((x) => x.id === a.id)) this.approvals = [...this.approvals, a];
  }

  async answer(id: string, allow: boolean, remember: string) {
    await Backend.Answer(id, allow, remember);
    this.approvals = this.approvals.filter((a) => a.id !== id);
  }

  // Switching to another daemon (this computer or a VPS) reloads everything.
  async switchTo(name: string) {
    this.error = "";
    try {
      this.daemon = await Backend.SwitchConnection(name);
    } catch (err) {
      this.error = String(err);
      this.daemon = await Backend.Connect().catch(() => this.daemon);
      return;
    }
    this.selected = "";
    this.chat = [];
    this.employees = [];
    this.profiles = [];
    this.approvals = [];
    await this.refreshAll();
    if (this.employees.length) await this.open(this.employees[0].id);
    else this.view = "profiles";
  }

  async open(id: string) {
    this.selected = id;
    this.view = "chat";
    await this.loadChat(id);
  }

  // ---- conversation ----

  async loadChat(id: string) {
    const turns = ((await Backend.History(id, FIRST_PAGE, "")) as Turn[]).slice().reverse();
    if (id !== this.selected) return;
    const items: ChatItem[] = [];
    for (let i = 0; i < turns.length; i++) {
      const t = turns[i];
      items.push(...(await this.turnItems(t, i >= turns.length - EAGER_TURNS)));
    }
    this.chat = items;
    this.oldest = turns[0]?.started_at ?? "";
    this.hasOlder = turns.length === FIRST_PAGE;
  }

  async loadOlder() {
    const id = this.selected;
    const turns = ((await Backend.History(id, FIRST_PAGE, this.oldest)) as Turn[]).slice().reverse();
    if (id !== this.selected || !turns.length) { this.hasOlder = false; return; }
    const items: ChatItem[] = [];
    for (const t of turns) items.push(...(await this.turnItems(t, false)));
    this.chat = [...items, ...this.chat];
    this.oldest = turns[0].started_at;
    this.hasOlder = turns.length === FIRST_PAGE;
  }

  async turnItems(t: Turn, withAnswer: boolean): Promise<ChatItem[]> {
    const out: ChatItem[] = [{ key: t.id + ":u", turnId: t.id, kind: "user", text: t.prompt }];
    if (withAnswer) {
      const events = (await Backend.TurnEvents(t.id)) as Ev[];
      for (const ev of events) applyEvent(out, t.id, ev);
      if (t.state === "running" || t.state === "queued") markPending(out, t.id);
      else return endTurn(out, t.id, t.state);
    } else {
      out.push({ key: t.id + ":m", turnId: t.id, kind: "note", text: "Show the answer", more: true });
    }
    return out;
  }

  async showAnswer(item: ChatItem) {
    const events = (await Backend.TurnEvents(item.turnId)) as Ev[];
    const out: ChatItem[] = [];
    for (const ev of events) applyEvent(out, item.turnId, ev);
    const at = this.chat.findIndex((c) => c.key === item.key);
    if (at >= 0) this.chat = [...this.chat.slice(0, at), ...out, ...this.chat.slice(at + 1)];
  }

  async send(text: string) {
    const id = this.selected;
    const turnId = await Backend.Send(id, text);
    // The daemon announces the turn; add the prompt now so it appears at once.
    if (!this.chat.some((c) => c.turnId === turnId)) {
      this.chat = [...this.chat, { key: turnId + ":u", turnId, kind: "user", text }, { key: turnId + ":a", turnId, kind: "assistant", text: "", pending: true }];
    }
  }

  async cancel() {
    if (this.selected) await Backend.Cancel(this.selected);
  }

  onUpdate(u: Update | null) {
    if (!u) return;
    if (u.kind === "session" || u.kind === "turn_started" || u.kind === "turn_ended" || u.kind === "turn_queued") {
      this.refreshEmployees().catch(() => {});
    }
    if (u.employee_id !== this.selected) return;
    if (u.kind === "event" && u.event) {
      const next = this.chat.slice();
      applyEvent(next, u.turn_id, u.event);
      this.chat = next;
    } else if (u.kind === "turn_ended") {
      this.chat = endTurn(this.chat, u.turn_id, u.state);
    }
  }

  async saveSettings(s: Settings) {
    await Shell.SaveSettings(s);
    this.settings = s;
  }
}

export const app = new AppState();
