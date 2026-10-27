import { copyText } from '@/lib/helper/clipboard';
import { t } from '@/lib/i18n/index.svelte';
import {
  Table2,
  Eye,
  Layers,
  RefreshCw,
  FilePlus2,
  Workflow,
  Braces,
  ListOrdered,
  Package,
  Network,
  BookOpen,
  Copy,
  Plus,
  ListTree,
  FileCode,
  Unplug,
  Activity,
  UsersRound,
  Download,
  Upload,
  ArrowRightLeft,
  Sparkles,
  Ellipsis,
  Scissors,
  Eraser,
  Trash2,
} from 'lucide-svelte';
import type { MenuItem } from '@/lib/components/workspace/TreeMenu.svelte';
import {
  loadColumns,
  loadDefinition,
  openDiagram,
  openDocs,
  openQueryTab,
  openTableTab,
  refreshConnection,
  refreshSchema,
  runInNewTab,
  ws,
  disconnect,
  openTool,
} from '@/lib/stores/workspace.svelte';
import { openToolDialog } from '@/lib/stores/dialogs.svelte';
import { supportsUsers } from '@/lib/helper/userSql';
import { errorMessage, toast } from '@/lib/stores/toast.svelte';
import { reviewSQL } from '@/lib/stores/review.svelte';
import * as sqlx from '@/lib/helper/treeSql';
import type { ConnectionView, DbObject, Routine } from '@/lib/api/types';

// The groups shown under a schema, in DBX's order.
export type GroupKey = 'table' | 'view' | 'materialized_view' | 'foreign_table' | 'procedure' | 'function' | 'package' | 'sequence';

export const groups: { key: GroupKey; label: string; icon: typeof Table2; color: string }[] = [
  { key: 'table', label: 'tree.tables', icon: Table2, color: 'text-obj-table' },
  { key: 'view', label: 'tree.views', icon: Eye, color: 'text-obj-view' },
  { key: 'materialized_view', label: 'tree.materializedViews', icon: Layers, color: 'text-obj-matview' },
  { key: 'foreign_table', label: 'tree.foreignTables', icon: Table2, color: 'text-muted' },
  { key: 'procedure', label: 'tree.procedures', icon: Workflow, color: 'text-obj-procedure' },
  { key: 'function', label: 'tree.functions', icon: Braces, color: 'text-obj-function' },
  { key: 'package', label: 'tree.packages', icon: Package, color: 'text-obj-package' },
  { key: 'sequence', label: 'tree.sequences', icon: ListOrdered, color: 'text-obj-sequence' },
];

export const objectKinds = new Set(['table', 'view', 'materialized_view', 'foreign_table']);

export const kindIcon: Record<string, typeof Table2> = { table: Table2, view: Eye, materialized_view: Layers, foreign_table: Table2 };

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

export function showGroup(dbType: string, key: GroupKey, count: number, filtering: boolean) {
  if (count) return true;
  if (filtering) return false;
  return (supported[dbType] ?? ['table', 'view']).includes(key);
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

async function closeConnection(c: ConnectionView) {
  try {
    await disconnect(c.id);
    toast(t('tree.disconnected', { name: c.name }), 'success', 1500);
  } catch (err) {
    toast(errorMessage(err), 'error');
  }
}

export function collapse(connId: string) {
  for (const key of Object.keys(ws.expanded)) if (key.startsWith(connId + '|')) ws.expanded[key] = false;
}

export async function showDefinition(c: ConnectionView, schema: string, r: Routine) {
  try {
    const def = (await loadDefinition(c.id, schema, r)) || `-- ${t('tree.noDefinition')}`;
    // A second click returns to the tab it opened, while left unchanged.
    const open = ws.tabs.find((x) => x.kind === 'query' && x.connectionId === c.id && x.title === r.name && x.sql === def);
    if (open) ws.activeId = open.id;
    else openQueryTab(c.id, def, r.name);
  } catch (err) {
    toast(errorMessage(err), 'error');
  }
}

export function connectionMenu(c: ConnectionView): MenuItem[] {
  const ro = c.read_only;
  const connected = !!ws.schemas[c.id];
  return [
    { label: t('tree.newQuery'), icon: FilePlus2, action: () => openQueryTab(c.id) },
    { label: t('tree.copyName'), icon: Copy, action: () => copyText(c.name) },
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
  ];
}

export function schemaMenu(c: ConnectionView, schema: string): MenuItem[] {
  const ro = c.read_only;
  return [
    { label: t('tree.newQuery'), icon: FilePlus2, action: () => openQueryTab(c.id, sqlx.useSchemaSQL(c.db_type, schema)) },
    { label: t('tree.viewDiagram'), icon: Network, action: () => openDiagram(c.id, schema) },
    { label: t('tree.viewDocs'), icon: BookOpen, action: () => openDocs(c.id, schema) },
    { label: t('tree.copyName'), icon: Copy, action: () => copyText(schema) },
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
  ];
}

export function objectMenu(c: ConnectionView, schema: string, o: DbObject): MenuItem[] {
  const ro = c.read_only;
  const isTable = o.kind === 'table';
  return [
    { label: t('tree.openData'), icon: Table2, action: () => openTableTab(c.id, schema, o.name, 'data') },
    { label: t('tree.structure'), icon: ListTree, action: () => openTableTab(c.id, schema, o.name, 'structure') },
    { label: t('tree.ddl'), icon: FileCode, action: () => openTableTab(c.id, schema, o.name, 'ddl') },
    { label: t('tree.copyName'), icon: Copy, action: () => copyText(o.name) },
    { label: t('tree.viewDiagram'), icon: Network, action: () => openDiagram(c.id, schema) },
    { label: t('tree.viewDocs'), icon: BookOpen, action: () => openDocs(c.id, schema, o.name) },
    { sep: true },
    { label: t('tree.exportTable'), icon: Download, action: () => openToolDialog('export', c.id, schema, [o.name]) },
    { label: t('tree.importInto'), icon: Upload, hidden: ro || !isTable, action: () => openToolDialog('import', c.id, schema, [o.name]) },
    { label: t('tree.generateData'), icon: Sparkles, hidden: ro || !isTable, action: () => openToolDialog('generate', c.id, schema, [o.name]) },
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
  ];
}

export function routineMenu(c: ConnectionView, schema: string, r: Routine): MenuItem[] {
  const ro = c.read_only;
  return [
    { label: t('tree.definition'), icon: FileCode, action: () => showDefinition(c, schema, r) },
    { label: t('tree.copyName'), icon: Copy, action: () => copyText(r.name) },
    { label: r.kind === 'sequence' ? t('tree.nextValue') : t('tree.callTemplate'), action: () => openQueryTab(c.id, sqlx.callSQL(c.db_type, schema, r.name, r.kind)) },
    { sep: true, hidden: ro },
    {
      label: t('tree.drop'),
      danger: true,
      hidden: ro,
      action: () => review(c, t('tree.drop'), sqlx.dropRoutineSQL(c.db_type, schema, r.name, r.kind, r.signature), true, schema),
    },
  ];
}

// Group rows offer what makes sense for their kind.
export function groupMenu(c: ConnectionView, schema: string, kind: GroupKey): MenuItem[] {
  const ro = c.read_only;
  const create: Record<string, [string, () => string]> = {
    table: ['tree.createTable', () => sqlx.createTableSQL(c.db_type, schema)],
    view: ['tree.createView', () => sqlx.createViewSQL(c.db_type, schema)],
    materialized_view: ['tree.createMaterializedView', () => sqlx.createMaterializedViewSQL(c.db_type, schema)],
    procedure: ['tree.createProcedure', () => sqlx.createProcedureSQL(c.db_type, schema)],
    function: ['tree.createFunction', () => sqlx.createFunctionSQL(c.db_type, schema)],
    sequence: ['tree.createSequence', () => sqlx.createSequenceSQL(c.db_type, schema)],
  };
  const make = create[kind];
  return [
    { label: t(make?.[0] ?? 'tree.createTable'), icon: Plus, hidden: ro || !make, action: () => make && openQueryTab(c.id, make[1](), t(make[0])) },
    { label: t('tree.newQuery'), icon: FilePlus2, action: () => openQueryTab(c.id, sqlx.useSchemaSQL(c.db_type, schema)) },
    { label: t('tree.viewDiagram'), icon: Network, hidden: !objectKinds.has(kind), action: () => openDiagram(c.id, schema) },
    { label: t('tree.export'), icon: Download, hidden: kind !== 'table', action: () => openToolDialog('export', c.id, schema) },
    { label: t('tree.import'), icon: Upload, hidden: ro || kind !== 'table', action: () => openToolDialog('import', c.id, schema) },
    { label: t('sidebar.refresh'), icon: RefreshCw, action: () => refreshSchema(c.id, schema) },
  ];
}
