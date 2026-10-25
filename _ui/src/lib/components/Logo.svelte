<script lang="ts">
  let { size = 20 }: { size?: number } = $props();
</script>

<svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true" class="shrink-0">
  <rect width="32" height="32" rx="7" fill="var(--color-accent)" />
  <path d="M9 9.5C9 8.1 12.1 7 16 7s7 1.1 7 2.5v13c0 1.4-3.1 2.5-7 2.5s-7-1.1-7-2.5z" fill="none" stroke="#fff" stroke-width="2" />
  <path d="M9 9.5c0 1.4 3.1 2.5 7 2.5s7-1.1 7-2.5" fill="none" stroke="#fff" stroke-width="2" />
  <path d="M13 16.5h7m-2.5-2.5 2.5 2.5-2.5 2.5" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
</svg>
