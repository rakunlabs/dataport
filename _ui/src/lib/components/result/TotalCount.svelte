<script lang="ts">
  import { LoaderCircle, Sigma } from 'lucide-svelte';
  import { fmtNumber, t } from '@/lib/i18n/index.svelte';
  import { countResult, totalOf, type Tab } from '@/lib/stores/workspace.svelte';

  // The footer button that counts every row of a result, as DBeaver's
  // "Calculate total row count": the grid holds only the first rows.
  let { tab, index = 0 }: { tab: Tab; index?: number } = $props();

  const rs = $derived(tab.result?.results[index]);
  const total = $derived(totalOf(tab, rs));
</script>

{#if rs?.kind === 'rows' && !rs.plan}
  {#if total?.count !== undefined}
    <button class="flex h-[22px] items-center gap-1 rounded px-1 tabular-nums text-fg-2 hover:bg-hover" onclick={() => countResult(tab.id, index)} title={t('result.countAgain')}>
      <Sigma size={12} class="text-accent" />
      {t('result.total', { count: fmtNumber(total.count) })}
    </button>
  {:else if total?.running}
    <button class="flex h-[22px] items-center gap-1 rounded px-1 text-muted hover:bg-hover" onclick={() => countResult(tab.id, index)} title={t('result.countStop')}>
      <LoaderCircle size={12} class="animate-spin" />
      {t('result.counting')}
    </button>
  {:else}
    <button class="flex h-[22px] items-center gap-1 rounded px-1 text-muted hover:bg-hover hover:text-fg" onclick={() => countResult(tab.id, index)} title={total?.error ?? t('result.countHint')}>
      <Sigma size={12} class={total?.error ? 'text-danger' : ''} />
      {total?.error ? t('result.countFailed') : t('result.count')}
    </button>
  {/if}
{/if}
