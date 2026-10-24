<script lang="ts">
  import { Backend } from "../../bindings/aotus/cmd/aotus-desktop";
  import { app } from "./store.svelte";
  import Avatar from "./Avatar.svelte";

  let name = $state("");
  let role = $state("");
  let prompt = $state("");
  let profileId = $state(app.profiles[0]?.id ?? "");
  let error = $state("");

  async function create() {
    error = "";
    try {
      const e = await Backend.CreateEmployee({ name: name.trim(), role, system_prompt: prompt, profile_id: profileId } as any);
      await app.refreshEmployees();
      await app.open(e.id);
    } catch (err) { error = String(err); }
  }
</script>

<div class="page"><div class="inner">
  {#if !app.profiles.length}
    <div class="empty">
      <h1>Link a subscription first</h1>
      <p class="lede">An employee runs the official CLI of a plan you already pay for. Link one, then come back.</p>
      <button class="primary" onclick={() => (app.view = "profiles")}>Go to subscriptions</button>
    </div>
  {:else}
    <div class="row" style="gap:16px; margin-bottom:8px">
      <Avatar name={name || "?"} size="xl" />
      <div><h1>New employee</h1><p class="muted" style="margin:0">A persistent agent with a name, a job and its own folder.</p></div>
    </div>
    <div class="card" style="margin-top:20px">
      <label for="n" style="margin-top:0">Name</label><input id="n" bind:value={name} placeholder="Atlas" />
      <label for="r">Role</label><input id="r" bind:value={role} placeholder="Code reviewer" />
      <label for="p">Subscription</label>
      <select id="p" bind:value={profileId}>{#each app.profiles as p (p.id)}<option value={p.id}>{p.name} ({p.kind})</option>{/each}</select>
      <label for="s">Instructions</label><textarea id="s" rows="6" bind:value={prompt} placeholder="What this employee is responsible for and how it should work."></textarea>
      {#if error}<p class="err" style="margin-top:12px">{error}</p>{/if}
      <div class="actions"><button class="primary" disabled={!name.trim() || !profileId} onclick={create}>Create</button></div>
    </div>
  {/if}
</div></div>
