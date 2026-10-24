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

<div class="page"><div class="inner">
  <h1>Connections</h1>
  <p class="lede">This window works with one daemon at a time: the one on this computer, or one on a server you reach over Tailscale. Employees keep working in their daemon whichever you look at.</p>

  <h3 class="section">Daemons</h3>
  <div class="list">
    {#each list as c (c.name)}
      <div class="lrow">
        <div class="tile"><span class="dot" class:running={c.active}></span></div>
        <div class="grow">
          <b class="name">{c.remote ? c.name : "This computer"}</b>
          <div class="desc mono ellipsis">{c.remote ? c.address : "local"}{c.active ? " · in use" : ""}</div>
          {#if results[c.name]}
            <div class="tray-note" style="margin-top:2px" class:err={!results[c.name].ok} class:faint={results[c.name].ok}>
              {#if results[c.name].ok}
                Connected as {results[c.name].login || "the local owner"}{results[c.name].device ? ` from ${results[c.name].device}` : ""} · {results[c.name].role}
              {:else}
                {results[c.name].error}
              {/if}
            </div>
          {/if}
        </div>
        <button onclick={() => test(c.name)} disabled={busy === c.name}>Test</button>
        {#if !c.active}<button class="primary" onclick={() => use(c.name)} disabled={busy === c.name}>Use</button>{/if}
        {#if c.remote}<button class="ghost danger" onclick={() => remove(c.name)}>Remove</button>{/if}
      </div>
    {/each}
  </div>

  <h3 class="section">Add a server</h3>
  <div class="card">
    <p class="muted tray-note" style="margin:0">On the server, run <span class="mono">aotusd --tailnet</span> with Tailscale up, and ask its owner to allow your login (<span class="mono">aotus access allow</span>). Nothing secret is entered here: Tailscale tells the server who you are.</p>
    <label for="cn">Name</label><input id="cn" bind:value={name} placeholder="vps" />
    <label for="ca">Address (name:port)</label><input id="ca" bind:value={address} placeholder="vps.tail1234.ts.net:7843" />
    {#if error}<p class="err" style="margin-top:12px">{error}</p>{/if}
    <div class="actions"><button class="primary" disabled={!name.trim() || !address.trim()} onclick={add}>Save</button></div>
  </div>
</div></div>
