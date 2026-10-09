<script lang="ts" generics="T extends { key: string }">
  // Renders only the rows near the viewport, so a conversation of thousands of
  // lines costs the same memory as one of twenty. Row heights are measured as
  // rows appear; rows never seen use an estimate.
  import type { Snippet } from "svelte";
  let { items, row, estimate = 64, overscan = 6 }: { items: T[]; row: Snippet<[T]>; estimate?: number; overscan?: number } = $props();

  let box: HTMLDivElement;
  let scrollTop = $state(0);
  let height = $state(600);
  let stick = true; // follow the end while the user is at the bottom
  const sizes = new Map<string, number>();
  let version = $state(0); // bumps when a measured size changes

  const offsets = $derived.by(() => {
    version; // recompute when sizes change
    const o = new Array<number>(items.length + 1);
    o[0] = 0;
    for (let i = 0; i < items.length; i++) o[i + 1] = o[i] + (sizes.get(items[i].key) ?? estimate);
    return o;
  });
  const total = $derived(offsets[items.length] ?? 0);
  const start = $derived.by(() => {
    let lo = 0, hi = items.length;
    while (lo < hi) { const mid = (lo + hi) >> 1; if (offsets[mid + 1] < scrollTop) lo = mid + 1; else hi = mid; }
    return Math.max(0, lo - overscan);
  });
  const end = $derived.by(() => {
    let i = start;
    while (i < items.length && offsets[i] < scrollTop + height) i++;
    return Math.min(items.length, i + overscan);
  });

  function onScroll() {
    scrollTop = box.scrollTop;
    stick = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
  }

  // Measure each rendered row.
  function measure(node: HTMLElement, key: string) {
    const ro = new ResizeObserver(() => {
      const h = node.offsetHeight;
      if (sizes.get(key) !== h) { sizes.set(key, h); version++; }
    });
    ro.observe(node);
    return { destroy: () => ro.disconnect() };
  }

  $effect(() => {
    total; items.length;
    if (stick && box) queueMicrotask(() => { box.scrollTop = box.scrollHeight; scrollTop = box.scrollTop; });
  });
  $effect(() => {
    const ro = new ResizeObserver(() => (height = box.clientHeight));
    ro.observe(box);
    height = box.clientHeight;
    return () => ro.disconnect();
  });
</script>

<div class="msgs" bind:this={box} onscroll={onScroll}>
  <div style="height:{total}px; position:relative">
    <div style="position:absolute; top:{offsets[start]}px; left:0; right:0">
      {#each items.slice(start, end) as item (item.key)}
        <div use:measure={item.key}>{@render row(item)}</div>
      {/each}
    </div>
  </div>
</div>
