<script lang="ts">
  import {
    RefreshCw,
    ChevronLeft,
    ChevronRight,
    Code,
    Lock,
    Plus,
    Copy,
    Trash2,
    Save,
    Undo2,
    Eye,
    Ban,
    KeyRound,
    LoaderCircle,
    ShieldAlert,
    X,
    Table2,
    ListTree,
    FileCode,
    PanelRight,
  } from 'lucide-svelte';
  import TableStructure from '@/lib/components/TableStructure.svelte';
  import ConditionInput from '@/lib/components/ConditionInput.svelte';
  import ExportMenu from '@/lib/components/ExportMenu.svelte';
  import ResultPanel from '@/lib/components/ResultPanel.svelte';
  import Modal from '@/lib/components/Modal.svelte';
  import { ApiError } from '@/lib/api/client';
  import {
    connection,
    loadColumns,
    openQueryTab,
    persist,
    quoteIdent,
    qualified,
    runTable,
    tableKey,
    tableSQL,
    ws,
    type Tab,
  } from '@/lib/stores/workspace.svelte';
  import { previewEdits, saveEdits } from '@/lib/stores/edits.svelte';
  import { confirmProduction } from '@/lib/stores/confirm.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { fmtNumber, t } from '@/lib/i18n/index.svelte';
  import { formatDuration } from '@/lib/helper/format';
  import { setValuePanel, valuePanel } from '@/lib/stores/valuePanel.svelte';

  let { tab }: { tab: Tab } = $props();

  let panel: ResultPanel | undefined = $state();
  let preview = $state<string[] | null>(null);
  let saving = $state(false);

  const conn = $derived(connection(tab.connectionId));
  const current = $derived(tab.result?.results?.[0]);
  const rowCount = $derived(current?.rows?.length ?? 0);
  const meta = $derived(ws.columns[tableKey(tab)]?.items ?? []);
  const hasKey = $derived(meta.some((c) => c.primary_key));
  const writable = $derived(!!conn && !conn.read_only && conn.editable !== false && current?.kind === 'rows');
  const pending = $derived(tab.edit.count);

  $effect(() => {
    void loadColumns(tab.connectionId, tab.schema, tab.table);
  });

  // Sorting from the grid header writes ORDER BY for a single column.
  const sort = $derived.by(() => {
    const m = tab.orderBy.trim().match(/^([`"\\[])((?:[^`"\\]]|``|"")+)[`"\\]](?:\s+(asc|desc))?$/i);
    if (!m) return { column: '', desc: false };
    return { column: m[2].replaceAll('``', '`').replaceAll('""', '"'), desc: (m[3] ?? '').toLowerCase() === 'desc' };
  });

  function guard(): boolean {
    if (pending && !confirm(t('edit.discardConfirm', { count: pending }))) return false;
    return true;
  }

  function onsort(column: string) {
    if (!guard()) return;
    const quoted = quoteIdent(conn?.db_type ?? 'postgres', column);
    if (sort.column !== column) tab.orderBy = `${quoted} ASC`;
    else if (!sort.desc) tab.orderBy = `${quoted} DESC`;
    else tab.orderBy = '';
    tab.offset = 0;
    persist();
    void runTable(tab.id);
  }

  function apply(e?: Event) {
    e?.preventDefault();
    if (!guard()) return;
    tab.offset = 0;
    persist();
    void runTable(tab.id);
  }

  function page(dir: number) {
    if (!guard()) return;
    tab.offset = Math.max(0, tab.offset + dir * tab.limit);
    void runTable(tab.id);
  }

  function refresh() {
    if (!guard()) return;
    void runTable(tab.id);
  }

  // ////////////////////////////////////////////
  // editing

  function columnNames() {
    return current?.columns?.map((c) => c.name) ?? [];
  }

  function addRow() {
    tab.edit.addRow(columnNames().length);
    const grid = panel?.grid();
    grid?.focusCell(rowCount + tab.edit.inserted.length - 1, 0);
  }

  function cloneRows() {
    const grid = panel?.grid();
    const sel = grid?.selection();
    if (!sel || !current?.rows) return;
    // Key columns are cleared so the database assigns new ones.
    const keyIdx = new Set(meta.filter((c) => c.primary_key).map((c) => columnNames().indexOf(c.name)));
    for (const r of sel.existing) {
      const row = current.rows[r].map((v, i) => (keyIdx.has(i) ? null : tab.edit.value(r, i, v)));
      tab.edit.addRow(row.length, row);
    }
    for (const i of sel.added) tab.edit.addRow(columnNames().length, [...tab.edit.inserted[i]]);
  }

  function deleteRows() {
    const sel = panel?.grid()?.selection();
    if (!sel) return;
    for (const i of sel.added) tab.edit.removeNew(i);
    if (sel.existing.length) tab.edit.toggleDelete(sel.existing);
  }

  function edits() {
    return tab.edit.edits(columnNames(), meta, current?.rows ?? []);
  }

  async function showPreview() {
    try {
      preview = (await previewEdits(tab.connectionId, tab.schema, tab.table, edits())).statements;
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  async function save(confirmed = false) {
    if (saving || !pending) return;
    saving = true;
    try {
      const res = await saveEdits(tab.connectionId, tab.schema, tab.table, edits(), confirmed);
      toast(t('edit.saved', { count: res.affected }), 'success');
      preview = null;
      await runTable(tab.id);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409 && e.data?.confirmation_required) {
        saving = false;
        let sql = preview?.join(';\n');
        if (!sql) {
          try {
            sql = (await previewEdits(tab.connectionId, tab.schema, tab.table, edits())).statements.join(';\n');
          } catch {
            sql = '';
          }
        }
        if (await confirmProduction({ connection: conn?.name ?? '', sql: sql ?? '', risk: 'write' })) return save(true);
        return;
      }
      // Pending changes stay so the user can fix and retry.
      toast(errorMessage(e), 'error', 8000);
    } finally {
      saving = false;
    }
  }

  function discard() {
    tab.edit.clear();
    preview = null;
  }

  function onkeydown(e: KeyboardEvent) {
    const mod = e.metaKey || e.ctrlKey;
    // Ctrl+Enter and F5 reload the table, with the filter typed so far.
    if ((mod && e.key === 'Enter' && !e.shiftKey) || e.key === 'F5') {
      if ((e.target as HTMLElement)?.closest?.('.cm-editor') && tab.view !== 'data') return;
      e.preventDefault();
      if (tab.view === 'data') apply();
      return;
    }
    if (!writable) return;
    if (mod && e.key === 's') {
      e.preventDefault();
      void save();
    } else if (mod && e.key === 'Enter' && e.shiftKey) {
      e.preventDefault();
      addRow();
    } else if (mod && (e.key === 'Delete' || e.key === 'Backspace') && (e.target as HTMLElement)?.getAttribute('role') === 'grid') {
      e.preventDefault();
      deleteRows();
    }
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="flex h-full min-h-0 flex-col" {onkeydown}>
  <div class="toolbar">
    <div class="segmented" role="group" aria-label={t('structure.view')}>
      {#each [{ id: 'data', icon: Table2, label: t('structure.data') }, { id: 'structure', icon: ListTree, label: t('structure.structure') }, { id: 'ddl', icon: FileCode, label: 'DDL' }] as v (v.id)}
        <button aria-pressed={tab.view === v.id} onclick={() => (tab.view = v.id as Tab['view'])}>
          <v.icon size={13} /> {v.label}
        </button>
      {/each}
    </div>

    <span class="ml-1 flex min-w-0 items-center font-mono text-xs">
      <span class="truncate text-muted">{tab.schema}.</span><span class="truncate font-medium text-fg">{tab.table}</span>
    </span>
    {#if conn?.production}<span class="pill-prod" title={t('conn.productionHint')}><ShieldAlert size={10} /> {t('conn.production')}</span>{/if}
    {#if conn?.read_only}
      <span class="pill-ro" title={t('conn.readOnlyHint')}><Lock size={10} /> {t('conn.readOnlyShort')}</span>
    {/if}

    {#if writable && tab.view === 'data' && !hasKey && meta.length}
      <span class="pill-rw" title={t('edit.noKeyHint')}><KeyRound size={10} /> {t('edit.noKey')}</span>
    {/if}

    <div class="flex-1"></div>

    {#if pending && tab.view !== 'data'}
      <button class="pill-rw h-[22px] hover:brightness-110" onclick={() => (tab.view = 'data')}>{t('edit.pending', { count: pending })}</button>
    {/if}

    {#if tab.view === 'data'}
      {#if writable}
        <button class="icon-btn" onclick={() => panel?.grid()?.setNull()} title={t('edit.setNull')} aria-label={t('edit.setNull')}><Ban size={14} /></button>
        <button class="icon-btn" onclick={cloneRows} title={t('edit.cloneHint')} aria-label={t('edit.clone')}><Copy size={14} /></button>
        <button class="icon-btn hover:text-danger" onclick={deleteRows} disabled={!hasKey} title={t('edit.deleteHint')} aria-label={t('edit.deleteToggle')}><Trash2 size={14} /></button>
        <button class="btn" onclick={addRow} title={t('edit.addRowHint')}><Plus size={14} /> {t('edit.addRow')}</button>
      {/if}
      <div class="toolbar-sep"></div>
      <button class="icon-btn" aria-pressed={valuePanel.open} onclick={() => setValuePanel(!valuePanel.open)} title={t('valuePanel.toggle')} aria-label={t('valuePanel.toggle')}>
        <PanelRight size={14} />
      </button>
      <button class="btn" onclick={() => openQueryTab(tab.connectionId, tableSQL(tab, false) + ';', `${tab.table} query`)} title={t('table.openSql')}>
        <Code size={14} /> SQL
      </button>
      <button class="icon-btn" onclick={refresh} disabled={tab.running} title={t('table.refresh')} aria-label={t('table.refresh')}>
        <RefreshCw size={14} class={tab.running ? 'animate-spin' : ''} />
      </button>
      {#if pending}
        <div class="toolbar-sep"></div>
        <span class="mr-1 flex shrink-0 items-center gap-1.5 text-xs font-medium whitespace-nowrap text-row-dirty-fg" title={t('edit.pending', { count: pending })}><span class="size-2 rounded-full bg-warn"></span>{pending}</span>
        <button class="icon-btn" onclick={showPreview} title={t('edit.preview')} aria-label={t('edit.preview')}><Eye size={14} /></button>
        <button class="icon-btn" onclick={discard} title={t('edit.discard')} aria-label={t('edit.discard')}><Undo2 size={14} /></button>
        <button class={conn?.production ? 'btn-danger' : 'btn-primary'} onclick={() => save()} disabled={saving} title="Ctrl+S">
          {#if saving}<LoaderCircle size={13} class="animate-spin" />{:else}<Save size={13} />{/if}
          {t('edit.save', { count: pending })}
        </button>
      {/if}
    {/if}
  </div>

  {#if tab.view !== 'data'}
    <div class="min-h-0 flex-1"><TableStructure {tab} /></div>
  {:else}
    <form class="flex h-8 shrink-0 items-stretch border-b border-line bg-bg" onsubmit={apply}>
      <label class="flex min-w-0 flex-[3] items-center gap-2 border-r border-line pl-3">
        <span class="font-mono text-2xs font-semibold tracking-wide text-accent">WHERE</span>
        <ConditionInput
          kind="where"
          bind:value={tab.where}
          columns={meta}
          dbType={conn?.db_type ?? 'postgres'}
          quote={(n) => quoteIdent(conn?.db_type ?? 'postgres', n)}
          placeholder="id > 10 AND name LIKE 'a%'"
          onsubmit={() => apply()}
        />
        {#if tab.where}
          <button type="button" class="icon-btn mr-1 size-5" onclick={() => ((tab.where = ''), apply())} aria-label={t('common.clear')}><X size={12} /></button>
        {/if}
      </label>
      <label class="flex min-w-0 flex-[2] items-center gap-2 pl-3">
        <span class="font-mono text-2xs font-semibold tracking-wide text-type">ORDER BY</span>
        <ConditionInput
          kind="order"
          bind:value={tab.orderBy}
          columns={meta}
          dbType={conn?.db_type ?? 'postgres'}
          quote={(n) => quoteIdent(conn?.db_type ?? 'postgres', n)}
          placeholder="created_at DESC"
          onsubmit={() => apply()}
        />
        {#if tab.orderBy}
          <button type="button" class="icon-btn mr-1 size-5" onclick={() => ((tab.orderBy = ''), apply())} aria-label={t('common.clear')}><X size={12} /></button>
        {/if}
      </label>
      <button class="hidden" type="submit">{t('table.apply')}</button>
    </form>

    <div class="min-h-0 flex-1">
      <ResultPanel
        bind:this={panel}
        {tab}
        rowOffset={tab.offset}
        sortColumn={sort.column}
        sortDesc={sort.desc}
        {onsort}
        edit={tab.edit}
        editable={writable}
      >
        {#snippet menuItems()}
          {#if writable}
            <button class="menu-item" onclick={() => panel?.grid()?.setNull()}><Ban size={13} /> {t('edit.setNull')}</button>
            <button class="menu-item" onclick={cloneRows}><Copy size={13} /> {t('edit.clone')}</button>
            <button class="menu-item" onclick={addRow}><Plus size={13} /> {t('edit.addRow')}</button>
            <div class="menu-sep"></div>
            <button class="menu-item" onclick={deleteRows} disabled={!hasKey}>
              <Trash2 size={13} /> {t('edit.deleteToggle')}
            </button>
          {/if}
        {/snippet}
      </ResultPanel>
    </div>

    <div class="flex h-7 shrink-0 items-center gap-3 border-t border-line bg-panel pr-1 pl-3 text-xs text-muted">
      <span class="tabular-nums">
        {#if rowCount}
          {t('table.rowsRange', { from: fmtNumber(tab.offset + 1), to: fmtNumber(tab.offset + rowCount) })}
          {#if tab.hasMore}<span class="ml-1 text-faint">· {t('table.more')}</span>{:else}<span class="ml-1 text-faint">· {t('table.total', { count: fmtNumber(tab.offset + rowCount) })}</span>{/if}
        {:else}
          {t('table.noRows')}
        {/if}
      </span>
      {#if tab.result}<span class="tabular-nums">{formatDuration(tab.result.duration_ms)}{#if conn?.timeout_ms}<span class="ml-1 text-faint">/ {Math.round(conn.timeout_ms / 1000)} {t('table.seconds')}</span>{/if}</span>{/if}
      <span class="min-w-0 flex-1 truncate font-mono text-faint" title={tab.result?.results?.[0]?.statement}>{tab.result?.results?.[0]?.statement ?? ''}</span>
      {#if current?.kind === 'rows' && rowCount}
        <ExportMenu columns={current.columns ?? []} rows={current.rows ?? []} name={tab.table} dbType={conn?.db_type} table={qualified(conn?.db_type ?? 'postgres', tab.schema, tab.table)} />
      {/if}
      <select class="input h-[22px] w-auto py-0 pl-1.5 text-xs" bind:value={tab.limit} onchange={() => apply()} aria-label={t('table.pageSize')}>
        {#each [50, 100, 500, 1000] as n (n)}<option value={n}>{t('table.perPage', { count: n })}</option>{/each}
      </select>
      <span class="flex items-center">
        <button class="icon-btn size-[22px]" onclick={() => page(-1)} disabled={tab.offset === 0 || tab.running} aria-label={t('table.prev')}><ChevronLeft size={14} /></button>
        <span class="min-w-6 text-center tabular-nums">{Math.floor(tab.offset / tab.limit) + 1}</span>
        <button class="icon-btn size-[22px]" onclick={() => page(1)} disabled={!tab.hasMore || tab.running} aria-label={t('table.next')}><ChevronRight size={14} /></button>
      </span>
    </div>
  {/if}
</div>

{#if preview}
  <Modal title={t('edit.previewTitle')} onclose={() => (preview = null)} width="max-w-3xl">
    <p class="mb-2 text-xs text-muted">{t('edit.previewHint')}</p>
    <pre class="max-h-[55vh] overflow-auto rounded-md border border-line bg-sunken p-3 font-mono text-xs whitespace-pre-wrap break-all">{preview.join(';\n\n')};</pre>
    {#snippet footer()}
      <button class="btn" onclick={() => (preview = null)}>{t('common.close')}</button>
      <button class={conn?.production ? 'btn-danger' : 'btn-primary'} onclick={() => save()} disabled={saving}><Save size={13} /> {t('edit.save', { count: pending })}</button>
    {/snippet}
  </Modal>
{/if}
