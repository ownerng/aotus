<script lang="ts">
  import { Backend } from "../../bindings/aotus/cmd/aotus-desktop";
  import { app } from "./store.svelte";
  import TerminalView from "./TerminalView.svelte";

  let kind = $state("claude");
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
      const p = await Backend.CreateProfile({ kind, name: name.trim(), mode: kind === "claude" ? "terminal" : kind === "codex" ? "terminal" : "api", model, base_url: baseURL } as any);
      if (kind === "openai-api" && apiKey) await Backend.SetAPIKey(p.id, apiKey);
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
  <div class="bar"><b>Login</b><span class="muted">in the profile's own isolated folder</span>
    <button style="margin-left:auto" onclick={() => { loginFor = ""; check(loginFor); }}>Done</button></div>
  <TerminalView label="Login" open={(r, c) => Backend.OpenLogin(loginFor, r, c)} />
{:else}
  <div class="page">
    <h1>Subscriptions</h1>
    <p class="muted">Link the plans you already pay for. Aotus runs the official CLI of each one; it never reads or copies your credentials.</p>

    {#each app.profiles as p (p.id)}
      <div class="card">
        <div class="row">
          <b class="grow">{p.name}</b><span class="muted">{p.kind} · {p.mode}</span>
          <button onclick={() => check(p.id)}>Check</button>
          {#if p.kind !== "openai-api"}<button onclick={() => (loginFor = p.id)}>Log in</button>{/if}
          <button class="danger" onclick={() => remove(p.id)}>Remove</button>
        </div>
        {#if detail[p.id]}<div class="muted tray-note" style="margin-top:6px">{detail[p.id]}</div>{/if}
        {#if p.kind === "claude" && !p.accepted_notices?.includes("claude-headless") && p.mode === "structured"}
          <div class="banner" style="margin:8px 0 0">Headless mode uses the CLI's non-interactive mode; read the provider's terms first. <button onclick={() => accept(p.id)}>I understand</button></div>
        {/if}
      </div>
    {:else}
      <p class="muted">No subscription linked yet.</p>
    {/each}

    <h2 style="margin-top:20px">Link a subscription</h2>
    <div class="card">
      <label for="k">Provider</label>
      <select id="k" bind:value={kind}>
        <option value="claude">Claude Code (Claude subscription)</option>
        <option value="codex">Codex CLI (ChatGPT subscription)</option>
        <option value="openai-api">OpenAI-compatible API (key)</option>
      </select>
      <label for="n">Name</label>
      <input id="n" bind:value={name} placeholder="e.g. Personal Claude" />
      {#if kind === "openai-api"}
        <label for="u">Base URL</label><input id="u" bind:value={baseURL} placeholder="https://api.openai.com/v1" />
        <label for="m">Model</label><input id="m" bind:value={model} />
        <label for="a">API key (stored in the OS credential store)</label><input id="a" type="password" bind:value={apiKey} />
      {/if}
      {#if error}<p class="err">{error}</p>{/if}
      <div style="margin-top:12px"><button class="primary" disabled={busy || !name.trim()} onclick={add}>Link</button></div>
    </div>
  </div>
{/if}
