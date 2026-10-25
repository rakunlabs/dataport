<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { X, Search, RefreshCw, Trash2, CircleCheck, CircleX, Ban, Unplug } from 'lucide-svelte';
  import { api, qs } from '@/lib/api/client';
  import type { QueryLog } from '@/lib/api/types';
  import { connection, deleteSaved, loadSaved, openQueryTab, ws } from '@/lib/stores/workspace.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { formatDuration, formatTime } from '@/lib/helper/format';
  import DbIcon from '@/lib/components/DbIcon.svelte';

  let history = $state<QueryLog[]>([]);
  let search = $state('');
  let loading = $state(false);

  async function loadHistory() {
    loading = true;
    try {
      history = await api.get<QueryLog[]>('/history' + qs({ search, limit: 200 }));
    } catch (e) {
      toast(errorMessage(e), 'error');
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    if (ws.rightPanel === 'history') void loadHistory();
    if (ws.rightPanel === 'saved') void loadSaved().catch((e) => toast(errorMessage(e), 'error'));
  });

  let timer: ReturnType<typeof setTimeout>;
  function onsearch() {
    clearTimeout(timer);
    timer = setTimeout(loadHistory, 250);
  }

  const savedFiltered = $derived(
    ws.saved.filter((q) => !search || q.name.toLowerCase().includes(search.toLowerCase()) || q.sql.toLowerCase().includes(search.toLowerCase())),
  );

  async function remove(id: string, name: string) {
    if (!confirm(t('panel.deleteSaved', { name }))) return;
    try {
      await deleteSaved(id);
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  function openLog(l: QueryLog) {
    const conn = connection(l.connection_id) ? l.connection_id : ws.connections[0]?.id;
    if (conn) openQueryTab(conn, l.sql);
  }
</script>

<aside class="flex h-full flex-col bg-panel">
  <div class="flex h-9 shrink-0 items-center gap-1 border-b border-line pr-1.5 pl-3">
    <span class="flex-1 text-xs font-semibold text-fg-2">{ws.rightPanel === 'history' ? t('app.history') : t('app.saved')}</span>
    {#if ws.rightPanel === 'history'}
      <button class="icon-btn size-6" onclick={loadHistory} aria-label={t('common.refresh')}><RefreshCw size={13} class={loading ? 'animate-spin' : ''} /></button>
    {/if}
    <button class="icon-btn size-6" onclick={() => (ws.rightPanel = '')} aria-label={t('common.close')}><X size={14} /></button>
  </div>

  <div class="p-1.5">
    <div class="relative">
      <Search size={13} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-faint" />
      <input class="input h-[26px] pl-7 text-xs" placeholder={t('common.search')} bind:value={search} oninput={onsearch} />
    </div>
  </div>

  <div class="min-h-0 flex-1 overflow-auto">
    {#if ws.rightPanel === 'history'}
      {#each history as l (l.id)}
        <button class="block w-full border-b border-line-soft px-3 py-2 text-left hover:bg-hover" onclick={() => openLog(l)} title={t('panel.openInTab')}>
          <div class="mb-1 flex items-center gap-1.5 text-xs text-muted">
            {#if l.status === 'ok'}<CircleCheck size={12} class="text-ok" />
            {:else if l.status === 'rejected'}<Ban size={12} class="text-warn" />
            {:else if l.status === 'unreachable'}<Unplug size={12} class="text-danger" />
            {:else}<CircleX size={12} class="text-danger" />{/if}
            <span class="truncate">{l.connection_name}</span>
            <span class="ml-auto shrink-0">{formatTime(l.created_at)}</span>
          </div>
          <pre class="line-clamp-3 font-mono text-xs whitespace-pre-wrap [overflow-wrap:anywhere] [word-break:normal] text-fg">{l.sql}</pre>
          <div class="mt-1 flex gap-2 text-2xs text-muted tabular-nums">
            <span>{formatDuration(l.duration_ms)}</span>
            <span>{t('panel.rows', { count: l.row_count })}</span>
          </div>
          {#if l.error}<p class="mt-1 line-clamp-2 text-[11px] text-danger">{l.error}</p>{/if}
        </button>
      {:else}
        <p class="px-4 py-8 text-center text-xs text-muted">{loading ? t('common.loading') : t('panel.noQueries')}</p>
      {/each}
    {:else}
      {#each savedFiltered as q (q.id)}
        {@const conn = connection(q.connection_id)}
        <div class="group flex items-start gap-2 border-b border-line-soft px-3 py-2 hover:bg-hover">
          <button
            class="min-w-0 flex-1 text-left"
            onclick={() => {
              const existing = ws.tabs.find((t) => t.savedId === q.id);
              if (existing) ws.activeId = existing.id;
              else openQueryTab(conn ? q.connection_id : (ws.connections[0]?.id ?? ''), q.sql, q.name, q.id);
            }}
          >
            <div class="flex items-center gap-1.5">
              {#if conn}<DbIcon type={conn.db_type} size={12} />{/if}
              <span class="truncate text-sm font-medium">{q.name}</span>
            </div>
            <pre class="mt-1 line-clamp-2 font-mono text-xs whitespace-pre-wrap [overflow-wrap:anywhere] [word-break:normal] text-muted">{q.sql}</pre>
          </button>
          <button class="icon-btn size-6 opacity-0 group-hover:opacity-100" onclick={() => remove(q.id, q.name)} aria-label={t('common.delete')}>
            <Trash2 size={13} />
          </button>
        </div>
      {:else}
        <p class="px-4 py-8 text-center text-xs text-muted">{t('panel.noSaved')}</p>
      {/each}
    {/if}
  </div>
</aside>
