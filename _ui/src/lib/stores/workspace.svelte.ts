import { api, ApiError, qs } from '@/lib/api/client';
import type { ColumnInfo, ConnectionView, DbObject, ExecResult, ResultSet, Routine, SavedQuery, TableStructure } from '@/lib/api/types';
import { EditState } from '@/lib/stores/edits.svelte';
import { confirmProduction } from '@/lib/stores/confirm.svelte';
import { t } from '@/lib/i18n/index.svelte';
import { quoteIdent } from '@/lib/helper/copy';
import { askCredentials, needsCredentials, withCredentials } from '@/lib/stores/credentials.svelte';
import { prefs } from '@/lib/stores/prefs.svelte';
import { placeholders, substitute } from '@/lib/helper/variables';
import { askVariables } from '@/lib/stores/variables.svelte';

export type TabKind = 'query' | 'table' | 'settings' | 'diagram' | 'processes' | 'users';

export interface Tab {
  id: string;
  kind: TabKind;
  title: string;
  connectionId: string;
  schema: string;
  table: string;
  sql: string;
  savedId: string;
  dirty: boolean;
  running: boolean;
  queryId: string;
  result: ExecResult | null;
  error: string;
  activeResult: number;
  // Table browser state.
  where: string;
  orderBy: string;
  limit: number;
  offset: number;
  // hasMore is true when the page read one row more than it shows.
  hasMore: boolean;
  edit: EditState;
  view: 'data' | 'structure' | 'ddl';
  // Rows asked for per statement: 0 is the connection's default, -1 all
  // the server allows.
  maxRows: number;
  // Time limit in seconds for a run: -1 is the connection's, 0 none.
  timeoutSec: number;
}

interface TreeNode<T> {
  loading: boolean;
  loaded: boolean;
  error: string;
  items: T[];
}

export const ws = $state({
  connections: [] as ConnectionView[],
  connectionsLoaded: false,
  connectionsError: '',
  tabs: [] as Tab[],
  activeId: '',
  // Keys: connection id, "conn|schema", "conn|schema|table".
  expanded: {} as Record<string, boolean>,
  schemas: {} as Record<string, TreeNode<string>>,
  objects: {} as Record<string, TreeNode<DbObject>>,
  columns: {} as Record<string, TreeNode<ColumnInfo>>,
  routines: {} as Record<string, TreeNode<Routine>>,
  saved: [] as SavedQuery[],
  rightPanel: '' as '' | 'history' | 'saved',
  // Section shown in the settings tab.
  settingsSection: 'appearance',
});

let tabSeq = 0;

function newId(): string {
  tabSeq++;
  return `${Date.now().toString(36)}-${tabSeq}`;
}

export function connection(id: string): ConnectionView | undefined {
  return ws.connections.find((c) => c.id === id);
}

export function activeTab(): Tab | undefined {
  return ws.tabs.find((t) => t.id === ws.activeId);
}

export async function loadConnections() {
  try {
    ws.connections = await api.get<ConnectionView[]>('/connections');
    ws.connectionsError = '';
  } catch (e) {
    ws.connectionsError = e instanceof Error ? e.message : String(e);
  } finally {
    ws.connectionsLoaded = true;
  }
}

async function loadNode<T>(map: Record<string, TreeNode<T>>, key: string, fetcher: () => Promise<T[]>, force = false) {
  const node = map[key];
  if (node && (node.loading || (node.loaded && !force))) return;
  map[key] = { loading: true, loaded: false, error: '', items: node?.items ?? [] };
  try {
    const items = await fetcher();
    map[key] = { loading: false, loaded: true, error: '', items };
  } catch (e) {
    map[key] = { loading: false, loaded: false, error: e instanceof Error ? e.message : String(e), items: [] };
  }
}

export function loadSchemas(connId: string, force = false) {
  return loadNode(ws.schemas, connId, () => withCredentials(connId, () => api.get<string[]>(`/connections/${connId}/schemas`)), force);
}

export function loadObjects(connId: string, schema: string, force = false) {
  return loadNode(ws.objects, `${connId}|${schema}`, () =>
    withCredentials(connId, () => api.get<DbObject[]>(`/connections/${connId}/objects${qs({ schema })}`)), force);
}

export function loadRoutines(connId: string, schema: string, force = false) {
  return loadNode(ws.routines, `${connId}|${schema}`, () =>
    withCredentials(connId, () => api.get<Routine[]>(`/connections/${connId}/routines${qs({ schema })}`)), force);
}

export function loadDefinition(connId: string, schema: string, r: Routine): Promise<string> {
  return withCredentials(connId, () =>
    api.get<{ definition: string }>(`/connections/${connId}/definition${qs({ schema, name: r.name, kind: r.kind, signature: r.signature })}`),
  ).then((d) => d.definition);
}

export function loadColumns(connId: string, schema: string, table: string, force = false) {
  return loadNode(ws.columns, `${connId}|${schema}|${table}`, () =>
    withCredentials(connId, () => api.get<ColumnInfo[]>(`/connections/${connId}/columns${qs({ schema, table })}`)), force);
}

// Structures are cached per table until the tab asks for a refresh.
const structures = new Map<string, Promise<TableStructure>>();

export function loadStructure(connId: string, schema: string, table: string, force = false): Promise<TableStructure> {
  const key = `${connId}|${schema}|${table}`;
  let p = force ? undefined : structures.get(key);
  if (!p) {
    p = withCredentials(connId, () => api.get<TableStructure>(`/connections/${connId}/structure${qs({ schema, table })}`));
    p.catch(() => structures.delete(key));
    structures.set(key, p);
  }
  return p;
}

// refreshSchema reloads the objects of one schema.
export function refreshSchema(connId: string, schema: string) {
  const prefix = `${connId}|${schema}|`;
  for (const key of structures.keys()) if (key.startsWith(prefix)) structures.delete(key);
  for (const key of Object.keys(ws.columns)) if (key.startsWith(prefix)) delete ws.columns[key];
  return Promise.all([loadObjects(connId, schema, true), loadRoutines(connId, schema, true)]);
}

export function refreshConnection(connId: string) {
  for (const key of structures.keys()) if (key.startsWith(connId + '|')) structures.delete(key);
  for (const key of Object.keys(ws.objects)) if (key.startsWith(connId + '|')) delete ws.objects[key];
  for (const key of Object.keys(ws.columns)) if (key.startsWith(connId + '|')) delete ws.columns[key];
  for (const key of Object.keys(ws.routines)) if (key.startsWith(connId + '|')) delete ws.routines[key];
  return loadSchemas(connId, true);
}

export function toggle(key: string, value?: boolean) {
  ws.expanded[key] = value ?? !ws.expanded[key];
}

function baseTab(kind: TabKind, connectionId: string): Tab {
  return {
    id: newId(),
    kind,
    title: '',
    connectionId,
    schema: '',
    table: '',
    sql: '',
    savedId: '',
    dirty: false,
    running: false,
    queryId: '',
    result: null,
    error: '',
    activeResult: 0,
    where: '',
    orderBy: '',
    limit: 100,
    offset: 0,
    hasMore: false,
    edit: new EditState(),
    view: 'data',
    maxRows: 0,
    timeoutSec: -1,
  };
}

let queryCount = 0;

export function openQueryTab(connectionId: string, sql = '', title = '', savedId = '') {
  const tab = baseTab('query', connectionId);
  queryCount++;
  tab.title = title || t('tabs.query', { n: queryCount });
  tab.sql = sql;
  tab.savedId = savedId;
  ws.tabs.push(tab);
  ws.activeId = tab.id;
  persist();
  return tab;
}

// runInNewTab opens a query tab with the SQL and runs it there, so the
// result, any error and the production prompt show as for a typed query.
export function runInNewTab(connectionId: string, sql: string, title = '', maxRows = 0, timeoutSec = -1) {
  const tab = openQueryTab(connectionId, sql, title);
  tab.maxRows = maxRows;
  tab.timeoutSec = timeoutSec;
  tab.dirty = false;
  // The tab object in the store is the reactive proxy; return that one.
  const live = ws.tabs.find((x) => x.id === tab.id) ?? tab;
  const done = run(live.id);
  return { tab: live, done };
}

export function openTableTab(connectionId: string, schema: string, table: string, view?: Tab['view']) {
  const existing = ws.tabs.find(
    (t) => t.kind === 'table' && t.connectionId === connectionId && t.schema === schema && t.table === table,
  );
  if (existing) {
    if (view) existing.view = view;
    ws.activeId = existing.id;
    return existing;
  }

  const tab = baseTab('table', connectionId);
  tab.title = table;
  tab.schema = schema;
  tab.table = table;
  if (view) tab.view = view;
  ws.tabs.push(tab);
  ws.activeId = tab.id;
  void runTable(tab.id);
  persist();
  return tab;
}

// openDiagram shows the ER diagram of a schema, one tab per schema.
export function openDiagram(connectionId: string, schema: string) {
  const existing = ws.tabs.find((t) => t.kind === 'diagram' && t.connectionId === connectionId && t.schema === schema);
  if (existing) {
    ws.activeId = existing.id;
    return existing;
  }

  const tab = baseTab('diagram', connectionId);
  tab.title = schema;
  tab.schema = schema;
  ws.tabs.push(tab);
  ws.activeId = tab.id;
  persist();
  return ws.tabs[ws.tabs.length - 1];
}

// openTool shows a connection-wide tool, such as the process list, one tab
// per connection and tool.
export function openTool(kind: 'processes' | 'users', connectionId: string) {
  const existing = ws.tabs.find((t) => t.kind === kind && t.connectionId === connectionId);
  if (existing) {
    ws.activeId = existing.id;
    return existing;
  }

  const tab = baseTab(kind, connectionId);
  tab.title = t(kind === 'processes' ? 'processes.title' : 'users.title');
  ws.tabs.push(tab);
  ws.activeId = tab.id;
  persist();
  return ws.tabs[ws.tabs.length - 1];
}

// disconnect lets the server close the sessions it holds for the
// connection and folds its tree, so nothing stays connected.
export async function disconnect(connId: string) {
  await api.post(`/connections/${connId}/disconnect`);
  ws.expanded[connId] = false;
  for (const key of Object.keys(ws.expanded)) if (key.startsWith(connId + '|')) delete ws.expanded[key];
  for (const key of structures.keys()) if (key.startsWith(connId + '|')) structures.delete(key);
  for (const m of [ws.objects, ws.columns, ws.routines] as Record<string, unknown>[]) {
    for (const key of Object.keys(m)) if (key.startsWith(connId + '|')) delete m[key];
  }
  delete ws.schemas[connId];
}

// A settings tab asked for before the workspace started opens after the
// saved tabs are restored.
let pendingSettings = '';

// openSettings shows the single settings tab, on the given section.
export function openSettings(section?: string) {
  if (section) ws.settingsSection = section;
  if (!ws.connectionsLoaded) {
    pendingSettings = section || ws.settingsSection;
    return;
  }
  const existing = ws.tabs.find((t) => t.kind === 'settings');
  if (existing) {
    ws.activeId = existing.id;
    return existing;
  }

  const tab = baseTab('settings', '');
  tab.title = t('settings.title');
  ws.tabs.push(tab);
  ws.activeId = tab.id;
  persist();
  return tab;
}

export function closeTab(id: string) {
  const i = ws.tabs.findIndex((t) => t.id === id);
  if (i < 0) return;
  const tab = ws.tabs[i];
  if (tab.running) void cancel(tab.id);
  ws.tabs.splice(i, 1);
  if (ws.activeId === id) {
    ws.activeId = ws.tabs[Math.min(i, ws.tabs.length - 1)]?.id ?? '';
  }
  persist();
}

export function closeOthers(id: string) {
  for (const t of ws.tabs.filter((t) => t.id !== id)) closeTab(t.id);
}

function tab(id: string): Tab | undefined {
  return ws.tabs.find((t) => t.id === id);
}

// ////////////////////////////////////////////
// identifiers

export { quoteIdent };

export function qualified(dbType: string, schema: string, table: string): string {
  const t = quoteIdent(dbType, table);
  return schema ? `${quoteIdent(dbType, schema)}.${t}` : t;
}

// tableSQL reads one row more than the page shows, so the grid knows whether
// a next page exists without an expensive COUNT(*) (as DBX does).
export function tableSQL(t: Tab, probe = true): string {
  const c = connection(t.connectionId);
  const dbType = c?.db_type ?? 'postgres';
  const n = t.limit + (probe ? 1 : 0);
  const from = qualified(dbType, t.schema, t.table);
  const where = t.where.trim() ? ` WHERE ${t.where.trim()}` : '';
  // Without an explicit order, use the primary key so pages stay stable
  // while rows are edited.
  const pk = (ws.columns[tableKey(t)]?.items ?? []).filter((c) => c.primary_key).map((c) => quoteIdent(dbType, c.name));
  const order = t.orderBy.trim() || pk.join(', ');

  if (dbType === 'mssql') {
    if (t.offset === 0) return `SELECT TOP (${n}) * FROM ${from}${where}${order ? ` ORDER BY ${order}` : ''}`;
    // OFFSET needs an ORDER BY; (SELECT NULL) keeps the server's order.
    return `SELECT * FROM ${from}${where} ORDER BY ${order || '(SELECT NULL)'} OFFSET ${t.offset} ROWS FETCH NEXT ${n} ROWS ONLY`;
  }

  // Oracle 12c, DB2 and other ODBC targets use the standard row limit.
  if (dbType === 'oracle' || dbType === 'odbc') {
    const offset = t.offset ? ` OFFSET ${t.offset} ROWS` : '';
    return `SELECT * FROM ${from}${where}${order ? ` ORDER BY ${order}` : ''}${offset} FETCH FIRST ${n} ROWS ONLY`;
  }

  return `SELECT * FROM ${from}${where}${order ? ` ORDER BY ${order}` : ''} LIMIT ${n} OFFSET ${t.offset}`;
}

// ////////////////////////////////////////////
// execution

// How a run shows its results: replace those of the run being looked at,
// or add a new result next to the ones there (DBeaver's Ctrl+\\).
export type RunMode = 'replace' | 'append';

let runSeq = 0;

async function execute(t: Tab, sql: string, confirm = false, mode: RunMode = 'replace'): Promise<void> {
  if (t.running) return;
  t.running = true;
  t.error = '';
  t.queryId = newId();
  const run = ++runSeq;
  try {
    const res = await api.post<ExecResult>(`/connections/${t.connectionId}/query`, {
      sql,
      query_id: t.queryId,
      confirm,
      max_rows: t.kind === 'query' ? t.maxRows : 0,
      timeout_sec: t.kind === 'query' && t.timeoutSec >= 0 ? t.timeoutSec : undefined,
    });
    if (t.kind === 'table') {
      const rs = res.results[0];
      t.hasMore = !!rs?.rows && rs.rows.length > t.limit;
      if (t.hasMore) rs.rows = rs.rows!.slice(0, t.limit);
      t.edit.clear();
      t.result = res;
      t.activeResult = 0;
      return;
    }
    for (const r of res.results) {
      r.run = run;
      r.committed = res.committed;
      r.transactional = res.transactional;
    }
    const firstError = res.results.findIndex((r) => r.error);
    const lastRows = res.results.map((r) => r.kind).lastIndexOf('rows');
    place(t, res, res.results, mode, firstError >= 0 ? firstError : Math.max(lastRows, 0));
  } catch (e) {
    if (needsCredentials(e)) {
      t.running = false;
      t.queryId = '';
      if (await askCredentials(t.connectionId, (e as ApiError).message)) return execute(t, sql, confirm, mode);
      fail(t, sql, run, (e as ApiError).message, mode);
      return;
    }
    if (e instanceof ApiError && e.status === 409 && e.data?.confirmation_required) {
      t.running = false;
      t.queryId = '';
      const conn = connection(t.connectionId);
      if (await confirmProduction({ connection: conn?.name ?? '', sql, risk: String(e.data.risk ?? 'write') })) {
        return execute(t, sql, true, mode);
      }
      return;
    }
    fail(t, sql, run, e instanceof Error ? e.message : String(e), mode);
  } finally {
    t.running = false;
    t.queryId = '';
  }
}

// place puts the results of a run into the tab: in place of the run being
// looked at, or after the others. active is the result to show, within list.
function place(t: Tab, res: ExecResult, list: ResultSet[], mode: RunMode, active: number) {
  const old = t.result?.results ?? [];
  if (!old.length) {
    t.result = { ...res, results: list };
    t.activeResult = active;
    return;
  }
  let at = old.length;
  let next = [...old, ...list];
  if (mode === 'replace') {
    const current = old[t.activeResult]?.run;
    const first = old.findIndex((r) => r.run === current);
    if (first >= 0) {
      at = first;
      next = [...old.filter((r, i) => i < first && r.run !== current), ...list, ...old.filter((r, i) => i > first && r.run !== current)];
    } else {
      at = 0;
      next = list;
    }
  }
  t.result = { ...res, results: next };
  t.activeResult = at + active;
}

// fail shows a run that did not reach the database. With other results
// open it becomes one more result, so they stay; otherwise the panel shows
// the error alone.
function fail(t: Tab, sql: string, run: number, msg: string, mode: RunMode) {
  const others = (t.result?.results ?? []).length > 0 && (mode === 'append' || new Set(t.result!.results.map((r) => r.run)).size > 1);
  if (t.kind === 'query' && others) {
    const rs: ResultSet = { statement: sql, kind: 'exec', rows_affected: 0, truncated: false, duration_ms: 0, error: msg, run };
    place(t, { ...t.result!, results: [] }, [rs], mode, 0);
    return;
  }
  t.error = msg;
  t.result = null;
}

export async function run(id: string, sql?: string, mode: RunMode = 'replace') {
  const t = tab(id);
  if (!t) return;
  let text = (sql ?? t.sql).trim();
  if (!text) return;

  // Placeholders are filled in on the client; @set declarations above the
  // statement in the editor count too when only part of it runs.
  if (prefs.variables) {
    const syn = prefs.variableSyntaxes;
    const scope = sql !== undefined && sql.trim() !== t.sql.trim() ? declarationsBefore(t.sql, sql) + text : text;
    const missing = placeholders(scope, syn).filter((p) => p.value === undefined);
    let given: Record<string, string> = {};
    if (missing.length) {
      const answer = await askVariables(missing);
      if (!answer) return;
      given = answer;
    }
    text = substitute(scope, syn, given);
    if (!text) return;
  }

  return execute(t, text, false, mode);
}

// declarationsBefore returns the @set lines of the document, so running one
// statement still sees the values declared for the script.
function declarationsBefore(doc: string, part: string): string {
  const at = doc.indexOf(part.trim());
  const head = at > 0 ? doc.slice(0, at) : doc;
  const found = head.match(/@set\s+[\p{L}_][\p{L}\p{N}_]*\s*=\s*(?:'(?:[^']|'')*'|[^;]*?)\s*;/giu) ?? [];
  return found.length ? found.join('\n') + '\n' : '';
}

export async function runTable(id: string) {
  const t = tab(id);
  if (!t) return;
  await loadColumns(t.connectionId, t.schema, t.table);
  return execute(t, tableSQL(t));
}

// tableKey identifies the column metadata of a table tab in ws.columns.
export function tableKey(t: Tab) {
  return `${t.connectionId}|${t.schema}|${t.table}`;
}

export async function cancel(id: string) {
  const t = tab(id);
  if (!t?.queryId) return;
  try {
    await api.post(`/queries/${t.queryId}/cancel`);
  } catch {
    // The query may have finished in the meantime.
  }
}

// ////////////////////////////////////////////
// saved queries

export async function loadSaved() {
  ws.saved = await api.get<SavedQuery[]>('/saved');
}

export async function saveTab(id: string, name?: string) {
  const t = tab(id);
  if (!t) return;
  const body = { connection_id: t.connectionId, name: name ?? t.title, sql: t.sql };
  if (t.savedId) {
    await api.put(`/saved/${t.savedId}`, body);
  } else {
    const created = await api.post<SavedQuery>('/saved', body);
    t.savedId = created.id;
  }
  t.title = body.name;
  t.dirty = false;
  persist();
  await loadSaved();
}

export async function deleteSaved(id: string) {
  await api.del(`/saved/${id}`);
  for (const t of ws.tabs) if (t.savedId === id) t.savedId = '';
  await loadSaved();
}

// ////////////////////////////////////////////
// Open tabs are kept in localStorage, keyed by user, so people sharing a
// browser do not see each other's tabs. Results are not stored.

interface PersistedTab {
  maxRows?: number;
  timeoutSec?: number;
  kind: TabKind;
  title: string;
  connectionId: string;
  schema: string;
  table: string;
  sql: string;
  savedId: string;
}

let currentUser = '';
let persistTimer: ReturnType<typeof setTimeout> | undefined;

function storageKey(): string {
  return `dataport.tabs.${currentUser}`;
}

export function persist() {
  if (!currentUser) return;
  clearTimeout(persistTimer);
  persistTimer = setTimeout(() => {
    try {
      const tabs: PersistedTab[] = ws.tabs.map((t) => ({
        kind: t.kind, title: t.title, connectionId: t.connectionId,
        schema: t.schema, table: t.table, sql: t.sql, savedId: t.savedId, maxRows: t.maxRows, timeoutSec: t.timeoutSec,
      }));
      const active = ws.tabs.findIndex((t) => t.id === ws.activeId);
      localStorage.setItem(storageKey(), JSON.stringify({ tabs, active }));
    } catch {
      // Storage may be full or disabled.
    }
  }, 300);
}

// startWorkspace resets the state for the user and restores their tabs.
export async function startWorkspace(userId: string) {
  if (currentUser === userId && ws.connectionsLoaded) return;

  currentUser = userId;
  ws.tabs = [];
  ws.activeId = '';
  ws.schemas = {};
  ws.objects = {};
  ws.columns = {};
  ws.routines = {};
  ws.expanded = {};
  ws.saved = [];
  ws.connectionsLoaded = false;
  queryCount = 0;

  await loadConnections();

  try {
    const raw = localStorage.getItem(storageKey());
    if (raw) {
      const data = JSON.parse(raw) as { tabs: PersistedTab[]; active: number };
      for (const p of data.tabs) {
        if (p.kind !== 'settings' && !connection(p.connectionId)) continue;
        const t = baseTab(p.kind, p.connectionId);
        Object.assign(t, p);
        if (p.kind === 'settings') t.title = '';
        if (p.kind === 'query') queryCount++;
        ws.tabs.push(t);
      }
      ws.activeId = ws.tabs[data.active]?.id ?? ws.tabs[0]?.id ?? '';
    }
  } catch {
    // Ignore corrupt state.
  }

  // A single connection opens by itself, unless it still needs the user's
  // own account: the sign-in dialog should follow a click, not the page load.
  const only = ws.connections.length === 1 ? ws.connections[0] : undefined;
  if (only && !(only.user_credentials && !only.credential_user)) ws.expanded[only.id] = true;
  for (const c of ws.connections) if (ws.expanded[c.id]) void loadSchemas(c.id);

  if (pendingSettings) {
    openSettings(pendingSettings);
    pendingSettings = '';
  }

  void loadSaved().catch(() => {});
}
