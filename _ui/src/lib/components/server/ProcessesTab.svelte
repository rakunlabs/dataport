<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { RefreshCw, LoaderCircle, TriangleAlert, Search, X, CircleStop, Power, ArrowDown, ArrowUp } from 'lucide-svelte';
  import { api } from '@/lib/api/client';
  import type { ProcessList } from '@/lib/api/types';
  import { connection, openQueryTab, type Tab } from '@/lib/stores/workspace.svelte';
  import { withCredentials } from '@/lib/stores/credentials.svelte';
  import { confirmed } from '@/lib/stores/tools.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  let { tab }: { tab: Tab } = $props();

  const conn = $derived(connection(tab.connectionId));

  let data = $state<ProcessList | null>(null);
  let loading = $state(false);
  let error = $state('');
  let filter = $state('');
  let hideIdle = $state(true);
  // Seconds between refreshes; 0 is off.
  let every = $state(0);
  let sort = $state<{ col: number; desc: boolean }>({ col: -1, desc: true });
  let busy = $state('');

  const cols = $derived(data?.result.columns ?? []);
  const idx = (name: string) => cols.findIndex((c) => c.name.toLowerCase() === name);

  const rows = $derived.by(() => {
    let list = data?.result.rows ?? [];
    const state = idx('state');
    const command = idx('command');
    if (hideIdle) {
      // dataport's own session is always busy reading this list.
      list = list.filter((r) => String(r[0]) !== data?.self);
      list = list.filter((r) => {
        const s = String(r[state] ?? r[command] ?? '').toLowerCase();
        return s !== 'idle' && s !== 'sleep' && s !== 'sleeping' && s !== 'inactive';
      });
    }
    const f = filter.trim().toLowerCase();
    if (f) list = list.filter((r) => r.some((v) => v !== null && String(v).toLowerCase().includes(f)));
    if (sort.col >= 0) {
      const c = sort.col;
      list = [...list].sort((a, b) => {
        const x = a[c];
        const y = b[c];
        const cmp = typeof x === 'number' && typeof y === 'number' ? x - y : String(x ?? '').localeCompare(String(y ?? ''));
        return sort.desc ? -cmp : cmp;
      });
    }
    return list;
  });

  async function load() {
    if (loading) return;
    loading = true;
    try {
      data = await withCredentials(tab.connectionId, () => api.get<ProcessList>(`/connections/${tab.connectionId}/processes`));
      error = '';
    } catch (e) {
      error = errorMessage(e);
    } finally {
      loading = false;
    }
  }

  void load();

  $effect(() => {
    if (!every) return;
    const timer = setInterval(() => {
      if (!document.hidden && !busy) void load();
    }, every * 1000);
    return () => clearInterval(timer);
  });

  function sortBy(i: number) {
    sort = sort.col === i ? { col: i, desc: !sort.desc } : { col: i, desc: true };
  }

  async function kill(row: unknown[], terminate: boolean) {
    const id = String(row[0]);
    const what = t(terminate ? 'processes.terminateWhat' : 'processes.cancelWhat', { id });
    if (!terminate || conn?.production || confirm(t('processes.terminateConfirm', { id }))) {
      busy = id;
      try {
        const done = await confirmed(tab.connectionId, `/connections/${tab.connectionId}/processes/kill`, { id, terminate }, what);
        if (done !== null) {
          toast(t(terminate ? 'processes.terminated' : 'processes.cancelled', { id }), 'success');
          await load();
        }
      } catch (e) {
        toast(errorMessage(e), 'error');
      } finally {
        busy = '';
      }
    }
  }

  function cell(v: unknown) {
    if (v === null || v === undefined) return '';
    return typeof v === 'object' ? JSON.stringify(v) : String(v);
  }

  const queryCol = $derived(idx('query'));
</script>

<div class="flex h-full flex-col">
  <div class="flex h-10 shrink-0 items-center gap-2 border-b border-line px-3">
    <span class="text-sm font-medium">{t('processes.title')}</span>
    {#if data}<span class="text-xs text-faint tabular-nums">{t('processes.count', { n: rows.length, total: data.result.rows?.length ?? 0 })}</span>{/if}
    {#if data?.result.truncated}<span class="text-xs text-warn">{t('processes.truncated')}</span>{/if}
    <div class="flex-1"></div>
    <div class="relative">
      <Search size={13} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-faint" />
      <input class="input h-7 w-56 pr-6 pl-7 text-xs" placeholder={t('processes.filter')} bind:value={filter} />
      {#if filter}<button class="absolute top-1/2 right-1.5 -translate-y-1/2 text-faint hover:text-fg" onclick={() => (filter = '')} aria-label={t('common.clear')}><X size={12} /></button>{/if}
    </div>
    <label class="flex items-center gap-1.5 text-xs text-fg-2"><input type="checkbox" bind:checked={hideIdle} /> {t('processes.hideIdle')}</label>
    <label class="flex items-center gap-1.5 text-xs text-fg-2">
      {t('processes.refresh')}
      <select class="input h-7 w-20 text-xs" bind:value={every}>
        <option value={0}>{t('processes.off')}</option>
        {#each [2, 5, 10, 30] as n (n)}<option value={n}>{n}s</option>{/each}
      </select>
    </label>
    <button class="icon-btn" onclick={load} title={t('common.refresh')} aria-label={t('common.refresh')}>
      <RefreshCw size={14} class={loading ? 'animate-spin' : ''} />
    </button>
  </div>

  {#if error}
    <p class="flex items-start gap-1.5 border-b border-line px-3 py-2 text-xs text-danger"><TriangleAlert size={13} class="mt-0.5 shrink-0" /> {error}</p>
  {/if}

  <div class="min-h-0 flex-1 overflow-auto">
    {#if !data && loading}
      <p class="flex items-center gap-2 p-4 text-sm text-muted"><LoaderCircle size={14} class="animate-spin" /> {t('common.loading')}</p>
    {:else if data}
      <table class="w-full border-collapse text-xs">
        <thead class="sticky top-0 z-10 bg-panel">
          <tr>
            {#each cols as c, i (c.name)}
              <th class="h-7 border-b border-line px-2 text-left font-medium whitespace-nowrap text-fg-2">
                <button class="flex items-center gap-1 hover:text-fg" onclick={() => sortBy(i)}>
                  {c.name}
                  {#if sort.col === i}{#if sort.desc}<ArrowDown size={11} />{:else}<ArrowUp size={11} />{/if}{/if}
                </button>
              </th>
            {/each}
            <th class="sticky right-0 w-px border-b border-line bg-panel px-2"></th>
          </tr>
        </thead>
        <tbody>
          {#each rows as r, n (String(r[0]) + ':' + n)}
            {@const self = String(r[0]) === data.self}
            <tr class="group border-b border-line-soft hover:bg-hover {self ? 'text-faint' : ''}">
              {#each r as v, i (i)}
                <td
                  class="h-7 px-2 align-middle {i === queryCol ? 'max-w-[36rem] truncate font-mono' : 'whitespace-nowrap'} {typeof v === 'number' ? 'text-right tabular-nums' : ''}"
                  title={i === queryCol ? cell(v) : undefined}
                >
                  {cell(v)}{#if i === 0 && self}<span class="ml-1.5 rounded bg-sunken px-1 text-[10px]">{t('processes.self')}</span>{/if}
                </td>
              {/each}
              <td class="sticky right-0 bg-bg px-1 whitespace-nowrap group-hover:bg-hover">
                <div class="flex items-center justify-end gap-0.5">
                  {#if queryCol >= 0 && r[queryCol]}
                    <button class="btn h-6 px-1.5 text-xs" onclick={() => openQueryTab(tab.connectionId, cell(r[queryCol]))}>{t('processes.open')}</button>
                  {/if}
                  {#if !self}
                    {#if data.can_cancel}
                      <button class="icon-btn size-6" disabled={busy === String(r[0])} onclick={() => kill(r, false)} title={t('processes.cancel')} aria-label={t('processes.cancel')}>
                        <CircleStop size={13} />
                      </button>
                    {/if}
                    <button class="icon-btn size-6 hover:text-danger" disabled={busy === String(r[0])} onclick={() => kill(r, true)} title={t('processes.terminate')} aria-label={t('processes.terminate')}>
                      <Power size={13} />
                    </button>
                  {/if}
                </div>
              </td>
            </tr>
          {:else}
            <tr><td colspan={cols.length + 1} class="p-6 text-center text-sm text-muted">{t('processes.empty')}</td></tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
  <p class="shrink-0 border-t border-line px-3 py-1.5 text-xs text-faint">{t('processes.hint')}</p>
</div>
