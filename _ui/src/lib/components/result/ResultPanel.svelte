<script lang="ts">
  import { CircleCheck, CircleX, TriangleAlert, Info, Copy, ChevronRight, Rows3, X, PanelRight, RefreshCw, Square, LoaderCircle, Ban, Plus, Trash2, Eye, Undo2, Save, Pencil, KeyRound, Palette, Calculator, TableProperties, Table2, Eraser } from 'lucide-svelte';
  import type { Snippet } from 'svelte';
  import { formatRows, sqlFormats, type CopyFormat } from '@/lib/helper/copy';
  import { copyText } from '@/lib/helper/clipboard';
  import { cancel, cancelPending, cancelResult, connection, explain, loadColumns, totalOf, qualified, reloadResult, runTable, tableKey, ws } from '@/lib/stores/workspace.svelte';
  import RunTimer from '@/lib/components/result/RunTimer.svelte';
  import PlanView from '@/lib/components/result/PlanView.svelte';
  import TotalCount from '@/lib/components/result/TotalCount.svelte';
  import DataGrid from '@/lib/components/result/DataGrid.svelte';
  import CellDialog from '@/lib/components/result/CellDialog.svelte';
  import ValuePanel from '@/lib/components/result/ValuePanel.svelte';
  import CalcPanel from '@/lib/components/result/CalcPanel.svelte';
  import RecordView from '@/lib/components/result/RecordView.svelte';
  import RowColorDialog from '@/lib/components/result/RowColorDialog.svelte';
  import { paint as paintRow, palette, valueText } from '@/lib/helper/rowColors';
  import { addColorRule, clearColumnRules, colorRules } from '@/lib/stores/rowColors.svelte';
  import { saveValuePanel, setValuePanel, valuePanel } from '@/lib/stores/valuePanel.svelte';
  import { formatDuration } from '@/lib/helper/format';
  import ExportMenu from '@/lib/components/result/ExportMenu.svelte';
  import type { Tab } from '@/lib/stores/workspace.svelte';
  import type { ResultSet } from '@/lib/api/types';
  import { EditState, commitEdits, keepEdits, previewEdits } from '@/lib/stores/edits.svelte';
  import { defaultSchemas, sourceTable } from '@/lib/helper/sourceTable';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import VirtualKeyDialog from '@/lib/components/dialogs/VirtualKeyDialog.svelte';
  import { setVirtualKey, virtualKey } from '@/lib/stores/virtualKeys.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { api, qs } from '@/lib/api/client';
  import { withCredentials } from '@/lib/stores/credentials.svelte';
  import { fmtNumber, t } from '@/lib/i18n/index.svelte';

  let {
    tab,
    sortColumn = '',
    sortDesc = false,
    onsort,
    rowOffset = 0,
    edit: tableEdit,
    editable: tableEditable = false,
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
    if (/json/i.test(type) || (typeof v === 'string' && v.length > 2 && /^\s*[[{]/.test(v) && isJson(v))) setValuePanel(true, 'value');
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

  // ////////////////////////////////////////////
  // record view, calculator and row colours

  const record = $derived(tab.layout === 'record' && current?.kind === 'rows');
  let recordRow = $state(0);

  // toggleRecord switches between the grid and the record view, which
  // opens on the given row or the selected one.
  export function toggleRecord(row?: number) {
    menu = null;
    if (tab.layout === 'record') {
      tab.layout = 'grid';
      return;
    }
    recordRow = row ?? focused?.row ?? 0;
    tab.layout = 'record';
  }

  export function manageColors() {
    colorDialog = { column: '' };
  }

  export function toggleCalc() {
    setValuePanel(!(valuePanel.open && valuePanel.view === 'calc'), 'calc');
  }

  // The cells the calculator reads: the grid's selected rows and columns.
  let range = $state<{ rows: number[]; cols: number[] }>({ rows: [], cols: [] });
  const calcRows = $derived.by(() => {
    void edit?.count;
    void current;
    if (!gridRef) return [];
    if (record) return [gridRef.rowValues(recordRow)];
    return range.rows.map((r) => gridRef!.rowValues(r));
  });
  const calcCols = $derived(record ? (current?.columns ?? []).map((_, i) => i) : range.cols);

  // Rules are kept per table, or per query when its table is unknown.
  const colorScope = $derived.by(() => {
    if (tab.kind === 'table') return `${tab.connectionId}|${tab.schema}|${tab.table}`;
    if (resolved && resolved.statement === current?.statement) return `${tab.connectionId}|${resolved.schema}|${resolved.table}`;
    return `${tab.connectionId}|sql|${(current?.statement ?? '').replace(/\s+/g, ' ').trim().slice(0, 400)}`;
  });
  const rules = $derived(colorRules(colorScope));
  const columnNames = $derived((current?.columns ?? []).map((c) => c.name));
  const paint = $derived.by(() => {
    if (!rules.some((r) => r.enabled)) return undefined;
    const rs = rules;
    const names = columnNames;
    void edit?.count;
    return (row: number) => paintRow(rs, names, (c) => gridRef?.cellAt(row, c));
  });
  let colorDialog = $state<{ column: string } | null>(null);

  function colorBy(color: string) {
    if (!menu) return;
    const col = current?.columns?.[menu.col]?.name;
    if (!col) return;
    const v = valueText(gridRef?.cellAt(menu.row, menu.col));
    addColorRule(colorScope, v === null ? { column: col, op: 'null', value: '', color } : { column: col, op: '=', value: v, color });
    menu = null;
  }

  // ////////////////////////////////////////////
  // editing a query result

  // A query result that is one table's rows, read whole by a plain SELECT,
  // can be edited like the table: the source table is found from the SQL
  // and must have a primary key, or a virtual key, the result includes.
  const queryConn = $derived(connection(tab.connectionId));
  const canWrite = $derived(tab.kind === 'query' && !!queryConn && !queryConn.read_only && queryConn.editable !== false);
  const source = $derived.by(() => {
    if (!canWrite || current?.kind !== 'rows' || current.pending || current.error) return null;
    const found = sourceTable(current.statement, queryConn?.db_type ?? '');
    if (!found) return null;
    const schemas = found.schema ? [found.schema] : [...defaultSchemas(queryConn?.db_type ?? ''), ...(ws.schemas[tab.connectionId]?.items ?? [])];
    return { ...found, candidates: [...new Set(schemas)] };
  });

  // The schema of the source table: the one named, or the one the database
  // reads it from, as its search path resolves it. A driver that cannot
  // tell gets one only when a single schema has such a table, so edits never
  // go to a table of the same name elsewhere.
  let resolved = $state<{ statement: string; schema: string; table: string } | null>(null);
  // What was last looked up, so schemas loading later in the tree do not
  // drop and look up the table again while it is being edited.
  let resolving = '';
  $effect(() => {
    const s = source;
    const statement = current?.statement ?? '';
    const lookup = s ? `${tab.connectionId}|${statement}` : '';
    if (lookup === resolving) return;
    resolving = lookup;
    resolved = null;
    if (!s) return;
    const conn = tab.connectionId;
    const has = async (schema: string) => {
      await loadColumns(conn, schema, s.table);
      return !!ws.columns[`${conn}|${schema}|${s.table}`]?.items.length;
    };
    void (async () => {
      let schema: string | null = null;
      if (s.schema) {
        schema = (await has(s.schema)) ? s.schema : null;
      } else {
        const r = await withCredentials(conn, () => api.get<{ schema: string; known: boolean }>(`/connections/${conn}/resolve-table${qs({ table: s.table })}`)).catch(() => null);
        if (r?.known) {
          schema = r.schema && (await has(r.schema)) ? r.schema : null;
        } else if (r) {
          const found: string[] = [];
          for (const c of s.candidates) if (await has(c)) found.push(c);
          schema = found.length === 1 ? found[0] : null;
        }
      }
      if (resolving === lookup && schema !== null) resolved = { statement, schema, table: s.table };
    })();
  });

  const queryMeta = $derived(resolved ? (ws.columns[`${tab.connectionId}|${resolved.schema}|${resolved.table}`]?.items ?? []) : []);
  const queryPrimary = $derived(queryMeta.filter((c) => c.primary_key).map((c) => c.name));
  // Without a primary key, a virtual key the user chose finds the rows.
  const queryVirtual = $derived(
    resolved && !queryPrimary.length ? virtualKey(tab.connectionId, resolved.schema, resolved.table, queryMeta.map((c) => c.name)) : [],
  );
  const queryKeys = $derived(queryPrimary.length ? queryPrimary : queryVirtual);
  let keyDialog = $state(false);
  const resultColumns = $derived(current?.columns?.map((c) => c.name) ?? []);
  // Every column must be a column of the table, once, and the key must be
  // among them, so each row can be found again.
  const queryEditable = $derived(
    !!resolved &&
      resolved.statement === current?.statement &&
      queryKeys.length > 0 &&
      new Set(resultColumns).size === resultColumns.length &&
      resultColumns.every((c) => queryMeta.some((m) => m.name === c)) &&
      queryKeys.every((k) => resultColumns.includes(k)) &&
      !tab.txOpen,
  );
  // Why a result read from one table cannot be edited, for a hint.
  const queryNoEdit = $derived.by(() => {
    if (!resolved || resolved.statement !== current?.statement || queryEditable) return '';
    if (tab.txOpen) return t('edit.queryTx');
    if (!queryKeys.length) return t('edit.noKeyChoose');
    return t('edit.queryNeedsKey');
  });

  $effect(() => {
    if (queryEditable && current && !current.edit) current.edit = new EditState();
  });

  const edit = $derived(tab.kind === 'query' ? current?.edit : tableEdit);
  const editable = $derived(tab.kind === 'query' ? queryEditable : tableEditable);
  const queryPending = $derived(tab.kind === 'query' ? (current?.edit?.count ?? 0) : 0);
  let queryPreview = $state<string[] | null>(null);
  let querySaving = $state(false);

  function queryEdits() {
    return current?.edit?.edits(resultColumns, queryMeta, current.rows ?? [], queryVirtual) ?? [];
  }

  function queryAddRow() {
    if (!current?.edit) return;
    current.edit.addRow(resultColumns.length);
    gridRef?.focusCell((current.rows?.length ?? 0) + current.edit.inserted.length - 1, 0);
  }

  function queryCloneRows() {
    const sel = gridRef?.selection();
    if (!sel || !current?.rows || !current.edit) return;
    current.edit.clone(current.rows, sel, resultColumns.length, new Set(queryPrimary.map((k) => resultColumns.indexOf(k))));
  }

  function queryDeleteRows() {
    const sel = gridRef?.selection();
    if (sel) current?.edit?.remove(sel);
  }

  async function queryShowPreview() {
    if (!resolved) return;
    try {
      queryPreview = (await previewEdits(tab.connectionId, resolved.schema, resolved.table, queryEdits(), queryVirtual)).statements;
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  async function saveQueryEdits() {
    if (querySaving || !queryPending || !resolved || !current) return;
    // The user may move to another result while saving: the saved one is
    // the result that was looked at, found again by itself.
    const saved = current;
    const target = { connectionId: tab.connectionId, connectionName: queryConn?.name ?? '', schema: resolved.schema, table: resolved.table, keyColumns: queryVirtual };
    querySaving = true;
    try {
      if (!(await commitEdits(target, queryEdits(), queryPreview))) return;
      queryPreview = null;
      // The result is read again, so it shows what the table holds now.
      const at = tab.result?.results.indexOf(saved) ?? -1;
      if (at >= 0) void reloadResult(tab.id, at);
      else saved.edit?.clear();
    } finally {
      querySaving = false;
    }
  }

  function queryDiscard() {
    current?.edit?.clear();
    queryPreview = null;
  }

  function onQueryKey(e: KeyboardEvent) {
    if (tab.kind !== 'query' || !queryEditable) return;
    const mod = e.metaKey || e.ctrlKey;
    if ((e.target as HTMLElement)?.closest?.('.cm-editor')) return;
    if (mod && e.key === 's' && queryPending) {
      e.preventDefault();
      e.stopPropagation();
      void saveQueryEdits();
    } else if (mod && e.key === 'Enter' && e.shiftKey) {
      e.preventDefault();
      queryAddRow();
    } else if (mod && (e.key === 'Delete' || e.key === 'Backspace') && (e.target as HTMLElement)?.getAttribute('role') === 'grid') {
      e.preventDefault();
      queryDeleteRows();
    }
  }

  // Results of several runs can sit side by side; each knows its own run.
  const runs = $derived(new Set(results.map((r) => r.run)).size);
  const committed = (r: ResultSet) => r.committed ?? tab.result?.committed ?? false;
  const runOf = (r: ResultSet) => results.filter((x) => x.run === r.run);

  // A result whose run is still going (Ctrl+\) shows that run instead.
  const pending = $derived(current?.pending);
  const anyPending = $derived(results.some((r) => r.pending));

  function timer(i: number) {
    const p = results[i]?.pending;
    return p ? { startedAt: p.startedAt, cancelling: p.cancelling, oncancel: () => cancelResult(tab.id, i) } : undefined;
  }

  // Closing results gives the editor the whole tab until the next run;
  // runs still going behind them stop.
  function closeAll() {
    if (!keepEdits(tab.result?.results)) return;
    cancelPending(tab);
    tab.result = null;
    tab.error = '';
    tab.activeResult = 0;
  }

  function closeResult(i: number) {
    if (!tab.result) return;
    if (tab.result.results.length <= 1) return closeAll();
    if (!keepEdits([tab.result.results[i]])) return;
    if (tab.result.results[i]?.pending) void cancelResult(tab.id, i);
    tab.result.results.splice(i, 1);
    if (tab.activeResult > i || tab.activeResult >= tab.result.results.length) tab.activeResult = Math.max(0, tab.activeResult - 1);
  }

  function label(i: number) {
    const r = results[i];
    if (r.pending) return t('result.result', { n: i + 1 });
    if (r.error) return t('result.error');
    if (r.kind === 'rows') return t('result.result', { n: i + 1 });
    return t('result.statement', { n: i + 1 });
  }

  // A result's tab names the start of its statement, so runs of different
  // queries can be told apart.
  function hint(r: ResultSet) {
    return r.statement.replace(/\s+/g, ' ').trim();
  }

  let menu = $state<{ x: number; y: number; row: number; col: number } | null>(null);
  let copyOpen = $state(false);
  let menuEl: HTMLDivElement | undefined = $state();
  let copyRow: HTMLDivElement | undefined = $state();
  let copyEl: HTMLDivElement | undefined = $state();
  // Where the menu and its "Copy as" submenu show, kept inside the window.
  let menuPos = $state({ left: 0, top: 0 });
  let copyPos = $state({ left: 0, top: 0 });

  // The menu opens where the click was; one that does not fit below or to
  // the right moves up or left just enough.
  $effect(() => {
    if (!menu || !menuEl) return;
    const w = menuEl.offsetWidth;
    const h = menuEl.offsetHeight;
    menuPos = { left: Math.max(4, Math.min(menu.x, window.innerWidth - w - 4)), top: Math.max(4, Math.min(menu.y, window.innerHeight - h - 4)) };
  });

  // The submenu opens beside its row: to the right, or the left when it does
  // not fit; it moves up when it would pass the bottom of the window.
  $effect(() => {
    if (!copyOpen || !copyRow || !copyEl) return;
    void menuPos;
    const r = copyRow.getBoundingClientRect();
    const w = copyEl.offsetWidth;
    const h = copyEl.offsetHeight;
    const left = r.right + w + 2 > window.innerWidth - 4 ? r.left - w - 2 : r.right + 2;
    copyPos = { left: Math.max(4, left), top: Math.max(4, Math.min(r.top - 4, window.innerHeight - h - 4)) };
  });
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
  const keys = $derived.by(() => {
    if (tab.kind !== 'table') return queryKeys;
    const meta = ws.columns[tableKey(tab)]?.items ?? [];
    const pk = meta.filter((c) => c.primary_key).map((c) => c.name);
    return pk.length ? pk : virtualKey(tab.connectionId, tab.schema, tab.table, meta.map((c) => c.name));
  });

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

  function oncontext(e: MouseEvent, row: number, col: number) {
    clearTimeout(copyCloseTimer);
    copyOpen = false;
    menu = { x: e.clientX, y: e.clientY, row, col };
    menuPos = { left: e.clientX, top: e.clientY };
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
        table: tab.kind === 'table' ? qualified(dbType, tab.schema, tab.table) : resolved?.statement === current?.statement ? qualified(dbType, resolved?.schema ?? '', resolved?.table ?? '') : undefined,
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

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="flex h-full min-h-0 flex-col" onkeydown={onQueryKey}>
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
            {#if r.pending}<LoaderCircle size={12} class="animate-spin text-accent" />{:else if r.error}<CircleX size={12} class="text-danger" />{:else if r.kind === 'rows'}<Rows3 size={12} class="text-accent" />{:else}<CircleCheck size={12} class="text-ok" />{/if}
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

  {#if tab.kind === 'query' && (queryEditable || queryNoEdit) && current?.kind === 'rows'}
    <div class="flex h-[30px] shrink-0 items-center gap-1 border-b border-line bg-panel px-2 text-xs">
      {#if queryEditable}
        <span class="flex min-w-0 items-center gap-1.5 text-muted" title={t('edit.queryHint')}>
          <Pencil size={12} class="shrink-0 text-accent" />
          <span class="truncate">{t('edit.queryEditing', { table: (resolved?.schema ? resolved.schema + '.' : '') + resolved?.table })}</span>
        </span>
        {#if queryVirtual.length}
          <button class="pill-own ml-1 max-w-56 hover:brightness-110" onclick={() => (keyDialog = true)} title={t('edit.virtualKeyActive', { columns: queryVirtual.join(', ') })}>
            <KeyRound size={10} class="shrink-0" /> <span class="truncate">{t('edit.virtualKey')}: {queryVirtual.join(', ')}</span>
          </button>
        {/if}
        <div class="flex-1"></div>
        <button class="icon-btn size-6" onclick={() => gridRef?.setNull()} title={t('edit.setNull')} aria-label={t('edit.setNull')}><Ban size={13} /></button>
        <button class="icon-btn size-6" onclick={queryCloneRows} title={t('edit.cloneHint')} aria-label={t('edit.clone')}><Copy size={13} /></button>
        <button class="icon-btn size-6 hover:text-danger" onclick={queryDeleteRows} title={t('edit.deleteHint')} aria-label={t('edit.deleteToggle')}><Trash2 size={13} /></button>
        <button class="btn h-6" onclick={queryAddRow} title={t('edit.addRowHint')}><Plus size={13} /> {t('edit.addRow')}</button>
        {#if queryPending}
          <div class="toolbar-sep"></div>
          <span class="mr-1 flex shrink-0 items-center gap-1.5 font-medium whitespace-nowrap text-row-dirty-fg" title={t('edit.pending', { count: queryPending })}><span class="size-2 rounded-full bg-warn"></span>{queryPending}</span>
          <button class="icon-btn size-6" onclick={queryShowPreview} title={t('edit.preview')} aria-label={t('edit.preview')}><Eye size={13} /></button>
          <button class="icon-btn size-6" onclick={queryDiscard} title={t('edit.discard')} aria-label={t('edit.discard')}><Undo2 size={13} /></button>
          <button class="{queryConn?.production ? 'btn-danger' : 'btn-primary'} h-6" onclick={() => saveQueryEdits()} disabled={querySaving} title="Ctrl+S">
            {#if querySaving}<LoaderCircle size={12} class="animate-spin" />{:else}<Save size={12} />{/if}
            {t('edit.save', { count: queryPending })}
          </button>
        {/if}
      {:else}
        <span class="flex min-w-0 items-center gap-1.5 text-faint" title={queryNoEdit}>
          <KeyRound size={12} class="shrink-0" />
          <span class="truncate">{t('edit.queryReadOnly', { table: (resolved?.schema ? resolved.schema + '.' : '') + resolved?.table })} · {queryNoEdit}</span>
        </span>
        {#if !queryPrimary.length && !tab.txOpen && queryMeta.length}
          <div class="flex-1"></div>
          <button class="btn h-6 shrink-0" onclick={() => (keyDialog = true)}><KeyRound size={12} /> {queryVirtual.length ? t('edit.virtualKey') : t('edit.chooseKey')}</button>
        {/if}
      {/if}
    </div>
  {/if}

  <div class="relative min-h-0 flex-1 bg-bg">
    {#if pending}
      <div class="flex h-full flex-col items-center justify-center gap-4 px-6">
        <RunTimer run={timer(tab.activeResult)} large />
        <pre class="max-h-24 max-w-full overflow-hidden font-mono text-xs text-ellipsis whitespace-pre-wrap text-faint">{current?.statement}</pre>
      </div>
    {:else if tab.running && !current}
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
    {:else if current.plan}
      <PlanView result={current} onreexplain={() => explain(tab.id, current.plan!.source, current.plan!.options)} />
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
          {#if record}
            <div class="absolute inset-0 z-[2] bg-bg">
              <RecordView
                columns={current.columns ?? []}
                rows={current.rows ?? []}
                bind:row={recordRow}
                {rowOffset}
                {edit}
                {editable}
                {paint}
                oncontext={(e, row, col) => {
                  gridRef?.focusCell(row, col, false);
                  oncontext(e, row, col);
                }}
                onselect={(row, col) => {
                  focused = { row, col };
                  // Toolbar actions such as delete act on the grid's selection.
                  gridRef?.focusCell(row, col, false);
                }}
              />
            </div>
          {/if}
          <div class="h-full {record ? 'invisible' : ''}">
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
            onrange={(rows, cols) => (range = { rows, cols })}
            {paint}
          />
          </div>
          {#if !current.rows?.length && !(edit?.inserted.length)}
            <div class="pointer-events-none absolute inset-x-0 top-14 text-center text-sm text-muted">
              {tab.kind === 'table' ? (tab.where.trim() ? t('result.noMatch') : tab.offset ? t('result.noMorePages') : t('result.tableEmpty')) : t('result.noRows')}
            </div>
          {/if}
        </div>
        {#if valuePanel.open}
          <div class="relative z-10 -mr-[3px] w-[5px] shrink-0 cursor-col-resize hover:bg-accent/50" onpointerdown={resizePanel} role="separator" aria-orientation="vertical"></div>
          <div class="flex min-h-0 shrink-0 flex-col" style="width:{valuePanel.width}px">
            <div class="flex h-[26px] shrink-0 items-stretch gap-px border-b border-l border-line bg-panel px-1 text-xs" role="tablist">
              {#each [{ id: 'value', label: t('valuePanel.tab'), icon: PanelRight }, { id: 'calc', label: t('calc.title'), icon: Calculator }] as v (v.id)}
                {@const on = valuePanel.view === v.id}
                <button
                  class="relative flex items-center gap-1.5 px-2.5 {on ? 'text-fg' : 'text-muted hover:text-fg'}"
                  role="tab"
                  aria-selected={on}
                  onclick={() => setValuePanel(true, v.id as 'value' | 'calc')}
                >
                  <v.icon size={12} />
                  {v.label}
                  {#if on}<span class="absolute inset-x-1 bottom-0 h-0.5 rounded-full bg-accent"></span>{/if}
                </button>
              {/each}
            </div>
            <div class="min-h-0 flex-1">
              {#if valuePanel.view === 'calc'}
                <CalcPanel columns={current.columns ?? []} rows={calcRows} cols={calcCols} onclose={() => setValuePanel(false)} />
              {:else if focused && current.columns?.[focused.col]}
                {@const col = current.columns[focused.col]}
                <ValuePanel
                  column={col.name}
                  type={col.type}
                  value={focusedValue}
                  editable={editable && !!gridRef?.canEditCell(focused.row)}
                  onchange={(v) => focused && gridRef?.setCell(focused.row, focused.col, v)}
                  onclose={() => setValuePanel(false)}
                />
              {:else}
                <p class="border-l border-line p-3 text-xs text-faint">{t('valuePanel.pick')}</p>
              {/if}
            </div>
          </div>
        {/if}
      </div>
    {/if}

    {#if (tab.running && current) || pending}
      <div class="absolute inset-x-0 top-0 h-0.5 overflow-hidden bg-accent/20"><span class="block h-full w-1/3 animate-[progress_1.1s_ease-in-out_infinite] bg-accent"></span></div>
    {/if}
  </div>

  {#if tab.kind === 'query'}
    <div class="flex h-7 shrink-0 items-center gap-3 border-t border-line bg-panel pr-1 pl-3 text-xs text-muted">
      {#if pending}
        <RunTimer run={timer(tab.activeResult)} />
        <button class="btn h-[22px] px-1.5 text-xs" onclick={() => cancelResult(tab.id, tab.activeResult)} disabled={pending.cancelling}>
          <Square size={9} fill="currentColor" class="text-danger" /> {t('query.stop')}
        </button>
        <span class="text-line">|</span>
      {:else if tab.running && current}
        <RunTimer {tab} />
        <button class="btn h-[22px] px-1.5 text-xs" onclick={() => cancel(tab.id)} disabled={tab.cancelling || !tab.queryId}>
          <Square size={9} fill="currentColor" class="text-danger" /> {t('query.stop')}
        </button>
        <span class="text-line">|</span>
      {/if}
      {#if current?.kind === 'rows'}
        <span class="tabular-nums">
          {t('result.rows', { count: fmtNumber(current.rows?.length ?? 0) })}
          {#if current.truncated && totalOf(tab, current)?.count === undefined}<span class="ml-1 text-warn">· {t('result.truncated')}</span>{/if}
        </span>
        <TotalCount {tab} index={tab.activeResult} />
      {/if}
      {#if current && !pending}<span class="tabular-nums">{formatDuration(current.duration_ms)}</span>{/if}
      {#if current}<span class="min-w-0 flex-1 truncate font-mono text-faint" title={current.statement}>{current.statement}</span>{:else}<span class="flex-1"></span>{/if}

      {#if current?.kind === 'rows' && current.rows?.length}
        <ExportMenu columns={current.columns ?? []} rows={current.rows} name={tab.table || tab.title} {dbType} />
      {/if}
      {#if current?.kind === 'rows'}
        <button class="icon-btn size-[22px]" aria-pressed={record} onclick={() => toggleRecord()} title={t('record.toggle')} aria-label={t('record.toggle')}><TableProperties size={13} /></button>
        <button class="icon-btn size-[22px]" aria-pressed={!!rules.length} onclick={() => (colorDialog = { column: '' })} title={t('rowColors.manage')} aria-label={t('rowColors.manage')}><Palette size={13} /></button>
        <button class="icon-btn size-[22px]" aria-pressed={valuePanel.open && valuePanel.view === 'calc'} onclick={toggleCalc} title={t('calc.toggle')} aria-label={t('calc.toggle')}><Calculator size={13} /></button>
        <button class="icon-btn size-[22px]" aria-pressed={valuePanel.open && valuePanel.view === 'value'} onclick={() => setValuePanel(!(valuePanel.open && valuePanel.view === 'value'), 'value')} title={t('valuePanel.toggle')} aria-label={t('valuePanel.toggle')}><PanelRight size={13} /></button>
      {/if}
      {#if (tab.result || tab.error) && !tab.running && !anyPending}
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
  <div bind:this={menuEl} class="popover fixed z-50 w-60" style="left:{menuPos.left}px; top:{menuPos.top}px" role="menu" tabindex="-1" onclick={(e) => e.stopPropagation()}>
    <button class="menu-item" onclick={copyCell}><Copy size={13} /> {t('grid.copyCell')}</button>
    <div bind:this={copyRow} class="relative" role="menu" tabindex="-1" onmouseenter={openCopy} onmouseleave={closeCopy}>
      <button class="menu-item" onclick={() => (copyOpen = !copyOpen)}>
        <Copy size={13} class="opacity-0" /> {t('grid.copyAs')} <span class="ml-auto text-xs opacity-60">{t('grid.selectedRows', { count: gridRef?.selectedValues().rows.length ?? 0 })}</span>
        <ChevronRight size={13} class="opacity-60" />
      </button>
      {#if copyOpen}
        <div bind:this={copyEl} class="fixed z-50" style="left:{copyPos.left}px; top:{copyPos.top}px">
          <div class="popover w-44">
          {#each formats as f (f.id)}
            {#if f.sep}<div class="menu-sep"></div>{/if}
            <button
              class="menu-item"
              onclick={() => copyAs(f.id)}
              title={sqlFormats.includes(f.id) && tab.kind !== 'table' && !queryEditable ? t('grid.noTableHint') : undefined}
            >
              {f.id === 'tsv-headers' ? t('grid.tsvHeaders') : f.label}
            </button>
          {/each}
          </div>
        </div>
      {/if}
    </div>
    <div class="menu-sep"></div>
    {#if current?.columns?.[menu.col]}
      {@const colName = current.columns[menu.col].name}
      {@const v = valueText(gridRef?.cellAt(menu.row, menu.col))}
      <div class="px-2.5 pt-1 pb-0.5 text-2xs text-faint">
        <span class="flex items-center gap-1.5"><Palette size={11} /> <span class="truncate">{t('rowColors.colorWhere', { column: colName, value: v === null ? 'NULL' : v.length > 24 ? v.slice(0, 24) + '…' : v })}</span></span>
      </div>
      <div class="flex flex-wrap gap-1 px-2.5 pt-0.5 pb-1.5">
        {#each palette as c (c)}
          <button class="size-4 rounded-sm border border-black/20 hover:scale-125" style="background:{c}" onclick={() => colorBy(c)} title={c} aria-label={c}></button>
        {/each}
      </div>
      {#if rules.some((r) => r.column === colName)}
        <button class="menu-item" onclick={() => (clearColumnRules(colorScope, colName), (menu = null))}><Eraser size={13} /> {t('rowColors.clearColumn', { column: colName })}</button>
      {/if}
      <button class="menu-item" onclick={() => ((colorDialog = { column: colName }), (menu = null))}><Palette size={13} class="opacity-0" /> {t('rowColors.manage')}…</button>
    {/if}
    <button class="menu-item" onclick={() => toggleRecord(menu?.row)}>
      {#if record}<Table2 size={13} /> {t('record.backToGrid')}{:else}<TableProperties size={13} /> {t('record.toggle')}{/if}
    </button>
    <button class="menu-item" onclick={() => ((menu = null), setValuePanel(true, 'calc'))}><Calculator size={13} /> {t('calc.open')}</button>
    {#if menuItems}
      <div class="menu-sep"></div>
      {@render menuItems()}
    {:else if tab.kind === 'query' && queryEditable}
      <div class="menu-sep"></div>
      <button class="menu-item" onclick={() => ((menu = null), gridRef?.setNull())}><Ban size={13} /> {t('edit.setNull')}</button>
      <button class="menu-item" onclick={() => ((menu = null), queryCloneRows())}><Copy size={13} /> {t('edit.clone')}</button>
      <button class="menu-item" onclick={() => ((menu = null), queryAddRow())}><Plus size={13} /> {t('edit.addRow')}</button>
      <div class="menu-sep"></div>
      <button class="menu-item" onclick={() => ((menu = null), queryDeleteRows())}><Trash2 size={13} /> {t('edit.deleteToggle')}</button>
    {/if}
  </div>
{/if}

{#if keyDialog && resolved}
  {@const r = resolved}
  <VirtualKeyDialog
    connectionId={tab.connectionId}
    schema={r.schema}
    table={r.table}
    columns={queryMeta}
    initial={queryVirtual}
    onsave={(cols) => {
      setVirtualKey(tab.connectionId, r.schema, r.table, cols);
      keyDialog = false;
      queryPreview = null;
    }}
    onclose={() => (keyDialog = false)}
  />
{/if}

{#if queryPreview}
  <Modal title={t('edit.previewTitle')} onclose={() => (queryPreview = null)} width="max-w-3xl">
    <p class="mb-2 text-xs text-muted">{t('edit.previewHint')}</p>
    <pre class="max-h-[55vh] overflow-auto rounded-md border border-line bg-sunken p-3 font-mono text-xs whitespace-pre-wrap break-all">{queryPreview.join(';\n\n')};</pre>
    {#snippet footer()}
      <button class="btn" onclick={() => (queryPreview = null)}>{t('common.close')}</button>
      <button class={queryConn?.production ? 'btn-danger' : 'btn-primary'} onclick={() => saveQueryEdits()} disabled={querySaving}><Save size={13} /> {t('edit.save', { count: queryPending })}</button>
    {/snippet}
  </Modal>
{/if}

{#if colorDialog}
  <RowColorDialog scope={colorScope} columns={columnNames} column={colorDialog.column} onclose={() => (colorDialog = null)} />
{/if}

