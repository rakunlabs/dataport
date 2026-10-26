<script lang="ts">
  import { Download, Copy, Check } from 'lucide-svelte';
  import type { Column } from '@/lib/api/types';
  import { download, toCSV, toJSON } from '@/lib/helper/format';
  import { formatRows, type CopyFormat } from '@/lib/helper/copy';
  import { toast } from '@/lib/stores/toast.svelte';
  import { t } from '@/lib/i18n/index.svelte';

  // table is the qualified, quoted table name, for INSERT statements; dbType
  // decides how values are written in SQL.
  let { columns, rows, name, table = '', dbType = 'postgres' }: { columns: Column[]; rows: unknown[][]; name: string; table?: string; dbType?: string } = $props();

  let open = $state(false);
  let copied = $state('');

  function exportAs(kind: 'csv' | 'json') {
    const base = (name || 'result').replace(/[^\w.-]+/g, '_');
    if (kind === 'csv') download(`${base}.csv`, toCSV(columns, rows), 'text/csv;charset=utf-8');
    else download(`${base}.json`, toJSON(columns, rows), 'application/json');
    open = false;
  }

  const copies: { id: CopyFormat; label: string }[] = [
    { id: 'csv', label: 'CSV' },
    { id: 'tsv-headers', label: 'TSV' },
    { id: 'json', label: 'JSON' },
    { id: 'markdown', label: 'Markdown' },
    { id: 'insert', label: 'SQL INSERT' },
  ];

  async function copyAs(id: CopyFormat) {
    const text = id === 'json' ? toJSON(columns, rows) : formatRows(id, { columns, rows, dbType, table: table || undefined });
    try {
      await navigator.clipboard.writeText(text);
      copied = id;
      toast(t('result.copiedRows', { count: rows.length }), 'success', 1500);
      setTimeout(() => {
        copied = '';
        open = false;
      }, 500);
    } catch {
      toast(t('grid.clipboardUnavailable'), 'error');
    }
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
    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
    <div class="popover absolute right-0 bottom-7 z-30 w-52" role="menu" tabindex="-1" onclick={(e) => e.stopPropagation()}>
      <p class="px-3 pt-1 pb-0.5 text-2xs font-medium text-faint uppercase">{t('result.download')}</p>
      <button class="menu-item" role="menuitem" onclick={() => exportAs('csv')}><Download size={13} /> CSV</button>
      <button class="menu-item" role="menuitem" onclick={() => exportAs('json')}><Download size={13} /> JSON</button>
      <div class="menu-sep"></div>
      <p class="px-3 pt-1 pb-0.5 text-2xs font-medium text-faint uppercase">{t('result.copyClipboard')}</p>
      {#each copies as c (c.id)}
        <button class="menu-item" role="menuitem" onclick={() => copyAs(c.id)}>
          {#if copied === c.id}<Check size={13} class="text-ok" />{:else}<Copy size={13} />{/if}
          {c.label}
        </button>
      {/each}
    </div>
  {/if}
</div>
