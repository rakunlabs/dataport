<script lang="ts">
  import { t, fmtDateTime } from '@/lib/i18n/index.svelte';
  import { Plus, Trash2, Copy, Check, Lock, LockOpen, TriangleAlert } from 'lucide-svelte';
  import { api } from '@/lib/api/client';
  import type { AccessToken, TokenList } from '@/lib/api/types';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  let info = $state<TokenList | null>(null);
  let name = $state('');
  let readOnly = $state(true);
  let days = $state<number | ''>('');
  let busy = $state(false);
  // The raw token is shown once, right after it is created.
  let created = $state<{ token: string; access_token: AccessToken } | null>(null);
  let copied = $state('');
  let client = $state<'claude' | 'json' | 'curl'>('claude');

  async function load() {
    try {
      info = await api.get<TokenList>('/tokens');
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  void load();

  const endpoint = $derived.by(() => {
    if (!info) return '';
    const base = info.external_base || location.origin;
    return base + info.mcp_path;
  });

  async function create(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    try {
      created = await api.post('/tokens', { name, read_only: readOnly, expires_in_days: Number(days) || 0 });
      name = '';
      await load();
    } catch (err) {
      toast(errorMessage(err), 'error');
    } finally {
      busy = false;
    }
  }

  async function revoke(tok: AccessToken) {
    if (!confirm(t('mcp.revokeConfirm', { name: tok.name }))) return;
    try {
      await api.del(`/tokens/${tok.id}`);
      if (created?.access_token.id === tok.id) created = null;
      await load();
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  const token = $derived(created?.token ?? '<token>');

  const snippet = $derived(
    client === 'claude'
      ? `claude mcp add --transport http dataport ${endpoint} --header "Authorization: Bearer ${token}"`
      : client === 'json'
        ? JSON.stringify({ mcpServers: { dataport: { type: 'http', url: endpoint, headers: { Authorization: `Bearer ${token}` } } } }, null, 2)
        : `curl -s ${endpoint} -H "Authorization: Bearer ${token}" -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \\\n  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1"}}}'`,
  );

  async function copy(text: string, what: string) {
    try {
      await navigator.clipboard.writeText(text);
      copied = what;
      setTimeout(() => (copied = copied === what ? '' : copied), 1500);
    } catch {
      toast(t('grid.clipboardUnavailable'), 'error');
    }
  }
</script>

<div class="max-w-2xl">
  {#if info && !info.mcp_enabled}
    <p class="rounded-md bg-warn-soft px-3 py-2 text-sm text-warn">{t('mcp.disabled')}</p>
  {:else}
    <div class="space-y-5">
      <p class="text-sm text-fg-2">{t('mcp.intro')}</p>

      <section>
        <span class="label">{t('mcp.endpoint')}</span>
        <div class="flex gap-2">
          <input class="input font-mono" readonly value={endpoint} aria-label={t('mcp.endpoint')} />
          <button type="button" class="btn-outline" onclick={() => copy(endpoint, 'url')}>
            {#if copied === 'url'}<Check size={14} />{:else}<Copy size={14} />{/if}
            {t('common.copy')}
          </button>
        </div>
      </section>

      {#if created}
        <section class="rounded-md border border-accent bg-accent-soft p-3">
          <p class="mb-2 flex items-center gap-1.5 text-sm font-medium"><TriangleAlert size={14} class="text-warn" /> {t('mcp.copyNow')}</p>
          <div class="flex gap-2">
            <input class="input font-mono" readonly value={created.token} aria-label={t('mcp.token')} />
            <button type="button" class="btn-primary" onclick={() => copy(created!.token, 'token')}>
              {#if copied === 'token'}<Check size={14} />{:else}<Copy size={14} />{/if}
              {t('common.copy')}
            </button>
          </div>
        </section>
      {/if}

      <section>
        <div class="mb-1.5 flex items-center justify-between">
          <span class="label mb-0">{t('mcp.setup')}</span>
          <div class="segmented">
            <button type="button" aria-pressed={client === 'claude'} onclick={() => (client = 'claude')}>Claude Code</button>
            <button type="button" aria-pressed={client === 'json'} onclick={() => (client = 'json')}>{t('mcp.jsonConfig')}</button>
            <button type="button" aria-pressed={client === 'curl'} onclick={() => (client = 'curl')}>curl</button>
          </div>
        </div>
        <div class="relative">
          <pre class="max-h-48 overflow-auto rounded-md border border-line bg-sunken p-3 pr-10 font-mono text-xs whitespace-pre-wrap [overflow-wrap:anywhere]">{snippet}</pre>
          <button type="button" class="icon-btn absolute top-1.5 right-1.5" onclick={() => copy(snippet, 'snippet')} aria-label={t('common.copy')}>
            {#if copied === 'snippet'}<Check size={14} />{:else}<Copy size={14} />{/if}
          </button>
        </div>
        {#if !created}<p class="hint mt-1">{t('mcp.snippetHint')}</p>{/if}
      </section>

      <section>
        <span class="label">{t('mcp.newToken')}</span>
        <form class="grid grid-cols-6 items-end gap-2" onsubmit={create}>
          <div class="col-span-3">
            <input class="input" bind:value={name} required maxlength="100" placeholder={t('mcp.namePlaceholder')} aria-label={t('mcp.name')} />
          </div>
          <div class="col-span-1">
            <input
              class="input"
              type="number"
              min="1"
              max={info?.max_ttl_days || undefined}
              bind:value={days}
              placeholder={info?.max_ttl_days ? String(info.max_ttl_days) : '∞'}
              title={t('mcp.days')}
              aria-label={t('mcp.days')}
            />
          </div>
          <button class="btn-primary col-span-2 justify-center" disabled={busy}><Plus size={14} /> {t('mcp.create')}</button>
          <label class="col-span-6 flex items-center gap-2 text-sm">
            <input type="checkbox" bind:checked={readOnly} />
            {t('mcp.readOnly')}
            <span class="text-xs text-muted">{t('mcp.readOnlyHint')}</span>
          </label>
        </form>
      </section>

      <section>
        <span class="label">{t('mcp.tokens')}</span>
        <div class="divide-y divide-line-soft rounded-md border border-line">
          {#each info?.tokens ?? [] as tok (tok.id)}
            {@const expired = tok.expires_at > 0 && tok.expires_at < Date.now()}
            <div class="flex items-center gap-3 px-3 py-2 text-sm">
              {#if tok.read_only}
                <span class="pill-ro"><Lock size={10} /> {t('admin.readOnly')}</span>
              {:else}
                <span class="pill-rw"><LockOpen size={10} /> {t('admin.readWrite')}</span>
              {/if}
              <span class="min-w-0 flex-1">
                <span class="block truncate font-medium {expired ? 'text-faint line-through' : ''}">{tok.name}</span>
                <span class="block text-xs text-muted">
                  {t('mcp.lastUsed', { when: tok.last_used_at ? fmtDateTime(tok.last_used_at) : t('mcp.never') })}
                  · {tok.expires_at ? t(expired ? 'mcp.expired' : 'mcp.expires', { when: fmtDateTime(tok.expires_at) }) : t('mcp.noExpiry')}
                </span>
              </span>
              <button type="button" class="icon-btn hover:text-danger" onclick={() => revoke(tok)} aria-label={t('mcp.revoke')} title={t('mcp.revoke')}>
                <Trash2 size={14} />
              </button>
            </div>
          {:else}
            <p class="px-3 py-3 text-center text-xs text-faint">{info ? t('mcp.noTokens') : t('common.loading')}</p>
          {/each}
        </div>
      </section>
    </div>
  {/if}
</div>
