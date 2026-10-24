<script lang="ts">
  import { app } from "./store.svelte";
  import Avatar from "./Avatar.svelte";
  import Icon from "./Icon.svelte";
  const who = (id: string) => app.employees.find((e) => e.id === id)?.name ?? id;
</script>

<div class="page"><div class="inner">
  <h1>Approvals</h1>
  <p class="lede">What your employees want to do. Nothing runs until you allow it; a request nobody answers is denied after 15 minutes.</p>
  <div style="margin-top:24px">
    {#each app.approvals as a (a.id)}
      <div class="card approval">
        <div class="row"><Avatar name={who(a.employee_id)} /><span><b style="font-weight:500">{who(a.employee_id)}</b> <span class="muted">wants to {a.kind}</span></span></div>
        <div class="cmd">{a.target}</div>
        <div class="row" style="justify-content:flex-end">
          <button class="ghost danger" onclick={() => app.answer(a.id, false, "none")}>Deny</button>
          <button onclick={() => app.answer(a.id, true, "kind")}>Always this kind</button>
          <button onclick={() => app.answer(a.id, true, "exact")}>Always this</button>
          <button class="primary" onclick={() => app.answer(a.id, true, "none")}>Allow once</button>
        </div>
      </div>
    {:else}
      <div class="empty" style="padding-top:40px">
        <span class="tile" style="width:52px;height:52px"><Icon name="check" /></span>
        <p class="muted">Nothing is waiting for you.</p>
      </div>
    {/each}
  </div>
</div></div>
