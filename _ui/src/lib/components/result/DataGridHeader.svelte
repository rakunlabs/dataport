<script lang="ts">
  import { ArrowDown, ArrowUp, ArrowUpDown } from 'lucide-svelte';
  import type { Column } from '@/lib/api/types';
  import { typeColor } from '@/lib/components/result/grid';

  let {
    columns,
    template,
    sortColumn,
    sortDesc,
    onsort,
    onresize,
  }: {
    columns: Column[];
    template: string;
    sortColumn: string;
    sortDesc: boolean;
    onsort?: (column: string) => void;
    onresize: (e: PointerEvent, i: number) => void;
  } = $props();
</script>

<div class="sticky top-0 z-10 grid border-b border-line bg-grid-head" style="grid-template-columns:{template}; height:28px">
  <div class="sticky left-0 z-10 flex items-center justify-end border-r border-line bg-grid-head pr-2 text-2xs text-faint"></div>
  {#each columns as col, i (i)}
    {@const sorted = sortColumn === col.name}
    <div
      class="group relative flex min-w-0 items-center border-r border-line-soft {onsort ? 'hover:bg-hover' : ''}"
      role="columnheader"
      aria-sort={sorted ? (sortDesc ? 'descending' : 'ascending') : undefined}
    >
      <button
        class="flex h-full min-w-0 flex-1 items-center gap-1.5 px-2 text-left"
        onclick={() => onsort?.(col.name)}
        disabled={!onsort}
        title="{col.name} · {col.type || '?'}"
      >
        <span class="truncate text-xs font-semibold text-fg">{col.name}</span>
        <span class="shrink truncate font-mono text-2xs" style="color:{typeColor(col.type)}">{col.type}</span>
        {#if onsort}
          <span class="ml-auto shrink-0">
            {#if sorted}
              {#if sortDesc}<ArrowDown size={12} class="text-accent" />{:else}<ArrowUp size={12} class="text-accent" />{/if}
            {:else}
              <ArrowUpDown size={12} class="text-faint opacity-0 group-hover:opacity-100" />
            {/if}
          </span>
        {/if}
      </button>
      <div
        class="absolute top-0 right-[-3px] z-10 h-full w-[6px] cursor-col-resize hover:bg-accent/50"
        onpointerdown={(e) => onresize(e, i)}
        role="separator"
        aria-orientation="vertical"
      ></div>
    </div>
  {/each}
</div>
