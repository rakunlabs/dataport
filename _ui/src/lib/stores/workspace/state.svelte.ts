import type { ColumnInfo, ConnectionView, DbObject, ExecResult, Routine, SavedQuery } from '@/lib/api/types';
import type { EditState } from '@/lib/stores/edits.svelte';
import type { Notebook, NotebookResult } from '@/lib/api/notebooks';

export type TabKind = 'query' | 'table' | 'settings' | 'diagram' | 'docs' | 'docsHub' | 'processes' | 'users' | 'notebooks' | 'notebook';

export interface Tab {
  notebook?: Notebook;
  notebookResult?: NotebookResult;
  notebookAbort?: AbortController;
  notebookInput?: string;
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
  // When the run in progress started (ms), and whether a stop was asked.
  runStartedAt: number;
  cancelling: boolean;
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
  // Manual transaction: runs share one transaction, identified by txId,
  // until the user commits or rolls it back. txOpen is what the server
  // last said; txStatements counts what ran in it.
  manualTx: boolean;
  txId: string;
  txOpen: boolean;
  txStatements: number;
  txEnding: boolean;
}

export interface TreeNode<T> {
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

export function newId(): string {
  tabSeq++;
  return `${Date.now().toString(36)}-${tabSeq}`;
}

export function connection(id: string): ConnectionView | undefined {
  return ws.connections.find((c) => c.id === id);
}

export function activeTab(): Tab | undefined {
  return ws.tabs.find((t) => t.id === ws.activeId);
}

export function findTab(id: string): Tab | undefined {
  return ws.tabs.find((t) => t.id === id);
}
