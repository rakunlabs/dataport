<script lang="ts">
  // The documentation page the top bar opens: every schema of the user's
  // connections, whether its documentation was built and when, and the
  // server's refresh schedule. Opening a schema shows its documentation tab.
  import { onMount } from 'svelte';
  import { BookOpen, RefreshCw, LoaderCircle, TriangleAlert, ChevronRight, ChevronDown, Search, Settings, CircleCheck, Circle } from 'lucide-svelte';
  import { api } from '@/lib/api/client';
  import type { DocInfo, DocList } from '@/lib/api/types';
  import { t, i18n } from '@/lib/i18n/index.svelte';
  import { loadSchemas, openDocs, openSettings, ws, workspaceConnections } from '@/lib/stores/workspace.svelte';
  import { session } from '@/lib/stores/session.svelte';
  import { errorMessage } from '@/lib/stores/toast.svelte';
  import DbIcon from '@/lib/components/ui/DbIcon.svelte';

  let list = $state<DocList | null>(null);
  let loading = $state(false);
  let error = $state('');
  let filter = $state('');
  // Connections whose schemas are shown; those with documentation open
  // by themselves.
  let open = $state<Record<string, boolean>>({});

  async function load() {
    loading = true;
    error = '';
    try {
      list = await api.get<DocList>('/docs');
      for (const d of list.docs) if (open[d.connection_id] === undefined) open[d.connection_id] = true;
    } catch (e) {
      error = errorMessage(e);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    // With a few connections every schema shows at once; with many, only
    // those with documentation open by themselves.
    if (workspaceConnections().length <= 5) for (const c of workspaceConnections()) open[c.id] = true;
    void load();
  });

  // Follow a refresh in progress, slowly otherwise.
  $effect(() => {
    const every = list?.run.running || list?.docs.some((d) => d.refreshing) ? 3000 : 30000;
    const timer = setInterval(() => !document.hidden && void load(), every);
    return () => clearInterval(timer);
  });

  // Schemas of open connections come from the tree, loaded on demand.
  $effect(() => {
    for (const c of workspaceConnections()) if (open[c.id]) void loadSchemas(c.id, 'failed');
  });

  const byKey = $derived(new Map((list?.docs ?? []).map((d) => [`${d.connection_id}|${d.schema}`, d])));
  const counts = $derived(
    (list?.docs ?? []).reduce<Record<string, number>>((m, d) => ((m[d.connection_id] = (m[d.connection_id] ?? 0) + (d.ready ? 1 : 0)), m), {}),
  );

  function schemasOf(connId: string): string[] {
    const loaded = ws.schemas[connId]?.items ?? [];
    // A saved document of a schema the tree no longer lists still shows.
    const saved = (list?.docs ?? []).filter((d) => d.connection_id === connId).map((d) => d.schema);
    const all = [...new Set([...loaded, ...saved])];
    const f = filter.trim().toLowerCase();
    return f ? all.filter((s) => s.toLowerCase().includes(f)) : all;
  }

  const fullTime = (at: number) => new Date(at).toLocaleString(i18n.locale, { dateStyle: 'medium', timeStyle: 'short' });

  function ago(at: number): string {
    const s = Math.max(0, Math.round((Date.now() - at) / 1000));
    const rtf = new Intl.RelativeTimeFormat(i18n.locale, { numeric: 'auto' });
    if (s < 60) return rtf.format(-s, 'second');
    if (s < 3600) return rtf.format(-Math.round(s / 60), 'minute');
    if (s < 86400) return rtf.format(-Math.round(s / 3600), 'hour');
    return rtf.format(-Math.round(s / 86400), 'day');
  }

  function docState(d: DocInfo | undefined) {
    if (!d || !d.ready) return { icon: Circle, cls: 'text-faint', text: d?.refreshing ? t('docsHub.refreshing') : t('docsHub.notBuilt') };
    if (d.refreshing) return { icon: LoaderCircle, cls: 'animate-spin text-accent', text: t('docsHub.refreshing') };
    if (d.last_error) return { icon: TriangleAlert, cls: 'text-warn', text: t('docsHub.failed') };
    return { icon: CircleCheck, cls: 'text-ok', text: t('docsHub.built', { time: ago(d.generated_at) }) };
  }
</script>

<div class="flex h-full flex-col">
  <div class="toolbar">
    <BookOpen size={15} class="ml-1 text-muted" />
    <h1 class="text-sm font-semibold">{t('docsHub.title')}</h1>
    <div class="relative ml-3 w-56">
      <Search size={13} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-faint" />
      <input class="input h-[26px] pl-7 text-xs" bind:value={filter} placeholder={t('docsHub.search')} aria-label={t('docsHub.search')} />
    </div>
    <div class="flex-1"></div>
    {#if session.user?.role === 'admin'}
      <button class="btn" onclick={() => openSettings('docs')}><Settings size={14} /> {t('docsHub.settings')}</button>
    {/if}
    <button class="icon-btn" onclick={load} disabled={loading} title={t('common.refresh')} aria-label={t('common.refresh')}>
      <RefreshCw size={14} class={loading ? 'animate-spin' : ''} />
    </button>
  </div>

  {#if list}
    <div class="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-b border-line px-4 py-2 text-xs text-muted" role="status" aria-live="polite">
      {#if list.run.running}
        <span class="flex items-center gap-1.5 text-fg-2"><LoaderCircle size={12} class="animate-spin text-accent" /> {t('docsHub.running', { done: list.run.done, total: list.run.total })}</span>
      {:else if list.run.finished_at}
        <span title={fullTime(list.run.finished_at)}>{t('docsHub.lastRun', { time: ago(list.run.finished_at) })}</span>
      {/if}
      <span>{list.next_run ? t('docsHub.nextRun', { time: fullTime(list.next_run) }) : t('docsHub.scheduleOff')}</span>
    </div>
  {/if}

  <div class="min-h-0 flex-1 overflow-auto">
    <p class="max-w-3xl px-4 pt-3 pb-2 text-xs text-muted">{t('docsHub.description')}</p>

    {#if error}
      <p class="mx-4 flex items-start gap-1.5 rounded-md border border-danger/30 bg-danger-soft p-2 text-xs text-danger"><TriangleAlert size={13} class="mt-0.5 shrink-0" /> {error}</p>
    {:else if !workspaceConnections().length}
      <p class="px-4 py-6 text-sm text-muted">{t('docsHub.empty')}</p>
    {/if}

    <div class="px-2 pb-6">
      {#each workspaceConnections() as c (c.id)}
        {@const node = ws.schemas[c.id]}
        {@const expanded = !!open[c.id]}
        <div class="mt-1">
          <button class="flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-sm hover:bg-hover" onclick={() => (open[c.id] = !expanded)} aria-expanded={expanded}>
            {#if expanded}<ChevronDown size={14} class="text-faint" />{:else}<ChevronRight size={14} class="text-faint" />{/if}
            <DbIcon type={c.db_type} size={14} />
            <span class="font-medium">{c.name}</span>
            {#if counts[c.id]}<span class="rounded bg-sunken px-1.5 text-[11px] text-muted tabular-nums">{counts[c.id]}</span>{/if}
          </button>
          {#if expanded}
            {#if node?.loading && !node.loaded}
              <p class="flex h-7 items-center gap-1.5 pl-9 text-xs text-muted"><LoaderCircle size={12} class="animate-spin" /> {t('common.loading')}</p>
            {:else if node?.error}
              <p class="flex h-7 items-center gap-1.5 pl-9 text-xs text-danger"><TriangleAlert size={12} /> {node.error}</p>
            {/if}
            {@const schemas = schemasOf(c.id)}
            {#if node?.loaded && !schemas.length}
              <p class="h-7 pl-9 text-xs text-faint">{t('docsHub.noSchemas')}</p>
            {/if}
            {#each schemas as s (s)}
              {@const d = byKey.get(`${c.id}|${s}`)}
              {@const st = docState(d)}
              <button
                class="group flex h-8 w-full items-center gap-2 rounded-md pr-2 pl-9 text-left text-sm hover:bg-hover"
                onclick={() => openDocs(c.id, s)}
                title={d?.ready ? fullTime(d.generated_at) : ''}
              >
                <span class="min-w-0 flex-1 truncate font-mono text-xs">{s}</span>
                <span class="flex items-center gap-1.5 text-xs text-muted"><st.icon size={12} class={st.cls} /> {st.text}</span>
                <span class="flex w-16 items-center justify-end gap-0.5 text-xs text-accent opacity-0 group-hover:opacity-100">{t('docsHub.open')} <ChevronRight size={12} /></span>
              </button>
            {/each}
          {/if}
        </div>
      {/each}
    </div>
  </div>
</div>
