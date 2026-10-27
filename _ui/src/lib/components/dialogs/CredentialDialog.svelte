<script lang="ts">
  import { KeyRound, LoaderCircle, ChevronRight, RotateCcw } from 'lucide-svelte';
  import LimitsFields from '@/lib/components/ui/LimitsFields.svelte';
  import type { Limits } from '@/lib/api/types';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import DbIcon from '@/lib/components/ui/DbIcon.svelte';
  import { closeCredentials, credentialState, saveCredentials } from '@/lib/stores/credentials.svelte';
  import { connection } from '@/lib/stores/workspace.svelte';
  import { errorMessage } from '@/lib/stores/toast.svelte';
  import { t } from '@/lib/i18n/index.svelte';

  const req = $derived(credentialState.current);
  const conn = $derived(req ? connection(req.connectionId) : undefined);

  let username = $state('');
  let password = $state('');
  let limits = $state<Limits>({});
  let error = $state('');
  let busy = $state(false);

  // Start from the saved account, so a changed password or limit is one
  // field. With a saved account, an empty password keeps it.
  $effect(() => {
    if (req) {
      username = conn?.credential_user ?? '';
      password = '';
      limits = { ...(conn?.my_limits ?? {}) };
      error = '';
    }
  });

  const saved = $derived(!!conn?.credential_user);
  const custom = $derived(Object.values(limits).some(Boolean));

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!req) return;
    busy = true;
    error = '';
    try {
      await saveCredentials(req.connectionId, username.trim(), saved && !password ? null : password, limits);
      closeCredentials(true);
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }
</script>

{#if req}
  <Modal title={t('credentials.title')} onclose={() => closeCredentials(false)} width="max-w-lg">
    <form id="cred-form" onsubmit={submit} class="space-y-3">
      <div class="flex items-center gap-2 rounded-md border border-line bg-panel px-3 py-2">
        {#if conn}<DbIcon type={conn.db_type} />{/if}
        <span class="font-medium">{conn?.name ?? ''}</span>
        {#if conn?.database}<span class="text-xs text-muted">{conn.database}</span>{/if}
      </div>
      <p class="text-xs text-muted">{t('credentials.hint')}</p>
      {#if req.reason && conn?.credential_user}
        <p class="text-xs text-warn">{req.reason}</p>
      {/if}
      <div>
        <label class="label" for="cred-user">{t('credentials.username')}</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input id="cred-user" class="input" bind:value={username} required maxlength="128" autocomplete="off" autofocus={!username} />
      </div>
      <div>
        <label class="label" for="cred-pass">{t('credentials.password')}</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input
          id="cred-pass"
          class="input"
          type="password"
          bind:value={password}
          autocomplete="new-password"
          autofocus={!!username}
          placeholder={saved ? t('credentials.keepPassword') : ''}
        />
      </div>

      <details class="group rounded-md border border-line" open={custom}>
        <summary class="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-sm select-none hover:bg-hover">
          <ChevronRight size={14} class="text-faint disclosure-icon" />
          <span class="font-medium">{t('limits.details')}</span>
          <span class="text-xs text-muted">{custom ? t('limits.custom') : t('limits.usingDefaults')}</span>
        </summary>
        <div class="space-y-2 border-t border-line-soft px-3 pt-2.5 pb-3">
          <p class="hint">{t('limits.userHint')}</p>
          <LimitsFields id="cred-limits" bind:value={limits} inherited={conn?.limits ?? {}} />
          {#if custom}
            <button type="button" class="btn h-6 px-1.5 text-xs" onclick={() => (limits = {})}><RotateCcw size={12} /> {t('limits.reset')}</button>
          {/if}
        </div>
      </details>
      {#if error}<p class="text-xs text-danger">{error}</p>{/if}
      <p class="text-2xs text-muted">{t('credentials.storage')}</p>
    </form>

    {#snippet footer()}
      <button class="btn-outline" onclick={() => closeCredentials(false)}>{t('common.cancel')}</button>
      <button class="btn-primary" form="cred-form" disabled={busy || !username.trim()}>
        {#if busy}<LoaderCircle size={13} class="animate-spin" />{:else}<KeyRound size={13} />{/if}
        {t('credentials.connect')}
      </button>
    {/snippet}
  </Modal>
{/if}
