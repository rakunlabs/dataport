<script lang="ts">
  import { Copy, KeyRound, Link, RefreshCw, TriangleAlert, LoaderCircle, Code } from 'lucide-svelte';
  import SqlEditor from '@/lib/components/SqlEditor.svelte';
  import type { TableStructure } from '@/lib/api/types';
  import { connection, loadStructure, openQueryTab, openTableTab, type Tab } from '@/lib/stores/workspace.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { t } from '@/lib/i18n/index.svelte';

  let { tab }: { tab: Tab } = $props();

  let data = $state<TableStructure | null>(null);
  let error = $state('');
  let loading = $state(false);

  const conn = $derived(connection(tab.connectionId));

  async function load(force = false) {
    loading = true;
    error = '';
    try {
      data = await loadStructure(tab.connectionId, tab.schema, tab.table, force);
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

  // Columns that take part in a foreign key, for the link icon.
  const fkColumns = $derived(new Set(data?.foreign_keys.flatMap((f) => f.columns) ?? []));

  async function copyDDL() {
    try {
      await navigator.clipboard.writeText(data?.ddl ?? '');
      toast(t('grid.copied'), 'success', 1200);
    } catch {
      toast(t('grid.clipboardUnavailable'), 'error');
    }
  }
</script>

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
        <SqlEditor value={data.ddl} dbType={conn?.db_type ?? 'postgres'} readonly />
      {/key}
    </div>
  {:else if data}
    <div class="min-h-0 flex-1 overflow-auto bg-bg px-4 py-3">
      <section>
        <div class="mb-2 flex items-center gap-2">
          <h3 class="text-sm font-semibold">{t('structure.columns')}</h3>
          <span class="text-xs text-muted tabular-nums">{data.columns.length}</span>
          <div class="flex-1"></div>
          <button class="icon-btn" onclick={() => load(true)} aria-label={t('common.refresh')}><RefreshCw size={13} class={loading ? 'animate-spin' : ''} /></button>
        </div>
        <div class="overflow-hidden rounded-md border border-line">
          <table class="table">
            <thead>
              <tr>
                <th class="w-10">#</th>
                <th>{t('structure.name')}</th>
                <th>{t('structure.type')}</th>
                <th class="w-24">{t('structure.nullable')}</th>
                <th>{t('structure.default')}</th>
              </tr>
            </thead>
            <tbody>
              {#each data.columns as c, i (c.name)}
                <tr>
                  <td class="text-xs text-faint tabular-nums">{i + 1}</td>
                  <td class="px-3 py-1.5">
                    <span class="flex items-center gap-1.5 font-mono text-xs">
                      {c.name}
                      {#if c.primary_key}<span title={t('structure.primaryKey')}><KeyRound size={12} class="text-warn" /></span>{/if}
                      {#if fkColumns.has(c.name)}<span title={t('structure.foreignKey')}><Link size={12} class="text-accent" /></span>{/if}
                    </span>
                  </td>
                  <td class="font-mono text-xs text-type">{c.type}</td>
                  <td class="text-xs">{c.nullable ? t('common.yes') : t('common.no')}</td>
                  <td class="max-w-80 truncate font-mono text-xs text-muted" title={c.default}>{c.default || ''}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>

      <section class="mt-6">
        <div class="mb-2 flex items-center gap-2">
          <h3 class="text-sm font-semibold">{t('structure.indexes')}</h3>
          <span class="text-xs text-muted tabular-nums">{data.indexes.length}</span>
        </div>
        {#if data.indexes.length}
          <div class="overflow-hidden rounded-md border border-line">
            <table class="table">
              <thead>
                <tr>
                  <th>{t('structure.name')}</th>
                  <th>{t('structure.columns')}</th>
                  <th class="w-40">{t('structure.kind')}</th>
                  <th class="w-32">{t('structure.method')}</th>
                </tr>
              </thead>
              <tbody>
                {#each data.indexes as ix (ix.name)}
                  <tr>
                    <td class="font-mono text-xs">{ix.name}</td>
                    <td class="font-mono text-xs">{ix.columns.join(', ')}</td>
                    <td class="px-3 py-1.5">
                      {#if ix.primary}<span class="pill-rw"><KeyRound size={10} /> {t('structure.primary')}</span>
                      {:else if ix.unique}<span class="pill bg-accent-soft text-accent">{t('structure.unique')}</span>
                      {:else}<span class="pill-ro">{t('structure.index')}</span>{/if}
                    </td>
                    <td class="font-mono text-xs text-muted">{ix.method ?? ''}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {:else}
          <p class="text-xs text-faint">{t('structure.noIndexes')}</p>
        {/if}
      </section>

      {#if conn?.db_type !== 'clickhouse'}
        <section class="mt-6">
          <div class="mb-2 flex items-center gap-2">
            <h3 class="text-sm font-semibold">{t('structure.foreignKeys')}</h3>
            <span class="text-xs text-muted tabular-nums">{data.foreign_keys.length}</span>
          </div>
          {#if data.foreign_keys.length}
            <div class="overflow-hidden rounded-md border border-line">
              <table class="table">
                <thead>
                  <tr>
                    <th>{t('structure.name')}</th>
                    <th>{t('structure.columns')}</th>
                    <th>{t('structure.references')}</th>
                    <th class="w-32">ON DELETE</th>
                    <th class="w-32">ON UPDATE</th>
                  </tr>
                </thead>
                <tbody>
                  {#each data.foreign_keys as fk (fk.name)}
                    <tr>
                      <td class="font-mono text-xs">{fk.name}</td>
                      <td class="font-mono text-xs">{fk.columns.join(', ')}</td>
                      <td class="font-mono text-xs">
                        <button
                          class="text-accent hover:underline"
                          onclick={() => openTableTab(tab.connectionId, fk.ref_schema || tab.schema, fk.ref_table)}
                          title={t('structure.openTable')}
                        >
                          {fk.ref_schema && fk.ref_schema !== tab.schema ? `${fk.ref_schema}.` : ''}{fk.ref_table}
                        </button>
                        ({fk.ref_columns.join(', ')})
                      </td>
                      <td class="text-xs text-muted">{fk.on_delete ?? ''}</td>
                      <td class="text-xs text-muted">{fk.on_update ?? ''}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {:else}
            <p class="text-xs text-faint">{t('structure.noForeignKeys')}</p>
          {/if}
        </section>
      {/if}
    </div>
  {/if}
</div>
