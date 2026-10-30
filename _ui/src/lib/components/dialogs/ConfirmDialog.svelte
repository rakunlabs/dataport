<script lang="ts">
  import { TriangleAlert } from 'lucide-svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import { answer, confirmState } from '@/lib/stores/confirm.svelte';
  import { t } from '@/lib/i18n/index.svelte';
</script>

{#if confirmState.current}
  {@const req = confirmState.current}
  {@const production = req.production !== false}
  {@const unbounded = req.unbounded ?? []}
  <Modal title={production ? t('production.title') : t('production.unboundedTitle')} onclose={() => answer(false)} width="max-w-2xl">
    {#if production}
      <div class="mb-3 flex items-start gap-2.5 rounded-md border border-danger/30 bg-danger-soft p-3 text-sm text-danger">
        <TriangleAlert size={16} class="mt-0.5 shrink-0" />
        <p>{t('production.body', { connection: req.connection })}</p>
      </div>
    {/if}
    {#if unbounded.length}
      <div class="mb-3 flex items-start gap-2.5 rounded-md border border-warn/40 bg-warn-soft p-3 text-sm text-warn">
        <TriangleAlert size={16} class="mt-0.5 shrink-0" />
        <div class="min-w-0">
          <p>{t('production.unbounded', { count: unbounded.length, connection: req.connection })}</p>
          <pre class="mt-2 max-h-40 overflow-auto font-mono text-xs whitespace-pre-wrap break-all">{unbounded.join(';\n\n')};</pre>
        </div>
      </div>
    {/if}
    {#if production}<p class="mb-1.5 text-xs text-muted">{req.risk === 'ddl' ? t('production.ddl') : t('production.write')}</p>{/if}
    <pre class="max-h-72 overflow-auto rounded-md border border-line bg-sunken p-3 font-mono text-xs whitespace-pre-wrap break-all">{req.sql}</pre>

    {#snippet footer()}
      <button class="btn-outline" onclick={() => answer(false)}>{t('common.cancel')}</button>
      <button class="btn-danger" onclick={() => answer(true)}>{production ? t('production.run') : t('production.runEveryRow')}</button>
    {/snippet}
  </Modal>
{/if}
