<script lang="ts">
  import { Backend } from "../../bindings/aotus/cmd/aotus-desktop";
  import type { ConnectionInfo, ConnectionTest } from "../../bindings/aotus/cmd/aotus-desktop";
  import { app } from "./store.svelte";

  let list = $state<ConnectionInfo[]>([]);
  let results = $state<Record<string, ConnectionTest>>({});
  let name = $state("");
  let address = $state("");
  let error = $state("");
  let busy = $state("");

  async function load() {
    list = ((await Backend.Connections()) ?? []) as ConnectionInfo[];
  }
  $effect(() => { load(); });
  // The list follows the connection in use.
  $effect(() => { app.daemon?.connection; load(); });

  async function add() {
    error = "";
    try {
      await Backend.AddConnection(name.trim(), address.trim());
      name = ""; address = "";
      await load();
    } catch (e) { error = String(e); }
  }
  async function test(n: string) {
    busy = n;
    try { results[n] = (await Backend.TestConnection(n)) as ConnectionTest; } finally { busy = ""; }
  }
  async function use(n: string) {
    busy = n; error = "";
    try { await app.switchTo(n); await load(); } finally { busy = ""; }
  }
  async function remove(n: string) {
    error = "";
    try { await Backend.RemoveConnection(n); await app.refreshAll(); await load(); } catch (e) { error = String(e); }
  }
</script>

<div class="page">
  <h1>Connections</h1>
  <p class="muted">The window works against one daemon at a time: the one on this computer, or one on a server you reach over Tailscale. Your employees keep working in their daemon whichever you look at.</p>

  {#each list as c (c.name)}
    <div class="card">
      <div class="row">
        <span class="dot" class:running={c.active} class:paused={!c.active}></span>
        <b class="grow">{c.remote ? c.name : "This computer"}</b>
        <span class="muted mono">{c.remote ? c.address : "local"}</span>
        <button onclick={() => test(c.name)} disabled={busy === c.name}>Test</button>
        {#if !c.active}<button class="primary" onclick={() => use(c.name)} disabled={busy === c.name}>Use</button>{:else}<span class="muted">in use</span>{/if}
        {#if c.remote}<button class="danger" onclick={() => remove(c.name)}>Remove</button>{/if}
      </div>
      {#if results[c.name]}
        <div class="tray-note" style="margin-top:6px" class:err={!results[c.name].ok}>
          {#if results[c.name].ok}
            Connected as {results[c.name].login || "the local owner"}{results[c.name].device ? ` from ${results[c.name].device}` : ""} · {results[c.name].role}
          {:else}
            {results[c.name].error}
          {/if}
        </div>
      {/if}
    </div>
  {/each}

  <h2 style="margin-top:20px">Add a server</h2>
  <div class="card">
    <p class="muted tray-note" style="margin-top:0">On the server, run <span class="mono">aotusd --tailnet</span> with Tailscale up, and ask its owner to allow your login (<span class="mono">aotus access allow</span>). Nothing secret is entered here: Tailscale tells the server who you are.</p>
    <label for="cn">Name</label><input id="cn" bind:value={name} placeholder="vps" />
    <label for="ca">Address (name:port)</label><input id="ca" bind:value={address} placeholder="vps.tail1234.ts.net:7843" />
    {#if error}<p class="err">{error}</p>{/if}
    <div style="margin-top:12px"><button class="primary" disabled={!name.trim() || !address.trim()} onclick={add}>Save</button></div>
  </div>
</div>
