<script lang="ts" module>
  export type Section = 'overview' | 'tables' | 'columns' | 'relationships' | 'anomalies' | 'routines';
</script>

<script lang="ts">
  // Schema documentation in the manner of SchemaSpy: an overview, every
  // table and column, the relationships, and the design anomalies, with a
  // page per table. It reads only the catalog and can be downloaded as one
  // HTML file to share.
  import { t, i18n } from '@/lib/i18n/index.svelte';
  import { RefreshCw, LoaderCircle, TriangleAlert, Search, X, Download, ArrowRight, Table2, Wrench, Lock } from 'lucide-svelte';
  import { api, qs } from '@/lib/api/client';
  import type { Anomaly, AnomalyKind, SchemaDoc, DocStatus, SavedDoc } from '@/lib/api/types';
  import { connection, openQueryTab, persist, type Tab } from '@/lib/stores/workspace.svelte';
  import { withCredentials } from '@/lib/stores/credentials.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { session } from '@/lib/stores/session.svelte';
  import { download } from '@/lib/helper/format';
  import { anomalyKinds, fixSQL, reportHTML, reportName } from '@/lib/helper/docReport';
  import { dbLabel } from '@/lib/helper/databases';
  import DocsTablePage from '@/lib/components/docs/DocsTablePage.svelte';
  import DocsOverview from '@/lib/components/docs/DocsOverview.svelte';
  import DocsLists from '@/lib/components/docs/DocsLists.svelte';

  let { tab }: { tab: Tab } = $props();

  const conn = $derived(connection(tab.connectionId));

  let data = $state<SchemaDoc | null>(null);
  let loading = $state(false);
  let error = $state('');
  let status = $state<DocStatus | null>(null);
  // Users outside the groups that build documentation only read it.
  const canBuild = $derived(session.user?.can_build_docs !== false && !status?.read_only);
  let filter = $state('');
  let section = $state<Section>('overview');
  let scroller: HTMLDivElement | undefined = $state();

  async function load(force = false) {
    if (loading) return;
    loading = true;
    error = '';
    try {
      const saved = await withCredentials(tab.connectionId, () => {
        const base = `/connections/${tab.connectionId}/docs`;
        const query = qs({ schema: tab.schema });
        return force ? api.post<SavedDoc>(`${base}/refresh${query}`) : api.get<SavedDoc>(`${base}${query}`);
      });
      data = saved.document;
      status = saved.status;
    } catch (e) {
      error = errorMessage(e);
    } finally {
      loading = false;
    }
  }

  void load();

  // Also notice scheduled/other-tab refreshes without doing catalog reads.
  $effect(() => {
    if (!data) return;
    const timer = setInterval(() => {
      if (!document.hidden && !loading) void load();
    }, status?.refreshing ? 3000 : 30000);
    return () => clearInterval(timer);
  });

  const fullTime = (at: number) => new Date(at).toLocaleString(i18n.locale, { dateStyle: 'medium', timeStyle: 'short' });

  const byName = $derived(new Map((data?.tables ?? []).map((x) => [x.name, x])));
  const current = $derived(tab.table ? byName.get(tab.table) : undefined);

  function show(name: string) {
    tab.table = name;
    persist();
    scroller?.scrollTo({ top: 0 });
  }

  function back() {
    tab.table = '';
    persist();
  }

  function go(s: Section) {
    section = s;
    if (tab.table) back();
  }

  const needle = $derived(filter.trim().toLowerCase());
  const has = (...v: (string | undefined)[]) => !needle || v.some((x) => x?.toLowerCase().includes(needle));

  const tables = $derived((data?.tables ?? []).filter((x) => has(x.name, x.comment)));
  const columns = $derived(
    (data?.tables ?? []).flatMap((tb) => tb.columns.filter((c) => has(tb.name, c.name, c.type, c.comment)).map((c) => ({ tb, c }))),
  );
  const relations = $derived(
    (data?.tables ?? []).flatMap((tb) => tb.foreign_keys.map((fk) => ({ tb, fk }))).filter(({ tb, fk }) => has(tb.name, fk.ref_table, fk.name)),
  );
  const routines = $derived((data?.routines ?? []).filter((r) => has(r.name)));

  const anomalyGroups = $derived.by(() => {
    const groups = new Map<AnomalyKind, Anomaly[]>();
    for (const a of data?.anomalies ?? []) {
      if (!has(a.table, a.ref, ...(a.columns ?? []))) continue;
      if (!groups.has(a.kind)) groups.set(a.kind, []);
      groups.get(a.kind)!.push(a);
    }
    return anomalyKinds.filter((k) => groups.has(k)).map((k) => ({ kind: k, items: groups.get(k)! }));
  });

  const stats = $derived.by(() => {
    const list = data?.tables ?? [];
    const tablesOnly = list.filter((x) => x.kind === 'table');
    return {
      tables: tablesOnly.length,
      views: list.length - tablesOnly.length,
      columns: list.reduce((n, x) => n + x.columns.length, 0),
      foreignKeys: list.reduce((n, x) => n + x.foreign_keys.length, 0),
      indexes: list.reduce((n, x) => n + x.indexes.length, 0),
      anomalies: data?.anomalies.length ?? 0,
      routines: data?.routines.length ?? 0,
      rows: tablesOnly.some((x) => x.rows !== undefined) ? tablesOnly.reduce((n, x) => n + (x.rows ?? 0), 0) : undefined,
    };
  });

  const tableAnomalies = $derived(current ? (data?.anomalies ?? []).filter((a) => a.table === current.name) : []);

  const writable = $derived(!!conn && !conn.read_only);

  function fix(a: Anomaly) {
    const sql = conn && fixSQL(conn.db_type, tab.schema, a);
    if (sql) openQueryTab(tab.connectionId, sql, t('docs.fixSQL'));
  }

  function exportHTML() {
    if (!data || !conn) return;
    download(reportName(data), reportHTML(data, conn.name, dbLabel(conn.db_type)), 'text/html');
    toast(t('docs.downloaded'), 'success');
  }

  const sections: Section[] = ['overview', 'tables', 'columns', 'relationships', 'anomalies', 'routines'];
  const count = (s: Section) =>
    !data
      ? 0
      : s === 'tables'
        ? data.tables.length
        : s === 'columns'
          ? stats.columns
          : s === 'relationships'
            ? stats.foreignKeys
            : s === 'anomalies'
              ? stats.anomalies
              : s === 'routines'
                ? stats.routines
                : 0;
</script>

{#snippet tableLink(name: string)}
  {#if byName.has(name)}
    <button class="font-medium text-accent hover:underline" onclick={() => show(name)}>{name}</button>
  {:else}
    <span class="font-medium">{name}</span>
  {/if}
{/snippet}

{#snippet anomalyItem(a: Anomaly, withTable: boolean)}
  <li class="flex items-center gap-2 py-1 text-sm">
    {#if withTable}{@render tableLink(a.table)}{/if}
    {#if a.columns?.length}<span class="font-mono text-xs text-fg-2">({a.columns.join(', ')})</span>{/if}
    {#if a.ref}
      <ArrowRight size={12} class="text-faint" />
      {#if a.kind === 'duplicate_index' || a.kind === 'nullable_unique'}
        <span class="font-mono text-xs text-muted">{a.ref}</span>
      {:else}
        {@render tableLink(a.ref)}{#if a.ref_columns?.length}<span class="font-mono text-xs text-fg-2">({a.ref_columns.join(', ')})</span>{/if}
      {/if}
    {/if}
    {#if writable && conn && fixSQL(conn.db_type, tab.schema, a)}
      <button class="btn ml-auto h-6 text-xs" onclick={() => fix(a)} title={t('docs.fixSQL')}><Wrench size={12} /> {t('docs.fixSQL')}</button>
    {/if}
  </li>
{/snippet}

<div class="flex h-full min-h-0 flex-col">
  <div class="toolbar overflow-x-auto whitespace-nowrap">
    <span class="text-sm font-medium">{t('docs.title', { schema: tab.schema })}</span>
    {#if conn}<span class="text-xs text-muted">{conn.name}</span>{/if}
    {#if data}<span class="text-xs text-muted">{t('docs.generated', { time: fullTime(data.generated_at) })}</span>{/if}
    {#if data?.truncated}<span class="text-xs text-warn">{t('docs.truncated', { n: data.tables.length, total: data.table_count })}</span>{/if}
    <div class="flex-1"></div>
    <div class="relative">
      <Search size={13} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-faint" />
      <input class="input h-7 w-52 pr-6 pl-7 text-xs" placeholder={t(section === 'columns' ? 'docs.filterColumns' : 'docs.filter')} bind:value={filter} />
      {#if filter}<button class="absolute top-1/2 right-1.5 -translate-y-1/2 text-faint hover:text-fg" onclick={() => (filter = '')} aria-label={t('common.clear')}><X size={12} /></button>{/if}
    </div>
    <button class="btn" onclick={exportHTML} disabled={!data}><Download size={14} /> {t('docs.download')}</button>
    {#if canBuild}<button class="btn shrink-0" onclick={() => load(true)} disabled={loading || status?.refreshing}><RefreshCw size={14} class={loading || status?.refreshing ? 'animate-spin' : ''} /> {t(loading || status?.refreshing ? 'docs.snapshot.refreshing' : 'docs.snapshot.refresh')}</button>{/if}
  </div>

  {#if data}
    <div class="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-line px-3 py-1.5 text-xs text-muted" role="status">
      <span>{t('docs.snapshot.stored')}</span>
      {#if status?.read_only}<span class="flex items-center gap-1.5 text-fg-2"><Lock size={12} />{t('docs.snapshotReadOnly')}</span>{/if}
      {#if status?.refreshing}<span class="flex items-center gap-1.5"><LoaderCircle size={12} class="animate-spin" />{t('docs.snapshot.refreshing')}</span>{/if}
      {#if status?.next_run}<span>{t('docs.snapshot.next', { time: fullTime(status.next_run) })}</span>{:else}<span>{t('docs.snapshot.manual')}</span>{/if}
      {#if status?.last_error}<span class="text-warn">{t('docs.snapshot.failed')}</span>{/if}
    </div>
  {/if}
  {#if error}<p role="alert" class="flex shrink-0 items-start gap-2 border-b border-line px-3 py-2 text-xs text-danger"><TriangleAlert size={14} class="shrink-0" /> {error}</p>{/if}

  {#if loading && !data}
    <div class="flex flex-1 items-center justify-center gap-2 text-sm text-muted"><LoaderCircle size={15} class="animate-spin" /> {t('common.loading')}</div>
  {:else if !data && error}
    <div class="p-3"><button class="btn-outline" onclick={() => load()}>{t('common.refresh')}</button></div>
  {:else if data && !data.tables.length}
    <div class="flex flex-1 items-center justify-center text-sm text-muted">{t('docs.empty')}</div>
  {:else if data}
    <div class="flex min-h-0 flex-1">
      <nav class="flex w-48 shrink-0 flex-col border-r border-line bg-panel py-2 text-sm">
        {#each sections as s (s)}
          <button
            class="mx-1.5 flex h-7 items-center justify-between rounded-sm px-2 text-left {section === s && !current ? 'bg-active font-medium text-fg' : 'text-fg-2 hover:bg-hover'}"
            onclick={() => go(s)}
          >
            {t(`docs.sections.${s}`)}
            {#if count(s)}<span class="text-xs tabular-nums {s === 'anomalies' ? 'text-warn' : 'text-faint'}">{count(s)}</span>{/if}
          </button>
        {/each}
        <div class="mx-3 my-2 h-px bg-line"></div>
        <div class="min-h-0 flex-1 overflow-auto">
          {#each tables as tb (tb.name)}
            <button
              class="mx-1.5 flex h-6 w-[calc(100%-0.75rem)] items-center gap-1.5 truncate rounded-sm px-2 text-left text-xs {current?.name === tb.name ? 'bg-active font-medium text-fg' : 'text-fg-2 hover:bg-hover'}"
              onclick={() => show(tb.name)}
              title={tb.name}
            >
              <Table2 size={12} class="shrink-0 {tb.kind === 'table' ? 'text-obj-table' : 'text-obj-view'}" />
              <span class="truncate">{tb.name}</span>
            </button>
          {/each}
        </div>
      </nav>

      <div class="min-h-0 flex-1 overflow-auto bg-bg" bind:this={scroller}>
        {#if current}
          <DocsTablePage {tab} {current} anomalies={tableAnomalies} {back} {tableLink} {anomalyItem} />
        {:else if section === 'overview'}
          <DocsOverview {data} {stats} {anomalyGroups} {go} {tableLink} />
        {:else}
          <DocsLists {section} keys={data.keys} {needle} {tables} {columns} {relations} {routines} {anomalyGroups} {tableLink} {anomalyItem} />
        {/if}
      </div>
    </div>
  {/if}
</div>
