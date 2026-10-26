<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { RefreshCw, CircleCheck, CircleX, Ban, Unplug } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import { api, qs } from '@/lib/api/client';
  import type { QueryLog } from '@/lib/api/types';
  import { admin, loadConnections, loadUsers } from '@/pages/admin/admin.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { formatDuration, formatTime } from '@/lib/helper/format';

  const pageSize = 100;

  let logs = $state<QueryLog[]>([]);
  let filter = $state({ user_id: '', connection_id: '', source: '', search: '' });
  let loading = $state(false);
  let more = $state(false);
  let expanded = $state<string | null>(null);

  void Promise.all([loadUsers(), loadConnections()]).catch(() => {});

  async function load(append = false) {
    loading = true;
    try {
      const before = append ? logs[logs.length - 1]?.created_at : undefined;
      const page = await api.get<QueryLog[]>('/admin/logs' + qs({ ...filter, before, limit: pageSize }));
      logs = append ? [...logs, ...page] : page;
      more = page.length === pageSize;
    } catch (e) {
      toast(errorMessage(e), 'error');
    } finally {
      loading = false;
    }
  }

  void load();

  let timer: ReturnType<typeof setTimeout>;
  function changed() {
    clearTimeout(timer);
    timer = setTimeout(() => load(), 250);
  }
</script>

<Page title={t('admin.logs')} description={t('admin.log.description')}>
  {#snippet actions()}
    <button class="btn-outline" onclick={() => load()}><RefreshCw size={14} class={loading ? 'animate-spin' : ''} /> {t('common.refresh')}</button>
  {/snippet}

  <div class="sticky top-0 z-[2] flex gap-2 border-b border-line bg-panel px-4 py-2">
    <select class="input w-48" bind:value={filter.user_id} onchange={changed}>
      <option value="">{t('admin.log.allUsers')}</option>
      {#each admin.users as u (u.id)}<option value={u.id}>{u.username}</option>{/each}
    </select>
    <select class="input w-56" bind:value={filter.connection_id} onchange={changed}>
      <option value="">{t('admin.log.allConnections')}</option>
      {#each admin.connections as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
    </select>
    <select class="input w-36" bind:value={filter.source} onchange={changed} aria-label={t('admin.log.source')}>
      <option value="">{t('admin.log.allSources')}</option>
      <option value="web">{t('admin.log.sourceWeb')}</option>
      <option value="mcp">MCP</option>
    </select>
    <input class="input flex-1" placeholder={t('admin.log.search')} bind:value={filter.search} oninput={changed} />
  </div>

    <table class="table table-fixed">
      <thead>
        <tr>
          <th class="w-8"></th>
          <th class="w-40">{t('admin.log.time')}</th>
          <th class="w-32">{t('admin.log.user')}</th>
          <th class="w-40">{t('admin.log.connection')}</th>
          <th>SQL</th>
          <th class="w-20 text-right">{t('admin.log.rows')}</th>
          <th class="w-20 text-right">{t('admin.log.duration')}</th>
        </tr>
      </thead>
      <tbody>
        {#each logs as l (l.id)}
          <tr class="cursor-pointer align-top" onclick={() => (expanded = expanded === l.id ? null : l.id)}>
            <td class="py-2">
              {#if l.status === 'ok'}<CircleCheck size={14} class="text-ok" />
              {:else if l.status === 'rejected'}<Ban size={14} class="text-warn" />
              {:else if l.status === 'unreachable'}<Unplug size={14} class="text-danger" />
              {:else}<CircleX size={14} class="text-danger" />{/if}
            </td>
            <td class="py-2 text-xs text-muted tabular-nums">{formatTime(l.created_at)}</td>
            <td class="truncate py-2">{l.username}{#if l.source === 'mcp'} <span class="chip ml-1" title={t('admin.log.viaMcp')}>MCP</span>{/if}</td>
            <td class="truncate py-2 text-fg-2">{l.connection_name}</td>
            <td class="py-2">
              <pre class="font-mono text-xs {expanded === l.id ? 'whitespace-pre-wrap [overflow-wrap:anywhere]' : 'truncate'}">{l.sql}</pre>
              {#if l.error}<p class="mt-1 text-xs text-danger {expanded === l.id ? '' : 'line-clamp-1'}">{l.error}</p>{/if}
            </td>
            <td class="py-2 text-right text-xs tabular-nums">{l.row_count}</td>
            <td class="py-2 text-right text-xs tabular-nums">{formatDuration(l.duration_ms)}</td>
          </tr>
        {:else}
          <tr><td colspan="7" class="h-24 text-center text-muted">{loading ? t('common.loading') : t('admin.log.empty')}</td></tr>
        {/each}
      </tbody>
    </table>

  {#if more}
    <div class="flex justify-center py-3">
      <button class="btn-outline" onclick={() => load(true)} disabled={loading}>{t('admin.log.loadMore')}</button>
    </div>
  {/if}
</Page>
