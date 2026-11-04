<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { PlugZap, LoaderCircle, TriangleAlert, ChevronRight } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import AiCompletionSettings from '@/pages/admin/AiCompletionSettings.svelte';
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
    max_steps: number;
    retries: number;
    proxy: string;
    // The server's own proxy, which an empty proxy takes.
    env_proxy?: string;
  }

  // The server's defaults, which the form shows as placeholders.
  const defaultSteps = 12;
  const defaultRetries = 2;

  let saved = $state<AI | null>(null);
  let form = $state({ enabled: false, base_url: '', model: '', prompt: '', max_steps: defaultSteps, retries: defaultRetries, proxy: '' });
  let key = $state('');
  let clearKey = $state(false);
  let error = $state('');
  let saving = $state(false);
  let testing = $state(false);
  let testResult = $state('');

  function apply(v: AI) {
    saved = v;
    form = { enabled: v.enabled, base_url: v.base_url, model: v.model, prompt: v.prompt, max_steps: v.max_steps, retries: v.retries, proxy: v.proxy };
    key = '';
    clearKey = false;
  }

  void api
    .get<AI>('/admin/ai')
    .then(apply)
    .catch((e) => toast(errorMessage(e), 'error'));

  // The advanced box opens by itself when it holds something changed. It is
  // set once, on load: after that it stays as the user left it, and a save
  // that fails does not fold it over the field with the error.
  const advancedSet = $derived(!!saved && (saved.max_steps !== defaultSteps || saved.retries !== defaultRetries || !!saved.proxy));
  let advancedOpen = $state(false);
  let advancedInit = false;
  $effect(() => {
    if (advancedInit || !saved) return;
    advancedInit = true;
    advancedOpen = advancedSet;
  });

  const dirty = $derived(
    !!saved &&
      (form.enabled !== saved.enabled ||
        form.base_url.trim() !== saved.base_url ||
        form.model.trim() !== saved.model ||
        form.prompt.trim() !== saved.prompt ||
        form.max_steps !== saved.max_steps ||
        form.retries !== saved.retries ||
        form.proxy.trim() !== saved.proxy ||
        !!key.trim() ||
        clearKey),
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

    <details class="group rounded-md border border-line" bind:open={advancedOpen}>
      <summary class="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-sm select-none hover:bg-hover">
        <ChevronRight size={14} class="disclosure-icon text-faint" />
        <span class="font-medium">{t('admin.ai.advanced')}</span>
        <span class="text-xs text-muted">{advancedSet ? t('admin.ai.advancedChanged') : t('admin.ai.advancedDefaults')}</span>
      </summary>
      <div class="space-y-3 border-t border-line-soft px-3 pt-2.5 pb-3">
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="label" for="ai-steps">{t('admin.ai.maxSteps')}</label>
            <input id="ai-steps" class="input tabular-nums" type="number" min="1" max="50" step="1" bind:value={form.max_steps} required disabled={!saved || saving} />
            <p class="hint mt-1">{t('admin.ai.maxStepsHint', { n: defaultSteps })}</p>
          </div>
          <div>
            <label class="label" for="ai-retries">{t('admin.ai.retries')}</label>
            <input id="ai-retries" class="input tabular-nums" type="number" min="0" max="5" step="1" bind:value={form.retries} required disabled={!saved || saving} />
            <p class="hint mt-1">{t('admin.ai.retriesHint', { n: defaultRetries })}</p>
          </div>
        </div>
        <div>
          <label class="label" for="ai-proxy">{t('admin.ai.proxy')}</label>
          <input id="ai-proxy" class="input font-mono" type="url" placeholder={saved?.env_proxy || 'http://proxy.example.com:3128'} bind:value={form.proxy} maxlength="2000" disabled={!saved || saving} />
          <p class="hint mt-1">{saved?.env_proxy ? t('admin.ai.proxyHintEnv', { proxy: saved.env_proxy }) : t('admin.ai.proxyHint')}</p>
        </div>
      </div>
    </details>

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

  <AiCompletionSettings />
</Page>
