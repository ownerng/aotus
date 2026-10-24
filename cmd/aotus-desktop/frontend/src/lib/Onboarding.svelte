<script lang="ts">
  // First run: welcome, link the subscriptions already on this computer in one
  // step, then meet the first teammate. Nothing is created until the person
  // presses Link; FindCLIs only asks each CLI how it is.
  import { Backend } from "../../bindings/aotus/cmd/aotus-desktop";
  import type { FoundCLI } from "../../bindings/aotus/internal/client/models";
  import { app, skipOnboarding } from "./store.svelte";
  import Mark from "./Mark.svelte";
  import Icon from "./Icon.svelte";
  import NewEmployee from "./NewEmployee.svelte";

  let step = $state<"welcome" | "subscriptions" | "teammate">("welcome");
  let found = $state<FoundCLI[]>([]);
  let picked = $state<Record<string, boolean>>({});
  let accepted = $state(false);
  let looking = $state(false);
  let busy = $state(false);
  let error = $state("");

  const tiles: Record<string, string> = { claude: "CC", codex: "CX" };
  const usable = (f: FoundCLI) => f.installed && !f.linked;
  const chosen = $derived(found.filter((f) => usable(f) && picked[f.kind]));
  const needsClaudeNotice = $derived(chosen.some((f) => f.kind === "claude"));
  const notice = $derived(app.notices["claude-headless"] ?? "");

  async function look() {
    looking = true; error = "";
    try {
      found = ((await Backend.FindCLIs()) ?? []) as FoundCLI[];
      for (const f of found) if (picked[f.kind] === undefined) picked[f.kind] = usable(f) && f.login === "logged_in";
    } catch (e) { error = String(e); } finally { looking = false; }
  }
  function skip() { skipOnboarding(); app.view = "profiles"; }
  async function begin() { step = "subscriptions"; await look(); }

  async function link() {
    busy = true; error = "";
    try {
      for (const f of chosen) {
        const p = await Backend.CreateProfile({ kind: f.kind, name: f.title, computer_login: true } as any);
        if (f.kind === "claude") await Backend.AcceptNotice(p.id, "claude-headless");
      }
      await app.refreshAll();
      step = "teammate";
    } catch (e) { error = String(e); } finally { busy = false; }
  }
  function status(f: FoundCLI): string {
    if (f.linked) return `Linked as "${f.linked}"`;
    if (!f.installed) return "Not installed on this computer";
    const v = f.version ? `${f.version}` : "installed";
    if (f.login === "logged_in") return `${v} · signed in`;
    if (f.login === "logged_out") return `${v} · not signed in yet: you can sign in from Aotus after linking`;
    return v;
  }
</script>

<header class="solo-top">
  <Mark size={16} /><span class="wordmark">aotus</span>
  <span class="grow"></span>
  {#if step !== "welcome"}
    <span class="steps" aria-label={step === "subscriptions" ? "Step 1 of 2" : "Step 2 of 2"}>
      <span class="step" class:on={true}></span><span class="step" class:on={step === "teammate"}></span>
    </span>
  {/if}
  <button class="ghost" onclick={skip}>{step === "teammate" ? "Later" : "Skip"}</button>
</header>

<div class="solo-body" class:center={step === "welcome"}><div class="solo-inner">
  {#if step === "welcome"}
    <div class="empty" style="padding:0">
      <Mark size={56} looking />
      <h1 style="font-size:30px; margin-top:22px">Teammates that keep working</h1>
      <p class="lede">Aotus runs AI teammates on the subscriptions you already pay for (Claude, ChatGPT and more), on this computer. They have a name, a job and a memory, and they keep working when you close the window.</p>
      <div class="row" style="margin-top:14px">
        <button class="primary" onclick={begin}>Get started</button>
      </div>
    </div>
  {:else if step === "subscriptions"}
    <h1>Your subscriptions</h1>
    <p class="lede">These are the official CLIs on this computer. Aotus uses the sign-in you already have, as your terminal does, and never reads it.</p>
    <div class="list" style="margin-top:20px">
      {#each found as f (f.kind)}
        <label class="lrow" style="cursor:{usable(f) ? 'pointer' : 'default'}; margin:0">
          <div class="tile">{tiles[f.kind] ?? "?"}</div>
          <div class="grow">
            <b class="name" style="color:var(--text)">{f.title}</b>
            <div class="desc">{status(f)}</div>
            {#if !f.installed}<div class="faint tray-note" style="margin-top:4px">Install it from its official site, sign in once in your terminal, then press Check again.</div>{/if}
          </div>
          {#if usable(f)}<input type="checkbox" class="switch" bind:checked={picked[f.kind]} />{:else if f.linked}<span class="check"><Icon name="check" /></span>{/if}
        </label>
      {:else}
        <p class="muted">{looking ? "Looking for Claude Code and Codex…" : "Nothing found yet."}</p>
      {/each}
    </div>
    {#if needsClaudeNotice}
      <div class="notice" style="margin-top:16px">
        <p style="margin:0 0 10px">{notice || "Loading the notice…"}</p>
        <label class="row" style="margin:0; color:var(--text); gap:10px; cursor:pointer"><input type="checkbox" style="width:auto" bind:checked={accepted} disabled={!notice} /> I have read this and accept it</label>
      </div>
    {/if}
    {#if error}<p class="err" style="margin-top:12px">{error}</p>{/if}
    <div class="actions">
      <button onclick={look} disabled={looking}>{looking ? "Looking…" : "Check again"}</button>
      {#if found.some((f) => f.linked) && !chosen.length}
        <button class="primary" onclick={() => (step = "teammate")}>Continue</button>
      {:else}
        <button class="primary" disabled={busy || !chosen.length || (needsClaudeNotice && !accepted)} onclick={link}>{busy ? "Linking…" : chosen.length > 1 ? `Link ${chosen.length} subscriptions` : "Link"}</button>
      {/if}
    </div>
  {:else}
    <NewEmployee embedded />
  {/if}
</div></div>
