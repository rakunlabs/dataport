<script lang="ts">
  // The table's structure, editable as in DBX: columns, indexes, foreign keys
  // and triggers in tabs. Changes stay in a draft until saved; saving shows
  // the ALTER statements first and runs them like any other SQL.
  import {
    Copy,
    KeyRound,
    Link,
    RefreshCw,
    TriangleAlert,
    LoaderCircle,
    Code,
    Plus,
    Trash2,
    Undo2,
    Save,
    Search,
    Eye,
    Info,
    ArrowUpRight,
  } from 'lucide-svelte';
  import SqlEditor from '@/lib/components/SqlEditor.svelte';
  import type { TableStructure } from '@/lib/api/types';
  import { connection, loadColumns, loadObjects, loadStructure, openQueryTab, openTableTab, refreshSchema, runTable, ws, type Tab } from '@/lib/stores/workspace.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { reviewSQL } from '@/lib/stores/review.svelte';
  import { confirmed } from '@/lib/stores/tools.svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { alterSQL, draftOf, fkActions, newColumn, supported, typeSuggestions, type Draft, type Loaded } from '@/lib/helper/structureSql';
  import type { ExecResult } from '@/lib/api/types';

  let { tab }: { tab: Tab } = $props();

  let data = $state<TableStructure | null>(null);
  let error = $state('');
  let loading = $state(false);
  let draft = $state<Draft | null>(null);
  let section = $state<'columns' | 'indexes' | 'fks' | 'triggers'>('columns');
  let filter = $state('');
  let saving = $state(false);

  const conn = $derived(connection(tab.connectionId));
  const db = $derived(conn?.db_type ?? 'postgres');
  const can = $derived(supported(db));
  const writable = $derived(!!conn && !conn.read_only);

  const loaded = $derived<Loaded | null>(
    data ? { comment: data.comment ?? '', comments: data.comments ?? {}, columns: data.columns, indexes: data.indexes, fks: data.foreign_keys } : null,
  );

  async function load(force = false) {
    loading = true;
    error = '';
    try {
      data = await loadStructure(tab.connectionId, tab.schema, tab.table, force);
      draft = loaded ? draftOf(loaded) : null;
    } catch (e) {
      error = errorMessage(e);
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    void tab.table;
    void load();
  });

  const plan = $derived(draft && loaded ? alterSQL(db, tab.schema, tab.table, loaded, draft) : { statements: [], notes: [] });
  const dirty = $derived(plan.statements.length > 0);

  const fkColumns = $derived(new Set(data?.foreign_keys.flatMap((f) => f.columns) ?? []));
  const columnNames = $derived(draft?.columns.filter((c) => !c.dropped).map((c) => c.name) ?? []);

  // Changed fields are marked, so it is clear what a save will touch.
  function changedField(i: number, field: 'name' | 'type' | 'nullable' | 'default' | 'primary' | 'comment'): boolean {
    const c = draft?.columns[i];
    if (!c || !c.original || !data) return false;
    const old = data.columns.find((x) => x.name === c.original);
    if (!old) return false;
    switch (field) {
      case 'name':
        return c.name !== old.name;
      case 'type':
        return c.type.trim() !== old.type;
      case 'nullable':
        return c.nullable !== old.nullable;
      case 'default':
        return c.default.trim() !== old.default;
      case 'primary':
        return c.primary !== old.primary_key;
      case 'comment':
        return c.comment !== (data.comments?.[old.name] ?? '');
    }
  }

  const shownColumns = $derived.by(() => {
    const f = filter.trim().toLowerCase();
    return (draft?.columns ?? []).map((c, i) => ({ c, i })).filter(({ c }) => !f || c.name.toLowerCase().includes(f) || c.type.toLowerCase().includes(f));
  });

  function addColumn() {
    if (!draft) return;
    draft.columns.push(newColumn(draft.columns.length + 1));
    setTimeout(() => (document.querySelector('[data-new-col]:last-of-type input') as HTMLInputElement | null)?.select(), 0);
  }

  function removeColumn(i: number) {
    if (!draft) return;
    const c = draft.columns[i];
    if (!c.original) draft.columns.splice(i, 1);
    else c.dropped = !c.dropped;
  }

  function addIndex() {
    draft?.indexes.push({ original: '', name: '', columns: [], unique: false, dropped: false, changed: false });
  }

  function addFk() {
    draft?.fks.push({ original: '', name: '', columns: [], refSchema: tab.schema, refTable: '', refColumns: [], onDelete: '', onUpdate: '', dropped: false, changed: false });
  }

  function toggleList(list: string[], name: string) {
    const i = list.indexOf(name);
    if (i >= 0) list.splice(i, 1);
    else list.push(name);
  }

  // Tables and their columns of the schema, for foreign keys.
  $effect(() => {
    if (section === 'fks') void loadObjects(tab.connectionId, tab.schema);
  });
  const tables = $derived((ws.objects[`${tab.connectionId}|${tab.schema}`]?.items ?? []).filter((o) => o.kind === 'table').map((o) => o.name));
  function refColumnsOf(schema: string, table: string) {
    if (!table) return [];
    return (ws.columns[`${tab.connectionId}|${schema || tab.schema}|${table}`]?.items ?? []).map((c) => c.name);
  }

  // The columns of the referenced tables load as they are chosen.
  $effect(() => {
    if (section !== 'fks' || !draft) return;
    for (const f of draft.fks) if (f.refTable) void loadColumns(tab.connectionId, f.refSchema || tab.schema, f.refTable);
  });

  function discard() {
    if (loaded) draft = draftOf(loaded);
  }

  async function save() {
    if (!dirty) return;
    const sql = plan.statements.join('\n');
    const danger = /\bDROP\b/i.test(sql);
    const r = await reviewSQL({ title: t('structureEdit.saveTitle', { table: tab.table }), connection: conn?.name ?? '', sql, danger });
    if (r.answer === 'edit') openQueryTab(tab.connectionId, r.sql, `${tab.table} ALTER`);
    if (r.answer !== 'run') return;
    saving = true;
    try {
      const res = await confirmed<ExecResult>(tab.connectionId, `/connections/${tab.connectionId}/query`, { sql: r.sql }, r.sql);
      if (!res) return;
      const failed = res.results.find((x) => x.error);
      if (failed) {
        toast(t('structureEdit.failed', { error: failed.error ?? '', statement: failed.statement }), 'error', 8000);
        if (!res.transactional) await load(true);
        return;
      }
      toast(t('structureEdit.saved'), 'success');
      await load(true);
      void refreshSchema(tab.connectionId, tab.schema);
      void loadColumns(tab.connectionId, tab.schema, tab.table, true);
      void runTable(tab.id);
    } catch (e) {
      toast(errorMessage(e), 'error');
    } finally {
      saving = false;
    }
  }

  async function copyDDL() {
    try {
      await navigator.clipboard.writeText(data?.ddl ?? '');
      toast(t('grid.copied'), 'success', 1200);
    } catch {
      toast(t('grid.clipboardUnavailable'), 'error');
    }
  }

  function onkeydown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === 's' && tab.view === 'structure') {
      e.preventDefault();
      void save();
    }
  }

  const inputCls = 'input h-7 px-2 font-mono text-xs';
  const types = $derived(typeSuggestions(db));
</script>

<svelte:window {onkeydown} />

<datalist id="structure-types-{tab.id}">
  {#each types as ty (ty)}<option value={ty}></option>{/each}
</datalist>

<div class="flex h-full min-h-0 flex-col">
  {#if loading && !data}
    <div class="flex h-full items-center justify-center gap-2 text-sm text-muted"><LoaderCircle size={15} class="animate-spin" /> {t('common.loading')}</div>
  {:else if error}
    <div class="m-3 flex items-start gap-2 rounded-md border border-danger/30 bg-danger-soft p-3 text-sm text-danger">
      <TriangleAlert size={16} class="mt-0.5 shrink-0" />
      <span class="flex-1">{error}</span>
      <button class="btn-outline" onclick={() => load(true)}>{t('common.refresh')}</button>
    </div>
  {:else if data && tab.view === 'ddl'}
    <div class="flex h-8 shrink-0 items-center gap-1 border-b border-line bg-bg pr-1 pl-3">
      <span class="text-xs text-muted">{t('structure.ddlHint')}</span>
      <div class="flex-1"></div>
      <button class="btn" onclick={copyDDL}><Copy size={14} /> {t('common.copy')}</button>
      <button class="btn" onclick={() => openQueryTab(tab.connectionId, data?.ddl ?? '', `${tab.table} DDL`)}><Code size={14} /> {t('structure.openInEditor')}</button>
      <button class="icon-btn" onclick={() => load(true)} aria-label={t('common.refresh')}><RefreshCw size={14} class={loading ? 'animate-spin' : ''} /></button>
    </div>
    <div class="min-h-0 flex-1">
      {#key data.ddl}
        <SqlEditor value={data.ddl} dbType={db} readonly />
      {/key}
    </div>
  {:else if data && draft}
    <!-- table comment and the tabs -->
    <div class="flex shrink-0 flex-wrap items-center gap-2 border-b border-line bg-bg px-3 py-2">
      {#if can.comments}
        <label class="flex min-w-72 flex-1 items-center gap-2 text-xs text-muted">
          {t('structureEdit.comment')}
          <input class="input h-7 max-w-md flex-1 text-xs {draft.comment !== (data.comment ?? '') ? 'ring-1 ring-warn' : ''}" bind:value={draft.comment} placeholder={t('structureEdit.commentPlaceholder')} disabled={!writable} />
        </label>
      {:else}
        <div class="flex-1"></div>
      {/if}
      {#if dirty}
        <span class="flex items-center gap-1.5 text-xs font-medium text-row-dirty-fg"><span class="size-2 rounded-full bg-warn"></span>{t('structureEdit.pending', { count: plan.statements.length })}</span>
        <button class="icon-btn" onclick={discard} title={t('edit.discard')} aria-label={t('edit.discard')}><Undo2 size={14} /></button>
        <button class="btn" onclick={() => openQueryTab(tab.connectionId, plan.statements.join('\n'), `${tab.table} ALTER`)} title={t('structure.openInEditor')}><Eye size={14} /> SQL</button>
        <button class={conn?.production ? 'btn-danger' : 'btn-primary'} onclick={save} disabled={saving} title="Ctrl+S">
          {#if saving}<LoaderCircle size={13} class="animate-spin" />{:else}<Save size={13} />{/if}
          {t('structureEdit.save')}
        </button>
      {/if}
      <button class="icon-btn" onclick={() => (dirty && !confirm(t('structureEdit.reloadConfirm')) ? null : load(true))} title={t('common.refresh')} aria-label={t('common.refresh')}>
        <RefreshCw size={14} class={loading ? 'animate-spin' : ''} />
      </button>
    </div>

    <div class="flex h-9 shrink-0 items-center gap-2 border-b border-line bg-panel px-3">
      <div class="segmented" role="group">
        {#each [
          { id: 'columns', label: t('structure.columns'), n: draft.columns.filter((c) => !c.dropped).length },
          { id: 'indexes', label: t('structure.indexes'), n: data.indexes.length },
          { id: 'fks', label: t('structure.foreignKeys'), n: data.foreign_keys.length },
          { id: 'triggers', label: t('structureEdit.triggers'), n: data.triggers?.length ?? 0 },
        ] as s (s.id)}
          <button aria-pressed={section === s.id} onclick={() => (section = s.id as typeof section)}>
            {s.label} <span class="text-faint tabular-nums">{s.n}</span>
          </button>
        {/each}
      </div>
      <div class="flex-1"></div>
      {#if section === 'columns'}
        <div class="relative">
          <Search size={13} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-faint" />
          <input class="input h-7 w-48 pl-7 text-xs" placeholder={t('structureEdit.searchColumn')} bind:value={filter} />
        </div>
        {#if writable && can.columns}<button class="btn-primary h-7" onclick={addColumn}><Plus size={13} /> {t('structureEdit.addColumn')}</button>{/if}
      {:else if section === 'indexes' && writable && can.indexes}
        <button class="btn-primary h-7" onclick={addIndex}><Plus size={13} /> {t('structureEdit.addIndex')}</button>
      {:else if section === 'fks' && writable && can.fks}
        <button class="btn-primary h-7" onclick={addFk}><Plus size={13} /> {t('structureEdit.addFk')}</button>
      {/if}
    </div>

    {#if plan.notes.length}
      <div class="shrink-0 border-b border-line bg-warn/10 px-3 py-1.5 text-xs text-warn">
        {#each plan.notes as n (n)}<p class="flex items-start gap-1.5"><Info size={13} class="mt-0.5 shrink-0" /> {t('structureEdit.note_' + n)}</p>{/each}
      </div>
    {/if}

    <div class="min-h-0 flex-1 overflow-auto bg-bg">
      {#if section === 'columns'}
        <table class="w-full border-collapse text-sm">
          <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2">
            <tr class="[&>th]:h-8 [&>th]:border-b [&>th]:border-line [&>th]:px-2 [&>th]:text-left [&>th]:font-medium">
              <th class="w-10">#</th>
              <th class="min-w-44">{t('structure.name')}</th>
              <th class="min-w-52">{t('structure.type')}</th>
              <th class="w-20">{t('structure.nullable')}</th>
              <th class="w-16 text-center">PK</th>
              <th class="min-w-40">{t('structure.default')}</th>
              {#if can.comments}<th class="min-w-48">{t('structureEdit.comment')}</th>{/if}
              <th class="w-12"></th>
            </tr>
          </thead>
          <tbody>
            {#each shownColumns as { c, i } (i)}
              {@const ro = !writable || !can.columns || c.dropped}
              {@const roAlter = ro || (!!c.original && !can.alterColumn)}
              <tr class="border-b border-line-soft {c.dropped ? 'bg-danger-soft opacity-60' : !c.original ? 'bg-ok/5' : ''} [&>td]:px-2 [&>td]:py-1" data-new-col={!c.original ? '' : undefined}>
                <td class="text-xs text-faint tabular-nums">
                  <span class="flex items-center gap-1">
                    {i + 1}
                    {#if c.primary}<KeyRound size={11} class="text-warn" />{/if}
                    {#if fkColumns.has(c.original)}<Link size={11} class="text-accent" />{/if}
                  </span>
                </td>
                <td><input class="{inputCls} w-full {changedField(i, 'name') ? 'ring-1 ring-warn' : ''} {c.dropped ? 'line-through' : ''}" bind:value={c.name} disabled={ro} /></td>
                <td>
                  <input class="{inputCls} w-full text-type {changedField(i, 'type') ? 'ring-1 ring-warn' : ''}" list="structure-types-{tab.id}" bind:value={c.type} disabled={roAlter} />
                </td>
                <td>
                  <label class="flex items-center gap-1.5 text-xs {changedField(i, 'nullable') ? 'text-warn' : ''}">
                    <input type="checkbox" bind:checked={c.nullable} disabled={roAlter || c.primary} />
                    {c.nullable ? t('common.yes') : t('common.no')}
                  </label>
                </td>
                <td class="text-center">
                  <input
                    type="checkbox"
                    class={changedField(i, 'primary') ? 'outline outline-warn' : ''}
                    bind:checked={c.primary}
                    onchange={() => c.primary && (c.nullable = false)}
                    disabled={ro || db === 'sqlite' || db === 'clickhouse'}
                  />
                </td>
                <td><input class="{inputCls} w-full {changedField(i, 'default') ? 'ring-1 ring-warn' : ''}" bind:value={c.default} disabled={roAlter} placeholder="—" /></td>
                {#if can.comments}
                  <td><input class="input h-7 w-full px-2 text-xs {changedField(i, 'comment') ? 'ring-1 ring-warn' : ''}" bind:value={c.comment} disabled={ro && !c.dropped ? true : c.dropped} /></td>
                {/if}
                <td class="text-right">
                  {#if writable && can.columns}
                    <button class="icon-btn size-6 {c.dropped ? '' : 'hover:text-danger'}" onclick={() => removeColumn(i)} title={c.dropped ? t('structureEdit.restore') : t('structureEdit.dropColumn')} aria-label={c.dropped ? t('structureEdit.restore') : t('structureEdit.dropColumn')}>
                      {#if c.dropped}<Undo2 size={13} />{:else}<Trash2 size={13} />{/if}
                    </button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
        {#if !can.alterColumn && can.columns}<p class="px-3 py-2 text-xs text-muted">{t('structureEdit.sqliteHint')}</p>{/if}
      {:else if section === 'indexes'}
        <table class="w-full border-collapse text-sm">
          <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2">
            <tr class="[&>th]:h-8 [&>th]:border-b [&>th]:border-line [&>th]:px-2 [&>th]:text-left [&>th]:font-medium">
              <th class="min-w-48">{t('structure.name')}</th>
              <th>{t('structure.columns')}</th>
              <th class="w-24">{t('structure.unique')}</th>
              <th class="w-28">{t('structure.method')}</th>
              <th class="w-12"></th>
            </tr>
          </thead>
          <tbody>
            {#each data.indexes.filter((x) => x.primary) as ix (ix.name)}
              <tr class="border-b border-line-soft text-xs [&>td]:px-2 [&>td]:py-1.5">
                <td class="font-mono">{ix.name}</td>
                <td class="font-mono">{ix.columns.join(', ')}</td>
                <td><span class="pill-rw"><KeyRound size={10} /> {t('structure.primary')}</span></td>
                <td class="font-mono text-muted">{ix.method ?? ''}</td>
                <td></td>
              </tr>
            {/each}
            {#each draft.indexes as ix, n (n)}
              {@const ro = !writable || !can.indexes || ix.dropped}
              {@const method = data.indexes.find((x) => x.name === ix.original)?.method ?? ''}
              <tr class="border-b border-line-soft {ix.dropped ? 'bg-danger-soft opacity-60' : !ix.original ? 'bg-ok/5' : ''} [&>td]:px-2 [&>td]:py-1 align-top">
                <td><input class="{inputCls} w-full {ix.dropped ? 'line-through' : ''}" bind:value={ix.name} oninput={() => (ix.changed = true)} disabled={ro} placeholder={t('structureEdit.autoName')} /></td>
                <td>
                  <div class="flex flex-wrap gap-1 py-0.5">
                    {#each columnNames as name (name)}
                      <button
                        class="rounded border px-1.5 py-0.5 font-mono text-xs {ix.columns.includes(name) ? 'border-accent bg-accent/10 text-accent' : 'border-line text-muted hover:bg-hover'}"
                        disabled={ro}
                        onclick={() => {
                          toggleList(ix.columns, name);
                          ix.changed = true;
                        }}>{name}{#if ix.columns.includes(name)}<sup class="ml-0.5">{ix.columns.indexOf(name) + 1}</sup>{/if}</button
                      >
                    {/each}
                  </div>
                </td>
                <td><input type="checkbox" bind:checked={ix.unique} onchange={() => (ix.changed = true)} disabled={ro} /></td>
                <td class="font-mono text-xs text-muted">{method}</td>
                <td class="text-right">
                  {#if writable && can.indexes}
                    <button
                      class="icon-btn size-6"
                      onclick={() => (ix.original ? (ix.dropped = !ix.dropped) : draft!.indexes.splice(n, 1))}
                      title={ix.dropped ? t('structureEdit.restore') : t('structureEdit.drop')}
                      aria-label={ix.dropped ? t('structureEdit.restore') : t('structureEdit.drop')}
                    >
                      {#if ix.dropped}<Undo2 size={13} />{:else}<Trash2 size={13} />{/if}
                    </button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
        {#if !data.indexes.length && !draft.indexes.length}<p class="px-3 py-3 text-xs text-faint">{t('structure.noIndexes')}</p>{/if}
        {#if draft.indexes.some((x) => x.original && x.changed && !x.dropped)}<p class="px-3 py-2 text-xs text-muted">{t('structureEdit.indexRecreate')}</p>{/if}
      {:else if section === 'fks'}
        <table class="w-full border-collapse text-sm">
          <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2">
            <tr class="[&>th]:h-8 [&>th]:border-b [&>th]:border-line [&>th]:px-2 [&>th]:text-left [&>th]:font-medium">
              <th class="min-w-40">{t('structure.name')}</th>
              <th>{t('structure.columns')}</th>
              <th class="min-w-40">{t('structure.references')}</th>
              <th>{t('structureEdit.refColumns')}</th>
              <th class="w-32">ON DELETE</th>
              <th class="w-32">ON UPDATE</th>
              <th class="w-16"></th>
            </tr>
          </thead>
          <tbody>
            {#each draft.fks as fk, n (n)}
              {@const ro = !writable || !can.fks || fk.dropped}
              {@const refCols = refColumnsOf(fk.refSchema, fk.refTable)}
              <tr class="border-b border-line-soft {fk.dropped ? 'bg-danger-soft opacity-60' : !fk.original ? 'bg-ok/5' : ''} [&>td]:px-2 [&>td]:py-1 align-top">
                <td><input class="{inputCls} w-full {fk.dropped ? 'line-through' : ''}" bind:value={fk.name} oninput={() => (fk.changed = true)} disabled={ro} placeholder={t('structureEdit.autoName')} /></td>
                <td>
                  <div class="flex flex-wrap gap-1 py-0.5">
                    {#each columnNames as name (name)}
                      <button
                        class="rounded border px-1.5 py-0.5 font-mono text-xs {fk.columns.includes(name) ? 'border-accent bg-accent/10 text-accent' : 'border-line text-muted hover:bg-hover'}"
                        disabled={ro}
                        onclick={() => {
                          toggleList(fk.columns, name);
                          fk.changed = true;
                        }}>{name}</button
                      >
                    {/each}
                  </div>
                </td>
                <td>
                  <select class="input h-7 w-full font-mono text-xs" bind:value={fk.refTable} onchange={() => ((fk.changed = true), (fk.refColumns = []))} disabled={ro}>
                    <option value="" disabled>{t('structureEdit.pickTable')}</option>
                    {#if fk.refTable && !tables.includes(fk.refTable)}<option value={fk.refTable}>{fk.refSchema ? fk.refSchema + '.' : ''}{fk.refTable}</option>{/if}
                    {#each tables as tb (tb)}<option value={tb}>{tb}</option>{/each}
                  </select>
                </td>
                <td>
                  <div class="flex flex-wrap gap-1 py-0.5">
                    {#each refCols as name (name)}
                      <button
                        class="rounded border px-1.5 py-0.5 font-mono text-xs {fk.refColumns.includes(name) ? 'border-accent bg-accent/10 text-accent' : 'border-line text-muted hover:bg-hover'}"
                        disabled={ro}
                        onclick={() => {
                          toggleList(fk.refColumns, name);
                          fk.changed = true;
                        }}>{name}</button
                      >
                    {:else}
                      <span class="font-mono text-xs text-muted">{fk.refColumns.join(', ')}</span>
                    {/each}
                  </div>
                </td>
                <td>
                  <select class="input h-7 w-full text-xs" bind:value={fk.onDelete} onchange={() => (fk.changed = true)} disabled={ro}>
                    {#each fkActions as a (a)}<option value={a}>{a || '—'}</option>{/each}
                  </select>
                </td>
                <td>
                  <select class="input h-7 w-full text-xs" bind:value={fk.onUpdate} onchange={() => (fk.changed = true)} disabled={ro || db === 'oracle'}>
                    {#each fkActions as a (a)}<option value={a}>{a || '—'}</option>{/each}
                  </select>
                </td>
                <td class="text-right whitespace-nowrap">
                  {#if fk.refTable}
                    <button class="icon-btn size-6" onclick={() => openTableTab(tab.connectionId, fk.refSchema || tab.schema, fk.refTable)} title={t('structure.openTable')} aria-label={t('structure.openTable')}><ArrowUpRight size={13} /></button>
                  {/if}
                  {#if writable && can.fks}
                    <button
                      class="icon-btn size-6"
                      onclick={() => (fk.original ? (fk.dropped = !fk.dropped) : draft!.fks.splice(n, 1))}
                      title={fk.dropped ? t('structureEdit.restore') : t('structureEdit.drop')}
                      aria-label={fk.dropped ? t('structureEdit.restore') : t('structureEdit.drop')}
                    >
                      {#if fk.dropped}<Undo2 size={13} />{:else}<Trash2 size={13} />{/if}
                    </button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
        {#if !draft.fks.length}<p class="px-3 py-3 text-xs text-faint">{t('structure.noForeignKeys')}</p>{/if}
      {:else}
        {#if data.triggers?.length}
          <div class="divide-y divide-line-soft">
            {#each data.triggers as tr (tr.name)}
              <div class="px-3 py-2.5">
                <div class="mb-1.5 flex flex-wrap items-center gap-2">
                  <span class="font-mono text-sm font-medium">{tr.name}</span>
                  {#if tr.timing}<span class="pill-ro">{tr.timing}</span>{/if}
                  {#if tr.event}<span class="pill bg-accent-soft text-accent">{tr.event}</span>{/if}
                  {#if !tr.enabled}<span class="pill bg-warn/15 text-warn">{t('structureEdit.disabled')}</span>{/if}
                  <div class="flex-1"></div>
                  <button class="btn h-6 text-xs" onclick={() => openQueryTab(tab.connectionId, tr.definition, tr.name)}><Code size={12} /> {t('structure.openInEditor')}</button>
                </div>
                {#if tr.definition}<pre class="max-h-48 overflow-auto rounded-md border border-line bg-sunken p-2 font-mono text-xs whitespace-pre-wrap">{tr.definition}</pre>{/if}
              </div>
            {/each}
          </div>
        {:else}
          <p class="px-3 py-3 text-xs text-faint">{t('structureEdit.noTriggers')}</p>
        {/if}
      {/if}
    </div>
  {/if}
</div>
