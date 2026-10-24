<script lang="ts">
  // A teammate sets itself up: the person says what they need, the teammate's
  // own subscription drafts its name, role, instructions and SOUL.md, and the
  // person reviews, edits or redrafts it before hiring. Writing it by hand is
  // still there for those who prefer it.
  import { Backend } from "../../bindings/aotus/cmd/aotus-desktop";
  import { app } from "./store.svelte";
  import { needsNotice } from "./profiles";
  import Avatar from "./Avatar.svelte";
  import Mark from "./Mark.svelte";

  // embedded: shown inside the first-run flow, which has its own frame.
  let { embedded = false }: { embedded?: boolean } = $props();
  type Step = "describe" | "drafting" | "review" | "manual";
  let step = $state<Step>("describe");
  let description = $state("");
  let profileId = $state(app.profiles[0]?.id ?? "");
  let name = $state("");
  let role = $state("");
  let instructions = $state("");
  let soul = $state("");
  let showSoul = $state(false);
  let error = $state("");
  let busy = $state(false);
  let accepted = $state(false);

  const profile = $derived(app.profiles.find((p) => p.id === profileId));
  // Claude's chat is behind its notice; a draft is a chat, so ask first.
  const gated = $derived(!!profile && needsNotice(profile.kind, profile.mode === "terminal" ? "structured" : profile.mode) && !profile.accepted_notices?.includes("claude-headless"));
  const notice = $derived(app.notices["claude-headless"] ?? "");
  const examples = [
    "Someone to review my pull requests and flag security problems",
    "A researcher who reads the news on my industry every morning and sends me a short summary",
    "An assistant to keep my notes and to-dos in order",
  ];

  async function draft() {
    error = ""; step = "drafting";
    try {
      if (gated && profile) { await Backend.AcceptNotice(profile.id, "claude-headless"); await app.refreshAll(); }
      const d = await Backend.DraftEmployee(profileId, description.trim());
      name = d.name; role = d.role; instructions = d.instructions; soul = d.soul;
      step = "review";
    } catch (e) {
      error = String(e).replace(/^RuntimeError:\s*/, "");
      step = "describe";
    }
  }
  async function hire() {
    error = ""; busy = true;
    try {
      const e = await Backend.CreateEmployee({ name: name.trim(), role, system_prompt: instructions, soul, profile_id: profileId } as any);
      await app.refreshEmployees();
      await app.open(e.id);
    } catch (err) { error = String(err).replace(/^RuntimeError:\s*/, ""); } finally { busy = false; }
  }
  function manual() { error = ""; step = "manual"; }
</script>

<div class={embedded ? "" : "page"}><div class="inner">
  {#if !app.profiles.length}
    <div class="empty">
      <h1>Link a subscription first</h1>
      <p class="lede">A teammate runs the official CLI of a plan you already pay for. Link one, then come back.</p>
      <button class="primary" onclick={() => (app.view = "profiles")}>Go to subscriptions</button>
    </div>
  {:else if step === "describe"}
    <h1>{embedded ? "Meet your first teammate" : "New teammate"}</h1>
    <p class="lede">Say what you need help with, in your own words. Your teammate sets up its own name, role, instructions and SOUL.md; you review it before it starts.</p>
    <div class="card" style="margin-top:20px">
      <label for="d" style="margin-top:0">What do you need help with?</label>
      <textarea id="d" rows="5" bind:value={description} placeholder={examples[0]}></textarea>
      <div class="row" style="flex-wrap:wrap; margin-top:10px; gap:6px">
        {#each examples as ex (ex)}<button class="chip" style="cursor:pointer" onclick={() => (description = ex)}>{ex}</button>{/each}
      </div>
      {#if app.profiles.length > 1}
        <label for="p">Runs on</label>
        <select id="p" bind:value={profileId}>{#each app.profiles as p (p.id)}<option value={p.id}>{p.name} ({p.kind})</option>{/each}</select>
      {/if}
      {#if gated}
        <div class="notice">
          <p style="margin:0 0 10px">{notice || "Loading the notice…"}</p>
          <label class="row" style="margin:0; color:var(--text); gap:10px; cursor:pointer"><input type="checkbox" style="width:auto" bind:checked={accepted} disabled={!notice} /> I have read this and accept it</label>
        </div>
      {/if}
      {#if error}<p class="err" style="margin-top:12px">{error}</p>{/if}
      <div class="actions">
        <button class="ghost" onclick={manual}>Write it myself</button>
        <button class="primary" disabled={!description.trim() || !profileId || (gated && !accepted)} onclick={draft}>Set up my teammate</button>
      </div>
    </div>
  {:else if step === "drafting"}
    <div class="empty">
      <Mark size={36} looking />
      <h1>Setting up your teammate…</h1>
      <p class="lede">{profile?.name ?? "Your subscription"} is writing its name, role, instructions and SOUL.md. This usually takes 10 to 30 seconds.</p>
    </div>
  {:else}
    <div class="row" style="gap:16px; margin-bottom:8px">
      <Avatar name={name || "?"} size="xl" />
      <div class="grow">
        {#if step === "review"}
          <p class="faint tray-note" style="margin:0">Here is your teammate. Change anything you like.</p>
          <input class="title-input" bind:value={name} aria-label="Name" />
          <input class="role-input" bind:value={role} aria-label="Role" placeholder="Role" />
        {:else}
          <h1>New teammate</h1><p class="muted" style="margin:0">Written by you, field by field.</p>
        {/if}
      </div>
    </div>
    <div class="card" style="margin-top:16px">
      {#if step === "manual"}
        <label for="n" style="margin-top:0">Name</label><input id="n" bind:value={name} placeholder="Atlas" />
        <label for="r">Role</label><input id="r" bind:value={role} placeholder="Code reviewer" />
        <label for="p2">Runs on</label>
        <select id="p2" bind:value={profileId}>{#each app.profiles as p (p.id)}<option value={p.id}>{p.name} ({p.kind})</option>{/each}</select>
      {/if}
      <label for="s" style={step === "review" ? "margin-top:0" : ""}>Instructions</label>
      <textarea id="s" rows="7" bind:value={instructions} placeholder="What this teammate is responsible for and how it should work."></textarea>
      {#if step === "review" || showSoul || soul}
        <label for="soul">SOUL.md <span class="faint">· its character, values and way of working; it lives in its folder and it can grow</span></label>
        <textarea id="soul" class="mono" rows="10" bind:value={soul}></textarea>
      {:else}
        <button class="ghost" style="margin-top:12px; padding-left:0" onclick={() => (showSoul = true)}>+ Add a SOUL.md</button>
      {/if}
      {#if error}<p class="err" style="margin-top:12px">{error}</p>{/if}
      <div class="actions">
        {#if step === "review"}
          <button class="ghost" onclick={() => (step = "describe")}>Change the description</button>
          <button onclick={draft}>Try another</button>
        {:else}
          <button class="ghost" onclick={() => (step = "describe")}>Describe it instead</button>
        {/if}
        <button class="primary" disabled={busy || !name.trim() || !profileId} onclick={hire}>{busy ? "Hiring…" : `Hire ${name.trim() || "teammate"}`}</button>
      </div>
    </div>
  {/if}
</div></div>
