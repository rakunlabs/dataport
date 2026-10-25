<script lang="ts">
  import { Download } from 'lucide-svelte';
  import type { Column } from '@/lib/api/types';
  import { download, toCSV, toJSON } from '@/lib/helper/format';
  import { t } from '@/lib/i18n/index.svelte';

  let { columns, rows, name }: { columns: Column[]; rows: unknown[][]; name: string } = $props();

  let open = $state(false);

  function exportAs(kind: 'csv' | 'json') {
    const base = (name || 'result').replace(/[^\w.-]+/g, '_');
    if (kind === 'csv') download(`${base}.csv`, toCSV(columns, rows), 'text/csv;charset=utf-8');
    else download(`${base}.json`, toJSON(columns, rows), 'application/json');
    open = false;
  }
</script>

<svelte:window onclick={() => (open = false)} />

<div class="relative">
  <button
    class="btn h-[22px] px-1.5 text-xs"
    aria-haspopup="menu"
    aria-expanded={open}
    onclick={(e) => {
      e.stopPropagation();
      open = !open;
    }}
  >
    <Download size={13} /> {t('result.export')}
  </button>
  {#if open}
    <div class="popover absolute right-0 bottom-7 z-30 w-36" role="menu">
      <button class="menu-item" role="menuitem" onclick={() => exportAs('csv')}>CSV</button>
      <button class="menu-item" role="menuitem" onclick={() => exportAs('json')}>JSON</button>
    </div>
  {/if}
</div>
