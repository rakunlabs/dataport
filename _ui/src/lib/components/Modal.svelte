<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import type { Snippet } from 'svelte';
  import { X } from 'lucide-svelte';

  let {
    title,
    onclose,
    children,
    footer,
    width = 'max-w-md',
  }: { title: string; onclose: () => void; children: Snippet; footer?: Snippet; width?: string } = $props();

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') onclose();
  }
</script>

<svelte:window {onkeydown} />

<div class="fixed inset-0 z-50 flex items-start justify-center bg-black/45 px-4 py-[6vh]" role="presentation">
  <div class="flex max-h-[88vh] w-full {width} flex-col rounded-lg border border-line bg-raised shadow-dialog" role="dialog" aria-modal="true" aria-label={title}>
    <div class="flex h-11 shrink-0 items-center justify-between border-b border-line pr-2 pl-4">
      <h2 class="text-sm font-semibold">{title}</h2>
      <button class="icon-btn" onclick={onclose} aria-label={t('common.close')}><X size={15} /></button>
    </div>
    <div class="min-h-0 flex-1 overflow-y-auto px-4 py-4">
      {@render children()}
    </div>
    {#if footer}
      <div class="flex shrink-0 items-center justify-end gap-2 rounded-b-lg border-t border-line bg-panel px-4 py-2.5">
        {@render footer()}
      </div>
    {/if}
  </div>
</div>
