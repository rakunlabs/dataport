import type { Notebook } from '@/lib/api/notebooks';
import { ws, type TabKind } from '@/lib/stores/workspace/state.svelte';

// Open tabs are kept in localStorage, keyed by user, so people sharing a
// browser do not see each other's tabs. Results are not stored.

export interface PersistedTab {
  notebook?: Notebook;
  notebookInput?: string;
  dirty?: boolean;
  maxRows?: number;
  timeoutSec?: number;
  manualTx?: boolean;
  txId?: string;
  kind: TabKind;
  fromDocs?: boolean;
  where?: string;
  orderBy?: string;
  limit?: number;
  view?: 'data' | 'structure' | 'ddl';
  title: string;
  connectionId: string;
  schema: string;
  table: string;
  sql: string;
  savedId: string;
}

let currentUser = '';
let persistTimer: ReturnType<typeof setTimeout> | undefined;
// Saving waits while the tabs are being restored, and stays off when they
// could not be: the tabs on screen are then not all the user had, and
// writing them would lose the rest.
let paused = false;

export function pausePersist(on: boolean) {
  paused = on;
  if (on) {
    clearTimeout(persistTimer);
    persistTimer = undefined;
  }
}

// openConnections lists the connections left open in the tree, so the
// next visit opens those and leaves the closed ones closed.
export function openConnections(): string[] {
  return Object.keys(ws.expanded).filter((k) => !k.includes('|') && ws.expanded[k]);
}

export function persistedUser(): string {
  return currentUser;
}

export function setPersistedUser(userId: string) {
  currentUser = userId;
}

function storageKey(): string {
  return `dataport.tabs.${currentUser}`;
}

export function persist() {
  if (!currentUser || paused) return;
  clearTimeout(persistTimer);
  persistTimer = setTimeout(write, 300);
}

// A notebook draft bigger than this is not kept in the browser: the
// storage of a site holds about 5 MB, and one large notebook would stop
// every other tab from being saved. A saved notebook reloads from the
// server anyway.
const maxNotebookDraft = 512 * 1024;

function notebookDraft(t: (typeof ws.tabs)[number]): Partial<PersistedTab> {
  if (t.kind !== 'notebook') return {};
  const draft = { notebook: t.notebook, notebookInput: t.notebookInput, dirty: t.dirty };
  if (JSON.stringify(draft).length <= maxNotebookDraft) return draft;
  // The tab reloads a saved notebook by its id; unsaved changes this large
  // are not kept over a reload.
  return { dirty: false };
}

let warned = false;

function write() {
  persistTimer = undefined;
  if (!currentUser || paused) return;
  try {
    const tabs: PersistedTab[] = ws.tabs.map((t) => ({
      kind: t.kind, title: t.title, connectionId: t.connectionId,
      ...notebookDraft(t),
      schema: t.schema, table: t.table, sql: t.sql, savedId: t.savedId, maxRows: t.maxRows, timeoutSec: t.timeoutSec,
      ...(t.kind === 'query' ? { manualTx: t.manualTx, txId: t.txId } : {}),
      ...(t.kind === 'diagram' && t.fromDocs ? { fromDocs: true } : {}),
      ...(t.kind === 'table' ? { where: t.where, orderBy: t.orderBy, limit: t.limit, view: t.view } : {}),
    }));
    const active = ws.tabs.findIndex((t) => t.id === ws.activeId);
    localStorage.setItem(storageKey(), JSON.stringify({ tabs, active, open: openConnections() }));
    warned = false;
  } catch {
    // Storage may be full or disabled; said once, not on every keystroke.
    if (!warned) {
      warned = true;
      console.warn('dataport: open tabs cannot be saved in this browser');
    }
  }
}

// What was typed just before the page closes or reloads is kept too.
function flush() {
  if (persistTimer === undefined) return;
  clearTimeout(persistTimer);
  write();
}

if (typeof window !== 'undefined') {
  window.addEventListener('pagehide', flush);
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') flush();
  });
}

// loadPersisted reads the user's saved tabs; corrupt state reads as none.
// open is missing in state saved before it was kept.
export function loadPersisted(): { tabs: PersistedTab[]; active: number; open?: string[] } | null {
  try {
    const raw = localStorage.getItem(storageKey());
    return raw ? (JSON.parse(raw) as { tabs: PersistedTab[]; active: number; open?: string[] }) : null;
  } catch {
    return null;
  }
}
