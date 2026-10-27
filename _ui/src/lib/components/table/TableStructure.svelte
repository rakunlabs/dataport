<script lang="ts">
  import { copyText } from '@/lib/helper/clipboard';
  // The table's structure, editable as in DBX: columns, indexes, foreign keys
  // and triggers in tabs. Changes stay in a draft until saved; saving shows
  // the ALTER statements first and runs them like any other SQL.
  import { Copy, RefreshCw, TriangleAlert, LoaderCircle, Code, Plus, Undo2, Save, Search, Eye, Info } from 'lucide-svelte';
  import SqlEditor from '@/lib/components/editor/SqlEditor.svelte';
  import StructureColumns from '@/lib/components/table/StructureColumns.svelte';
  import StructureIndexes from '@/lib/components/table/StructureIndexes.svelte';
  import StructureForeignKeys from '@/lib/components/table/StructureForeignKeys.svelte';
  import type { TableStructure } from '@/lib/api/types';
  import { connection, loadColumns, loadStructure, openQueryTab, refreshSchema, runTable, type Tab } from '@/lib/stores/workspace.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { reviewSQL } from '@/lib/stores/review.svelte';
  import { confirmed } from '@/lib/stores/tools.svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { alterSQL, draftOf, newColumn, supported, typeSuggestions, type Draft, type Loaded } from '@/lib/helper/structureSql';
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

  function addColumn() {
    if (!draft) return;
    draft.columns.push(newColumn(draft.columns.length + 1));
    setTimeout(() => (document.querySelector('[data-new-col]:last-of-type input') as HTMLInputElement | null)?.select(), 0);
  }

  function addIndex() {
    draft?.indexes.push({ original: '', name: '', columns: [], unique: false, dropped: false, changed: false });
  }

  function addFk() {
    draft?.fks.push({ original: '', name: '', columns: [], refSchema: tab.schema, refTable: '', refColumns: [], onDelete: '', onUpdate: '', dropped: false, changed: false });
  }

  function discard() {
    if (loaded) draft = draftOf(loaded);
  }

  async function save() {
    if (!dirty || saving) return;
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

  function copyDDL() {
    void copyText(data?.ddl ?? '');
  }

  function onkeydown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === 's' && tab.view === 'structure') {
      e.preventDefault();
      void save();
    }
  }
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
        <StructureColumns {tab} {data} bind:draft {db} {writable} {filter} />
      {:else if section === 'indexes'}
        <StructureIndexes {data} bind:draft {db} {writable} />
      {:else if section === 'fks'}
        <StructureForeignKeys {tab} bind:draft {db} {writable} />
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
