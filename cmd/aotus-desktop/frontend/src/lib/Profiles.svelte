<script lang="ts">
  import { Backend } from "../../bindings/aotus/cmd/aotus-desktop";
  import { app } from "./store.svelte";
  import TerminalView from "./TerminalView.svelte";
  import { defaultLogin, defaultMode, loginChoices, modeChoices, needsNotice } from "./profiles";

  const kindName: Record<string, string> = { claude: "Claude Code", codex: "Codex CLI", "openai-api": "OpenAI-compatible API" };
  const kindTile: Record<string, string> = { claude: "CC", codex: "CX", "openai-api": "API" };
  const modeName: Record<string, string> = { structured: "Chat", terminal: "Terminal", api: "API" };

  let kind = $state("claude");
  let mode = $state(defaultMode("claude"));
  let login = $state(defaultLogin("claude"));
  let accepted = $state(false); // the person read the notice of the chosen mode
  const notices = $derived(app.notices);
  // Where "this computer" is: the window may be talking to a daemon on a server.
  const where = $derived(app.daemon?.remote ? app.daemon.connection : "this computer");
  let name = $state("");
  let apiKey = $state("");
  let model = $state("");
  let baseURL = $state("");
  let error = $state("");
  let busy = $state(false);
  let detail = $state<Record<string, string>>({});
  let loginFor = $state("");

  async function add() {
    error = ""; busy = true;
    try {
      const p = await Backend.CreateProfile({ kind, name: name.trim(), mode, model, base_url: baseURL, computer_login: login === "computer" } as any);
      if (kind === "openai-api" && apiKey) await Backend.SetAPIKey(p.id, apiKey);
      if (needsNotice(kind, mode) && accepted) await Backend.AcceptNotice(p.id, "claude-headless");
      accepted = false;
      name = ""; apiKey = ""; model = ""; baseURL = "";
      await app.refreshAll();
      await check(p.id);
    } catch (e) { error = String(e); } finally { busy = false; }
  }
  async function check(id: string) {
    try {
      const d = await Backend.Detect(id);
      detail[id] = !d.installed ? `CLI not found. ${d.detail}` : `${d.version}${d.version_ok ? "" : " (untested version)"} · login: ${d.login}${d.detail ? " · " + d.detail : ""}`;
    } catch (e) { detail[id] = String(e); }
  }
  async function remove(id: string) {
    try { await Backend.DeleteProfile(id); await app.refreshAll(); } catch (e) { error = String(e); }
  }
  async function accept(id: string) {
    await Backend.AcceptNotice(id, "claude-headless"); await app.refreshAll();
  }
</script>

{#if loginFor}
  <div class="bar"><b class="title">Log in</b><span class="muted">in this subscription's own private folder</span>
    <button class="primary" style="margin-left:auto" onclick={() => { const id = loginFor; loginFor = ""; check(id); }}>Done</button></div>
  <TerminalView label="Official login" open={(r, c) => Backend.OpenLogin(loginFor, r, c)} />
{:else}
  <div class="page"><div class="inner">
    <h1>Subscriptions</h1>
    <p class="lede">The plans you already pay for. Aotus runs each one's official CLI in its own folder; it never reads or copies your credentials.</p>

    <h3 class="section">Linked</h3>
    <div class="list">
      {#each app.profiles as p (p.id)}
        <div class="lrow" data-profile={p.name}>
          <div class="tile">{kindTile[p.kind] ?? "?"}</div>
          <div class="grow">
            <b class="name">{p.name}</b>
            <div class="desc ellipsis">{kindName[p.kind] ?? p.kind} · {modeName[p.mode] ?? p.mode}{p.computer_login ? ` · sign-in on ${where}` : ""}</div>
            {#if detail[p.id]}<div class="faint tray-note" style="margin-top:2px">{detail[p.id]}</div>{/if}
            {#if needsNotice(p.kind, p.mode) && !p.accepted_notices?.includes("claude-headless")}
              <div class="notice"><p style="margin:0 0 10px">{notices["claude-headless"] ?? "Chat mode uses the CLI's non-interactive mode; the provider's terms are ambiguous about it."}</p><button onclick={() => accept(p.id)}>I have read this and accept it</button></div>
            {/if}
          </div>
          <button onclick={() => check(p.id)}>Check</button>
          {#if p.kind !== "openai-api"}<button onclick={() => (loginFor = p.id)}>Log in</button>{/if}
          <button class="ghost danger" onclick={() => remove(p.id)}>Remove</button>
        </div>
      {:else}
        <p class="muted">No subscription linked yet. Link one below to create your first employee.</p>
      {/each}
    </div>

    <h3 class="section">Link a subscription</h3>
    <div class="card">
      <label for="k" style="margin-top:0">Provider</label>
      <select id="k" bind:value={kind} onchange={() => { mode = defaultMode(kind); login = defaultLogin(kind); }}>
        <option value="claude">Claude Code (Claude subscription)</option>
        <option value="codex">Codex CLI (ChatGPT subscription)</option>
        <option value="openai-api">OpenAI-compatible API (key)</option>
      </select>
      <label for="n">Name</label>
      <input id="n" bind:value={name} placeholder="e.g. Personal Claude" />
      {#if kind !== "openai-api"}
        <label for="lg">Sign-in</label>
        <select id="lg" bind:value={login}>
          {#each loginChoices(where) as c (c.value)}<option value={c.value}>{c.label}</option>{/each}
        </select>
        {#if login === "computer"}<p class="faint tray-note" style="margin-top:8px">If you are already signed in to the CLI on {where}, there is nothing else to do. Aotus never reads your sign-in; the CLI uses it as in your terminal, with your settings too (hooks, MCP servers), and its sessions show up in your CLI's history. Choose a separate sign-in to keep them apart.</p>{/if}
        <label for="md">How it works</label>
        <select id="md" bind:value={mode}>
          {#each modeChoices(kind) as c (c.value)}<option value={c.value}>{c.label}</option>{/each}
        </select>
        {#if needsNotice(kind, mode)}
          <div class="notice">
            <p style="margin:0 0 10px">{notices["claude-headless"] ?? "Loading the notice…"}</p>
            <label class="row" style="margin:0; color:var(--text); gap:10px; cursor:pointer"><input type="checkbox" style="width:auto" bind:checked={accepted} disabled={!notices["claude-headless"]} /> I have read this and accept it</label>
          </div>
        {/if}
      {/if}
      {#if kind === "openai-api"}
        <label for="u">Base URL</label><input id="u" bind:value={baseURL} placeholder="https://api.openai.com/v1" />
        <label for="m">Model</label><input id="m" bind:value={model} />
        <label for="a">API key (kept in the system's credential store)</label><input id="a" type="password" bind:value={apiKey} />
      {/if}
      {#if error}<p class="err" style="margin-top:12px">{error}</p>{/if}
      <div class="actions"><button class="primary" disabled={busy || !name.trim() || (needsNotice(kind, mode) && !accepted)} onclick={add}>Link</button></div>
    </div>
  </div></div>
{/if}
