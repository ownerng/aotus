<script lang="ts">
  // A small set of line icons, 1.6 px strokes, drawn for Aotus.
  type Name = "plus" | "subscriptions" | "approvals" | "connections" | "settings" | "send" | "stop" | "tool" | "terminal" | "check" | "chat";
  let { name }: { name: Name } = $props();
</script>

<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
  {#if name === "plus"}<path d="M12 5v14M5 12h14" />
  {:else if name === "subscriptions"}<rect x="3" y="6" width="18" height="13" rx="3" /><path d="M3 10h18M7 15h4" />
  {:else if name === "approvals"}<path d="M12 3l7 3v5c0 4.5-3 8-7 10-4-2-7-5.5-7-10V6z" /><path d="M9 12l2 2 4-4" />
  {:else if name === "connections"}<circle cx="6" cy="12" r="2.5" /><circle cx="18" cy="6" r="2.5" /><circle cx="18" cy="18" r="2.5" /><path d="M8.2 10.8l7.6-3.6M8.2 13.2l7.6 3.6" />
  {:else if name === "settings"}<circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" />
  {:else if name === "send"}<path d="M12 19V5M6 11l6-6 6 6" />
  {:else if name === "stop"}<rect x="7" y="7" width="10" height="10" rx="2" fill="currentColor" stroke="none" />
  {:else if name === "tool"}<path d="M14.7 6.3a4 4 0 0 0-5.4 5.4L4 17v3h3l5.3-5.3a4 4 0 0 0 5.4-5.4l-2.4 2.4-2.4-.6-.6-2.4z" />
  {:else if name === "terminal"}<rect x="3" y="4" width="18" height="16" rx="3" /><path d="M7 9l3 3-3 3M12 15h5" />
  {:else if name === "check"}<path d="M5 12.5l4.5 4.5L19 7.5" />
  {:else if name === "chat"}<path d="M20 12a8 8 0 0 1-11.6 7.1L4 20l.9-4.4A8 8 0 1 1 20 12z" />
  {/if}
</svg>
