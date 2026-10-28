import { t } from '@/lib/i18n/index.svelte';
import { EditState } from '@/lib/stores/edits.svelte';
import { connection, newId, workspaceConnections, ws, type Tab, type TabKind } from '@/lib/stores/workspace/state.svelte';
import { loadConnections, loadSchemas } from '@/lib/stores/workspace/tree';
import { loadPersisted, persist, persistedUser, setPersistedUser } from '@/lib/stores/workspace/persist';
import { cancel, endTx, run, runTable, setRunElsewhere, syncTxs } from '@/lib/stores/workspace/run';
import { prefs } from '@/lib/stores/prefs.svelte';
import { loadSaved } from '@/lib/stores/workspace/saved';
import { session } from '@/lib/stores/session.svelte';

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
    runStartedAt: 0,
    cancelling: false,
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
    manualTx: false,
    txId: '',
    txOpen: false,
    txStatements: 0,
    txEnding: false,
  };
}

let queryCount = 0;

export function openQueryTab(connectionId: string, sql = '', title = '', savedId = '') {
  const tab = baseTab('query', connectionId);
  queryCount++;
  tab.title = title || t('tabs.query', { n: queryCount });
  tab.sql = sql;
  tab.savedId = savedId;
  // New tabs follow the last choice where the connection allows it.
  tab.manualTx = prefs.manualTx && !!connection(connectionId)?.manual_tx;
  ws.tabs.push(tab);
  ws.activeId = tab.id;
  persist();
  return tab;
}

export function openNotebooks() {
  const existing = ws.tabs.find((t) => t.kind === 'notebooks');
  if (existing) { ws.activeId = existing.id; return; }
  const tab = baseTab('notebooks', '');
  tab.title = t('notebook.title');
  ws.tabs.push(tab); ws.activeId = tab.id; persist();
}

// openDocsHub shows the documentation page: every schema of the user's
// connections with its saved documentation.
export function openDocsHub() {
  const existing = ws.tabs.find((t) => t.kind === 'docsHub');
  if (existing) { ws.activeId = existing.id; return; }
  const tab = baseTab('docsHub', '');
  tab.title = t('app.docs');
  ws.tabs.push(tab); ws.activeId = tab.id; persist();
}

export function openNotebook(id = '', sql = '', connectionId = '') {
  const existing = id && ws.tabs.find((t) => t.kind === 'notebook' && t.savedId === id);
  if (existing) { ws.activeId = existing.id; return existing; }
  const tab = baseTab('notebook', connectionId);
  tab.title = t('notebook.untitled'); tab.savedId = id; tab.sql = sql;
  ws.tabs.push(tab); ws.activeId = tab.id; persist();
  return ws.tabs.find((t) => t.id === tab.id)!;
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

setRunElsewhere((connectionId, sql, maxRows, timeoutSec) => void runInNewTab(connectionId, sql, '', maxRows, timeoutSec));

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

// openDocs shows the documentation of a schema, one tab per schema.
export function openDocs(connectionId: string, schema: string, table = '') {
  const existing = ws.tabs.find((t) => t.kind === 'docs' && t.connectionId === connectionId && t.schema === schema);
  if (existing) {
    if (table) existing.table = table;
    ws.activeId = existing.id;
    return existing;
  }

  const tab = baseTab('docs', connectionId);
  tab.title = schema;
  tab.schema = schema;
  tab.table = table;
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
  if (tab.kind === 'notebook' && tab.dirty && !confirm(t('notebook.closeConfirm'))) return;
  if (tab.kind === 'notebook') tab.notebookAbort?.abort();
  if (tab.txOpen && !confirm(t('tx.closeConfirm', { n: tab.txStatements }))) return;
  if (tab.running) void cancel(tab.id);
  if (tab.txOpen) void endTx(tab, false, true);
  ws.tabs.splice(i, 1);
  if (ws.activeId === id) {
    ws.activeId = ws.tabs[Math.min(i, ws.tabs.length - 1)]?.id ?? '';
  }
  persist();
}

export function closeOthers(id: string) {
  for (const t of ws.tabs.filter((t) => t.id !== id)) closeTab(t.id);
}

// startWorkspace resets the state for the user and restores their tabs.
export async function startWorkspace(userId: string) {
  if (persistedUser() === userId && ws.connectionsLoaded) return;

  setPersistedUser(userId);
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

  const data = loadPersisted();
  if (data) {
    try {
      for (const p of data.tabs) {
        if (!['settings', 'notebooks', 'notebook', 'docsHub'].includes(p.kind) && !connection(p.connectionId)) continue;
        // Notebooks may have been closed to this user since.
        if ((p.kind === 'notebooks' || p.kind === 'notebook') && !session.user?.can_use_notebooks) continue;
        const t = baseTab(p.kind, p.connectionId);
        Object.assign(t, p);
        if (p.kind === 'settings') t.title = '';
        if (p.kind === 'query') queryCount++;
        ws.tabs.push(t);
      }
      ws.activeId = ws.tabs[data.active]?.id ?? ws.tabs[0]?.id ?? '';
    } catch {
      // Ignore corrupt state.
    }
  }

  // Tabs learn which of their transactions the server still holds.
  void syncTxs();

  // A single connection opens by itself, unless it still needs the user's
  // own account: the sign-in dialog should follow a click, not the page load.
  const visible = workspaceConnections();
  const only = visible.length === 1 ? visible[0] : undefined;
  if (only && !(only.user_credentials && !only.credential_user)) ws.expanded[only.id] = true;
  for (const c of visible) if (ws.expanded[c.id]) void loadSchemas(c.id);

  if (pendingSettings) {
    openSettings(pendingSettings);
    pendingSettings = '';
  }

  void loadSaved().catch(() => {});
}
