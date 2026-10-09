<script lang="ts">
  import { app } from "./store.svelte";
  import VirtualList from "./VirtualList.svelte";

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
  <div class="page"><p class="muted">{emp.name} works in its own terminal (the official CLI's screen). Use the <b>Terminal</b> tab.</p></div>
{:else}
  {#if app.hasOlder}
    <div class="row" style="padding:6px 18px"><button class="ghost" onclick={() => app.loadOlder()}>Load earlier conversation</button></div>
  {/if}
  <VirtualList items={app.chat}>
    {#snippet row(item)}
      <div class="msg {item.kind}">
        {#if item.kind === "user"}
          <div class="who">You</div><div class="body">{item.text}</div>
        {:else if item.kind === "assistant"}
          <div class="who">{emp?.name}</div><div class="body">{item.text}{#if item.pending}<span class="muted"> ▍</span>{/if}</div>
        {:else if item.kind === "tool"}
          <div class="tool">{item.text}</div>
        {:else if item.kind === "error"}
          <div class="body err">{item.text}</div>
        {:else}
          <button class="ghost" onclick={() => app.showAnswer(item)}>{item.text}</button>
        {/if}
      </div>
    {/snippet}
  </VirtualList>
  {#if error}<div class="banner bad">{error}</div>{/if}
  <div class="composer">
    <textarea bind:value={text} onkeydown={key} placeholder={emp?.state === "paused" ? "Resume the employee to give it work" : `Ask ${emp?.name ?? ""} to do something…`} disabled={emp?.state === "paused"}></textarea>
    {#if working}
      <button class="danger" onclick={() => app.cancel()}>Stop</button>
    {/if}
    <button class="primary" onclick={submit} disabled={sending || !text.trim() || emp?.state === "paused"}>{working ? "Queue" : "Send"}</button>
  </div>
{/if}
