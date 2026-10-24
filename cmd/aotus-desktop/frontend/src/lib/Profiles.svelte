<script lang="ts">
  import { Backend } from "../../bindings/aotus/cmd/aotus-desktop";
  import { app } from "./store.svelte";
  import TerminalView from "./TerminalView.svelte";
  import { defaultMode, modeChoices, needsNotice } from "./profiles";

  const kindName: Record<string, string> = { claude: "Claude Code", codex: "Codex CLI", "openai-api": "OpenAI-compatible API" };
  const kindTile: Record<string, string> = { claude: "CC", codex: "CX", "openai-api": "API" };
  const modeName: Record<string, string> = { structured: "Chat", terminal: "Terminal", api: "API" };

  let kind = $state("claude");
  let mode = $state(defaultMode("claude"));
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
      const p = await Backend.CreateProfile({ kind, name: name.trim(), mode, model, base_url: baseURL } as any);
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
            <div class="desc ellipsis">{kindName[p.kind] ?? p.kind} · {modeName[p.mode] ?? p.mode}</div>
            {#if detail[p.id]}<div class="faint tray-note" style="margin-top:2px">{detail[p.id]}</div>{/if}
            {#if needsNotice(p.kind, p.mode) && !p.accepted_notices?.includes("claude-headless")}
              <div class="notice">Chat mode uses the CLI's non-interactive mode; the provider's terms are ambiguous about it. <button style="margin-left:6px" onclick={() => accept(p.id)}>I understand</button></div>
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
      <select id="k" bind:value={kind} onchange={() => (mode = defaultMode(kind))}>
        <option value="claude">Claude Code (Claude subscription)</option>
        <option value="codex">Codex CLI (ChatGPT subscription)</option>
        <option value="openai-api">OpenAI-compatible API (key)</option>
      </select>
      <label for="n">Name</label>
      <input id="n" bind:value={name} placeholder="e.g. Personal Claude" />
      {#if kind !== "openai-api"}
        <label for="md">How it works</label>
        <select id="md" bind:value={mode}>
          {#each modeChoices(kind) as c (c.value)}<option value={c.value}>{c.label}</option>{/each}
        </select>
        {#if needsNotice(kind, mode)}<p class="faint tray-note" style="margin-top:8px">After linking, you will be asked to read and accept a notice about this mode.</p>{/if}
      {/if}
      {#if kind === "openai-api"}
        <label for="u">Base URL</label><input id="u" bind:value={baseURL} placeholder="https://api.openai.com/v1" />
        <label for="m">Model</label><input id="m" bind:value={model} />
        <label for="a">API key (kept in the system's credential store)</label><input id="a" type="password" bind:value={apiKey} />
      {/if}
      {#if error}<p class="err" style="margin-top:12px">{error}</p>{/if}
      <div class="actions"><button class="primary" disabled={busy || !name.trim()} onclick={add}>Link</button></div>
    </div>
  </div></div>
{/if}
