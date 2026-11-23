<script lang="ts">
  // The statements pending grid edits will run, shown before saving.
  import { t } from '@/lib/i18n/index.svelte';
  import { Save } from '@lucide/svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';

  let {
    statements,
    pending,
    saving,
    production = false,
    onclose,
    onsave,
  }: { statements: string[]; pending: number; saving: boolean; production?: boolean; onclose: () => void; onsave: () => void } = $props();
</script>

<Modal title={t('edit.previewTitle')} {onclose} width="max-w-3xl">
  <p class="mb-2 text-xs text-muted">{t('edit.previewHint')}</p>
  <pre class="max-h-[55vh] overflow-auto rounded-md border border-line bg-sunken p-3 font-mono text-xs whitespace-pre-wrap break-all">{statements.join(';\n\n')};</pre>
  {#snippet footer()}
    <button class="btn" onclick={onclose}>{t('common.close')}</button>
    <button class={production ? 'btn-danger' : 'btn-primary'} onclick={onsave} disabled={saving}><Save size={13} /> {t('edit.save', { count: pending })}</button>
  {/snippet}
</Modal>
