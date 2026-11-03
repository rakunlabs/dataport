<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { PlugZap, LoaderCircle } from 'lucide-svelte';
  import PasswordInput from '@/lib/components/ui/PasswordInput.svelte';
  import { api } from '@/lib/api/client';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { loadSession } from '@/lib/stores/session.svelte';

  // Inline completion in the SQL editor: a small, fast model of its own.
  type Mode = 'fim' | 'codestral' | 'chat';
  interface Completion {
    enabled: boolean;
    mode: Mode;
    shared: boolean;
    base_url: string;
    path: string;
    model: string;
    has_key: boolean;
  }

  const modes: { value: Mode; label: string; hint: string; url: string; path: string; model: string }[] = [
    { value: 'fim', label: 'admin.aiComplete.modeFim', hint: 'admin.aiComplete.modeFimHint', url: 'http://localhost:11434/v1', path: '/completions', model: 'qwen2.5-coder:1.5b' },
    { value: 'codestral', label: 'admin.aiComplete.modeCodestral', hint: 'admin.aiComplete.modeCodestralHint', url: 'https://codestral.mistral.ai/v1', path: '/fim/completions', model: 'codestral-latest' },
    { value: 'chat', label: 'admin.aiComplete.modeChat', hint: 'admin.aiComplete.modeChatHint', url: 'https://api.openai.com/v1', path: '/chat/completions', model: 'gpt-4.1-nano' },
  ];

  let saved = $state<Completion | null>(null);
  let form = $state({ enabled: false, mode: 'fim' as Mode, shared: false, base_url: '', path: '', model: '' });
  let key = $state('');
  let clearKey = $state(false);
  let error = $state('');
  let saving = $state(false);
  let testing = $state(false);
  let testResult = $state('');

  const mode = $derived(modes.find((m) => m.value === form.mode) ?? modes[0]);

  function apply(v: Completion) {
    saved = v;
    form = { enabled: v.enabled, mode: v.mode, shared: v.shared, base_url: v.base_url, path: v.path ?? '', model: v.model };
    key = '';
    clearKey = false;
  }

  void api
    .get<Completion>('/admin/ai/completion')
    .then(apply)
    .catch((e) => toast(errorMessage(e), 'error'));

  const dirty = $derived(
    !!saved &&
      (form.enabled !== saved.enabled ||
        form.mode !== saved.mode ||
        form.shared !== saved.shared ||
        form.base_url.trim() !== saved.base_url ||
        form.path.trim() !== (saved.path ?? '') ||
        form.model.trim() !== saved.model ||
        !!key.trim() ||
        clearKey),
  );

  async function save(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    error = '';
    testResult = '';
    try {
      const body: Record<string, unknown> = { ...form };
      if (key.trim()) body.key = key.trim();
      else if (clearKey) body.key = '';
      apply(await api.put<Completion>('/admin/ai/completion', body));
      toast(t('admin.aiComplete.saved'), 'success');
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
      const out = await api.post<{ text: string; ms: number }>('/admin/ai/completion/test');
      testResult = t('admin.aiComplete.testOk', { text: out.text.trim().slice(0, 80) || '∅', ms: out.ms });
    } catch (err) {
      error = errorMessage(err);
    } finally {
      testing = false;
    }
  }
</script>

<form class="max-w-2xl space-y-4 border-t border-line px-6 py-5" onsubmit={save}>
  <div>
    <h2 class="text-sm font-semibold">{t('admin.aiComplete.title')}</h2>
    <p class="mt-0.5 text-xs text-muted">{t('admin.aiComplete.description')}</p>
  </div>

  <label class="flex items-center gap-2 text-sm font-medium">
    <input type="checkbox" bind:checked={form.enabled} disabled={!saved || saving} />
    {t('admin.aiComplete.enabled')}
  </label>

  <fieldset class="space-y-1.5" disabled={!saved || saving}>
    <legend class="label">{t('admin.aiComplete.mode')}</legend>
    {#each modes as m (m.value)}
      <label class="flex items-start gap-2 text-sm">
        <input type="radio" class="mt-0.5" name="ai-complete-mode" value={m.value} bind:group={form.mode} />
        <span>
          <span class="block">{t(m.label)}</span>
          <span class="block text-xs text-muted">{t(m.hint)}</span>
        </span>
      </label>
    {/each}
  </fieldset>

  <label class="flex items-start gap-2 text-sm">
    <input type="checkbox" class="mt-0.5" bind:checked={form.shared} disabled={!saved || saving} />
    <span>
      <span class="block">{t('admin.aiComplete.shared')}</span>
      <span class="block text-xs text-muted">{t('admin.aiComplete.sharedHint')}</span>
    </span>
  </label>

  {#if !form.shared}
    <div>
      <label class="label" for="aic-url">{t('admin.ai.baseUrl')}</label>
      <input id="aic-url" class="input font-mono" type="url" placeholder={mode.url} bind:value={form.base_url} required={form.enabled} disabled={!saved || saving} />
    </div>
  {/if}

  <div>
    <label class="label" for="aic-path">{t('admin.aiComplete.path')}</label>
    <input id="aic-path" class="input font-mono" placeholder={mode.path} bind:value={form.path} maxlength="200" pattern="/.*" disabled={!saved || saving} />
    <p class="hint mt-1">{t('admin.aiComplete.pathHint')}</p>
  </div>

  <div>
    <label class="label" for="aic-model">{t('admin.ai.model')}</label>
    <input id="aic-model" class="input font-mono" placeholder={mode.model} bind:value={form.model} required={form.enabled} maxlength="200" disabled={!saved || saving} />
    <p class="hint mt-1">{t('admin.aiComplete.modelHint')}</p>
  </div>

  {#if !form.shared}
    <div>
      <label class="label" for="aic-key">{t('admin.ai.key')} {#if saved?.has_key && !clearKey}<span class="font-normal text-faint">· {t('admin.ai.keySaved')}</span>{/if}</label>
      <PasswordInput id="aic-key" class="input font-mono" autocomplete="off" bind:value={key} placeholder={saved?.has_key ? '••••••••' : ''} disabled={!saved || saving || clearKey} />
      {#if saved?.has_key}
        <label class="mt-1 flex items-center gap-2 text-xs text-muted"><input type="checkbox" bind:checked={clearKey} disabled={saving} /> {t('admin.ai.keyClear')}</label>
      {/if}
    </div>
  {/if}

  <p class="text-xs text-muted">{t('admin.aiComplete.privacy')}</p>

  {#if error}<p role="alert" class="text-xs text-danger">{error}</p>{/if}
  {#if testResult}<p role="status" class="font-mono text-xs whitespace-pre-wrap text-ok">{testResult}</p>{/if}

  <div class="flex items-center gap-2">
    <button type="submit" class="btn-primary" disabled={!dirty || saving}>{saving ? t('common.loading') : t('common.save')}</button>
    <button type="button" class="btn-outline" onclick={test} disabled={testing || dirty || !saved?.model}>
      {#if testing}<LoaderCircle size={14} class="animate-spin" />{:else}<PlugZap size={14} />{/if}
      {t('admin.ai.test')}
    </button>
  </div>
</form>
