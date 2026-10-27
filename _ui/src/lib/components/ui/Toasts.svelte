<script lang="ts">
  import { t as tr } from '@/lib/i18n/index.svelte';
  import { X, CircleX, CircleCheck, Info } from 'lucide-svelte';
  import { dismiss, toasts } from '@/lib/stores/toast.svelte';
</script>

<div class="pointer-events-none fixed right-4 bottom-4 z-50 flex w-80 flex-col gap-2">
  {#each toasts as t (t.id)}
    <div
      class="pointer-events-auto flex items-start gap-2.5 rounded-lg border border-line bg-raised px-3 py-2.5 shadow-pop"
      role={t.kind === 'error' ? 'alert' : 'status'}
    >
      {#if t.kind === 'error'}<CircleX size={15} class="mt-px shrink-0 text-danger" />{:else if t.kind === 'success'}<CircleCheck size={15} class="mt-px shrink-0 text-ok" />{:else}<Info size={15} class="mt-px shrink-0 text-accent" />{/if}
      <p class="min-w-0 flex-1 text-sm break-words">{t.message}</p>
      <button class="text-muted hover:text-fg" onclick={() => dismiss(t.id)} aria-label={tr('common.close')}><X size={14} /></button>
    </div>
  {/each}
</div>
