<script lang="ts">
  import { app } from "./store.svelte";
  import VirtualList from "./VirtualList.svelte";
  import Icon from "./Icon.svelte";
  import Avatar from "./Avatar.svelte";

  let text = $state("");
  let sending = $state(false);
  let error = $state("");
  const emp = $derived(app.current);
  const working = $derived(!!emp?.working);

  async function submit() {
    const t = text.trim();
    if (!t || sending) return;
    sending = true; error = "";
    try { await app.send(t); text = ""; } catch (e) { error = String(e); } finally { sending = false; }
  }
  function key(e: KeyboardEvent) {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); submit(); }
  }
</script>

{#if emp?.mode === "terminal"}
  <div class="empty">
    <Avatar name={emp.name} size="xl" />
    <h1>{emp.name} works in its terminal</h1>
    <p class="lede">This subscription runs the official CLI's own screen. Open the Terminal tab to work with {emp.name}; closing it leaves the program running.</p>
    <button class="primary" onclick={() => (app.view = "terminal")}>Open the terminal</button>
  </div>
{:else}
  {#if app.hasOlder}
    <div class="row" style="justify-content:center; padding:10px"><button class="ghost" onclick={() => app.loadOlder()}>Load earlier conversation</button></div>
  {/if}
  {#if !app.chat.length && emp}
    <div class="empty" style="flex:1">
      <Avatar name={emp.name} size="xl" />
      <h1>{emp.name}</h1>
      <p class="lede">{emp.role || "Ready for work."} Give it a task below; it keeps working when you close the window.</p>
    </div>
  {:else}
  <VirtualList items={app.chat}>
    {#snippet row(item)}
      <div class="msg {item.kind}">
        {#if item.kind === "user"}
          <div class="body">{item.text}</div>
        {:else if item.kind === "assistant"}
          <div class="body">{item.text}{#if item.pending}<span class="cursor"></span>{/if}</div>
        {:else if item.kind === "tool"}
          <span class="chip"><Icon name="tool" /><span class="mono">{item.text}</span></span>
        {:else if item.kind === "error"}
          <div class="body err">{item.text}</div>
        {:else if item.kind === "status"}
          <div class="status muted">{item.text}</div>
        {:else}
          <button class="ghost" onclick={() => app.showAnswer(item)}>{item.text}</button>
        {/if}
      </div>
    {/snippet}
  </VirtualList>
  {/if}
  {#if error}<div class="banner bad">{error}</div>{/if}
  <div class="composer-wrap">
    <div class="composer">
      <textarea bind:value={text} onkeydown={key} placeholder={emp?.state === "paused" ? "Resume the employee to give it work" : `Ask ${emp?.name ?? ""} to do something…`} disabled={emp?.state === "paused"}></textarea>
      {#if working}
        <button class="icon" aria-label="Stop" title="Stop" onclick={() => app.cancel()}><Icon name="stop" /></button>
      {/if}
      <button class="icon primary" aria-label={working ? "Queue" : "Send"} title={working ? "Queue" : "Send"} onclick={submit} disabled={sending || !text.trim() || emp?.state === "paused"}><Icon name="send" /></button>
    </div>
    <div class="hint">Enter to send · Shift+Enter for a new line</div>
  </div>
{/if}
