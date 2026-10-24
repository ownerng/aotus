<script lang="ts">
  // The Aotus mark: the night monkey's two large amber eyes, which blink now
  // and then. Our own drawing, never another product's logo or mascot
  // (docs/DESIGN.md).
  let { size = 22, looking = false }: { size?: number; looking?: boolean } = $props();
</script>

<svg width={size * 1.6} height={size} viewBox="0 0 32 20" aria-label="Aotus" role="img" class:looking>
  <g class="eyes">
    <circle cx="8.4" cy="10" r="7.7" fill="#f2a93b" />
    <circle cx="23.6" cy="10" r="7.7" fill="#f2a93b" />
    <circle cx="8.4" cy="10" r="7.7" fill="none" stroke="#c97f1c" stroke-width="1" />
    <circle cx="23.6" cy="10" r="7.7" fill="none" stroke="#c97f1c" stroke-width="1" />
    <g class="pupils">
      <circle cx="9.2" cy="10.4" r="3.8" fill="#1b1206" />
      <circle cx="24.4" cy="10.4" r="3.8" fill="#1b1206" />
      <circle cx="10.5" cy="8.9" r="1.1" fill="#fff6e6" />
      <circle cx="25.7" cy="8.9" r="1.1" fill="#fff6e6" />
    </g>
  </g>
</svg>

<style>
  svg { display: block; flex: none; overflow: visible; }
  .eyes { transform-origin: 50% 50%; transform-box: fill-box; animation: blink 6.5s ease-in-out infinite; }
  .looking .pupils { animation: look 9s ease-in-out infinite; }
  @keyframes blink { 0%, 93%, 100% { transform: scaleY(1); } 95.5% { transform: scaleY(0.08); } }
  @keyframes look { 0%, 35%, 100% { transform: translateX(0); } 45%, 62% { transform: translateX(-1.8px); } 70%, 80% { transform: translateX(1.2px); } }
  @media (prefers-reduced-motion: reduce) { .eyes, .looking .pupils { animation: none; } }
</style>
