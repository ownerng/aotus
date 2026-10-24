<script lang="ts">
  import { onMount } from "svelte";
  import { Backend } from "../bindings/aotus/cmd/aotus-desktop";
  import { app } from "./lib/store.svelte";
  import Chat from "./lib/Chat.svelte";
  import TerminalView from "./lib/TerminalView.svelte";
  import Profiles from "./lib/Profiles.svelte";
  import NewEmployee from "./lib/NewEmployee.svelte";
  import Approvals from "./lib/Approvals.svelte";
  import Settings from "./lib/Settings.svelte";
  import Connections from "./lib/Connections.svelte";
  import Mark from "./lib/Mark.svelte";
  import Avatar from "./lib/Avatar.svelte";
  import Icon from "./lib/Icon.svelte";

  onMount(() => { app.start(); });
  const emp = $derived(app.current);
  type Emp = { working: boolean; state: string; role: string };
  // Status shows only when something is happening or stopped.
  const dot = (e: Emp) => (e.working ? "working" : e.state === "paused" ? "paused" : "");
  const sub = (e: Emp) => (e.working ? "Working…" : e.state === "paused" ? "Paused" : e.role || "Ready");
  const go = (v: typeof app.view) => (app.view = v);
</script>

<div class="shell">
  <aside class="side">
    <div class="brand"><Mark size={18} looking /><span>Aotus</span></div>
    <nav>
      <button class="item new" class:on={app.view === "new-employee"} onclick={() => go("new-employee")}><Icon name="plus" /><span class="grow">New employee</span></button>
      {#if app.employees.length}<div class="heading">Employees</div>{/if}
      {#each app.employees as e (e.id)}
        <button class="item" class:on={(app.view === "chat" || app.view === "terminal") && app.selected === e.id} onclick={() => app.open(e.id)}>
          <Avatar name={e.name} status={dot(e)} />
          <span class="grow"><span class="ellipsis" style="display:block; color:var(--text)">{e.name}</span><span class="sub ellipsis" style="display:block">{sub(e)}</span></span>
        </button>
      {/each}
    </nav>
    <footer>
      <button class="item" class:on={app.view === "approvals"} onclick={() => go("approvals")}>
        <Icon name="approvals" /><span class="grow">Approvals</span>{#if app.approvals.length}<span class="badge">{app.approvals.length}</span>{/if}
      </button>
      <button class="item" class:on={app.view === "profiles"} onclick={() => go("profiles")}><Icon name="subscriptions" /><span class="grow">Subscriptions</span></button>
      <button class="item" class:on={app.view === "connections"} onclick={() => go("connections")}><Icon name="connections" /><span class="grow">Connections</span></button>
      <button class="item" class:on={app.view === "settings"} onclick={() => go("settings")}><Icon name="settings" /><span class="grow">Settings</span></button>
      <button class="item" title="Where this window is connected" onclick={() => go("connections")}>
        <span class="dot" class:running={app.daemon?.connected} style:background={app.daemon && !app.daemon.connected ? "var(--bad)" : undefined}></span>
        <span class="grow sub ellipsis">{app.daemon?.remote ? app.daemon.connection : "This computer"}{#if app.daemon?.remote} · {app.daemon.role}{/if}</span>
      </button>
    </footer>
  </aside>

  <main class="main">
    {#if app.daemon && !app.daemon.connected}
      <div class="banner bad">Lost the connection to {app.daemon.remote ? app.daemon.connection : "the daemon"}: {app.daemon.error}. Trying again… what is on screen stays.</div>
    {:else if app.error}
      <div class="banner bad">{app.error}</div>
    {/if}

    {#if (app.view === "chat" || app.view === "terminal") && emp}
      <div class="bar">
        <Avatar name={emp.name} size="lg" status={dot(emp)} />
        <div style="min-width:0">
          <b class="title ellipsis" style="display:block">{emp.name}</b>
          <div class="muted ellipsis" style="font-size:13px">{sub(emp)}</div>
        </div>
        <div class="tabs">
          <button class:on={app.view === "chat"} onclick={() => go("chat")}>Chat</button>
          <button class:on={app.view === "terminal"} onclick={() => go("terminal")}>Terminal</button>
        </div>
        {#if emp.state === "paused"}
          <button onclick={() => Backend.Resume(emp.id).then(() => app.refreshEmployees())}>Resume</button>
        {:else}
          <button class="ghost" onclick={() => Backend.Pause(emp.id).then(() => app.refreshEmployees())}>Pause</button>
        {/if}
        <button class="ghost danger" onclick={() => Backend.DeleteEmployee(emp.id).then(async () => { app.selected = ""; app.view = "profiles"; await app.refreshAll(); })}>Remove</button>
      </div>
      {#if app.view === "chat"}
        <Chat />
      {:else}
        {#key emp.id}
          <TerminalView label={emp.name} open={(r, c) => Backend.OpenTerminal(emp.id, r, c)} />
        {/key}
      {/if}
    {:else if app.view === "new-employee"}
      <NewEmployee />
    {:else if app.view === "approvals"}
      <Approvals />
    {:else if app.view === "connections"}
      <Connections />
    {:else if app.view === "settings"}
      <Settings />
    {:else}
      <Profiles />
    {/if}
  </main>
</div>
