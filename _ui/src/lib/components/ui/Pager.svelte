<script lang="ts">
  import { ChevronLeft, ChevronRight } from 'lucide-svelte';
  import { t } from '@/lib/i18n/index.svelte';

  // Pages through a list held in full: shows "1–50 of 320" and moves
  // between pages. page is 0-based and kept in range by its owner.
  let { page = $bindable(0), size, total }: { page?: number; size: number; total: number } = $props();

  const pages = $derived(Math.max(1, Math.ceil(total / size)));
  const from = $derived(total ? page * size + 1 : 0);
  const to = $derived(Math.min(total, (page + 1) * size));
</script>

{#if total > size}
  <div class="flex items-center justify-end gap-2 border-t border-line px-4 py-2 text-xs text-muted">
    <span class="tabular-nums">{t('pager.range', { from, to, total })}</span>
    <button class="icon-btn size-6" onclick={() => (page = Math.max(0, page - 1))} disabled={page === 0} title={t('pager.previous')} aria-label={t('pager.previous')}>
      <ChevronLeft size={14} />
    </button>
    <span class="tabular-nums">{t('pager.page', { page: page + 1, pages })}</span>
    <button class="icon-btn size-6" onclick={() => (page = Math.min(pages - 1, page + 1))} disabled={page >= pages - 1} title={t('pager.next')} aria-label={t('pager.next')}>
      <ChevronRight size={14} />
    </button>
  </div>
{/if}
