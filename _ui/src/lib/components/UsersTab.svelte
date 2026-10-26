<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { RefreshCw, LoaderCircle, TriangleAlert, Search, X, UserRound, UsersRound, Lock, Plus, KeyRound, ShieldCheck, ShieldOff, Trash2, LockOpen } from 'lucide-svelte';
  import { api, qs } from '@/lib/api/client';
  import type { DbUser, ExecResult } from '@/lib/api/types';
  import { connection, loadSchemas, openQueryTab, ws, type Tab } from '@/lib/stores/workspace.svelte';
  import { confirmed } from '@/lib/stores/tools.svelte';
  import { withCredentials } from '@/lib/stores/credentials.svelte';
  import { reviewSQL } from '@/lib/stores/review.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import * as usql from '@/lib/helper/userSql';

  let { tab }: { tab: Tab } = $props();

  const conn = $derived(connection(tab.connectionId));
  const db = $derived(conn?.db_type ?? 'postgres');
  const writes = $derived(!!conn && !conn.read_only);
  const mysqlish = $derived(db === 'mysql' || db === 'mariadb');

  let users = $state<DbUser[]>([]);
  let loading = $state(false);
  let error = $state('');
  let filter = $state('');
  let selected = $state<DbUser | null>(null);
  let grants = $state<string[]>([]);
  let grantsLoading = $state(false);
  let grantsError = $state('');

  // The form on the right: new user or grant/revoke.
  let form = $state<'' | 'create' | 'grant'>('');
  let newName = $state('');
  let newHost = $state('%');
  let newPassword = $state('');
  let privs = $state<string[]>(['SELECT']);
  let target = $state({ schema: '', table: '' });
  let revoke = $state(false);

  const key = (u: DbUser) => u.name + '@' + (u.host ?? '');

  const shown = $derived.by(() => {
    const f = filter.trim().toLowerCase();
    return f ? users.filter((u) => u.name.toLowerCase().includes(f) || (u.host ?? '').toLowerCase().includes(f)) : users;
  });

  async function load() {
    loading = true;
    try {
      users = await withCredentials(tab.connectionId, () => api.get<DbUser[]>(`/connections/${tab.connectionId}/users`));
      error = '';
      if (selected) {
        const again = users.find((u) => key(u) === key(selected!));
        selected = again ?? null;
        if (again) void loadGrants(again);
      }
    } catch (e) {
      error = errorMessage(e);
    } finally {
      loading = false;
    }
  }

  async function loadGrants(u: DbUser) {
    grantsLoading = true;
    grantsError = '';
    try {
      grants = await withCredentials(tab.connectionId, () => api.get<string[]>(`/connections/${tab.connectionId}/grants${qs({ name: u.name, host: u.host })}`));
    } catch (e) {
      grants = [];
      grantsError = errorMessage(e);
    } finally {
      grantsLoading = false;
    }
  }

  void load();
  $effect(() => {
    void loadSchemas(tab.connectionId);
  });

  function pick(u: DbUser) {
    selected = u;
    form = '';
    void loadGrants(u);
  }

  // review shows the SQL and runs it here, without a query tab: passwords
  // would otherwise be kept with the saved tabs. The server masks them in
  // the query log.
  async function review(title: string, sql: string, danger = false, input?: { label: string; value: string; build: (v: string) => string }) {
    const r = await reviewSQL({ title, connection: conn?.name ?? '', sql, danger, input });
    if (r.answer === 'edit') openQueryTab(tab.connectionId, r.sql, title);
    if (r.answer !== 'run') return false;
    try {
      const res = await confirmed<ExecResult>(tab.connectionId, `/connections/${tab.connectionId}/query`, { sql: r.sql }, r.sql);
      if (!res) return false;
      const failed = res.results.find((x) => x.error);
      if (failed) {
        toast(failed.error!, 'error');
        return false;
      }
      toast(t('users.done'), 'success');
      await load();
      return true;
    } catch (e) {
      toast(errorMessage(e), 'error');
      return false;
    }
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    if (!newName.trim()) return;
    if (await review(t('users.create'), usql.createUserSQL(db, newName.trim(), newPassword, newHost.trim() || '%'))) {
      form = '';
      newName = '';
      newPassword = '';
    }
  }

  function changePassword(u: DbUser) {
    void review(t('users.password'), '', false, { label: t('users.newPassword'), value: '', build: (v) => usql.passwordSQL(db, u, v) });
  }

  async function grant(e: SubmitEvent) {
    e.preventDefault();
    if (!selected) return;
    if (await review(revoke ? t('users.revoke') : t('users.grant'), usql.grantSQL(db, selected, privs, target.schema, target.table, revoke), revoke)) form = '';
  }

  function toggle(p: string) {
    privs = privs.includes(p) ? privs.filter((x) => x !== p) : [...privs, p];
  }

  const schemas = $derived(ws.schemas[tab.connectionId]?.items ?? []);
  const lockable = $derived(['mysql', 'mariadb', 'mssql', 'oracle', 'postgres'].includes(db));
</script>

<div class="flex h-full flex-col">
  <div class="flex h-10 shrink-0 items-center gap-2 border-b border-line px-3">
    <span class="text-sm font-medium">{t('users.title')}</span>
    <span class="text-xs text-faint tabular-nums">{users.length}</span>
    <div class="flex-1"></div>
    {#if writes}
      <button class="btn" onclick={() => ((form = 'create'), (selected = null))}><Plus size={13} /> {t('users.create')}</button>
    {/if}
    <button class="icon-btn" onclick={load} title={t('common.refresh')} aria-label={t('common.refresh')}><RefreshCw size={14} class={loading ? 'animate-spin' : ''} /></button>
  </div>

  {#if error}
    <p class="flex items-start gap-1.5 border-b border-line px-3 py-2 text-xs text-danger"><TriangleAlert size={13} class="mt-0.5 shrink-0" /> {error}</p>
  {/if}

  <div class="flex min-h-0 flex-1">
    <div class="flex w-72 shrink-0 flex-col border-r border-line">
      <div class="p-1.5">
        <div class="relative">
          <Search size={13} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-faint" />
          <input class="input h-[26px] pr-6 pl-7 text-xs" placeholder={t('users.filter')} bind:value={filter} />
          {#if filter}<button class="absolute top-1/2 right-1.5 -translate-y-1/2 text-faint hover:text-fg" onclick={() => (filter = '')} aria-label={t('common.clear')}><X size={12} /></button>{/if}
        </div>
      </div>
      <div class="min-h-0 flex-1 overflow-auto pb-2">
        {#if loading && !users.length}
          <p class="flex items-center gap-2 px-3 py-2 text-xs text-muted"><LoaderCircle size={12} class="animate-spin" /> {t('common.loading')}</p>
        {/if}
        {#each shown as u (key(u))}
          {@const active = selected && key(selected) === key(u)}
          <button class="flex h-8 w-full items-center gap-2 px-3 text-left text-sm {active ? 'bg-row-active' : 'hover:bg-hover'}" onclick={() => pick(u)}>
            {#if u.login}<UserRound size={13} class="shrink-0 text-accent" />{:else}<UsersRound size={13} class="shrink-0 text-type" />{/if}
            <span class="min-w-0 flex-1 truncate">{u.name}{#if u.host}<span class="text-faint">@{u.host}</span>{/if}</span>
            {#if u.locked}<Lock size={12} class="shrink-0 text-warn" />{/if}
          </button>
        {/each}
      </div>
    </div>

    <div class="min-w-0 flex-1 overflow-auto px-5 py-4">
      {#if form === 'create'}
        <form class="max-w-md space-y-3" onsubmit={create}>
          <h3 class="text-sm font-semibold">{t('users.create')}</h3>
          <div>
            <label class="label" for="u-name">{t('users.name')}</label>
            <!-- svelte-ignore a11y_autofocus -->
            <input id="u-name" class="input" bind:value={newName} autofocus required autocomplete="off" />
          </div>
          {#if mysqlish}
            <div>
              <label class="label" for="u-host">{t('users.host')}</label>
              <input id="u-host" class="input font-mono" bind:value={newHost} />
            </div>
          {/if}
          <div>
            <label class="label" for="u-pw">{t('users.password')}</label>
            <input id="u-pw" type="password" class="input" bind:value={newPassword} autocomplete="new-password" />
          </div>
          <p class="text-xs text-muted">{t('users.reviewHint')}</p>
          <div class="flex gap-2">
            <button class="btn-primary" disabled={!newName.trim()}>{t('users.reviewSql')}</button>
            <button type="button" class="btn-outline" onclick={() => (form = '')}>{t('common.cancel')}</button>
          </div>
        </form>
      {:else if selected}
        {@const u = selected}
        <div class="flex flex-wrap items-center gap-2">
          {#if u.login}<UserRound size={16} class="text-accent" />{:else}<UsersRound size={16} class="text-type" />{/if}
          <h3 class="text-base font-semibold">{u.name}{#if u.host}<span class="text-faint">@{u.host}</span>{/if}</h3>
          {#if u.locked}<span class="rounded bg-warn/15 px-1.5 py-0.5 text-xs text-warn">{t('users.locked')}</span>{/if}
          {#if !u.login}<span class="rounded bg-sunken px-1.5 py-0.5 text-xs text-muted">{t('users.role')}</span>{/if}
        </div>
        {#if u.attributes}<p class="mt-1 font-mono text-xs text-muted">{u.attributes}</p>{/if}

        {#if writes}
          <div class="mt-3 flex flex-wrap gap-2">
            <button class="btn" onclick={() => ((form = 'grant'), (revoke = false))}><ShieldCheck size={13} /> {t('users.grant')}</button>
            <button class="btn" onclick={() => ((form = 'grant'), (revoke = true))}><ShieldOff size={13} /> {t('users.revoke')}</button>
            {#if u.login}<button class="btn" onclick={() => changePassword(u)}><KeyRound size={13} /> {t('users.password')}</button>{/if}
            {#if lockable}
              <button class="btn" onclick={() => review(u.locked ? t('users.unlock') : t('users.lock'), usql.lockSQL(db, u, !u.locked))}>
                {#if u.locked}<LockOpen size={13} /> {t('users.unlock')}{:else}<Lock size={13} /> {t('users.lock')}{/if}
              </button>
            {/if}
            <button class="btn hover:text-danger" onclick={() => review(t('users.drop'), usql.dropUserSQL(db, u), true)}><Trash2 size={13} /> {t('users.drop')}</button>
          </div>
        {/if}

        {#if form === 'grant'}
          <form class="mt-4 max-w-xl space-y-3 rounded-md border border-line p-3" onsubmit={grant}>
            <h4 class="text-sm font-semibold">{revoke ? t('users.revoke') : t('users.grant')}</h4>
            <div class="flex flex-wrap gap-x-4 gap-y-1.5">
              {#each usql.privileges(db) as p (p)}
                <label class="flex items-center gap-1.5 text-xs"><input type="checkbox" checked={privs.includes(p)} onchange={() => toggle(p)} /> {p}</label>
              {/each}
            </div>
            <div class="grid grid-cols-2 gap-3">
              <div>
                <label class="label" for="g-schema">{t('users.onSchema')}</label>
                <select id="g-schema" class="input" bind:value={target.schema}>
                  <option value="">{mysqlish || db === 'clickhouse' ? t('users.allDatabases') : t('users.defaultSchema')}</option>
                  {#each schemas as s (s)}<option value={s}>{s}</option>{/each}
                </select>
              </div>
              <div>
                <label class="label" for="g-table">{t('users.onTable')}</label>
                <input id="g-table" class="input font-mono" bind:value={target.table} placeholder={t('users.allTables')} />
              </div>
            </div>
            <div class="flex gap-2">
              <button class={revoke ? 'btn-danger' : 'btn-primary'} disabled={!privs.length}>{t('users.reviewSql')}</button>
              <button type="button" class="btn-outline" onclick={() => (form = '')}>{t('common.cancel')}</button>
            </div>
          </form>
        {/if}

        <h4 class="mt-5 mb-2 text-sm font-semibold">{t('users.privileges')}</h4>
        {#if grantsLoading}
          <p class="flex items-center gap-2 text-xs text-muted"><LoaderCircle size={12} class="animate-spin" /> {t('common.loading')}</p>
        {:else if grantsError}
          <p class="flex items-start gap-1.5 text-xs text-danger"><TriangleAlert size={13} class="mt-0.5 shrink-0" /> {grantsError}</p>
        {:else if grants.length}
          <ul class="divide-y divide-line-soft rounded-md border border-line">
            {#each grants as g, i (i)}<li class="px-3 py-1.5 font-mono text-xs break-all">{g}</li>{/each}
          </ul>
        {:else}
          <p class="text-xs text-muted">{t('users.noPrivileges')}</p>
        {/if}
      {:else}
        <div class="flex h-full items-center justify-center text-sm text-muted">{t('users.pick')}</div>
      {/if}
    </div>
  </div>
</div>
