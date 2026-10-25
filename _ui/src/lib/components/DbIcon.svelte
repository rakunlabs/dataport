<script lang="ts">
  // Database marks drawn as small monograms in each vendor's colour, so the
  // tree reads at a glance without shipping vendor logos.
  let { type, size = 14 }: { type: string; size?: number } = $props();

  const marks: Record<string, { color: string; letter: string }> = {
    postgres: { color: '#336791', letter: 'Pg' },
    mysql: { color: '#00758f', letter: 'My' },
    mariadb: { color: '#a5603f', letter: 'Ma' },
    mssql: { color: '#a91d22', letter: 'Ms' },
    clickhouse: { color: '#b38f00', letter: 'Ch' },
  };

  const mark = $derived(marks[type] ?? { color: '#6b7280', letter: type.slice(0, 2) });
</script>

<span
  class="inline-flex shrink-0 items-center justify-center rounded-[3px] font-semibold tracking-[-0.02em] text-white"
  style="background:{mark.color};width:{size + 2}px;height:{size + 2}px;font-size:{Math.round(size * 0.52)}px;line-height:1"
  title={type}
>
  {mark.letter}
</span>
