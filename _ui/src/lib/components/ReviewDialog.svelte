<script lang="ts">
  import { TriangleAlert, FileCode2 } from 'lucide-svelte';
  import Modal from '@/lib/components/Modal.svelte';
  import { answerReview, reviewState } from '@/lib/stores/review.svelte';
  import { t } from '@/lib/i18n/index.svelte';

  let value = $state('');
  let seen: unknown = null;

  // Each new request starts from its own default value.
  $effect(() => {
    const cur = reviewState.current;
    if (cur !== seen) {
      seen = cur;
      value = cur?.input?.value ?? '';
      if (cur?.input) setTimeout(() => (document.getElementById('review-input') as HTMLInputElement | null)?.select(), 0);
    }
  });

  const sql = $derived(reviewState.current?.input ? reviewState.current.input.build(value) : (reviewState.current?.sql ?? ''));
</script>

{#if reviewState.current}
  {@const req = reviewState.current}
  <Modal title={req.title} onclose={() => answerReview('cancel')} width="max-w-2xl">
    {#if req.danger}
      <div class="mb-3 flex items-start gap-2.5 rounded-md border border-danger/30 bg-danger-soft p-3 text-sm text-danger">
        <TriangleAlert size={16} class="mt-0.5 shrink-0" />
        <p>{t('review.danger', { connection: req.connection })}</p>
      </div>
    {/if}
    {#if req.input}
      <label class="label" for="review-input">{req.input.label}</label>
      <!-- svelte-ignore a11y_autofocus -->
      <input id="review-input" class="input mb-3 font-mono" bind:value autofocus onkeydown={(e) => e.key === 'Enter' && value.trim() && answerReview('run', sql)} />
    {/if}
    <p class="mb-1.5 text-xs text-muted">{t('review.willRun', { connection: req.connection })}</p>
    <pre class="max-h-72 overflow-auto rounded-md border border-line bg-sunken p-3 font-mono text-xs whitespace-pre-wrap break-all">{sql}</pre>

    {#snippet footer()}
      <button class="btn-outline mr-auto" onclick={() => answerReview('edit', sql)}><FileCode2 size={14} /> {t('review.edit')}</button>
      <button class="btn-outline" onclick={() => answerReview('cancel')}>{t('common.cancel')}</button>
      <button class={req.danger ? 'btn-danger' : 'btn-primary'} disabled={!!req.input && !value.trim()} onclick={() => answerReview('run', sql)}>{t('review.run')}</button>
    {/snippet}
  </Modal>
{/if}
