<script lang="ts">
  import type { Snippet } from 'svelte';
  import { t, fmtNumber } from '@/lib/i18n/index.svelte';
  import { TriangleAlert, Info } from 'lucide-svelte';
  import type { Anomaly, AnomalyKind, SchemaDoc } from '@/lib/api/types';
  import { rows } from '@/lib/components/docs/docFormat';
  import type { Section } from '@/lib/components/docs/DocsTab.svelte';

  let {
    data,
    stats,
    anomalyGroups,
    go,
    tableLink,
  }: {
    data: SchemaDoc;
    stats: Record<string, number | undefined>;
    anomalyGroups: { kind: AnomalyKind; items: Anomaly[] }[];
    go: (s: Section) => void;
    tableLink: Snippet<[string]>;
  } = $props();

  const largest = $derived(
    [...(data?.tables ?? [])].filter((x) => x.rows !== undefined && x.rows > 0).sort((a, b) => b.rows! - a.rows!).slice(0, 8),
  );
  const mostReferenced = $derived(
    [...(data?.tables ?? [])].filter((x) => x.referenced_by.length).sort((a, b) => b.referenced_by.length - a.referenced_by.length).slice(0, 8),
  );
</script>

<div class="mx-auto max-w-6xl space-y-6 p-5">
  {#if !data.keys}<p class="flex items-start gap-2 text-sm text-muted"><Info size={14} class="mt-0.5 shrink-0" /> {t('docs.noKeys')}</p>{/if}
  <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
    {#each Object.entries(stats) as [k, v] (k)}
      {#if v !== undefined}
        <button
          class="rounded-md border border-line bg-raised p-3 text-left hover:border-accent/50"
          onclick={() => go(k === 'anomalies' ? 'anomalies' : k === 'routines' ? 'routines' : k === 'columns' ? 'columns' : k === 'foreignKeys' ? 'relationships' : 'tables')}
        >
          <div class="text-xs text-muted">{t(`docs.stats.${k}`)}</div>
          <div class="mt-1 text-lg font-semibold tabular-nums {k === 'anomalies' && v ? 'text-warn' : ''}">{fmtNumber(v)}</div>
        </button>
      {/if}
    {/each}
  </div>

  <div class="grid gap-6 lg:grid-cols-2">
    <section>
      <h3 class="mb-2 text-sm font-semibold">{t('docs.sections.anomalies')}</h3>
      {#if anomalyGroups.length}
        <ul class="space-y-1">
          {#each anomalyGroups as g (g.kind)}
            <li>
              <button class="flex w-full items-center gap-2 rounded-sm px-1 py-0.5 text-left text-sm hover:bg-hover" onclick={() => go('anomalies')}>
                <TriangleAlert size={13} class="shrink-0 text-warn" />
                <span class="flex-1">{t(`docs.anomaly.${g.kind}.title`)}</span>
                <span class="text-xs text-muted tabular-nums">{g.items.length}</span>
              </button>
            </li>
          {/each}
        </ul>
      {:else}<p class="text-sm text-faint">{t('docs.noAnomalies')}</p>{/if}
    </section>
    <section>
      <h3 class="mb-2 text-sm font-semibold" title={t('docs.rowsHint')}>{t('docs.largest')}</h3>
      {#if largest.length}
        <ul class="space-y-1 text-sm">
          {#each largest as tb (tb.name)}
            {@const max = largest[0].rows || 1}
            <li class="flex items-center gap-2">
              <span class="w-40 truncate">{@render tableLink(tb.name)}</span>
              <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-sunken"><span class="block h-full rounded-full bg-accent/60" style="width: {Math.max(2, ((tb.rows ?? 0) / max) * 100)}%"></span></span>
              <span class="w-20 text-right text-xs text-muted tabular-nums">{rows(tb.rows)}</span>
            </li>
          {/each}
        </ul>
      {:else}<p class="text-sm text-faint">{t('docs.none')}</p>{/if}
    </section>
    {#if mostReferenced.length}
      <section>
        <h3 class="mb-2 text-sm font-semibold">{t('docs.mostReferenced')}</h3>
        <ul class="space-y-1 text-sm">
          {#each mostReferenced as tb (tb.name)}
            <li class="flex items-center gap-2">{@render tableLink(tb.name)}<span class="text-xs text-muted">{t('docs.referencedCount', { count: tb.referenced_by.length })}</span></li>
          {/each}
        </ul>
      </section>
    {/if}
  </div>
</div>
