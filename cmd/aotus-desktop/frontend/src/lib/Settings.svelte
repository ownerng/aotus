<script lang="ts">
  import { app } from "./store.svelte";
  let error = $state("");
  async function toggle(e: Event) {
    error = "";
    try { await app.saveSettings({ ...app.settings, keep_in_tray: (e.target as HTMLInputElement).checked }); }
    catch (err) { error = String(err); }
  }
</script>

<div class="page">
  <h1>Settings</h1>
  <div class="card">
    <label class="row" style="margin:0; color:var(--text)">
      <input type="checkbox" style="width:auto" checked={app.settings.keep_in_tray} onchange={toggle} />
      Keep Aotus in the system tray when the window is closed
    </label>
    <p class="muted tray-note">Off by default. Closing the window always leaves your employees working (they live in the background daemon, about 15 MB). Keeping the app in the tray holds the window libraries in memory, around 170 MB.</p>
    {#if error}<p class="err">{error}</p>{/if}
  </div>
  <p class="muted tray-note">Aotus {app.version} · daemon {app.daemon?.version} at {app.daemon?.address}</p>
</div>
