<script lang="ts">
  import type { Snippet } from 'svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { TriangleAlert, KeyRound, Link, ArrowRight, Info } from 'lucide-svelte';
  import type { Anomaly, AnomalyKind, DocColumn, DocTable, ForeignKey, Routine } from '@/lib/api/types';
  import { kindLabel, rows, td, th } from '@/lib/components/docs/docFormat';
  import type { Section } from '@/lib/components/docs/DocsTab.svelte';

  let {
    section,
    keys,
    needle,
    tables,
    columns,
    relations,
    routines,
    anomalyGroups,
    tableLink,
    anomalyItem,
  }: {
    section: Exclude<Section, 'overview'>;
    keys: boolean;
    needle: string;
    tables: DocTable[];
    columns: { tb: DocTable; c: DocColumn }[];
    relations: { tb: DocTable; fk: ForeignKey }[];
    routines: Routine[];
    anomalyGroups: { kind: AnomalyKind; items: Anomaly[] }[];
    tableLink: Snippet<[string]>;
    anomalyItem: Snippet<[Anomaly, boolean]>;
  } = $props();
</script>

{#if section === 'tables'}
  <table class="w-full border-collapse text-sm">
    <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2">
      <tr class={th}>
        <th>{t('docs.col.table')}</th><th>{t('docs.col.kind')}</th><th class="text-right!" title={t('docs.rowsHint')}>{t('docs.col.rows')}</th>
        <th class="text-right!">{t('docs.col.columns')}</th><th class="text-right!">{t('docs.col.parents')}</th><th class="text-right!">{t('docs.col.children')}</th>
        <th class="text-right!">{t('docs.col.indexes')}</th><th>{t('docs.col.comment')}</th>
      </tr>
    </thead>
    <tbody>
      {#each tables as tb (tb.name)}
        <tr class={td}>
          <td>{@render tableLink(tb.name)}</td>
          <td class="text-xs text-muted">{kindLabel(tb.kind)}</td>
          <td class="text-right text-xs tabular-nums">{rows(tb.rows)}</td>
          <td class="text-right text-xs tabular-nums">{tb.columns.length}</td>
          <td class="text-right text-xs tabular-nums">{tb.foreign_keys.length || ''}</td>
          <td class="text-right text-xs tabular-nums">{tb.referenced_by.length || ''}</td>
          <td class="text-right text-xs tabular-nums">{tb.indexes.length || ''}</td>
          <td class="max-w-96 truncate text-xs text-fg-2" title={tb.comment}>{tb.comment ?? ''}</td>
        </tr>
      {:else}
        <tr><td colspan="8" class="p-4 text-sm text-muted">{t('docs.noMatch')}</td></tr>
      {/each}
    </tbody>
  </table>
{:else if section === 'columns'}
  <table class="w-full border-collapse text-sm">
    <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2">
      <tr class={th}><th>{t('docs.col.table')}</th><th>{t('docs.col.column')}</th><th>{t('docs.col.type')}</th><th>{t('docs.col.nullable')}</th><th>{t('docs.col.default')}</th><th>{t('docs.col.keys')}</th><th>{t('docs.col.comment')}</th></tr>
    </thead>
    <tbody>
      {#each columns.slice(0, 2000) as { tb, c } (tb.name + '.' + c.name)}
        {@const isFk = tb.foreign_keys.some((f) => f.columns.includes(c.name))}
        <tr class={td}>
          <td class="text-xs">{@render tableLink(tb.name)}</td>
          <td>{c.name}</td>
          <td class="font-mono text-xs text-type">{c.type}</td>
          <td class="text-xs">{c.nullable ? t('common.yes') : t('common.no')}</td>
          <td class="max-w-56 truncate font-mono text-xs text-fg-2" title={c.default}>{c.default}</td>
          <td><span class="flex items-center gap-0.5">{#if c.primary_key}<KeyRound size={12} class="text-warn" />{/if}{#if isFk}<Link size={12} class="text-accent" />{/if}</span></td>
          <td class="max-w-96 truncate text-xs text-fg-2" title={c.comment}>{c.comment ?? ''}</td>
        </tr>
      {:else}
        <tr><td colspan="7" class="p-4 text-sm text-muted">{t('docs.noMatch')}</td></tr>
      {/each}
    </tbody>
  </table>
{:else if section === 'relationships'}
  {#if !relations.length}
    <p class="p-5 text-sm text-muted">{needle ? t('docs.noMatch') : t('docs.noRelationships')}</p>
  {:else}
    <table class="w-full border-collapse text-sm">
      <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2">
        <tr class={th}><th>{t('docs.col.from')}</th><th></th><th>{t('docs.col.to')}</th><th>{t('docs.col.name')}</th><th>{t('docs.col.onDelete')}</th></tr>
      </thead>
      <tbody>
        {#each relations as { tb, fk } (tb.name + '.' + fk.name)}
          <tr class={td}>
            <td>{@render tableLink(tb.name)} <span class="font-mono text-xs text-fg-2">({fk.columns.join(', ')})</span></td>
            <td class="w-6"><ArrowRight size={12} class="text-faint" /></td>
            <td>{@render tableLink(fk.ref_table)} <span class="font-mono text-xs text-fg-2">({fk.ref_columns.join(', ')})</span></td>
            <td class="text-xs text-muted">{fk.name}</td>
            <td class="text-xs">{fk.on_delete ?? ''}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
{:else if section === 'anomalies'}
  <div class="mx-auto max-w-5xl space-y-5 p-5">
    {#if !keys}<p class="flex items-start gap-2 text-sm text-muted"><Info size={14} class="mt-0.5 shrink-0" /> {t('docs.noKeys')}</p>{/if}
    {#each anomalyGroups as g (g.kind)}
      <section>
        <h3 class="flex items-center gap-2 text-sm font-semibold"><TriangleAlert size={14} class="text-warn" /> {t(`docs.anomaly.${g.kind}.title`)} <span class="font-normal text-faint">{g.items.length}</span></h3>
        <p class="mt-0.5 mb-1.5 text-xs text-muted">{t(`docs.anomaly.${g.kind}.hint`)}</p>
        <ul class="divide-y divide-line-soft border-y border-line-soft">
          {#each g.items as a, i (i)}{@render anomalyItem(a, true)}{/each}
        </ul>
      </section>
    {:else}
      <p class="text-sm text-muted">{needle ? t('docs.noMatch') : t('docs.noAnomalies')}</p>
    {/each}
  </div>
{:else if section === 'routines'}
  {#if !routines.length}
    <p class="p-5 text-sm text-muted">{needle ? t('docs.noMatch') : t('docs.noRoutines')}</p>
  {:else}
    <table class="w-full border-collapse text-sm">
      <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2"><tr class={th}><th>{t('docs.col.routine')}</th><th>{t('docs.col.kind')}</th></tr></thead>
      <tbody>
        {#each routines as r, i (i)}
          <tr class={td}><td><span class="font-medium">{r.name}</span><span class="font-mono text-xs text-muted">{r.signature ?? ''}</span></td><td class="text-xs text-muted">{kindLabel(r.kind)}</td></tr>
        {/each}
      </tbody>
    </table>
  {/if}
{/if}
