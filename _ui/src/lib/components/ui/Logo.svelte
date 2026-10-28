<script lang="ts">
  let { size = 20 }: { size?: number } = $props();
</script>

<!-- assets/favicon.svg -->
<svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" class="shrink-0" fill="none" stroke="#06d6a0" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
  <ellipse cx="12" cy="5" rx="9" ry="3" />
  <path d="M3 5V19A9 3 0 0 0 21 19V5" />
  <path d="M3 12A9 3 0 0 0 21 12" />
</svg>
