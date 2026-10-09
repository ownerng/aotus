<script lang="ts">
  // The official CLI's own screen, running in the daemon. Closing this view only
  // detaches: the program keeps running.
  import { onMount } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { Events } from "@wailsio/runtime";
  import { Backend } from "../../bindings/aotus/cmd/aotus-desktop";

  // open starts (or attaches to) the terminal and returns its key.
  let { open, label }: { open: (rows: number, cols: number) => Promise<string>; label: string } = $props();
  let host: HTMLDivElement;
  let status = $state("");

  onMount(() => {
    const term = new Terminal({ fontFamily: "ui-monospace, Menlo, Consolas, monospace", fontSize: 13, cursorBlink: true, theme: { background: "#000000" } });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();
    let key = "";
    let off = () => {};
    const dec = (b64: string) => Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));

    off = Events.On("terminal", (e) => {
      const c = e.data as { key: string; data: string; ended: boolean };
      if (c.key !== key) return;
      if (c.data) term.write(dec(c.data));
      if (c.ended) status = "The program ended.";
    });
    open(term.rows, term.cols).then((k) => {
      key = k;
      Backend.TerminalResize(key, term.rows, term.cols).catch(() => {});
    }).catch((err) => (status = String(err)));

    term.onData((d) => { if (key) Backend.TerminalInput(key, d).catch(() => {}); });
    const ro = new ResizeObserver(() => {
      fit.fit();
      if (key) Backend.TerminalResize(key, term.rows, term.cols).catch(() => {});
    });
    ro.observe(host);
    return () => {
      ro.disconnect(); off();
      if (key) Backend.DetachTerminal(key);
      term.dispose();
    };
  });
</script>

{#if status}<div class="banner">{label}: {status}</div>{/if}
<div class="term" bind:this={host}></div>
