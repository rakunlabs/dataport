<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { api } from '@/lib/api/client';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  // The address dataport is reached at from outside, for MCP sign-in and
  // single sign-on redirects. It applies at once, without a restart.
  interface External {
    value: string;
    saved: boolean;
    config: string;
    detected: string;
  }

  let saved = $state<External | null>(null);
  let draft = $state('');
  let error = $state('');
  let saving = $state(false);

  const dirty = $derived(!!saved && draft.trim().replace(/\/+$/, '') !== saved.value);

  function apply(v: External) {
    saved = v;
    draft = v.value;
  }

  void api
    .get<External>('/admin/external-url')
    .then(apply)
    .catch((e) => toast(errorMessage(e), 'error'));

  async function save(value: string | null) {
    saving = true;
    error = '';
    try {
      apply(await api.put<External>('/admin/external-url', { value }));
      toast(t('admin.externalUrl.saved'), 'success');
    } catch (e) {
      error = errorMessage(e);
    } finally {
      saving = false;
    }
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    void save(draft.trim());
  }
</script>

<form class="max-w-2xl space-y-3 border-t border-line px-6 py-5" onsubmit={submit}>
  <div>
    <h2 class="text-sm font-semibold">{t('admin.externalUrl.title')}</h2>
    <p class="hint mt-0.5">{t('admin.externalUrl.description')}</p>
  </div>
  <div class="flex flex-wrap items-center gap-2">
    <input
      class="input min-w-72 flex-1 font-mono"
      type="url"
      placeholder={saved?.detected || 'https://example.com/tools'}
      aria-label={t('admin.externalUrl.title')}
      bind:value={draft}
      disabled={!saved || saving}
    />
    {#if saved?.detected}
      <button type="button" class="btn" onclick={() => (draft = saved!.detected)} disabled={saving}>{t('admin.externalUrl.useDetected')}</button>
    {/if}
  </div>
  {#if saved}
    <p class="hint">
      {t('admin.externalUrl.detected', { url: saved.detected })}
      {#if saved.config}· {t('admin.externalUrl.config', { url: saved.config })}{/if}
    </p>
  {/if}
  <p class="hint">{t('admin.externalUrl.hint')}</p>
  {#if error}<p role="alert" class="text-xs text-danger">{error}</p>{/if}
  <div class="flex items-center gap-2">
    <button type="submit" class="btn-primary" disabled={!dirty || saving}>{saving ? t('common.loading') : t('common.save')}</button>
    {#if saved?.saved}
      <button type="button" class="btn-outline" onclick={() => save(null)} disabled={saving}>{t('admin.externalUrl.reset')}</button>
    {/if}
  </div>
</form>
