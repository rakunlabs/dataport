<script lang="ts">
  import { ArrowDown, ArrowUp, ArrowUpDown } from 'lucide-svelte';
  import type { Column } from '@/lib/api/types';
  import { t } from '@/lib/i18n/index.svelte';
  import { typeColor } from '@/lib/components/result/grid';

  let {
    columns,
    template,
    sortColumn,
    sortDesc,
    selectedCols,
    onsort,
    oncolumn,
    onresize,
  }: {
    columns: Column[];
    template: string;
    sortColumn: string;
    sortDesc: boolean;
    // Columns selected whole, highlighted in the header.
    selectedCols?: Set<number>;
    onsort?: (column: string) => void;
    // A click on a column's name selects the whole column.
    oncolumn?: (e: MouseEvent, i: number) => void;
    onresize: (e: PointerEvent, i: number) => void;
  } = $props();
</script>

<div class="sticky top-0 z-10 grid border-b border-line bg-grid-head" style="grid-template-columns:{template}; height:28px">
  <div class="sticky left-0 z-10 flex items-center justify-end border-r border-line bg-grid-head pr-2 text-2xs text-faint"></div>
  {#each columns as col, i (i)}
    {@const sorted = sortColumn === col.name}
    {@const picked = !!selectedCols?.has(i)}
    <div
      class="group relative flex min-w-0 items-center border-r border-line-soft {picked ? 'bg-accent/15' : 'hover:bg-hover'}"
      role="columnheader"
      aria-sort={sorted ? (sortDesc ? 'descending' : 'ascending') : undefined}
      aria-selected={picked}
    >
      <button
        class="flex h-full min-w-0 flex-1 items-center gap-1.5 pl-2 text-left {onsort ? '' : 'pr-2'}"
        onmousedown={(e) => e.shiftKey && e.preventDefault()}
        onclick={(e) => oncolumn?.(e, i)}
        disabled={!oncolumn}
        title="{col.name} · {col.type || '?'}"
      >
        <span class="truncate text-xs font-semibold text-fg">{col.name}</span>
        <span class="shrink truncate font-mono text-2xs" style="color:{typeColor(col.type)}">{col.type}</span>
      </button>
      {#if onsort}
        <button
          class="flex h-full shrink-0 items-center rounded-sm px-1.5 hover:bg-active"
          onclick={() => onsort?.(col.name)}
          title={t('grid.sortBy', { column: col.name })}
          aria-label={t('grid.sortBy', { column: col.name })}
        >
          {#if sorted}
            {#if sortDesc}<ArrowDown size={12} class="text-accent" />{:else}<ArrowUp size={12} class="text-accent" />{/if}
          {:else}
            <ArrowUpDown size={12} class="text-faint opacity-40 group-hover:opacity-100" />
          {/if}
        </button>
      {/if}
      <div
        class="absolute top-0 right-[-3px] z-10 h-full w-[6px] cursor-col-resize hover:bg-accent/50"
        onpointerdown={(e) => onresize(e, i)}
        role="separator"
        aria-orientation="vertical"
      ></div>
    </div>
  {/each}
</div>
