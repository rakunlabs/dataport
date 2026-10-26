<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import {
    ChevronRight,
    Folder,
    FolderOpen,
    Table2,
    Eye,
    Layers,
    KeyRound,
    Key,
    RefreshCw,
    Search,
    Lock,
    FilePlus2,
    LoaderCircle,
    TriangleAlert,
    X,
    Workflow,
    Braces,
    ListOrdered,
    Package,
    Network,
    Copy,
    Plus,
    ListTree,
    FileCode,
    ChevronsLeft,
    Unplug,
    Activity,
    UsersRound,
    Download,
    Upload,
    ArrowRightLeft,
    Ellipsis,
    Scissors,
    Eraser,
    Trash2,
  } from 'lucide-svelte';
  import DbIcon from '@/lib/components/DbIcon.svelte';
  import TreeMenu, { type MenuItem } from '@/lib/components/TreeMenu.svelte';
  import {
    loadColumns,
    loadDefinition,
    loadObjects,
    loadRoutines,
    loadSchemas,
    openDiagram,
    openQueryTab,
    openTableTab,
    refreshConnection,
    refreshSchema,
    runInNewTab,
    toggle,
    ws,
    activeTab,
    disconnect,
    openTool,
  } from '@/lib/stores/workspace.svelte';
  import { openToolDialog } from '@/lib/stores/dialogs.svelte';
  import { supportsUsers } from '@/lib/helper/userSql';
  import { askCredentials, forgetCredentials } from '@/lib/stores/credentials.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { reviewSQL } from '@/lib/stores/review.svelte';
  import * as sqlx from '@/lib/helper/treeSql';
  import type { ConnectionView, DbObject, Routine } from '@/lib/api/types';

  let { oncollapse }: { oncollapse?: () => void } = $props();

  let filter = $state('');
  const f = $derived(filter.trim().toLowerCase());
  const current = $derived(activeTab());

  // The groups shown under a schema, in DBX's order.
  type GroupKey = 'table' | 'view' | 'materialized_view' | 'foreign_table' | 'procedure' | 'function' | 'package' | 'sequence';

  const groups: { key: GroupKey; label: string; icon: typeof Table2; color: string }[] = [
    { key: 'table', label: 'tree.tables', icon: Table2, color: 'text-accent' },
    { key: 'view', label: 'tree.views', icon: Eye, color: 'text-type' },
    { key: 'materialized_view', label: 'tree.materializedViews', icon: Layers, color: 'text-type' },
    { key: 'foreign_table', label: 'tree.foreignTables', icon: Table2, color: 'text-muted' },
    { key: 'procedure', label: 'tree.procedures', icon: Workflow, color: 'text-type-time' },
    { key: 'function', label: 'tree.functions', icon: Braces, color: 'text-warn' },
    { key: 'package', label: 'tree.packages', icon: Package, color: 'text-type-time' },
    { key: 'sequence', label: 'tree.sequences', icon: ListOrdered, color: 'text-ok' },
  ];

  const objectKinds = new Set(['table', 'view', 'materialized_view', 'foreign_table']);

  // Databases whose sessions dataport can list and stop.
  const processTypes = new Set(['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'clickhouse', 'trino']);

  // The groups a database has, shown even when empty, as DBX does. Groups
  // outside this list appear only when they hold something.
  const supported: Record<string, GroupKey[]> = {
    postgres: ['table', 'view', 'materialized_view', 'procedure', 'function', 'sequence'],
    mysql: ['table', 'view', 'procedure', 'function'],
    mariadb: ['table', 'view', 'procedure', 'function', 'sequence'],
    mssql: ['table', 'view', 'procedure', 'function', 'sequence'],
    oracle: ['table', 'view', 'materialized_view', 'procedure', 'function', 'package', 'sequence'],
    clickhouse: ['table', 'view', 'materialized_view'],
    duckdb: ['table', 'view', 'function', 'sequence'],
  };

  function showGroup(dbType: string, key: GroupKey, count: number) {
    if (count) return true;
    if (f) return false;
    return (supported[dbType] ?? ['table', 'view']).includes(key);
  }

  function toggleConn(id: string) {
    toggle(id);
    if (ws.expanded[id]) void loadSchemas(id);
  }

  function openSchema(conn: string, schema: string) {
    void loadObjects(conn, schema);
    void loadRoutines(conn, schema);
  }

  function toggleSchema(conn: string, schema: string) {
    const key = `${conn}|${schema}`;
    toggle(key);
    if (ws.expanded[key]) openSchema(conn, schema);
  }

  function toggleTable(conn: string, schema: string, table: string) {
    const key = `${conn}|${schema}|${table}`;
    toggle(key);
    if (ws.expanded[key]) void loadColumns(conn, schema, table);
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

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      toast(t('grid.copied'), 'success', 1200);
    } catch {
      toast(t('grid.clipboardUnavailable'), 'error');
    }
  }

  // review shows the SQL of an action, then runs it in a new tab or opens it
  // in an editor. Changes refresh the schema afterwards.
  async function review(c: ConnectionView, title: string, sql: string, danger: boolean, schema: string, input?: { label: string; value: string; build: (v: string) => string }) {
    const r = await reviewSQL({ title, connection: c.name, sql, danger, input });
    if (r.answer === 'edit') openQueryTab(c.id, r.sql);
    if (r.answer !== 'run') return;
    const { tab, done } = runInNewTab(c.id, r.sql, title);
    await done;
    // Reload the tree when something may have changed.
    if (tab.result?.committed || (tab.result && !tab.result.transactional)) {
      if (schema) void refreshSchema(c.id, schema);
      else void refreshConnection(c.id);
    }
  }

  function readOnly(c: ConnectionView) {
    return c.read_only;
  }

  async function closeConnection(c: ConnectionView) {
    try {
      await disconnect(c.id);
      toast(t('tree.disconnected', { name: c.name }), 'success', 1500);
    } catch (err) {
      toast(errorMessage(err), 'error');
    }
  }

  function connectionMenu(e: MouseEvent, c: ConnectionView) {
    const ro = readOnly(c);
    const connected = !!ws.schemas[c.id];
    showMenu(e, [
      { label: t('tree.newQuery'), icon: FilePlus2, action: () => openQueryTab(c.id) },
      { label: t('tree.copyName'), icon: Copy, action: () => copy(c.name) },
      { label: t('sidebar.refresh'), icon: RefreshCw, action: () => refreshConnection(c.id) },
      { label: t('tree.disconnect'), icon: Unplug, hidden: !connected, action: () => closeConnection(c) },
      { sep: true },
      { label: t('tree.processes'), icon: Activity, hidden: !processTypes.has(c.db_type), action: () => openTool('processes', c.id) },
      { label: t('tree.users'), icon: UsersRound, hidden: !supportsUsers(c.db_type), action: () => openTool('users', c.id) },
      { sep: true },
      { label: t('tree.export'), icon: Download, action: () => openToolDialog('export', c.id) },
      { label: t('tree.import'), icon: Upload, hidden: ro, action: () => openToolDialog('import', c.id) },
      { label: t('tree.transfer'), icon: ArrowRightLeft, action: () => openToolDialog('transfer', c.id) },
      { sep: true },
      {
        label: t('tree.createSchema'),
        icon: Plus,
        hidden: ro || !sqlx.hasSchemas(c.db_type),
        action: () => review(c, t('tree.createSchema'), sqlx.createSchemaSQL(c.db_type), false, ''),
      },
      { label: t('tree.collapse'), action: () => collapse(c.id) },
    ]);
  }

  function schemaMenu(e: MouseEvent, c: ConnectionView, schema: string) {
    const ro = readOnly(c);
    showMenu(e, [
      { label: t('tree.newQuery'), icon: FilePlus2, action: () => openQueryTab(c.id, sqlx.useSchemaSQL(c.db_type, schema)) },
      { label: t('tree.viewDiagram'), icon: Network, action: () => openDiagram(c.id, schema) },
      { label: t('tree.copyName'), icon: Copy, action: () => copy(schema) },
      { label: t('sidebar.refresh'), icon: RefreshCw, action: () => refreshSchema(c.id, schema) },
      { sep: true },
      { label: t('tree.export'), icon: Download, action: () => openToolDialog('export', c.id, schema) },
      { label: t('tree.import'), icon: Upload, hidden: ro, action: () => openToolDialog('import', c.id, schema) },
      { label: t('tree.transfer'), icon: ArrowRightLeft, action: () => openToolDialog('transfer', c.id, schema) },
      { sep: true },
      { label: t('tree.createTable'), icon: Plus, hidden: ro, action: () => openQueryTab(c.id, sqlx.createTableSQL(c.db_type, schema), t('tree.createTable')) },
      { label: t('tree.createView'), icon: Plus, hidden: ro, action: () => openQueryTab(c.id, sqlx.createViewSQL(c.db_type, schema), t('tree.createView')) },
      { sep: true, hidden: ro || !sqlx.hasSchemas(c.db_type) },
      {
        label: t('tree.dropSchema'),
        danger: true,
        hidden: ro || !sqlx.hasSchemas(c.db_type),
        action: () => review(c, t('tree.dropSchema'), sqlx.dropSchemaSQL(c.db_type, schema), true, ''),
      },
    ]);
  }

  async function objectMenu(e: MouseEvent, c: ConnectionView, schema: string, o: DbObject) {
    const ro = readOnly(c);
    const isTable = o.kind === 'table';
    showMenu(e, [
      { label: t('tree.openData'), icon: Table2, action: () => openTableTab(c.id, schema, o.name, 'data') },
      { label: t('tree.structure'), icon: ListTree, action: () => openTableTab(c.id, schema, o.name, 'structure') },
      { label: t('tree.ddl'), icon: FileCode, action: () => openTableTab(c.id, schema, o.name, 'ddl') },
      { label: t('tree.copyName'), icon: Copy, action: () => copy(o.name) },
      { label: t('tree.viewDiagram'), icon: Network, action: () => openDiagram(c.id, schema) },
      { sep: true },
      { label: t('tree.exportTable'), icon: Download, action: () => openToolDialog('export', c.id, schema, [o.name]) },
      { label: t('tree.importInto'), icon: Upload, hidden: ro || !isTable, action: () => openToolDialog('import', c.id, schema, [o.name]) },
      { label: t('tree.transfer'), icon: ArrowRightLeft, hidden: !isTable, action: () => openToolDialog('transfer', c.id, schema, [o.name]) },
      { sep: true },
      { label: t('tree.selectRows'), action: () => void runInNewTab(c.id, sqlx.selectSQL(c.db_type, schema, o.name), o.name) },
      { label: t('tree.countRows'), action: () => void runInNewTab(c.id, sqlx.countSQL(c.db_type, schema, o.name), o.name) },
      {
        label: t('tree.insertTemplate'),
        hidden: ro || !isTable,
        action: async () => {
          await loadColumns(c.id, schema, o.name);
          const cols = ws.columns[`${c.id}|${schema}|${o.name}`]?.items.map((x) => x.name) ?? [];
          openQueryTab(c.id, sqlx.insertSQL(c.db_type, schema, o.name, cols));
        },
      },
      { sep: true, hidden: ro },
      {
        label: t('tree.rename'),
        hidden: ro || !isTable,
        action: () =>
          review(c, t('tree.rename'), '', false, schema, {
            label: t('tree.newName'),
            value: o.name,
            build: (v) => sqlx.renameTableSQL(c.db_type, schema, o.name, v.trim()),
          }),
      },
      {
        label: t('tree.more'),
        icon: Ellipsis,
        hidden: ro,
        children: [
          { label: t('tree.truncate'), icon: Scissors, danger: true, hidden: !isTable, action: () => review(c, t('tree.truncate'), sqlx.truncateSQL(c.db_type, schema, o.name), true, schema) },
          { label: t('tree.empty'), icon: Eraser, danger: true, hidden: !isTable, action: () => review(c, t('tree.empty'), sqlx.emptySQL(c.db_type, schema, o.name), true, schema) },
          { sep: true },
          { label: isTable ? t('tree.dropTable') : t('tree.drop'), icon: Trash2, danger: true, action: () => review(c, isTable ? t('tree.dropTable') : t('tree.drop'), sqlx.dropTableSQL(c.db_type, schema, o.name, o.kind), true, schema) },
        ],
      },
    ]);
  }

  async function showDefinition(c: ConnectionView, schema: string, r: Routine) {
    try {
      const def = await loadDefinition(c.id, schema, r);
      openQueryTab(c.id, def || `-- ${t('tree.noDefinition')}`, r.name);
    } catch (err) {
      toast(errorMessage(err), 'error');
    }
  }

  function routineMenu(e: MouseEvent, c: ConnectionView, schema: string, r: Routine) {
    const ro = readOnly(c);
    showMenu(e, [
      { label: t('tree.definition'), icon: FileCode, action: () => showDefinition(c, schema, r) },
      { label: t('tree.copyName'), icon: Copy, action: () => copy(r.name) },
      { label: r.kind === 'sequence' ? t('tree.nextValue') : t('tree.callTemplate'), action: () => openQueryTab(c.id, sqlx.callSQL(c.db_type, schema, r.name, r.kind)) },
      { sep: true, hidden: ro },
      {
        label: t('tree.drop'),
        danger: true,
        hidden: ro,
        action: () => review(c, t('tree.drop'), sqlx.dropRoutineSQL(c.db_type, schema, r.name, r.kind, r.signature), true, schema),
      },
    ]);
  }

  // Group rows offer what makes sense for their kind.
  function groupMenu(e: MouseEvent, c: ConnectionView, schema: string, kind: GroupKey) {
    const ro = readOnly(c);
    const create: Record<string, [string, () => string]> = {
      table: ['tree.createTable', () => sqlx.createTableSQL(c.db_type, schema)],
      view: ['tree.createView', () => sqlx.createViewSQL(c.db_type, schema)],
      materialized_view: ['tree.createMaterializedView', () => sqlx.createMaterializedViewSQL(c.db_type, schema)],
      procedure: ['tree.createProcedure', () => sqlx.createProcedureSQL(c.db_type, schema)],
      function: ['tree.createFunction', () => sqlx.createFunctionSQL(c.db_type, schema)],
      sequence: ['tree.createSequence', () => sqlx.createSequenceSQL(c.db_type, schema)],
    };
    const make = create[kind];
    showMenu(e, [
      { label: t(make?.[0] ?? 'tree.createTable'), icon: Plus, hidden: ro || !make, action: () => make && openQueryTab(c.id, make[1](), t(make[0])) },
      { label: t('tree.newQuery'), icon: FilePlus2, action: () => openQueryTab(c.id, sqlx.useSchemaSQL(c.db_type, schema)) },
      { label: t('tree.viewDiagram'), icon: Network, hidden: !objectKinds.has(kind), action: () => openDiagram(c.id, schema) },
      { label: t('tree.export'), icon: Download, hidden: kind !== 'table', action: () => openToolDialog('export', c.id, schema) },
      { label: t('tree.import'), icon: Upload, hidden: ro || kind !== 'table', action: () => openToolDialog('import', c.id, schema) },
      { label: t('sidebar.refresh'), icon: RefreshCw, action: () => refreshSchema(c.id, schema) },
    ]);
  }

  function collapse(connId: string) {
    for (const key of Object.keys(ws.expanded)) if (key.startsWith(connId + '|')) ws.expanded[key] = false;
  }

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

  const kindIcon: Record<string, typeof Table2> = { table: Table2, view: Eye, materialized_view: Layers, foreign_table: Table2 };

  // Rows share one rhythm: 24px, a 12px indent step and a fixed chevron slot.
  const row = 'group flex h-6 w-full items-center gap-1.5 pr-1.5 text-left text-sm hover:bg-hover';
</script>

<aside class="flex h-full flex-col bg-panel">
  <div class="flex h-9 shrink-0 items-center gap-2 border-b border-line pr-1.5 pl-3">
    <span class="text-xs font-semibold text-fg-2">{t('sidebar.connections')}</span>
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
            {#if sopen}<FolderOpen size={14} class="shrink-0 text-muted" />{:else}<Folder size={14} class="shrink-0 text-muted" />{/if}
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
                        ondblclick={() => showDefinition(c, schema, r)}
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
