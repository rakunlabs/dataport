<script lang="ts">
  import { TriangleAlert } from 'lucide-svelte';
  import Modal from '@/lib/components/Modal.svelte';
  import { answer, confirmState } from '@/lib/stores/confirm.svelte';
  import { t } from '@/lib/i18n/index.svelte';
</script>

{#if confirmState.current}
  {@const req = confirmState.current}
  <Modal title={t('production.title')} onclose={() => answer(false)} width="max-w-2xl">
    <div class="mb-3 flex items-start gap-2.5 rounded-md border border-danger/30 bg-danger-soft p-3 text-sm text-danger">
      <TriangleAlert size={16} class="mt-0.5 shrink-0" />
      <p>{t('production.body', { connection: req.connection })}</p>
    </div>
    <p class="mb-1.5 text-xs text-muted">{req.risk === 'ddl' ? t('production.ddl') : t('production.write')}</p>
    <pre class="max-h-72 overflow-auto rounded-md border border-line bg-sunken p-3 font-mono text-xs whitespace-pre-wrap break-all">{req.sql}</pre>

    {#snippet footer()}
      <button class="btn-outline" onclick={() => answer(false)}>{t('common.cancel')}</button>
      <button class="btn-danger" onclick={() => answer(true)}>{t('production.run')}</button>
    {/snippet}
  </Modal>
{/if}
