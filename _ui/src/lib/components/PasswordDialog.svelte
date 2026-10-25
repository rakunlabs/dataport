<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import Modal from '@/lib/components/Modal.svelte';
  import { api } from '@/lib/api/client';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  let { onclose }: { onclose: () => void } = $props();

  let current = $state('');
  let next = $state('');
  let confirm = $state('');
  let error = $state('');
  let busy = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (next !== confirm) {
      error = t('password.mismatch');
      return;
    }
    busy = true;
    error = '';
    try {
      await api.post('/me/password', { current, new: next });
      toast(t('password.changed'), 'success');
      onclose();
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }
</script>

<Modal title={t('password.title')} {onclose}>
  <form id="password-form" onsubmit={submit} class="space-y-3">
    <div>
      <label class="label" for="pw-current">{t('password.current')}</label>
      <input id="pw-current" class="input" type="password" autocomplete="current-password" bind:value={current} required />
    </div>
    <div>
      <label class="label" for="pw-new">{t('password.new')}</label>
      <input id="pw-new" class="input" type="password" autocomplete="new-password" minlength="8" bind:value={next} required />
    </div>
    <div>
      <label class="label" for="pw-confirm">{t('password.repeat')}</label>
      <input id="pw-confirm" class="input" type="password" autocomplete="new-password" bind:value={confirm} required />
    </div>
    {#if error}<p class="text-xs text-danger">{error}</p>{/if}
  </form>

  {#snippet footer()}
    <button class="btn-outline" onclick={onclose}>{t('common.cancel')}</button>
    <button class="btn-primary" form="password-form" disabled={busy}>{t('password.title')}</button>
  {/snippet}
</Modal>
