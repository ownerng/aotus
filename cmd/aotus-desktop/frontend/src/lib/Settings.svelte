<script lang="ts">
  import { app } from "./store.svelte";
  let error = $state("");
  async function toggle(e: Event) {
    error = "";
    try { await app.saveSettings({ ...app.settings, keep_in_tray: (e.target as HTMLInputElement).checked }); }
    catch (err) { error = String(err); }
  }
</script>

<div class="page"><div class="inner">
  <h1>Settings</h1>
  <div class="card" style="margin-top:24px">
    <label class="row" style="margin:0; color:var(--text); gap:14px; cursor:pointer">
      <span class="grow">Keep Aotus in the system tray when the window is closed</span>
      <input type="checkbox" class="switch" checked={app.settings.keep_in_tray} onchange={toggle} />
    </label>
    <p class="muted tray-note" style="margin-top:10px">Off by default. Closing the window always leaves your employees working (they live in the background daemon, about 15 MB). Keeping the app in the tray holds the window libraries in memory, around 170 MB.</p>
    {#if error}<p class="err">{error}</p>{/if}
  </div>
  <p class="faint tray-note" style="margin-top:16px">Aotus {app.version} · daemon {app.daemon?.version} at <span class="mono">{app.daemon?.address}</span></p>
</div></div>
