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
  title: string;
  connectionId: string;
  schema: string;
  table: string;
  sql: string;
  savedId: string;
}

let currentUser = '';
let persistTimer: ReturnType<typeof setTimeout> | undefined;

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
  if (!currentUser) return;
  clearTimeout(persistTimer);
  persistTimer = setTimeout(() => {
    try {
      const tabs: PersistedTab[] = ws.tabs.map((t) => ({
        kind: t.kind, title: t.title, connectionId: t.connectionId,
        ...(t.kind === 'notebook' ? { notebook: t.notebook, notebookInput: t.notebookInput, dirty: t.dirty } : {}),
        schema: t.schema, table: t.table, sql: t.sql, savedId: t.savedId, maxRows: t.maxRows, timeoutSec: t.timeoutSec,
        ...(t.kind === 'query' ? { manualTx: t.manualTx, txId: t.txId } : {}),
      }));
      const active = ws.tabs.findIndex((t) => t.id === ws.activeId);
      localStorage.setItem(storageKey(), JSON.stringify({ tabs, active }));
    } catch {
      // Storage may be full or disabled.
    }
  }, 300);
}

// loadPersisted reads the user's saved tabs; corrupt state reads as none.
export function loadPersisted(): { tabs: PersistedTab[]; active: number } | null {
  try {
    const raw = localStorage.getItem(storageKey());
    return raw ? (JSON.parse(raw) as { tabs: PersistedTab[]; active: number }) : null;
  } catch {
    return null;
  }
}
