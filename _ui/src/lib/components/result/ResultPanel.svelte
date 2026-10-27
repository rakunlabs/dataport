<script lang="ts">
  import { CircleCheck, CircleX, TriangleAlert, Info, Copy, ChevronRight, Rows3, X, PanelRight, RefreshCw, Square } from 'lucide-svelte';
  import type { Snippet } from 'svelte';
  import { formatRows, sqlFormats, type CopyFormat } from '@/lib/helper/copy';
  import { copyText } from '@/lib/helper/clipboard';
  import { cancel, connection, qualified, runTable, tableKey, ws } from '@/lib/stores/workspace.svelte';
  import RunTimer from '@/lib/components/result/RunTimer.svelte';
  import DataGrid from '@/lib/components/result/DataGrid.svelte';
  import CellDialog from '@/lib/components/result/CellDialog.svelte';
  import ValuePanel from '@/lib/components/result/ValuePanel.svelte';
  import { saveValuePanel, setValuePanel, valuePanel } from '@/lib/stores/valuePanel.svelte';
  import { formatDuration } from '@/lib/helper/format';
  import ExportMenu from '@/lib/components/result/ExportMenu.svelte';
  import type { Tab } from '@/lib/stores/workspace.svelte';
  import type { ResultSet } from '@/lib/api/types';
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

  export function toggleValuePanel() {
    setValuePanel(!valuePanel.open);
  }

  let cell = $state<{ row: number; col: number } | null>(null);

  // The cell the value panel shows. JSON cells open the panel by themselves,
  // as DBeaver does; it stays open until closed.
  let focused = $state<{ row: number; col: number } | null>(null);
  const focusedValue = $derived.by(() => {
    void edit?.count;
    return focused && gridRef ? gridRef.cellAt(focused.row, focused.col) : undefined;
  });

  function select(row: number, col: number) {
    focused = { row, col };
    if (valuePanel.open) return;
    const type = current?.columns?.[col]?.type ?? '';
    const v = gridRef?.cellAt(row, col);
    if (/json/i.test(type) || (typeof v === 'string' && v.length > 2 && /^\s*[[{]/.test(v) && isJson(v))) setValuePanel(true);
  }

  function isJson(s: string) {
    try {
      JSON.parse(s);
      return true;
    } catch {
      return false;
    }
  }

  function resizePanel(e: PointerEvent) {
    const startX = e.clientX;
    const start = valuePanel.width;
    const move = (ev: PointerEvent) => (valuePanel.width = Math.min(900, Math.max(220, start - (ev.clientX - startX))));
    const up = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
      saveValuePanel();
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  }

  const results = $derived(tab.result?.results ?? []);
  const current = $derived(results[tab.activeResult]);

  // Results of several runs can sit side by side; each knows its own run.
  const runs = $derived(new Set(results.map((r) => r.run)).size);
  const committed = (r: ResultSet) => r.committed ?? tab.result?.committed ?? false;
  const runOf = (r: ResultSet) => results.filter((x) => x.run === r.run);

  // Closing results gives the editor the whole tab until the next run.
  function closeAll() {
    tab.result = null;
    tab.error = '';
    tab.activeResult = 0;
  }

  function closeResult(i: number) {
    if (!tab.result) return;
    if (tab.result.results.length <= 1) return closeAll();
    tab.result.results.splice(i, 1);
    if (tab.activeResult > i || tab.activeResult >= tab.result.results.length) tab.activeResult = Math.max(0, tab.activeResult - 1);
  }

  function label(i: number) {
    const r = results[i];
    if (r.error) return t('result.error');
    if (r.kind === 'rows') return t('result.result', { n: i + 1 });
    return t('result.statement', { n: i + 1 });
  }

  // A result's tab names the start of its statement, so runs of different
  // queries can be told apart.
  function hint(r: ResultSet) {
    return r.statement.replace(/\s+/g, ' ').trim();
  }

  let menu = $state<{ x: number; y: number; col: number } | null>(null);
  let copyOpen = $state(false);
  let copyCloseTimer: ReturnType<typeof setTimeout> | undefined;

  // Delay closing so the pointer can travel diagonally into the submenu
  // without it vanishing when crossing neighbouring items.
  function openCopy() {
    clearTimeout(copyCloseTimer);
    copyOpen = true;
  }

  function closeCopy() {
    clearTimeout(copyCloseTimer);
    copyCloseTimer = setTimeout(() => (copyOpen = false), 300);
  }

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
    clearTimeout(copyCloseTimer);
    copyOpen = false;
    menu = { x: Math.min(e.clientX, window.innerWidth - 240), y: Math.min(e.clientY, window.innerHeight - 320), col };
  }

  function write(text: string) {
    void copyText(text);
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
  {#if results.length > 1 || runs > 1}
    <div class="flex h-[30px] shrink-0 items-stretch gap-px border-b border-line bg-panel px-1">
      <div class="flex min-w-0 flex-1 items-stretch gap-px overflow-x-auto" role="tablist">
        {#each results as r, i (i)}
          {@const active = i === tab.activeResult}
          <div
            class="group relative flex items-center gap-1.5 pr-1 pl-2.5 text-xs whitespace-nowrap {active ? 'text-fg' : 'text-muted hover:text-fg'}"
            role="tab"
            tabindex="0"
            aria-selected={active}
            onclick={() => (tab.activeResult = i)}
            onkeydown={(e) => e.key === 'Enter' && (tab.activeResult = i)}
            onauxclick={(e) => e.button === 1 && closeResult(i)}
            title={hint(r)}
          >
            {#if r.error}<CircleX size={12} class="text-danger" />{:else if r.kind === 'rows'}<Rows3 size={12} class="text-accent" />{:else}<CircleCheck size={12} class="text-ok" />{/if}
            {label(i)}
            {#if tab.kind === 'query'}
              <button
                class="flex size-4 items-center justify-center rounded-sm text-faint hover:bg-active hover:text-fg {active ? '' : 'invisible group-hover:visible'}"
                onclick={(e) => {
                  e.stopPropagation();
                  closeResult(i);
                }}
                title={t('result.closeOne')}
                aria-label={t('result.closeOne')}><X size={11} /></button
              >
            {/if}
            {#if active}<span class="absolute inset-x-1 bottom-0 h-0.5 rounded-full bg-accent"></span>{/if}
          </div>
        {/each}
      </div>
      {#if tab.kind === 'query'}
        <button class="icon-btn my-auto size-6 shrink-0" onclick={closeAll} title={t('result.close')} aria-label={t('result.close')}><X size={14} /></button>
      {/if}
    </div>
  {/if}

  <div class="relative min-h-0 flex-1 bg-bg">
    {#if tab.running && !current}
      <div class="flex h-full items-center justify-center"><RunTimer {tab} large /></div>
    {:else if tab.error}
      <div class="p-3">
        <div class="rounded-md border border-danger/30 bg-danger-soft">
          <div class="flex items-center gap-2 border-b border-danger/20 px-3 py-2 text-sm font-medium text-danger"><TriangleAlert size={14} /> {t('result.error')}</div>
          <pre class="px-3 py-2.5 font-mono text-xs whitespace-pre-wrap text-fg">{tab.error}</pre>
        </div>
      </div>
    {:else if !current}
      <div class="flex h-full flex-col items-center justify-center gap-2 text-sm text-muted">
        {#if tab.kind === 'table'}
          <span>{t('result.tableNotLoaded')}</span>
          <button class="btn" onclick={() => runTable(tab.id)}><RefreshCw size={13} /> {t('table.refresh')}</button>
        {:else}
          <span>{t('result.empty')}</span>
          <span class="flex items-center gap-1 text-xs text-faint"><span class="kbd">Ctrl</span><span class="kbd">Enter</span></span>
        {/if}
      </div>
    {:else if current.error}
      <div class="p-3">
        <div class="rounded-md border border-danger/30 bg-danger-soft">
          <div class="flex items-center gap-2 border-b border-danger/20 px-3 py-2 text-sm font-medium text-danger"><TriangleAlert size={14} /> {t('result.failed')}</div>
          <pre class="px-3 py-2.5 font-mono text-xs whitespace-pre-wrap text-fg">{current.error}</pre>
          <pre class="border-t border-danger/20 px-3 py-2 font-mono text-xs whitespace-pre-wrap text-muted">{current.statement}</pre>
          {#if current.inTx}
            <p class="flex items-center gap-1.5 border-t border-danger/20 px-3 py-2 text-xs text-fg-2">
              <Info size={13} class="shrink-0" />
              {t('tx.failedStillOpen')}
            </p>
          {:else if tab.result && !committed(current) && !runOf(current).every((r) => r.kind === 'rows')}
            <p class="flex items-center gap-1.5 border-t border-danger/20 px-3 py-2 text-xs text-fg-2">
              <Info size={13} class="shrink-0" />
              {(current.transactional ?? tab.result.transactional) ? t('result.rolledBack') : t('result.notTransactional')}
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
          {#if current.inTx}
            <p class="flex items-center gap-1.5 border-t border-line-soft px-3 py-2 text-xs text-warn"><Info size={13} /> {t('tx.pending')}</p>
          {:else if tab.result && !committed(current) && (current.transactional ?? tab.result.transactional)}
            <p class="flex items-center gap-1.5 border-t border-line-soft px-3 py-2 text-xs text-warn"><Info size={13} /> {t('result.notCommitted')}</p>
          {/if}
        </div>
      </div>
    {:else}
      <div class="flex h-full min-h-0">
        <div class="relative min-w-0 flex-1">
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
            oncell={(row, col) => (valuePanel.open ? (focused = { row, col }) : (cell = { row, col }))}
            onselect={select}
          />
          {#if !current.rows?.length && !(edit?.inserted.length)}
            <div class="pointer-events-none absolute inset-x-0 top-14 text-center text-sm text-muted">
              {tab.kind === 'table' ? (tab.where.trim() ? t('result.noMatch') : tab.offset ? t('result.noMorePages') : t('result.tableEmpty')) : t('result.noRows')}
            </div>
          {/if}
        </div>
        {#if valuePanel.open && focused && current.columns?.[focused.col]}
          {@const col = current.columns[focused.col]}
          <div class="relative z-10 -mr-[3px] w-[5px] shrink-0 cursor-col-resize hover:bg-accent/50" onpointerdown={resizePanel} role="separator" aria-orientation="vertical"></div>
          <div class="shrink-0" style="width:{valuePanel.width}px">
            <ValuePanel
              column={col.name}
              type={col.type}
              value={focusedValue}
              editable={editable && !!gridRef?.canEditCell(focused.row)}
              onchange={(v) => focused && gridRef?.setCell(focused.row, focused.col, v)}
              onclose={() => setValuePanel(false)}
            />
          </div>
        {/if}
      </div>
    {/if}

    {#if tab.running && current}
      <div class="absolute inset-x-0 top-0 h-0.5 overflow-hidden bg-accent/20"><span class="block h-full w-1/3 animate-[progress_1.1s_ease-in-out_infinite] bg-accent"></span></div>
    {/if}
  </div>

  {#if tab.kind === 'query'}
    <div class="flex h-7 shrink-0 items-center gap-3 border-t border-line bg-panel pr-1 pl-3 text-xs text-muted">
      {#if tab.running && current}
        <RunTimer {tab} />
        <button class="btn h-[22px] px-1.5 text-xs" onclick={() => cancel(tab.id)} disabled={tab.cancelling || !tab.queryId}>
          <Square size={9} fill="currentColor" class="text-danger" /> {t('query.stop')}
        </button>
        <span class="text-line">|</span>
      {/if}
      {#if current?.kind === 'rows'}
        <span class="tabular-nums">
          {t('result.rows', { count: fmtNumber(current.rows?.length ?? 0) })}
          {#if current.truncated}<span class="ml-1 text-warn">· {t('result.truncated')}</span>{/if}
        </span>
      {/if}
      {#if current}<span class="tabular-nums">{formatDuration(current.duration_ms)}</span>{/if}
      {#if current}<span class="min-w-0 flex-1 truncate font-mono text-faint" title={current.statement}>{current.statement}</span>{:else}<span class="flex-1"></span>{/if}

      {#if current?.kind === 'rows' && current.rows?.length}
        <ExportMenu columns={current.columns ?? []} rows={current.rows} name={tab.table || tab.title} {dbType} />
      {/if}
      {#if current?.kind === 'rows'}
        <button class="icon-btn size-[22px]" aria-pressed={valuePanel.open} onclick={() => setValuePanel(!valuePanel.open)} title={t('valuePanel.toggle')} aria-label={t('valuePanel.toggle')}><PanelRight size={13} /></button>
      {/if}
      {#if (tab.result || tab.error) && !tab.running}
        <button class="icon-btn size-[22px]" onclick={closeAll} title={t('result.close')} aria-label={t('result.close')}><X size={13} /></button>
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
    <div class="relative" role="menu" tabindex="-1" onmouseenter={openCopy} onmouseleave={closeCopy}>
      <button class="menu-item" onclick={() => (copyOpen = !copyOpen)}>
        <Copy size={13} class="opacity-0" /> {t('grid.copyAs')} <span class="ml-auto text-xs opacity-60">{t('grid.selectedRows', { count: gridRef?.selectedValues().rows.length ?? 0 })}</span>
        <ChevronRight size={13} class="opacity-60" />
      </button>
      {#if copyOpen}
        <div class="absolute -top-1 z-50 {menu.x > window.innerWidth - 480 ? 'right-full pr-0.5' : 'left-full pl-0.5'}">
          <div class="popover w-44">
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
        </div>
      {/if}
    </div>
    {#if menuItems}
      <div class="menu-sep"></div>
      {@render menuItems()}
    {/if}
  </div>
{/if}
