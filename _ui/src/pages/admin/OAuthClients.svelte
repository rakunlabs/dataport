<script lang="ts">
  import { t, i18n } from '@/lib/i18n/index.svelte';
  import { Bot, Trash2, RefreshCw, Search, X } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import Pager from '@/lib/components/ui/Pager.svelte';
  import { api } from '@/lib/api/client';
  import { pageSize } from '@/pages/admin/admin.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  // MCP clients that registered themselves (OAuth), such as Claude Code:
  // each registration, the tokens it holds and when it was used last.
  interface Client {
    id: string;
    name: string;
    redirect_uris: string[];
    created_at: number;
    tokens: number;
    users: number;
    last_used_at: number;
  }

  let clients = $state<Client[] | null>(null);
  let loading = $state(false);
  let query = $state('');
  let page = $state(0);

  async function load() {
    loading = true;
    try {
      clients = await api.get<Client[]>('/admin/oauth/clients');
    } catch (e) {
      toast(errorMessage(e), 'error');
    } finally {
      loading = false;
    }
  }

  void load();

  const filtered = $derived.by(() => {
    const q = query.trim().toLowerCase();
    const list = clients ?? [];
    return q ? list.filter((c) => c.name.toLowerCase().includes(q) || c.redirect_uris.some((u) => u.toLowerCase().includes(q))) : list;
  });
  const shown = $derived(filtered.slice(page * pageSize, (page + 1) * pageSize));
  $effect(() => {
    void query;
    page = 0;
  });

  async function remove(c: Client) {
    if (!confirm(t('admin.oauthClients.deleteConfirm', { name: c.name, count: c.tokens }))) return;
    try {
      await api.del(`/admin/oauth/clients/${encodeURIComponent(c.id)}`);
      await load();
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  const fullTime = (at: number) => (at ? new Date(at).toLocaleString(i18n.locale, { dateStyle: 'medium', timeStyle: 'short' }) : '—');
</script>

<Page title={t('admin.oauthClients.title')} description={t('admin.oauthClients.description')}>
  {#snippet actions()}
    <div class="relative">
      <Search size={13} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-faint" />
      <input class="input h-7 w-56 pr-6 pl-7 text-xs" placeholder={t('admin.oauthClients.search')} aria-label={t('admin.oauthClients.search')} bind:value={query} />
      {#if query}<button class="absolute top-1/2 right-1.5 -translate-y-1/2 text-faint hover:text-fg" onclick={() => (query = '')} aria-label={t('common.clear')}><X size={12} /></button>{/if}
    </div>
    <button class="icon-btn" onclick={load} disabled={loading} title={t('common.refresh')} aria-label={t('common.refresh')}>
      <RefreshCw size={14} class={loading ? 'animate-spin' : ''} />
    </button>
  {/snippet}

  <table class="table">
    <thead>
      <tr>
        <th>{t('admin.oauthClients.client')}</th>
        <th>{t('admin.oauthClients.redirect')}</th>
        <th class="w-28 text-right">{t('admin.oauthClients.tokens')}</th>
        <th class="w-44">{t('admin.oauthClients.registered')}</th>
        <th class="w-44">{t('admin.oauthClients.lastUsed')}</th>
        <th class="w-12"></th>
      </tr>
    </thead>
    <tbody>
      {#each shown as c (c.id)}
        <tr>
          <td class="py-1.5">
            <div class="flex items-center gap-2 font-medium"><Bot size={14} class="shrink-0 text-accent" />{c.name}</div>
            <div class="pl-[22px] font-mono text-2xs text-faint">{c.id}</div>
          </td>
          <td class="font-mono text-xs text-muted">
            {#each c.redirect_uris as u (u)}<div class="max-w-80 truncate" title={u}>{u}</div>{/each}
          </td>
          <td class="text-right text-xs tabular-nums">
            {#if c.tokens}{t('admin.oauthClients.tokenCount', { count: c.tokens, users: c.users })}{:else}<span class="text-faint">{t('admin.oauthClients.unused')}</span>{/if}
          </td>
          <td class="text-xs text-muted">{fullTime(c.created_at)}</td>
          <td class="text-xs text-muted">{fullTime(c.last_used_at)}</td>
          <td class="text-right">
            <button class="icon-btn hover:text-danger" onclick={() => remove(c)} title={t('admin.oauthClients.delete')} aria-label={t('admin.oauthClients.delete')}><Trash2 size={14} /></button>
          </td>
        </tr>
      {:else}
        <tr><td colspan="6" class="h-24 text-center text-muted">{clients === null ? t('common.loading') : query ? t('admin.oauthClients.noMatch') : t('admin.oauthClients.empty')}</td></tr>
      {/each}
    </tbody>
  </table>
  <Pager bind:page size={pageSize} total={filtered.length} />
</Page>
