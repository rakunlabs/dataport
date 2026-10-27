<script lang="ts">
  import type { Snippet } from 'svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { TriangleAlert, KeyRound, Link, ArrowLeft, ArrowRight, Table2, ListTree, Network } from 'lucide-svelte';
  import type { Anomaly, DocTable } from '@/lib/api/types';
  import { openDiagram, openTableTab, type Tab } from '@/lib/stores/workspace.svelte';
  import { kindLabel, rows, td, th } from '@/lib/components/docs/docFormat';

  let {
    tab,
    current,
    anomalies,
    back,
    tableLink,
    anomalyItem,
  }: {
    tab: Tab;
    current: DocTable;
    anomalies: Anomaly[];
    back: () => void;
    tableLink: Snippet<[string]>;
    anomalyItem: Snippet<[Anomaly, boolean]>;
  } = $props();

  const fks = $derived.by(() => {
    const m = new Map<string, { table: string; column: string }>();
    for (const fk of current.foreign_keys) fk.columns.forEach((c, i) => m.set(c, { table: fk.ref_table, column: fk.ref_columns[i] ?? '' }));
    return m;
  });
</script>

<div class="mx-auto max-w-6xl space-y-6 p-5">
  <div>
    <button class="mb-2 flex items-center gap-1 text-xs text-muted hover:text-fg" onclick={back}><ArrowLeft size={12} /> {t('docs.back')}</button>
    <div class="flex flex-wrap items-center gap-3">
      <h2 class="text-base font-semibold">{current.name}</h2>
      <span class="chip">{kindLabel(current.kind)}</span>
      {#if current.rows !== undefined}<span class="text-xs text-muted" title={t('docs.rowsHint')}>~{rows(current.rows)} {t('docs.col.rows').toLowerCase()}</span>{/if}
      <div class="flex-1"></div>
      <button class="btn" onclick={() => openTableTab(tab.connectionId, tab.schema, current.name, 'data')}><Table2 size={14} /> {t('docs.openData')}</button>
      <button class="btn" onclick={() => openTableTab(tab.connectionId, tab.schema, current.name, 'structure')}><ListTree size={14} /> {t('docs.openStructure')}</button>
      <button class="btn" onclick={() => openDiagram(tab.connectionId, tab.schema)}><Network size={14} /> {t('docs.showDiagram')}</button>
    </div>
    {#if current.comment}<p class="mt-2 text-sm whitespace-pre-line text-fg-2">{current.comment}</p>{/if}
  </div>

  {#if anomalies.length}
    <section class="rounded-md border border-warn/30 bg-warn-soft p-3">
      {#each anomalies as a, i (i)}
        <div class="flex items-start gap-2 text-sm">
          <TriangleAlert size={13} class="mt-1 shrink-0 text-warn" />
          <div class="min-w-0 flex-1">
            <span class="font-medium">{t(`docs.anomaly.${a.kind}.title`)}</span>
            <ul>{@render anomalyItem(a, false)}</ul>
          </div>
        </div>
      {/each}
    </section>
  {/if}

  <section>
    <h3 class="mb-2 text-sm font-semibold">{t('docs.sections.columns')} <span class="font-normal text-faint">{current.columns.length}</span></h3>
    <table class="w-full border-collapse text-sm">
      <thead class="text-xs text-fg-2"><tr class={th}><th class="w-8"></th><th>{t('docs.col.column')}</th><th>{t('docs.col.type')}</th><th>{t('docs.col.nullable')}</th><th>{t('docs.col.default')}</th><th>{t('docs.col.parents')}</th><th>{t('docs.col.comment')}</th></tr></thead>
      <tbody>
        {#each current.columns as c (c.name)}
          {@const ref = fks.get(c.name)}
          <tr class={td}>
            <td>
              <span class="flex items-center gap-0.5">
                {#if c.primary_key}<KeyRound size={12} class="text-warn" />{/if}
                {#if ref}<Link size={12} class="text-accent" />{/if}
              </span>
            </td>
            <td class="font-medium {c.primary_key ? '' : 'font-normal'}">{c.name}</td>
            <td class="font-mono text-xs text-type">{c.type}{#if c.extra}<span class="ml-1 text-muted">{c.extra}</span>{/if}</td>
            <td class="text-xs">{c.nullable ? t('common.yes') : t('common.no')}</td>
            <td class="max-w-56 truncate font-mono text-xs text-fg-2" title={c.default}>{c.default}</td>
            <td class="text-xs">{#if ref}{@render tableLink(ref.table)}<span class="text-muted">.{ref.column}</span>{/if}</td>
            <td class="text-xs whitespace-pre-line text-fg-2">{c.comment ?? ''}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </section>

  <div class="grid gap-6 lg:grid-cols-2">
    <section>
      <h3 class="mb-2 text-sm font-semibold">{t('docs.col.parents')}</h3>
      {#if current.foreign_keys.length}
        <ul class="space-y-1 text-sm">
          {#each current.foreign_keys as fk (fk.name)}
            <li class="flex flex-wrap items-center gap-1.5">
              <span class="font-mono text-xs">({fk.columns.join(', ')})</span>
              <ArrowRight size={12} class="text-faint" />
              {@render tableLink(fk.ref_table)}<span class="font-mono text-xs text-fg-2">({fk.ref_columns.join(', ')})</span>
              {#if fk.on_delete && fk.on_delete !== 'NO ACTION'}<span class="chip">ON DELETE {fk.on_delete}</span>{/if}
            </li>
          {/each}
        </ul>
      {:else}<p class="text-sm text-faint">{t('docs.none')}</p>{/if}
    </section>
    <section>
      <h3 class="mb-2 text-sm font-semibold">{t('docs.col.children')}</h3>
      {#if current.referenced_by.length}
        <ul class="space-y-1 text-sm">
          {#each current.referenced_by as r (r.table + r.name)}
            <li class="flex flex-wrap items-center gap-1.5">
              {@render tableLink(r.table)}<span class="font-mono text-xs text-fg-2">({r.columns.join(', ')})</span>
              <ArrowRight size={12} class="text-faint" />
              <span class="font-mono text-xs">({r.ref_columns.join(', ')})</span>
            </li>
          {/each}
        </ul>
      {:else}<p class="text-sm text-faint">{t('docs.none')}</p>{/if}
    </section>
  </div>

  {#if current.kind !== 'view'}
    <section>
      <h3 class="mb-2 text-sm font-semibold">{t('docs.col.indexes')}</h3>
      {#if current.indexes.length}
        <table class="w-full border-collapse text-sm">
          <thead class="text-xs text-fg-2"><tr class={th}><th>{t('docs.col.name')}</th><th>{t('docs.col.columns')}</th><th>{t('docs.col.unique')}</th></tr></thead>
          <tbody>
            {#each current.indexes as ix (ix.name)}
              <tr class={td}>
                <td class="flex items-center gap-1.5">{#if ix.primary}<KeyRound size={12} class="text-warn" />{/if}{ix.name}</td>
                <td class="font-mono text-xs">{ix.columns.join(', ')}</td>
                <td class="text-xs">{ix.unique ? t('common.yes') : ''}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}<p class="text-sm text-faint">{t('docs.none')}</p>{/if}
    </section>
  {/if}

  {#if current.definition}
    <section>
      <h3 class="mb-2 text-sm font-semibold">{t('docs.definition')}</h3>
      <pre class="overflow-auto rounded-md border border-line bg-sunken p-3 font-mono text-xs">{current.definition}</pre>
    </section>
  {/if}
</div>
