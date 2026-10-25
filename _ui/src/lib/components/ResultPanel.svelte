<script lang="ts">
  import { CircleCheck, CircleX, LoaderCircle, TriangleAlert, Info, Copy, ChevronRight, Rows3 } from 'lucide-svelte';
  import type { Snippet } from 'svelte';
  import { formatRows, sqlFormats, type CopyFormat } from '@/lib/helper/copy';
  import { connection, qualified, tableKey, ws } from '@/lib/stores/workspace.svelte';
  import { toast } from '@/lib/stores/toast.svelte';
  import DataGrid from '@/lib/components/DataGrid.svelte';
  import CellDialog from '@/lib/components/CellDialog.svelte';
  import { formatDuration } from '@/lib/helper/format';
  import ExportMenu from '@/lib/components/ExportMenu.svelte';
  import type { Tab } from '@/lib/stores/workspace.svelte';
  import type { EditState } from '@/lib/stores/edits.svelte';
  import { fmtNumber, t } from '@/lib/i18n/index.svelte';

  let {
    tab,
    sortColumn = '',
    sortDesc = false,
    onsort,
    rowOffset = 0,
    edit,
    editable = false,
    menuItems,
  }: {
    tab: Tab;
    sortColumn?: string;
    sortDesc?: boolean;
    onsort?: (column: string) => void;
    rowOffset?: number;
    edit?: EditState;
    editable?: boolean;
    // Extra context menu entries, such as the editing actions of a table tab.
    menuItems?: Snippet;
  } = $props();

  let gridRef: DataGrid | undefined = $state();

  export function grid() {
    return gridRef;
  }

  let cell = $state<{ row: number; col: number } | null>(null);

  const results = $derived(tab.result?.results ?? []);
  const current = $derived(results[tab.activeResult]);

  function label(i: number) {
    const r = results[i];
    if (r.error) return t('result.error');
    if (r.kind === 'rows') return t('result.result', { n: i + 1 });
    return t('result.statement', { n: i + 1 });
  }

  let menu = $state<{ x: number; y: number; col: number } | null>(null);
  let copyOpen = $state(false);

  const conn = $derived(connection(tab.connectionId));
  const dbType = $derived(conn?.db_type ?? 'postgres');
  // SQL formats name the table only for a table tab; a query result has no
  // reliable source table.
  const keys = $derived(tab.kind === 'table' ? (ws.columns[tableKey(tab)]?.items ?? []).filter((c) => c.primary_key).map((c) => c.name) : []);

  const formats: { id: CopyFormat; label: string; sep?: boolean }[] = [
    { id: 'tsv', label: 'TSV' },
    { id: 'tsv-headers', label: '' },
    { id: 'csv', label: 'CSV' },
    { id: 'json', label: 'JSON', sep: true },
    { id: 'markdown', label: 'Markdown' },
    { id: 'insert', label: 'INSERT', sep: true },
    { id: 'update', label: 'UPDATE' },
    { id: 'where', label: 'WHERE' },
    { id: 'in-list', label: 'IN (…)' },
  ];

  function oncontext(e: MouseEvent, _row: number, col: number) {
    copyOpen = false;
    menu = { x: Math.min(e.clientX, window.innerWidth - 240), y: Math.min(e.clientY, window.innerHeight - 320), col };
  }

  async function write(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      toast(t('grid.copied'), 'success', 1200);
    } catch {
      toast(t('grid.clipboardUnavailable'), 'error');
    }
    menu = null;
  }

  function copyAs(format: CopyFormat) {
    const sel = gridRef?.selectedValues();
    if (!sel || !current?.columns || !sel.rows.length) return;
    void write(
      formatRows(format, {
        columns: current.columns,
        rows: sel.rows,
        dbType,
        table: tab.kind === 'table' ? qualified(dbType, tab.schema, tab.table) : undefined,
        keys,
        column: sel.col,
      }),
    );
  }

  function copyCell() {
    const sel = gridRef?.selectedValues();
    if (!sel?.rows.length) return;
    const v = sel.rows[sel.rows.length - 1][sel.col];
    void write(v === null || v === undefined ? 'NULL' : typeof v === 'object' ? JSON.stringify(v) : String(v));
  }

</script>

<svelte:window
  onclick={() => (menu = null)}
  onkeydown={(e) => e.key === 'Escape' && (menu = null)}
/>

<div class="flex h-full min-h-0 flex-col">
  {#if results.length > 1}
    <div class="flex h-[30px] shrink-0 items-stretch gap-px overflow-x-auto border-b border-line bg-panel px-1" role="tablist">
      {#each results as r, i (i)}
        {@const active = i === tab.activeResult}
        <button
          class="relative flex items-center gap-1.5 px-2.5 text-xs whitespace-nowrap {active ? 'text-fg' : 'text-muted hover:text-fg'}"
          role="tab"
          aria-selected={active}
          onclick={() => (tab.activeResult = i)}
        >
          {#if r.error}<CircleX size={12} class="text-danger" />{:else if r.kind === 'rows'}<Rows3 size={12} class="text-accent" />{:else}<CircleCheck size={12} class="text-ok" />{/if}
          {label(i)}
          {#if active}<span class="absolute inset-x-1 bottom-0 h-0.5 rounded-full bg-accent"></span>{/if}
        </button>
      {/each}
    </div>
  {/if}

  <div class="relative min-h-0 flex-1 bg-bg">
    {#if tab.running && !current}
      <div class="flex h-full items-center justify-center gap-2 text-sm text-muted"><LoaderCircle size={15} class="animate-spin" /> {t('result.running')}</div>
    {:else if tab.error}
      <div class="p-3">
        <div class="rounded-md border border-danger/30 bg-danger-soft">
          <div class="flex items-center gap-2 border-b border-danger/20 px-3 py-2 text-sm font-medium text-danger"><TriangleAlert size={14} /> {t('result.error')}</div>
          <pre class="px-3 py-2.5 font-mono text-xs whitespace-pre-wrap text-fg">{tab.error}</pre>
        </div>
      </div>
    {:else if !current}
      <div class="flex h-full flex-col items-center justify-center gap-2 text-sm text-muted">
        <span>{t('result.empty')}</span>
        <span class="flex items-center gap-1 text-xs text-faint"><span class="kbd">Ctrl</span><span class="kbd">Enter</span></span>
      </div>
    {:else if current.error}
      <div class="p-3">
        <div class="rounded-md border border-danger/30 bg-danger-soft">
          <div class="flex items-center gap-2 border-b border-danger/20 px-3 py-2 text-sm font-medium text-danger"><TriangleAlert size={14} /> {t('result.failed')}</div>
          <pre class="px-3 py-2.5 font-mono text-xs whitespace-pre-wrap text-fg">{current.error}</pre>
          <pre class="border-t border-danger/20 px-3 py-2 font-mono text-xs whitespace-pre-wrap text-muted">{current.statement}</pre>
          {#if tab.result && !tab.result.committed && !tab.result.results.every((r) => r.kind === 'rows')}
            <p class="flex items-center gap-1.5 border-t border-danger/20 px-3 py-2 text-xs text-fg-2">
              <Info size={13} class="shrink-0" />
              {tab.result.transactional ? t('result.rolledBack') : t('result.notTransactional')}
            </p>
          {/if}
        </div>
      </div>
    {:else if current.kind === 'exec'}
      <div class="p-3">
        <div class="rounded-md border border-line bg-panel">
          <div class="flex items-center gap-2 px-3 py-2 text-sm">
            <CircleCheck size={14} class="shrink-0 text-ok" />
            <span class="font-medium">{t('result.affected', { count: current.rows_affected })}</span>
          </div>
          <pre class="border-t border-line-soft px-3 py-2 font-mono text-xs whitespace-pre-wrap text-muted">{current.statement}</pre>
          {#if tab.result && !tab.result.committed && tab.result.transactional}
            <p class="flex items-center gap-1.5 border-t border-line-soft px-3 py-2 text-xs text-warn"><Info size={13} /> {t('result.notCommitted')}</p>
          {/if}
        </div>
      </div>
    {:else}
      <DataGrid
        bind:this={gridRef}
        columns={current.columns ?? []}
        rows={current.rows ?? []}
        {rowOffset}
        {sortColumn}
        {sortDesc}
        {onsort}
        {edit}
        {editable}
        {oncontext}
        oncell={(row, col) => (cell = { row, col })}
      />
      {#if !current.rows?.length}
        <div class="pointer-events-none absolute inset-x-0 top-14 text-center text-sm text-muted">{t('result.noRows')}</div>
      {/if}
    {/if}

    {#if tab.running && current}
      <div class="absolute inset-x-0 top-0 h-0.5 overflow-hidden bg-accent/20"><span class="block h-full w-1/3 animate-[progress_1.1s_ease-in-out_infinite] bg-accent"></span></div>
    {/if}
  </div>

  {#if tab.kind === 'query'}
    <div class="flex h-7 shrink-0 items-center gap-3 border-t border-line bg-panel pr-1 pl-3 text-xs text-muted">
      {#if current?.kind === 'rows'}
        <span class="tabular-nums">
          {t('result.rows', { count: fmtNumber(current.rows?.length ?? 0) })}
          {#if current.truncated}<span class="ml-1 text-warn">· {t('result.truncated')}</span>{/if}
        </span>
      {/if}
      {#if tab.result}<span class="tabular-nums">{formatDuration(tab.result.duration_ms)}</span>{/if}
      {#if current}<span class="min-w-0 flex-1 truncate font-mono text-faint" title={current.statement}>{current.statement}</span>{:else}<span class="flex-1"></span>{/if}

      {#if current?.kind === 'rows' && current.rows?.length}
        <ExportMenu columns={current.columns ?? []} rows={current.rows} name={tab.table || tab.title} />
      {/if}
    </div>
  {/if}
</div>

{#if cell && current?.columns && current.rows}
  <CellDialog
    column={current.columns[cell.col].name}
    type={current.columns[cell.col].type}
    value={current.rows[cell.row][cell.col]}
    onclose={() => (cell = null)}
  />
{/if}

{#if menu}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="popover fixed z-50 w-60" style="left:{menu.x}px; top:{menu.y}px" role="menu" tabindex="-1" onclick={(e) => e.stopPropagation()}>
    <button class="menu-item" onclick={copyCell}><Copy size={13} /> {t('grid.copyCell')}</button>
    <div class="relative" role="menu" tabindex="-1" onmouseenter={() => (copyOpen = true)} onmouseleave={() => (copyOpen = false)}>
      <button class="menu-item" onclick={() => (copyOpen = !copyOpen)}>
        <Copy size={13} class="opacity-0" /> {t('grid.copyAs')} <span class="ml-auto text-xs opacity-60">{t('grid.selectedRows', { count: gridRef?.selectedValues().rows.length ?? 0 })}</span>
        <ChevronRight size={13} class="opacity-60" />
      </button>
      {#if copyOpen}
        <div class="popover absolute -top-1 z-50 w-44 {menu.x > window.innerWidth - 480 ? 'right-full mr-0.5' : 'left-full ml-0.5'}">
          {#each formats as f (f.id)}
            {#if f.sep}<div class="menu-sep"></div>{/if}
            <button
              class="menu-item"
              onclick={() => copyAs(f.id)}
              title={sqlFormats.includes(f.id) && tab.kind !== 'table' ? t('grid.noTableHint') : undefined}
            >
              {f.id === 'tsv-headers' ? t('grid.tsvHeaders') : f.label}
            </button>
          {/each}
        </div>
      {/if}
    </div>
    {#if menuItems}
      <div class="menu-sep"></div>
      {@render menuItems()}
    {/if}
  </div>
{/if}
