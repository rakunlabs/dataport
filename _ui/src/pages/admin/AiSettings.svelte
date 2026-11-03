<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { PlugZap, LoaderCircle, TriangleAlert } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import PasswordInput from '@/lib/components/ui/PasswordInput.svelte';
  import { api } from '@/lib/api/client';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { loadSession } from '@/lib/stores/session.svelte';

  // The OpenAI compatible service the workspace assistant talks to.
  interface AI {
    enabled: boolean;
    base_url: string;
    model: string;
    has_key: boolean;
    prompt: string;
  }

  let saved = $state<AI | null>(null);
  let form = $state({ enabled: false, base_url: '', model: '', prompt: '' });
  let key = $state('');
  let clearKey = $state(false);
  let error = $state('');
  let saving = $state(false);
  let testing = $state(false);
  let testResult = $state('');

  function apply(v: AI) {
    saved = v;
    form = { enabled: v.enabled, base_url: v.base_url, model: v.model, prompt: v.prompt };
    key = '';
    clearKey = false;
  }

  void api
    .get<AI>('/admin/ai')
    .then(apply)
    .catch((e) => toast(errorMessage(e), 'error'));

  const dirty = $derived(
    !!saved &&
      (form.enabled !== saved.enabled || form.base_url.trim() !== saved.base_url || form.model.trim() !== saved.model || form.prompt.trim() !== saved.prompt || !!key.trim() || clearKey),
  );

  async function save(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    error = '';
    try {
      const body: Record<string, unknown> = { ...form };
      if (key.trim()) body.key = key.trim();
      else if (clearKey) body.key = '';
      apply(await api.put<AI>('/admin/ai', body));
      toast(t('admin.ai.saved'), 'success');
      // The assistant appears or goes in the top bar.
      void loadSession();
    } catch (err) {
      error = errorMessage(err);
    } finally {
      saving = false;
    }
  }

  async function test() {
    testing = true;
    testResult = '';
    error = '';
    try {
      const out = await api.post<{ reply: string; ms: number }>('/admin/ai/test');
      testResult = t('admin.ai.testOk', { reply: out.reply.trim().slice(0, 60), ms: out.ms });
    } catch (err) {
      error = errorMessage(err);
    } finally {
      testing = false;
    }
  }
</script>

<Page title={t('admin.ai.title')} description={t('admin.ai.description')}>
  <form class="max-w-2xl space-y-4 px-6 py-5" onsubmit={save}>
    <p class="flex items-start gap-1.5 rounded-md border border-line bg-panel p-2 text-xs text-fg-2">
      <TriangleAlert size={13} class="mt-0.5 shrink-0 text-warn" /> {t('admin.ai.privacy')}
    </p>

    <label class="flex items-center gap-2 text-sm font-medium">
      <input type="checkbox" bind:checked={form.enabled} disabled={!saved || saving} />
      {t('admin.ai.enabled')}
    </label>

    <div>
      <label class="label" for="ai-url">{t('admin.ai.baseUrl')}</label>
      <input id="ai-url" class="input font-mono" type="url" placeholder="https://api.openai.com/v1" bind:value={form.base_url} required={form.enabled} disabled={!saved || saving} />
      <p class="hint mt-1">{t('admin.ai.baseUrlHint')}</p>
    </div>

    <div>
      <label class="label" for="ai-model">{t('admin.ai.model')}</label>
      <input id="ai-model" class="input font-mono" placeholder="gpt-4.1-mini" bind:value={form.model} required={form.enabled} maxlength="200" disabled={!saved || saving} />
    </div>

    <div>
      <label class="label" for="ai-key">{t('admin.ai.key')} {#if saved?.has_key && !clearKey}<span class="font-normal text-faint">· {t('admin.ai.keySaved')}</span>{/if}</label>
      <PasswordInput id="ai-key" class="input font-mono" autocomplete="off" bind:value={key} placeholder={saved?.has_key ? '••••••••' : ''} disabled={!saved || saving || clearKey} />
      <p class="hint mt-1">{t('admin.ai.keyHint')}</p>
      {#if saved?.has_key}
        <label class="mt-1 flex items-center gap-2 text-xs text-muted"><input type="checkbox" bind:checked={clearKey} disabled={saving} /> {t('admin.ai.keyClear')}</label>
      {/if}
    </div>

    <div>
      <label class="label" for="ai-prompt">{t('admin.ai.prompt')}</label>
      <textarea id="ai-prompt" class="input h-auto min-h-24 py-1.5 text-sm" rows="4" maxlength="8000" bind:value={form.prompt} disabled={!saved || saving}></textarea>
      <p class="hint mt-1">{t('admin.ai.promptHint')}</p>
    </div>

    {#if error}<p role="alert" class="text-xs text-danger">{error}</p>{/if}
    {#if testResult}<p role="status" class="text-xs text-ok">{testResult}</p>{/if}

    <div class="flex items-center gap-2">
      <button type="submit" class="btn-primary" disabled={!dirty || saving}>{saving ? t('common.loading') : t('common.save')}</button>
      <button type="button" class="btn-outline" onclick={test} disabled={testing || dirty || !saved?.base_url || !saved?.model}>
        {#if testing}<LoaderCircle size={14} class="animate-spin" />{:else}<PlugZap size={14} />{/if}
        {t('admin.ai.test')}
      </button>
    </div>
  </form>
</Page>
