<script lang="ts">
  // Schema documentation in the manner of SchemaSpy: an overview, every
  // table and column, the relationships, and the design anomalies, with a
  // page per table. It reads only the catalog and can be downloaded as one
  // HTML file to share.
  import { t, fmtNumber, i18n } from '@/lib/i18n/index.svelte';
  import {
    RefreshCw,
    LoaderCircle,
    TriangleAlert,
    Search,
    X,
    Download,
    KeyRound,
    Link,
    ArrowLeft,
    ArrowRight,
    Table2,
    ListTree,
    Network,
    Wrench,
    Info,
    Clock,
  } from 'lucide-svelte';
  import { api, qs } from '@/lib/api/client';
  import type { Anomaly, AnomalyKind, DocTable, SchemaDoc, DocStatus, SavedDoc } from '@/lib/api/types';
  import { connection, openDiagram, openQueryTab, openTableTab, persist, type Tab } from '@/lib/stores/workspace.svelte';
  import { withCredentials } from '@/lib/stores/credentials.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { download } from '@/lib/helper/format';
  import { anomalyKinds, fixSQL, reportHTML, reportName } from '@/lib/helper/docReport';
  import { dbLabel } from '@/lib/helper/databases';

  let { tab }: { tab: Tab } = $props();

  const conn = $derived(connection(tab.connectionId));

  let data = $state<SchemaDoc | null>(null);
  let loading = $state(false);
  let error = $state('');
  let status = $state<DocStatus | null>(null);
  let scheduleOpen = $state(false);
  let cronDraft = $state('');
  let scheduleError = $state('');
  let saving = $state(false);
  let filter = $state('');
  type Section = 'overview' | 'tables' | 'columns' | 'relationships' | 'anomalies' | 'routines';
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

  function toggleSchedule() {
    if (!scheduleOpen) {
      cronDraft = status?.cron_spec ?? '';
      scheduleError = '';
    }
    scheduleOpen = !scheduleOpen;
  }

  async function saveSchedule(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    scheduleError = '';
    try {
      status = await withCredentials(tab.connectionId, () => api.put<DocStatus>(
        `/connections/${tab.connectionId}/docs/schedule${qs({ schema: tab.schema })}`, { cron_spec: cronDraft.trim() },
      ));
      cronDraft = status.cron_spec;
      toast(t('docs.snapshot.saved'), 'success');
    } catch (e) {
      scheduleError = errorMessage(e);
    } finally {
      saving = false;
    }
  }

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

  const largest = $derived(
    [...(data?.tables ?? [])].filter((x) => x.rows !== undefined && x.rows > 0).sort((a, b) => b.rows! - a.rows!).slice(0, 8),
  );
  const mostReferenced = $derived(
    [...(data?.tables ?? [])].filter((x) => x.referenced_by.length).sort((a, b) => b.referenced_by.length - a.referenced_by.length).slice(0, 8),
  );

  const tableAnomalies = $derived(current ? (data?.anomalies ?? []).filter((a) => a.table === current.name) : []);

  const fkCols = (tb: DocTable) => {
    const m = new Map<string, { table: string; column: string }>();
    for (const fk of tb.foreign_keys) fk.columns.forEach((c, i) => m.set(c, { table: fk.ref_table, column: fk.ref_columns[i] ?? '' }));
    return m;
  };

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

  const kindLabel = (k: string) => t(`docs.kinds.${k}`);
  const rows = (n: number | undefined) => (n === undefined ? '—' : fmtNumber(n));

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

  const th = '[&>th]:h-8 [&>th]:border-b [&>th]:border-line [&>th]:px-2 [&>th]:text-left [&>th]:font-medium [&>th]:whitespace-nowrap';
  const td = 'border-b border-line-soft [&>td]:h-8 [&>td]:px-2 [&>td]:align-middle hover:bg-hover/60';
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
    <button class="btn shrink-0" onclick={toggleSchedule} aria-expanded={scheduleOpen}><Clock size={14} /> {t('docs.snapshot.schedule')}</button>
    <button class="btn shrink-0" onclick={() => load(true)} disabled={loading || status?.refreshing}><RefreshCw size={14} class={loading || status?.refreshing ? 'animate-spin' : ''} /> {t(loading || status?.refreshing ? 'docs.snapshot.refreshing' : 'docs.snapshot.refresh')}</button>
  </div>

  {#if scheduleOpen}
    <form class="shrink-0 space-y-2 border-b border-line bg-panel px-3 py-3" onsubmit={saveSchedule}>
      <div class="flex flex-wrap items-end gap-2">
        <div class="min-w-64 flex-1">
          <label class="label" for="doc-cron-{tab.id}">{t('docs.snapshot.cron')}</label>
          <input id="doc-cron-{tab.id}" class="input h-7 w-full font-mono text-xs" maxlength="256" placeholder="CRON_TZ=Europe/Istanbul 0 3 * * *" bind:value={cronDraft} aria-describedby="doc-cron-hint-{tab.id}" aria-invalid={!!scheduleError} disabled={saving} />
        </div>
        <button type="button" class="btn" onclick={() => (cronDraft = '@hourly')} disabled={saving}>{t('docs.snapshot.hourly')}</button>
        <button type="button" class="btn" onclick={() => (cronDraft = 'CRON_TZ=UTC 0 3 * * *')} disabled={saving}>{t('docs.snapshot.daily')}</button>
        <button type="button" class="btn" onclick={() => (cronDraft = '')} disabled={saving}>{t('docs.snapshot.off')}</button>
        <button type="submit" class="btn-primary" disabled={saving || cronDraft.trim() === (status?.cron_spec ?? '')}>{saving ? t('common.loading') : t('common.save')}</button>
      </div>
      <p id="doc-cron-hint-{tab.id}" class="max-w-4xl text-xs text-muted">{t('docs.snapshot.hint')}</p>
      {#if status?.next_run}<p class="text-xs text-muted">{t('docs.snapshot.next', { time: fullTime(status.next_run) })} <code class="ml-2 font-mono">{status.cron_spec}</code></p>{/if}
      {#if scheduleError}<p role="alert" class="text-xs text-danger">{scheduleError}</p>{/if}
    </form>
  {/if}

  {#if data}
    <div class="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-line px-3 py-1.5 text-xs text-muted" role="status">
      <span>{t('docs.snapshot.stored')}</span>
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
              <Table2 size={12} class="shrink-0 {tb.kind === 'table' ? 'text-accent' : 'text-type'}" />
              <span class="truncate">{tb.name}</span>
            </button>
          {/each}
        </div>
      </nav>

      <div class="min-h-0 flex-1 overflow-auto bg-bg" bind:this={scroller}>
        {#if current}
          {@const fks = fkCols(current)}
          <div class="mx-auto max-w-6xl space-y-6 p-5">
            <div>
              <button class="mb-2 flex items-center gap-1 text-xs text-muted hover:text-fg" onclick={back}><ArrowLeft size={12} /> {t('docs.back')}</button>
              <div class="flex flex-wrap items-center gap-3">
                <h2 class="text-base font-semibold">{current.name}</h2>
                <span class="chip">{kindLabel(current.kind)}</span>
                {#if current.rows !== undefined}<span class="text-xs text-muted" title={t('docs.rowsHint')}>~{rows(current.rows)} {t('docs.col.rows').toLowerCase()}</span>{/if}
                <div class="flex-1"></div>
                <button class="btn" onclick={() => openTableTab(tab.connectionId, tab.schema, current.name, 'data')}><Table2 size={14} /> {t('docs.openData')}</button>
                <button class="btn" onclick={() => openTableTab(tab.connectionId, tab.schema, current.name, 'structure')}><ListTree size={14} /> {t('docs.openStructure')}</button>
                <button class="btn" onclick={() => openDiagram(tab.connectionId, tab.schema)}><Network size={14} /> {t('docs.showDiagram')}</button>
              </div>
              {#if current.comment}<p class="mt-2 text-sm whitespace-pre-line text-fg-2">{current.comment}</p>{/if}
            </div>

            {#if tableAnomalies.length}
              <section class="rounded-md border border-warn/30 bg-warn-soft p-3">
                {#each tableAnomalies as a, i (i)}
                  <div class="flex items-start gap-2 text-sm">
                    <TriangleAlert size={13} class="mt-1 shrink-0 text-warn" />
                    <div class="min-w-0 flex-1">
                      <span class="font-medium">{t(`docs.anomaly.${a.kind}.title`)}</span>
                      <ul>{@render anomalyItem(a, false)}</ul>
                    </div>
                  </div>
                {/each}
              </section>
            {/if}

            <section>
              <h3 class="mb-2 text-sm font-semibold">{t('docs.sections.columns')} <span class="font-normal text-faint">{current.columns.length}</span></h3>
              <table class="w-full border-collapse text-sm">
                <thead class="text-xs text-fg-2"><tr class={th}><th class="w-8"></th><th>{t('docs.col.column')}</th><th>{t('docs.col.type')}</th><th>{t('docs.col.nullable')}</th><th>{t('docs.col.default')}</th><th>{t('docs.col.parents')}</th><th>{t('docs.col.comment')}</th></tr></thead>
                <tbody>
                  {#each current.columns as c (c.name)}
                    {@const ref = fks.get(c.name)}
                    <tr class={td}>
                      <td>
                        <span class="flex items-center gap-0.5">
                          {#if c.primary_key}<KeyRound size={12} class="text-warn" />{/if}
                          {#if ref}<Link size={12} class="text-accent" />{/if}
                        </span>
                      </td>
                      <td class="font-medium {c.primary_key ? '' : 'font-normal'}">{c.name}</td>
                      <td class="font-mono text-xs text-type">{c.type}{#if c.extra}<span class="ml-1 text-muted">{c.extra}</span>{/if}</td>
                      <td class="text-xs">{c.nullable ? t('common.yes') : t('common.no')}</td>
                      <td class="max-w-56 truncate font-mono text-xs text-fg-2" title={c.default}>{c.default}</td>
                      <td class="text-xs">{#if ref}{@render tableLink(ref.table)}<span class="text-muted">.{ref.column}</span>{/if}</td>
                      <td class="text-xs whitespace-pre-line text-fg-2">{c.comment ?? ''}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </section>

            <div class="grid gap-6 lg:grid-cols-2">
              <section>
                <h3 class="mb-2 text-sm font-semibold">{t('docs.col.parents')}</h3>
                {#if current.foreign_keys.length}
                  <ul class="space-y-1 text-sm">
                    {#each current.foreign_keys as fk (fk.name)}
                      <li class="flex flex-wrap items-center gap-1.5">
                        <span class="font-mono text-xs">({fk.columns.join(', ')})</span>
                        <ArrowRight size={12} class="text-faint" />
                        {@render tableLink(fk.ref_table)}<span class="font-mono text-xs text-fg-2">({fk.ref_columns.join(', ')})</span>
                        {#if fk.on_delete && fk.on_delete !== 'NO ACTION'}<span class="chip">ON DELETE {fk.on_delete}</span>{/if}
                      </li>
                    {/each}
                  </ul>
                {:else}<p class="text-sm text-faint">{t('docs.none')}</p>{/if}
              </section>
              <section>
                <h3 class="mb-2 text-sm font-semibold">{t('docs.col.children')}</h3>
                {#if current.referenced_by.length}
                  <ul class="space-y-1 text-sm">
                    {#each current.referenced_by as r (r.table + r.name)}
                      <li class="flex flex-wrap items-center gap-1.5">
                        {@render tableLink(r.table)}<span class="font-mono text-xs text-fg-2">({r.columns.join(', ')})</span>
                        <ArrowRight size={12} class="text-faint" />
                        <span class="font-mono text-xs">({r.ref_columns.join(', ')})</span>
                      </li>
                    {/each}
                  </ul>
                {:else}<p class="text-sm text-faint">{t('docs.none')}</p>{/if}
              </section>
            </div>

            {#if current.kind !== 'view'}
              <section>
                <h3 class="mb-2 text-sm font-semibold">{t('docs.col.indexes')}</h3>
                {#if current.indexes.length}
                  <table class="w-full border-collapse text-sm">
                    <thead class="text-xs text-fg-2"><tr class={th}><th>{t('docs.col.name')}</th><th>{t('docs.col.columns')}</th><th>{t('docs.col.unique')}</th></tr></thead>
                    <tbody>
                      {#each current.indexes as ix (ix.name)}
                        <tr class={td}>
                          <td class="flex items-center gap-1.5">{#if ix.primary}<KeyRound size={12} class="text-warn" />{/if}{ix.name}</td>
                          <td class="font-mono text-xs">{ix.columns.join(', ')}</td>
                          <td class="text-xs">{ix.unique ? t('common.yes') : ''}</td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                {:else}<p class="text-sm text-faint">{t('docs.none')}</p>{/if}
              </section>
            {/if}

            {#if current.definition}
              <section>
                <h3 class="mb-2 text-sm font-semibold">{t('docs.definition')}</h3>
                <pre class="overflow-auto rounded-md border border-line bg-sunken p-3 font-mono text-xs">{current.definition}</pre>
              </section>
            {/if}
          </div>
        {:else if section === 'overview'}
          <div class="mx-auto max-w-6xl space-y-6 p-5">
            {#if !data.keys}<p class="flex items-start gap-2 text-sm text-muted"><Info size={14} class="mt-0.5 shrink-0" /> {t('docs.noKeys')}</p>{/if}
            <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
              {#each Object.entries(stats) as [k, v] (k)}
                {#if v !== undefined}
                  <button
                    class="rounded-md border border-line bg-raised p-3 text-left hover:border-accent/50"
                    onclick={() => go(k === 'anomalies' ? 'anomalies' : k === 'routines' ? 'routines' : k === 'columns' ? 'columns' : k === 'foreignKeys' ? 'relationships' : 'tables')}
                  >
                    <div class="text-xs text-muted">{t(`docs.stats.${k}`)}</div>
                    <div class="mt-1 text-lg font-semibold tabular-nums {k === 'anomalies' && v ? 'text-warn' : ''}">{fmtNumber(v)}</div>
                  </button>
                {/if}
              {/each}
            </div>

            <div class="grid gap-6 lg:grid-cols-2">
              <section>
                <h3 class="mb-2 text-sm font-semibold">{t('docs.sections.anomalies')}</h3>
                {#if anomalyGroups.length}
                  <ul class="space-y-1">
                    {#each anomalyGroups as g (g.kind)}
                      <li>
                        <button class="flex w-full items-center gap-2 rounded-sm px-1 py-0.5 text-left text-sm hover:bg-hover" onclick={() => go('anomalies')}>
                          <TriangleAlert size={13} class="shrink-0 text-warn" />
                          <span class="flex-1">{t(`docs.anomaly.${g.kind}.title`)}</span>
                          <span class="text-xs text-muted tabular-nums">{g.items.length}</span>
                        </button>
                      </li>
                    {/each}
                  </ul>
                {:else}<p class="text-sm text-faint">{t('docs.noAnomalies')}</p>{/if}
              </section>
              <section>
                <h3 class="mb-2 text-sm font-semibold" title={t('docs.rowsHint')}>{t('docs.largest')}</h3>
                {#if largest.length}
                  <ul class="space-y-1 text-sm">
                    {#each largest as tb (tb.name)}
                      {@const max = largest[0].rows || 1}
                      <li class="flex items-center gap-2">
                        <span class="w-40 truncate">{@render tableLink(tb.name)}</span>
                        <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-sunken"><span class="block h-full rounded-full bg-accent/60" style="width: {Math.max(2, ((tb.rows ?? 0) / max) * 100)}%"></span></span>
                        <span class="w-20 text-right text-xs text-muted tabular-nums">{rows(tb.rows)}</span>
                      </li>
                    {/each}
                  </ul>
                {:else}<p class="text-sm text-faint">{t('docs.none')}</p>{/if}
              </section>
              {#if mostReferenced.length}
                <section>
                  <h3 class="mb-2 text-sm font-semibold">{t('docs.mostReferenced')}</h3>
                  <ul class="space-y-1 text-sm">
                    {#each mostReferenced as tb (tb.name)}
                      <li class="flex items-center gap-2">{@render tableLink(tb.name)}<span class="text-xs text-muted">{t('docs.referencedCount', { count: tb.referenced_by.length })}</span></li>
                    {/each}
                  </ul>
                </section>
              {/if}
            </div>
          </div>
        {:else if section === 'tables'}
          <table class="w-full border-collapse text-sm">
            <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2">
              <tr class={th}>
                <th>{t('docs.col.table')}</th><th>{t('docs.col.kind')}</th><th class="text-right!" title={t('docs.rowsHint')}>{t('docs.col.rows')}</th>
                <th class="text-right!">{t('docs.col.columns')}</th><th class="text-right!">{t('docs.col.parents')}</th><th class="text-right!">{t('docs.col.children')}</th>
                <th class="text-right!">{t('docs.col.indexes')}</th><th>{t('docs.col.comment')}</th>
              </tr>
            </thead>
            <tbody>
              {#each tables as tb (tb.name)}
                <tr class={td}>
                  <td>{@render tableLink(tb.name)}</td>
                  <td class="text-xs text-muted">{kindLabel(tb.kind)}</td>
                  <td class="text-right text-xs tabular-nums">{rows(tb.rows)}</td>
                  <td class="text-right text-xs tabular-nums">{tb.columns.length}</td>
                  <td class="text-right text-xs tabular-nums">{tb.foreign_keys.length || ''}</td>
                  <td class="text-right text-xs tabular-nums">{tb.referenced_by.length || ''}</td>
                  <td class="text-right text-xs tabular-nums">{tb.indexes.length || ''}</td>
                  <td class="max-w-96 truncate text-xs text-fg-2" title={tb.comment}>{tb.comment ?? ''}</td>
                </tr>
              {:else}
                <tr><td colspan="8" class="p-4 text-sm text-muted">{t('docs.noMatch')}</td></tr>
              {/each}
            </tbody>
          </table>
        {:else if section === 'columns'}
          <table class="w-full border-collapse text-sm">
            <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2">
              <tr class={th}><th>{t('docs.col.table')}</th><th>{t('docs.col.column')}</th><th>{t('docs.col.type')}</th><th>{t('docs.col.nullable')}</th><th>{t('docs.col.default')}</th><th>{t('docs.col.keys')}</th><th>{t('docs.col.comment')}</th></tr>
            </thead>
            <tbody>
              {#each columns.slice(0, 2000) as { tb, c } (tb.name + '.' + c.name)}
                {@const isFk = tb.foreign_keys.some((f) => f.columns.includes(c.name))}
                <tr class={td}>
                  <td class="text-xs">{@render tableLink(tb.name)}</td>
                  <td>{c.name}</td>
                  <td class="font-mono text-xs text-type">{c.type}</td>
                  <td class="text-xs">{c.nullable ? t('common.yes') : t('common.no')}</td>
                  <td class="max-w-56 truncate font-mono text-xs text-fg-2" title={c.default}>{c.default}</td>
                  <td><span class="flex items-center gap-0.5">{#if c.primary_key}<KeyRound size={12} class="text-warn" />{/if}{#if isFk}<Link size={12} class="text-accent" />{/if}</span></td>
                  <td class="max-w-96 truncate text-xs text-fg-2" title={c.comment}>{c.comment ?? ''}</td>
                </tr>
              {:else}
                <tr><td colspan="7" class="p-4 text-sm text-muted">{t('docs.noMatch')}</td></tr>
              {/each}
            </tbody>
          </table>
        {:else if section === 'relationships'}
          {#if !relations.length}
            <p class="p-5 text-sm text-muted">{needle ? t('docs.noMatch') : t('docs.noRelationships')}</p>
          {:else}
            <table class="w-full border-collapse text-sm">
              <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2">
                <tr class={th}><th>{t('docs.col.from')}</th><th></th><th>{t('docs.col.to')}</th><th>{t('docs.col.name')}</th><th>{t('docs.col.onDelete')}</th></tr>
              </thead>
              <tbody>
                {#each relations as { tb, fk } (tb.name + '.' + fk.name)}
                  <tr class={td}>
                    <td>{@render tableLink(tb.name)} <span class="font-mono text-xs text-fg-2">({fk.columns.join(', ')})</span></td>
                    <td class="w-6"><ArrowRight size={12} class="text-faint" /></td>
                    <td>{@render tableLink(fk.ref_table)} <span class="font-mono text-xs text-fg-2">({fk.ref_columns.join(', ')})</span></td>
                    <td class="text-xs text-muted">{fk.name}</td>
                    <td class="text-xs">{fk.on_delete ?? ''}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
        {:else if section === 'anomalies'}
          <div class="mx-auto max-w-5xl space-y-5 p-5">
            {#if !data.keys}<p class="flex items-start gap-2 text-sm text-muted"><Info size={14} class="mt-0.5 shrink-0" /> {t('docs.noKeys')}</p>{/if}
            {#each anomalyGroups as g (g.kind)}
              <section>
                <h3 class="flex items-center gap-2 text-sm font-semibold"><TriangleAlert size={14} class="text-warn" /> {t(`docs.anomaly.${g.kind}.title`)} <span class="font-normal text-faint">{g.items.length}</span></h3>
                <p class="mt-0.5 mb-1.5 text-xs text-muted">{t(`docs.anomaly.${g.kind}.hint`)}</p>
                <ul class="divide-y divide-line-soft border-y border-line-soft">
                  {#each g.items as a, i (i)}{@render anomalyItem(a, true)}{/each}
                </ul>
              </section>
            {:else}
              <p class="text-sm text-muted">{needle ? t('docs.noMatch') : t('docs.noAnomalies')}</p>
            {/each}
          </div>
        {:else if section === 'routines'}
          {#if !routines.length}
            <p class="p-5 text-sm text-muted">{needle ? t('docs.noMatch') : t('docs.noRoutines')}</p>
          {:else}
            <table class="w-full border-collapse text-sm">
              <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2"><tr class={th}><th>{t('docs.col.routine')}</th><th>{t('docs.col.kind')}</th></tr></thead>
              <tbody>
                {#each routines as r, i (i)}
                  <tr class={td}><td><span class="font-medium">{r.name}</span><span class="font-mono text-xs text-muted">{r.signature ?? ''}</span></td><td class="text-xs text-muted">{kindLabel(r.kind)}</td></tr>
                {/each}
              </tbody>
            </table>
          {/if}
        {/if}
      </div>
    </div>
  {/if}
</div>
