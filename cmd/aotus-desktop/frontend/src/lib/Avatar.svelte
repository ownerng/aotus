<script lang="ts">
  // An employee's monochrome initials, with its status dot when it matters.
  let { name, size = "", status = "" }: { name: string; size?: "" | "lg" | "xl"; status?: string } = $props();
  const initials = $derived(
    name.trim().split(/\s+/).slice(0, 2).map((w) => w[0]?.toUpperCase() ?? "").join("") || "?",
  );
</script>

<span class="avatar {size}" aria-hidden="true">{initials}{#if status}<span class="dot {status}"></span>{/if}</span>
