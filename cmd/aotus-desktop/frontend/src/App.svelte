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

  onMount(() => { app.start(); });
  const emp = $derived(app.current);
  const dot = (e: { working: boolean; state: string; terminal_running: boolean }) => (e.working ? "working" : e.state === "paused" ? "paused" : "running");
</script>

<div class="shell">
  <aside class="side">
    <header>Aotus</header>
    <button class="item" title="Where this window is connected" onclick={() => (app.view = "connections")}>
      <span class="dot" class:running={app.daemon?.connected} class:paused={!app.daemon?.connected}></span>
      <span class="grow">{app.daemon?.remote ? app.daemon.connection : "This computer"}</span>
      {#if app.daemon?.remote}<span class="muted">{app.daemon.role}</span>{/if}
    </button>
    <nav>
      <h3>Employees</h3>
      {#each app.employees as e (e.id)}
        <button class="item" class:on={app.view !== "profiles" && app.selected === e.id} onclick={() => app.open(e.id)}>
          <span class="dot {dot(e)}"></span><span class="grow">{e.name}</span><span class="muted">{e.role}</span>
        </button>
      {/each}
      <button class="item" onclick={() => (app.view = "new-employee")}>＋ New employee</button>
      <h3>Workspace</h3>
      <button class="item" class:on={app.view === "approvals"} onclick={() => (app.view = "approvals")}>
        <span class="grow">Approvals</span>{#if app.approvals.length}<span class="badge">{app.approvals.length}</span>{/if}
      </button>
      <button class="item" class:on={app.view === "profiles"} onclick={() => (app.view = "profiles")}>Subscriptions</button>
      <button class="item" class:on={app.view === "connections"} onclick={() => (app.view = "connections")}>Connections</button>
      <button class="item" class:on={app.view === "settings"} onclick={() => (app.view = "settings")}>Settings</button>
    </nav>
  </aside>

  <main class="main">
    {#if app.daemon && !app.daemon.connected}
      <div class="banner bad">Lost the connection to {app.daemon.remote ? app.daemon.connection : "the daemon"}: {app.daemon.error}. Trying again… what is on screen stays.</div>
    {:else if app.error}
      <div class="banner bad">{app.error}</div>
    {/if}

    {#if (app.view === "chat" || app.view === "terminal") && emp}
      <div class="bar">
        <b>{emp.name}</b><span class="muted">{emp.role}</span>
        {#if emp.state === "paused"}
          <button onclick={() => Backend.Resume(emp.id).then(() => app.refreshEmployees())}>Resume</button>
        {:else}
          <button class="ghost" onclick={() => Backend.Pause(emp.id).then(() => app.refreshEmployees())}>Pause</button>
        {/if}
        <button class="ghost danger" onclick={() => Backend.DeleteEmployee(emp.id).then(async () => { app.selected = ""; app.view = "profiles"; await app.refreshAll(); })}>Remove</button>
        <div class="tabs">
          <button class:on={app.view === "chat"} onclick={() => (app.view = "chat")}>Chat</button>
          <button class:on={app.view === "terminal"} onclick={() => (app.view = "terminal")}>Terminal</button>
        </div>
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
