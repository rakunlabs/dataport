<script lang="ts">
  import { untrack } from 'svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { Upload, Info, LoaderCircle, TriangleAlert, FileUp } from 'lucide-svelte';
  import Modal from '@/lib/components/Modal.svelte';
  import { connection, loadColumns, loadObjects, loadSchemas, refreshSchema, ws } from '@/lib/stores/workspace.svelte';
  import { closeToolDialog } from '@/lib/stores/dialogs.svelte';
  import { confirmed } from '@/lib/stores/tools.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { detectDelimiter, fromCSV, fromJSON, genericTypes, guessType, type ParsedFile } from '@/lib/helper/importFile';
  import type { ImportResult } from '@/lib/api/types';

  let props: { connectionId: string; schema: string; tables: string[]; schemaFixed: boolean } = $props();

  // The dialog is made anew for each request, so its first values are all
  // it needs from the props.
  const { connectionId, schema: initialSchema, tables, schemaFixed } = untrack(() => ({ ...props }));

  const conn = $derived(connection(connectionId));

  let schema = $state(initialSchema);
  let mode = $state<'existing' | 'new'>(tables.length ? 'existing' : 'new');
  let table = $state(tables[0] ?? '');
  let newTable = $state('');

  let fileName = $state('');
  let text = '';
  let format = $state<'csv' | 'json'>('csv');
  let delimiter = $state(',');
  let header = $state(true);
  let parsed = $state<ParsedFile | null>(null);
  let parseError = $state('');

  let emptyNull = $state(true);
  let truncate = $state(false);
  // For an existing table: the file column of each table column, or -1.
  let mapping = $state<Record<string, number>>({});
  // For a new table: name, type and key of each file column.
  let defs = $state<{ name: string; type: string; key: boolean; skip: boolean }[]>([]);

  let running = $state(false);
  let done = $state(0);
  let error = $state('');

  $effect(() => {
    void loadSchemas(connectionId);
  });
  $effect(() => {
    void loadObjects(connectionId, schema);
  });
  $effect(() => {
    if (!schemaFixed && !schema && schemas.length) schema = schemas[0];
  });

  const schemas = $derived(ws.schemas[connectionId]?.items ?? []);
  const tableList = $derived((ws.objects[`${connectionId}|${schema}`]?.items ?? []).filter((o) => o.kind === 'table'));
  const colKey = $derived(`${connectionId}|${schema}|${table}`);
  const tableCols = $derived(ws.columns[colKey]?.items ?? []);

  $effect(() => {
    if (mode === 'existing' && table) void loadColumns(connectionId, schema, table);
  });

  // Map table columns to file columns by name when either changes.
  $effect(() => {
    if (!parsed || mode !== 'existing') return;
    const names = parsed.columns.map((c) => c.toLowerCase());
    mapping = Object.fromEntries(tableCols.map((c) => [c.name, names.indexOf(c.name.toLowerCase())]));
  });

  async function pickFile(e: Event) {
    const f = (e.currentTarget as HTMLInputElement).files?.[0];
    if (!f) return;
    fileName = f.name;
    format = /\.json$/i.test(f.name) ? 'json' : 'csv';
    if (!newTable) newTable = f.name.replace(/\.[^.]+$/, '').replace(/[^\p{L}\p{N}_]+/gu, '_').toLowerCase();
    text = await f.text();
    if (format === 'csv') delimiter = detectDelimiter(text);
    parse();
  }

  function parse() {
    parseError = '';
    try {
      parsed = format === 'json' ? fromJSON(text) : fromCSV(text, delimiter, header);
      defs = parsed.columns.map((c, i) => ({
        name: c.replace(/[^\p{L}\p{N}_]+/gu, '_').toLowerCase() || `column${i + 1}`,
        type: guessType(parsed!.rows.map((r) => r[i])),
        key: false,
        skip: false,
      }));
    } catch (e) {
      parsed = null;
      parseError = errorMessage(e);
    }
  }

  const preview = $derived(parsed?.rows.slice(0, 5) ?? []);
  const target = $derived(mode === 'new' ? newTable.trim() : table);

  // The columns sent and, per row, the values in that order.
  function plan(): { columns: string[]; pick: number[] } {
    if (mode === 'new') {
      const kept = defs.map((d, i) => ({ d, i })).filter((x) => !x.d.skip);
      return { columns: kept.map((x) => x.d.name), pick: kept.map((x) => x.i) };
    }
    const used = tableCols.filter((c) => (mapping[c.name] ?? -1) >= 0);
    return { columns: used.map((c) => c.name), pick: used.map((c) => mapping[c.name]) };
  }

  const ready = $derived(!!parsed && !!target && plan().columns.length > 0);

  // Rows go in batches that stay well inside the server's request size.
  function batches(rows: unknown[][], pick: number[]): unknown[][][] {
    const out: unknown[][][] = [];
    let cur: unknown[][] = [];
    let size = 0;
    const enc = new TextEncoder();
    for (const r of rows) {
      const row = pick.map((i) => r[i] ?? null);
      // Bytes, not characters: the server limits the body in bytes.
      const len = enc.encode(JSON.stringify(row)).length;
      if (cur.length && (cur.length >= 2000 || size + len > 2_500_000)) {
        out.push(cur);
        cur = [];
        size = 0;
      }
      cur.push(row);
      size += len;
    }
    if (cur.length) out.push(cur);
    return out;
  }

  async function start() {
    if (!parsed) return;
    const { columns, pick } = plan();
    const parts = batches(parsed.rows, pick);
    running = true;
    done = 0;
    error = '';
    const path = `/connections/${connectionId}/import`;
    const what = t('import.what', { rows: parsed.rows.length, table: target });
    try {
      const session = { ok: false };
      for (let i = 0; i < Math.max(parts.length, 1); i++) {
        const body: Record<string, unknown> = { schema, table: target, columns, rows: parts[i] ?? [], empty_null: emptyNull };
        if (i === 0) {
          if (mode === 'new') {
            body.create = defs.filter((d) => !d.skip).map((d) => ({ name: d.name, type: d.type, nullable: !d.key, primary_key: d.key, default: '' }));
          }
          body.truncate = mode === 'existing' && truncate;
        }
        // The production prompt is answered once for the whole file.
        const res = await confirmed<ImportResult>(connectionId, path, body, what, session);
        if (!res) {
          running = false;
          return;
        }
        done += res.inserted;
      }
      toast(t('import.done', { n: done, table: target }), 'success');
      void refreshSchema(connectionId, schema);
      closeToolDialog();
    } catch (e) {
      error = errorMessage(e);
      if (done) error = t('import.partial', { n: done }) + ' ' + error;
      void refreshSchema(connectionId, schema);
    } finally {
      running = false;
    }
  }
</script>

<Modal title={t('import.title', { name: conn?.name ?? '' })} onclose={() => !running && closeToolDialog()} width="max-w-3xl">
  <div class="space-y-4">
    <div class="grid grid-cols-2 gap-3">
      {#if !schemaFixed && schemas.length}
        <div>
          <label class="label" for="imp-schema">{t('tools.schema')}</label>
          <select id="imp-schema" class="input" bind:value={schema}>
            {#each schemas as s (s)}<option value={s}>{s}</option>{/each}
          </select>
        </div>
      {/if}
      <div class={!schemaFixed && schemas.length ? '' : 'col-span-2'}>
        <span class="label">{t('import.target')}</span>
        <div class="flex gap-2">
          <label class="flex items-center gap-1.5 text-sm"><input type="radio" value="existing" bind:group={mode} /> {t('import.existing')}</label>
          <label class="flex items-center gap-1.5 text-sm"><input type="radio" value="new" bind:group={mode} /> {t('import.new')}</label>
        </div>
      </div>
    </div>

    {#if mode === 'existing'}
      <div>
        <label class="label" for="imp-table">{t('import.table')}</label>
        <select id="imp-table" class="input" bind:value={table}>
          <option value="" disabled>{t('import.pickTable')}</option>
          {#each tableList as o (o.name)}<option value={o.name}>{o.name}</option>{/each}
        </select>
      </div>
    {:else}
      <div>
        <label class="label" for="imp-new">{t('import.newName')}</label>
        <input id="imp-new" class="input font-mono" bind:value={newTable} />
      </div>
    {/if}

    <div class="flex flex-wrap items-end gap-3">
      <label class="btn-outline cursor-pointer">
        <FileUp size={14} />
        {fileName || t('import.choose')}
        <input type="file" class="hidden" accept=".csv,.tsv,.txt,.json,text/csv,application/json" onchange={pickFile} />
      </label>
      {#if fileName}
        <label class="flex items-center gap-1.5 text-xs">
          {t('import.format')}
          <select class="input h-7 w-20 text-xs" bind:value={format} onchange={parse}>
            <option value="csv">CSV</option>
            <option value="json">JSON</option>
          </select>
        </label>
        {#if format === 'csv'}
          <label class="flex items-center gap-1.5 text-xs">
            {t('import.delimiter')}
            <select class="input h-7 w-24 text-xs" bind:value={delimiter} onchange={parse}>
              <option value=",">, {t('import.comma')}</option>
              <option value=";">; {t('import.semicolon')}</option>
              <option value={'\t'}>Tab</option>
              <option value="|">|</option>
            </select>
          </label>
          <label class="flex items-center gap-1.5 text-xs"><input type="checkbox" bind:checked={header} onchange={parse} /> {t('import.header')}</label>
        {/if}
        {#if parsed}<span class="text-xs text-muted">{t('import.rows', { n: parsed.rows.length, cols: parsed.columns.length })}</span>{/if}
      {/if}
    </div>

    {#if parseError}
      <p class="flex items-start gap-1.5 text-xs text-danger"><TriangleAlert size={13} class="mt-0.5 shrink-0" /> {parseError}</p>
    {/if}

    {#if parsed}
      {#if mode === 'existing' && table}
        <div>
          <span class="label">{t('import.mapping')}</span>
          <div class="max-h-48 overflow-auto rounded-md border border-line">
            {#each tableCols as c (c.name)}
              <div class="flex h-8 items-center gap-2 border-b border-line-soft px-3 text-sm last:border-0">
                <span class="w-48 truncate font-mono text-xs">{c.name}</span>
                <span class="w-28 truncate text-xs text-faint">{c.type}</span>
                <select class="input h-6 flex-1 text-xs" bind:value={mapping[c.name]}>
                  <option value={-1}>{t('import.skip')}</option>
                  {#each parsed.columns as fc, i (i)}<option value={i}>{fc}</option>{/each}
                </select>
              </div>
            {/each}
          </div>
        </div>
      {:else if mode === 'new'}
        <div>
          <span class="label">{t('import.columns')}</span>
          <div class="max-h-48 overflow-auto rounded-md border border-line">
            {#each defs as d, i (i)}
              <div class="flex h-8 items-center gap-2 border-b border-line-soft px-3 text-sm last:border-0 {d.skip ? 'opacity-50' : ''}">
                <span class="w-36 truncate text-xs text-faint" title={parsed.columns[i]}>{parsed.columns[i]}</span>
                <input class="input h-6 flex-1 font-mono text-xs" bind:value={d.name} disabled={d.skip} />
                <select class="input h-6 w-32 text-xs" bind:value={d.type} disabled={d.skip}>
                  {#each genericTypes as gt (gt)}<option value={gt}>{gt}</option>{/each}
                </select>
                <label class="flex items-center gap-1 text-xs" title={t('import.keyHint')}><input type="checkbox" bind:checked={d.key} disabled={d.skip} /> {t('import.key')}</label>
                <label class="flex items-center gap-1 text-xs"><input type="checkbox" bind:checked={d.skip} /> {t('import.skipShort')}</label>
              </div>
            {/each}
          </div>
        </div>
      {/if}

      {#if preview.length}
        <div>
          <span class="label">{t('import.preview')}</span>
          <div class="max-h-40 overflow-auto rounded-md border border-line">
            <table class="w-full text-xs">
              <thead class="sticky top-0 bg-panel"><tr>{#each parsed.columns as c, i (i)}<th class="px-2 py-1 text-left font-medium whitespace-nowrap">{c}</th>{/each}</tr></thead>
              <tbody>
                {#each preview as r, n (n)}
                  <tr class="border-t border-line-soft">{#each r as v, i (i)}<td class="max-w-48 truncate px-2 py-1 font-mono">{v === null ? 'NULL' : typeof v === 'object' ? JSON.stringify(v) : String(v)}</td>{/each}</tr>
                {/each}
              </tbody>
            </table>
          </div>
        </div>
      {/if}

      <div class="flex flex-wrap gap-5">
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={emptyNull} /> {t('import.emptyNull')}</label>
        {#if mode === 'existing'}
          <label class="flex items-center gap-2 text-sm text-danger"><input type="checkbox" bind:checked={truncate} /> {t('import.truncate')}</label>
        {/if}
      </div>
    {/if}

    <p class="flex items-start gap-1.5 text-xs text-muted"><Info size={13} class="mt-0.5 shrink-0" /> {t('import.hint')}</p>

    {#if error}
      <p class="flex items-start gap-1.5 rounded-md border border-danger/30 bg-danger-soft p-2 text-xs text-danger"><TriangleAlert size={13} class="mt-0.5 shrink-0" /> {error}</p>
    {/if}
  </div>

  {#snippet footer()}
    {#if running}<span class="mr-auto flex items-center gap-2 text-xs text-muted"><LoaderCircle size={13} class="animate-spin" /> {t('import.progress', { n: done, total: parsed?.rows.length ?? 0 })}</span>{/if}
    <button class="btn-outline" disabled={running} onclick={closeToolDialog}>{t('common.cancel')}</button>
    <button class="btn-primary" disabled={!ready || running || conn?.read_only} onclick={start}><Upload size={14} /> {t('import.start')}</button>
  {/snippet}
</Modal>
