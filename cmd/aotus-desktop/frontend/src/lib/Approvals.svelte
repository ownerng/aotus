<script lang="ts">
  import { app } from "./store.svelte";
  const who = (id: string) => app.employees.find((e) => e.id === id)?.name ?? id;
</script>

<div class="page">
  <h1>Approvals</h1>
  <p class="muted">Actions your employees want to take. Nothing runs until you allow it; unanswered requests are denied after 15 minutes.</p>
  {#each app.approvals as a (a.id)}
    <div class="card approval">
      <div><b>{who(a.employee_id)}</b> wants to <b>{a.kind}</b></div>
      <div class="mono muted" style="margin:6px 0; overflow-wrap:anywhere">{a.target}</div>
      <div class="row">
        <button class="primary" onclick={() => app.answer(a.id, true, "none")}>Allow once</button>
        <button onclick={() => app.answer(a.id, true, "exact")}>Always this</button>
        <button onclick={() => app.answer(a.id, true, "kind")}>Always this kind</button>
        <button class="danger" onclick={() => app.answer(a.id, false, "none")}>Deny</button>
      </div>
    </div>
  {:else}
    <p class="muted">Nothing is waiting for you.</p>
  {/each}
</div>
