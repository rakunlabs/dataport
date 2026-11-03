<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { KeyRound, TriangleAlert, Copy, Check, Dices } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import PasswordInput from '@/lib/components/ui/PasswordInput.svelte';
  import { api } from '@/lib/api/client';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  // The key the stored secrets (connection passwords, SSO client secrets,
  // push tokens) are encrypted with. Rotating it seals all of them again;
  // the configuration has to follow before the next restart.
  interface KeyStatus {
    rotated: boolean;
    empty: boolean;
    shared: boolean;
    secrets: number;
    unreadable: number;
  }

  let status = $state<KeyStatus | null>(null);
  let current = $state('');
  let next = $state('');
  let confirmKey = $state('');
  let understood = $state(false);
  let busy = $state(false);
  let error = $state('');
  // The new key, kept on the page after a rotation to copy into the config.
  let rotatedKey = $state('');
  let copied = $state(false);

  async function load() {
    try {
      status = await api.get<KeyStatus>('/admin/secret-key');
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  void load();

  const mismatch = $derived(!!confirmKey && next !== confirmKey);
  const ready = $derived(next.length >= 16 && next === confirmKey && understood && !busy);

  function generate() {
    const bytes = crypto.getRandomValues(new Uint8Array(32));
    const key = btoa(String.fromCharCode(...bytes)).replace(/[+/=]/g, (c) => ({ '+': '-', '/': '_', '=': '' })[c] ?? '');
    next = key;
    confirmKey = key;
  }

  async function rotate(e: SubmitEvent) {
    e.preventDefault();
    if (!ready) return;
    busy = true;
    error = '';
    try {
      const out = await api.post<{ status: KeyStatus; rewritten: number; unreadable: number }>('/admin/secret-key/rotate', { current, key: next });
      status = out.status;
      rotatedKey = next;
      current = next = confirmKey = '';
      understood = false;
      toast(t('admin.secretKey.rotated', { count: out.rewritten }), 'success');
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(rotatedKey);
      copied = true;
      setTimeout(() => (copied = false), 1500);
    } catch {
      toast(t('grid.clipboardUnavailable'), 'error');
    }
  }
</script>

<Page title={t('admin.secretKey.title')} description={t('admin.secretKey.description')}>
  <div class="max-w-2xl space-y-5 px-6 py-5">
    {#if status}
      <dl class="grid grid-cols-[12rem_1fr] gap-y-1.5 text-sm">
        <dt class="text-muted">{t('admin.secretKey.secrets')}</dt>
        <dd class="tabular-nums">
          {status.secrets}
          {#if status.unreadable}<span class="ml-1 text-danger">· {t('admin.secretKey.unreadable', { count: status.unreadable })}</span>{/if}
        </dd>
        <dt class="text-muted">{t('admin.secretKey.inUse')}</dt>
        <dd>{status.empty ? t('admin.secretKey.emptyKey') : status.rotated ? t('admin.secretKey.fromUi') : t('admin.secretKey.fromConfig')}</dd>
      </dl>
    {/if}

    {#if rotatedKey || status?.rotated}
      <!-- The one thing that must not be missed: the config still holds the old key. -->
      <div class="space-y-2 rounded-md border border-warn/50 bg-warn-soft p-3 text-sm" role="alert">
        <p class="flex items-start gap-1.5 font-medium"><TriangleAlert size={14} class="mt-0.5 shrink-0 text-warn" /> {t('admin.secretKey.updateConfig')}</p>
        <p class="text-xs text-fg-2">{t('admin.secretKey.updateConfigHint')}</p>
        <pre class="overflow-auto rounded-sm bg-panel p-2 font-mono text-xs">DATAPORT_AUTH_SECRET_KEY=&lt;{t('admin.secretKey.newKeyShort')}&gt;
DATAPORT_AUTH_PREVIOUS_SECRET_KEY=&lt;{t('admin.secretKey.oldKeyShort')}&gt;</pre>
        {#if rotatedKey}
          <div class="flex gap-2">
            <input class="input font-mono text-xs" readonly value={rotatedKey} aria-label={t('admin.secretKey.new')} />
            <button type="button" class="btn-outline" onclick={copy}>
              {#if copied}<Check size={14} />{:else}<Copy size={14} />{/if}
              {t('common.copy')}
            </button>
          </div>
          <p class="text-xs text-muted">{t('admin.secretKey.shownOnce')}</p>
        {/if}
      </div>
    {/if}

    {#if status?.shared}
      <p class="flex items-start gap-1.5 rounded-md border border-line bg-panel p-2 text-xs text-fg-2">
        <TriangleAlert size={13} class="mt-0.5 shrink-0 text-warn" /> {t('admin.secretKey.replicas')}
      </p>
    {/if}

    <form class="space-y-3" onsubmit={rotate}>
      <h2 class="text-sm font-semibold">{t('admin.secretKey.rotate')}</h2>
      <div>
        <label class="label" for="key-current">{t('admin.secretKey.current')}</label>
        <PasswordInput id="key-current" class="input font-mono" autocomplete="off" bind:value={current} disabled={busy} />
        {#if status?.empty}<p class="hint mt-1">{t('admin.secretKey.currentEmpty')}</p>{/if}
      </div>
      <div>
        <div class="flex items-end justify-between">
          <label class="label" for="key-new">{t('admin.secretKey.new')}</label>
          <button type="button" class="btn mb-1 h-6 text-xs" onclick={generate} disabled={busy}><Dices size={12} /> {t('admin.secretKey.generate')}</button>
        </div>
        <PasswordInput id="key-new" class="input font-mono" autocomplete="new-password" minlength={16} bind:value={next} disabled={busy} />
        <p class="hint mt-1">{t('admin.secretKey.newHint')}</p>
      </div>
      <div>
        <label class="label" for="key-confirm">{t('admin.secretKey.confirm')}</label>
        <PasswordInput id="key-confirm" class="input font-mono" autocomplete="new-password" bind:value={confirmKey} disabled={busy} />
        {#if mismatch}<p class="mt-1 text-xs text-danger">{t('admin.secretKey.mismatch')}</p>{/if}
      </div>
      <label class="flex items-start gap-2 text-sm">
        <input type="checkbox" class="mt-0.5" bind:checked={understood} disabled={busy} />
        <span>{t('admin.secretKey.understand')}</span>
      </label>
      {#if error}<p role="alert" class="text-xs text-danger">{error}</p>{/if}
      <button type="submit" class="btn-primary" disabled={!ready}><KeyRound size={14} /> {busy ? t('common.loading') : t('admin.secretKey.rotateButton')}</button>
    </form>
  </div>
</Page>
