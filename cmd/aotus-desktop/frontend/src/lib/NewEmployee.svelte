<script lang="ts">
  import { Backend } from "../../bindings/aotus/cmd/aotus-desktop";
  import { app } from "./store.svelte";

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

<div class="page">
  <h1>New employee</h1>
  <p class="muted">An employee is a persistent agent with a name, a job and its own folder. It runs the official CLI of the subscription you choose.</p>
  {#if !app.profiles.length}
    <p>Link a subscription first.</p>
    <button class="primary" onclick={() => (app.view = "profiles")}>Go to subscriptions</button>
  {:else}
    <label for="n">Name</label><input id="n" bind:value={name} placeholder="Atlas" />
    <label for="r">Role</label><input id="r" bind:value={role} placeholder="Code reviewer" />
    <label for="p">Subscription</label>
    <select id="p" bind:value={profileId}>{#each app.profiles as p (p.id)}<option value={p.id}>{p.name} ({p.kind})</option>{/each}</select>
    <label for="s">Instructions</label><textarea id="s" rows="5" bind:value={prompt} placeholder="What this employee is responsible for and how it should work."></textarea>
    {#if error}<p class="err">{error}</p>{/if}
    <div style="margin-top:14px"><button class="primary" disabled={!name.trim() || !profileId} onclick={create}>Create</button></div>
  {/if}
</div>
