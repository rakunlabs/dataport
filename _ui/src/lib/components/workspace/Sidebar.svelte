<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { ChevronRight, Folder, FolderOpen, Table2, KeyRound, Key, RefreshCw, Search, Lock, FilePlus2, LoaderCircle, TriangleAlert, X, ChevronsLeft } from 'lucide-svelte';
  import DbIcon from '@/lib/components/ui/DbIcon.svelte';
  import TreeMenu, { type MenuItem } from '@/lib/components/workspace/TreeMenu.svelte';
  import {
    connectionMenu as connectionItems,
    groupMenu as groupItemsMenu,
    groups,
    kindIcon,
    objectKinds,
    objectMenu as objectItems,
    routineMenu as routineItems,
    schemaMenu as schemaItems,
    showDefinition,
    showGroup as showGroupFor,
    type GroupKey,
  } from '@/lib/components/workspace/treeMenus';
  import { loadColumns, loadObjects, loadRoutines, loadSchemas, openQueryTab, openTableTab, refreshConnection, toggle, ws, activeTab } from '@/lib/stores/workspace.svelte';
  import { askCredentials, forgetCredentials } from '@/lib/stores/credentials.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import type { ConnectionView, DbObject, Routine } from '@/lib/api/types';

  let { oncollapse }: { oncollapse?: () => void } = $props();

  let filter = $state('');
  const f = $derived(filter.trim().toLowerCase());
  const current = $derived(activeTab());

  function showGroup(dbType: string, key: GroupKey, count: number) {
    return showGroupFor(dbType, key, count, !!f);
  }

  function toggleConn(id: string) {
    toggle(id);
    if (ws.expanded[id]) void loadSchemas(id, 'failed');
  }

  function openSchema(conn: string, schema: string, retry = false) {
    void loadObjects(conn, schema, retry && 'failed');
    void loadRoutines(conn, schema, retry && 'failed');
  }

  function toggleSchema(conn: string, schema: string) {
    const key = `${conn}|${schema}`;
    toggle(key);
    if (ws.expanded[key]) openSchema(conn, schema, true);
  }

  function toggleTable(conn: string, schema: string, table: string) {
    const key = `${conn}|${schema}|${table}`;
    toggle(key);
    if (ws.expanded[key]) void loadColumns(conn, schema, table, 'failed');
  }

  function groupItems(conn: string, schema: string, key: GroupKey): (DbObject | Routine)[] {
    const sk = `${conn}|${schema}`;
    const items = objectKinds.has(key) ? (ws.objects[sk]?.items ?? []).filter((o) => o.kind === key) : (ws.routines[sk]?.items ?? []).filter((r) => r.kind === key);
    return f ? items.filter((o) => o.name.toLowerCase().includes(f)) : items;
  }

  // Tables and views are open by default; the rest is folded until asked.
  function groupOpen(key: string, kind: GroupKey) {
    const v = ws.expanded[key];
    if (f) return true;
    return v ?? (kind === 'table' || kind === 'view');
  }

  // While filtering, load every schema of the open connections so matches
  // in collapsed schemas show up too.
  $effect(() => {
    if (!f) return;
    for (const c of ws.connections) {
      if (!ws.expanded[c.id]) continue;
      for (const schema of ws.schemas[c.id]?.items ?? []) openSchema(c.id, schema);
    }
  });

  // ////////////////////////////////////////////
  // context menus

  let menu = $state<{ x: number; y: number; items: MenuItem[] } | null>(null);

  function showMenu(e: MouseEvent, items: MenuItem[]) {
    e.preventDefault();
    e.stopPropagation();
    menu = { x: e.clientX, y: e.clientY, items };
  }

  const connectionMenu = (e: MouseEvent, c: ConnectionView) => showMenu(e, connectionItems(c));
  const schemaMenu = (e: MouseEvent, c: ConnectionView, schema: string) => showMenu(e, schemaItems(c, schema));
  const objectMenu = (e: MouseEvent, c: ConnectionView, schema: string, o: DbObject) => showMenu(e, objectItems(c, schema, o));
  const routineMenu = (e: MouseEvent, c: ConnectionView, schema: string, r: Routine) => showMenu(e, routineItems(c, schema, r));
  const groupMenu = (e: MouseEvent, c: ConnectionView, schema: string, kind: GroupKey) => showMenu(e, groupItemsMenu(c, schema, kind));

  // ////////////////////////////////////////////
  // own accounts

  let credMenu = $state<{ id: string; x: number; y: number } | null>(null);

  async function changeAccount(id: string) {
    credMenu = null;
    if (await askCredentials(id)) await refreshConnection(id);
  }

  async function forgetAccount(id: string) {
    credMenu = null;
    try {
      await forgetCredentials(id);
      // Drop the cached tree, so nothing loaded with the old account stays.
      ws.expanded[id] = false;
      delete ws.schemas[id];
      toast(t('credentials.forgotten'), 'success');
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  // Rows share one rhythm: 24px, a 12px indent step and a fixed chevron slot.
  const row = 'group flex h-6 w-full items-center gap-1.5 pr-1.5 text-left text-sm hover:bg-hover';
</script>

<aside class="flex h-full flex-col bg-panel">
  <div class="pane-head items-center gap-2 pr-1.5 pl-3">
    <span class="pane-title">{t('sidebar.connections')}</span>
    <span class="text-xs text-faint tabular-nums">{ws.connections.length}</span>
    {#if oncollapse}
      <button class="icon-btn ml-auto size-6" onclick={oncollapse} title={t('sidebar.collapse')} aria-label={t('sidebar.collapse')} aria-expanded="true">
        <ChevronsLeft size={15} />
      </button>
    {/if}
  </div>

  <div class="shrink-0 p-1.5">
    <div class="relative">
      <Search size={13} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-faint" />
      <input class="input h-[26px] pr-6 pl-7 text-xs" placeholder={t('sidebar.filter')} bind:value={filter} onkeydown={(e) => e.key === 'Escape' && (filter = '')} />
      {#if filter}
        <button class="absolute top-1/2 right-1.5 -translate-y-1/2 text-faint hover:text-fg" onclick={() => (filter = '')} aria-label={t('common.clear')}><X size={12} /></button>
      {/if}
    </div>
  </div>

  <div class="min-h-0 flex-1 overflow-auto pb-3" role="tree">
    {#if !ws.connectionsLoaded}
      <p class="flex items-center gap-2 px-3 py-2 text-xs text-muted"><LoaderCircle size={12} class="animate-spin" /> {t('common.loading')}</p>
    {:else if ws.connectionsError}
      <p class="flex items-start gap-1.5 px-3 py-2 text-xs text-danger"><TriangleAlert size={12} class="mt-0.5 shrink-0" /> {ws.connectionsError}</p>
    {:else if ws.connections.length === 0}
      <div class="px-4 py-8 text-center">
        <Lock size={18} class="mx-auto mb-2 text-faint" />
        <p class="text-xs leading-relaxed text-muted">{t('sidebar.noAccess')}</p>
      </div>
    {/if}

    {#each ws.connections as c (c.id)}
      {@const open = ws.expanded[c.id]}
      {@const schemas = ws.schemas[c.id]}
      {@const here = current?.connectionId === c.id}
      <div class="{row} pl-1" role="treeitem" aria-expanded={!!open} aria-selected={false} tabindex="-1" oncontextmenu={(e) => connectionMenu(e, c)}>
        <button class="flex min-w-0 flex-1 items-center gap-1.5 self-stretch" onclick={() => toggleConn(c.id)}>
          <ChevronRight size={13} class="shrink-0 text-faint transition-transform duration-100 {open ? 'rotate-90' : ''}" />
          <DbIcon type={c.db_type} size={13} />
          <span class="truncate {here ? 'font-semibold text-fg' : 'font-medium text-fg'}">{c.name}</span>
          {#if c.color && !c.production}<span class="size-2 shrink-0 rounded-full" style="background:{c.color}"></span>{/if}
        </button>

        <span class="flex shrink-0 items-center gap-1 group-hover:hidden">
          {#if c.production}<span class="pill-prod" title={t('conn.productionHint')}>{t('conn.prodShort')}</span>{/if}
          {#if c.read_only}
            <span title={t('conn.readOnlyHint')}><Lock size={11} class="text-faint" /></span>
          {:else}
            <span class="pill-rw" title={t('conn.writeHint')}>{t('conn.rwShort')}</span>
          {/if}
        </span>

        {#if c.user_credentials}
          <button
            class="{c.credential_user ? 'pill-own' : 'pill border border-dashed border-ok/60 text-ok'} hover:brightness-110"
            title={c.credential_user ? t('credentials.signedInAs', { user: c.credential_user }) : t('credentials.notSignedIn')}
            onclick={(e) => {
              e.stopPropagation();
              if (!c.credential_user) void changeAccount(c.id);
              else credMenu = { id: c.id, x: e.clientX, y: e.clientY };
            }}
          >
            <KeyRound size={10} />
            <span class="max-w-16 truncate">{c.credential_user || t('credentials.signIn')}</span>
          </button>
        {/if}

        <span class="hidden shrink-0 items-center group-hover:flex">
          <button class="icon-btn size-5" title={t('sidebar.newQuery')} onclick={() => openQueryTab(c.id)}><FilePlus2 size={12} /></button>
          <button class="icon-btn size-5" title={t('sidebar.refresh')} onclick={() => refreshConnection(c.id)}><RefreshCw size={12} /></button>
        </span>
      </div>

      {#if open}
        {#if schemas?.loading && !schemas.items.length}
          <div class="flex h-6 items-center gap-1.5 pl-8 text-xs text-muted"><LoaderCircle size={12} class="animate-spin" /> {t('common.loading')}</div>
        {:else if schemas?.error}
          <div class="flex items-start gap-1.5 py-1 pr-2 pl-8 text-xs text-danger"><TriangleAlert size={12} class="mt-0.5 shrink-0" /> {schemas.error}</div>
        {/if}

        {#each schemas?.items ?? [] as schema (schema)}
          {@const skey = `${c.id}|${schema}`}
          {@const sopen = ws.expanded[skey] || !!f}
          {@const objects = ws.objects[skey]}
          {@const routines = ws.routines[skey]}
          <button
            class="{row} pl-4"
            onclick={() => toggleSchema(c.id, schema)}
            oncontextmenu={(e) => schemaMenu(e, c, schema)}
            role="treeitem"
            aria-expanded={sopen}
            aria-selected={false}
          >
            <ChevronRight size={13} class="shrink-0 text-faint transition-transform duration-100 {sopen ? 'rotate-90' : ''}" />
            {#if sopen}<FolderOpen size={14} class="shrink-0 text-obj-schema" />{:else}<Folder size={14} class="shrink-0 text-obj-schema" />{/if}
            <span class="truncate text-fg-2">{schema}</span>
          </button>

          {#if sopen}
            {#if objects?.loading && !objects.items.length}
              <div class="flex h-6 items-center gap-1.5 pl-12 text-xs text-muted"><LoaderCircle size={12} class="animate-spin" /> {t('common.loading')}</div>
            {:else if objects?.error}
              <div class="py-1 pr-2 pl-12 text-xs text-danger">{objects.error}</div>
            {/if}

            {#each groups as g (g.key)}
              {@const items = groupItems(c.id, schema, g.key)}
              {@const isObject = objectKinds.has(g.key)}
              {@const loaded = isObject ? objects?.loaded : routines?.loaded}
              <!-- Groups always shown: tables and views; others only when they have items. -->
              {#if loaded && showGroup(c.db_type, g.key, items.length)}
                {@const gkey = `${skey}|#${g.key}`}
                {@const gopen = groupOpen(gkey, g.key) && items.length > 0}
                <button
                  class="{row} pl-7"
                  onclick={() => toggle(gkey, !gopen)}
                  oncontextmenu={(e) => groupMenu(e, c, schema, g.key)}
                  role="treeitem"
                  aria-expanded={gopen}
                  aria-selected={false}
                >
                  <ChevronRight size={13} class="shrink-0 transition-transform duration-100 {gopen ? 'rotate-90' : ''} {items.length ? 'text-faint' : 'text-transparent'}" />
                  <g.icon size={14} class="shrink-0 {g.color}" />
                  <span class="truncate text-fg-2">{t(g.label)}</span>
                  <span class="ml-1 text-2xs text-faint tabular-nums">{items.length}</span>
                </button>

                {#if gopen}
                  {#each items as item (item.name + ((item as Routine).signature ?? ''))}
                    {#if isObject}
                      {@const o = item as DbObject}
                      {@const tkey = `${c.id}|${schema}|${o.name}`}
                      {@const topen = ws.expanded[tkey]}
                      {@const cols = ws.columns[tkey]}
                      {@const Icon = kindIcon[o.kind] ?? Table2}
                      {@const selected = current?.kind === 'table' && current.connectionId === c.id && current.schema === schema && current.table === o.name}
                      <div
                        class="{row} pl-10 {selected ? 'bg-row-active hover:bg-row-active' : ''}"
                        role="treeitem"
                        aria-expanded={!!topen}
                        aria-selected={selected}
                        tabindex="-1"
                        oncontextmenu={(e) => objectMenu(e, c, schema, o)}
                      >
                        <button class="flex size-[13px] shrink-0 items-center justify-center" onclick={() => toggleTable(c.id, schema, o.name)} aria-label={t('sidebar.columns')}>
                          <ChevronRight size={13} class="text-faint transition-transform duration-100 {topen ? 'rotate-90' : ''}" />
                        </button>
                        <button class="flex min-w-0 flex-1 items-center gap-1.5 self-stretch text-left" title={`${schema}.${o.name}`} onclick={() => openTableTab(c.id, schema, o.name)}>
                          <Icon size={14} class="shrink-0 {g.color}" />
                          <span class="truncate {selected ? 'font-medium text-fg' : 'text-fg'}">{o.name}</span>
                        </button>
                      </div>

                      {#if topen}
                        {#if cols?.loading}
                          <div class="flex h-6 items-center pl-[72px]"><LoaderCircle size={12} class="animate-spin text-muted" /></div>
                        {/if}
                        {#each cols?.items ?? [] as col (col.name)}
                          <div class="flex h-[22px] items-center gap-1.5 pr-2 pl-16 text-xs" title={col.default ? `default ${col.default}` : ''}>
                            {#if col.primary_key}<Key size={11} class="shrink-0 text-warn" />{:else}<span class="w-[11px] shrink-0"></span>{/if}
                            <span class="truncate text-fg-2">{col.name}</span>
                            <span class="ml-auto shrink-0 truncate pl-2 font-mono text-[11px] text-faint">{col.type}</span>
                          </div>
                        {/each}
                      {/if}
                    {:else}
                      {@const r = item as Routine}
                      <button
                        class="{row} pl-[52px]"
                        title={r.signature ? r.name + r.signature : r.name}
                        onclick={() => showDefinition(c, schema, r)}
                        oncontextmenu={(e) => routineMenu(e, c, schema, r)}
                        role="treeitem"
                        aria-selected={false}
                      >
                        <g.icon size={13} class="shrink-0 {g.color}" />
                        <span class="truncate text-fg">{r.name}</span>
                        {#if r.signature && r.signature !== '()'}<span class="truncate font-mono text-[11px] text-faint">{r.signature}</span>{/if}
                      </button>
                    {/if}
                  {/each}
                {/if}
              {/if}
            {/each}

            {#if f && objects?.loaded && routines?.loaded && groups.every((g) => !groupItems(c.id, schema, g.key).length)}
              <div class="py-1 pr-2 pl-12 text-xs text-faint">{t('sidebar.noMatch')}</div>
            {/if}
          {/if}
        {/each}
      {/if}
    {/each}
  </div>
</aside>

<svelte:window onclick={() => (credMenu = null)} />

{#if menu}
  <TreeMenu x={menu.x} y={menu.y} items={menu.items} onclose={() => (menu = null)} />
{/if}

{#if credMenu}
  {@const id = credMenu.id}
  <div class="popover fixed z-50 w-56" style="left:{credMenu.x}px; top:{credMenu.y + 8}px" role="menu">
    <button class="menu-item" role="menuitem" onclick={() => changeAccount(id)}><KeyRound size={13} /> {t('credentials.change')}</button>
    <button class="menu-item" role="menuitem" onclick={() => forgetAccount(id)}>{t('credentials.forget')}</button>
  </div>
{/if}
