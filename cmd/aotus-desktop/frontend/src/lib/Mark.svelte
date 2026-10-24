<script lang="ts">
  // The Aotus mark: the night monkey's two large round eyes. Our own, never
  // another product's logo or mascot (docs/DESIGN.md).
  let { size = 22, looking = false }: { size?: number; looking?: boolean } = $props();
</script>

<svg width={size * 1.6} height={size} viewBox="0 0 32 20" aria-label="Aotus" role="img" class:looking>
  <circle cx="9" cy="10" r="8.2" fill="#f5f5f5" />
  <circle cx="23" cy="10" r="8.2" fill="#f5f5f5" />
  <circle class="pupil" cx="10.2" cy="10.6" r="3.6" fill="#0b0b0b" />
  <circle class="pupil" cx="24.2" cy="10.6" r="3.6" fill="#0b0b0b" />
  <circle cx="11.4" cy="9.2" r="1" fill="#f5f5f5" />
  <circle cx="25.4" cy="9.2" r="1" fill="#f5f5f5" />
</svg>

<style>
  svg { display: block; flex: none; }
  .pupil { transition: transform .4s ease; }
  .looking .pupil { animation: look 6s ease-in-out infinite; }
  @keyframes look { 0%, 40%, 100% { transform: translateX(0); } 50%, 70% { transform: translateX(-2.2px); } }
</style>
