import { api, ApiError } from '@/lib/api/client';
import type { ExecResult, ResultSet, TxInfo } from '@/lib/api/types';
import { t as tr } from '@/lib/i18n/index.svelte';
import { prefs, savePrefs } from '@/lib/stores/prefs.svelte';
import { persist } from '@/lib/stores/workspace/persist';
import { confirmProduction } from '@/lib/stores/confirm.svelte';
import { quoteIdent } from '@/lib/helper/copy';
import { askCredentials, needsCredentials } from '@/lib/stores/credentials.svelte';
import { declarationLines, placeholders, substitute } from '@/lib/helper/variables';
import { askVariables } from '@/lib/stores/variables.svelte';
import { errorMessage, toast } from '@/lib/stores/toast.svelte';
import { connection, findTab, newId, ws, type Tab } from '@/lib/stores/workspace/state.svelte';
import { loadColumns } from '@/lib/stores/workspace/tree';

export { quoteIdent };

export function qualified(dbType: string, schema: string, table: string): string {
  const t = quoteIdent(dbType, table);
  return schema ? `${quoteIdent(dbType, schema)}.${t}` : t;
}

// tableKey identifies the column metadata of a table tab in ws.columns.
export function tableKey(t: Tab) {
  return `${t.connectionId}|${t.schema}|${t.table}`;
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

  // Trino takes OFFSET before LIMIT only.
  if (dbType === 'trino') {
    const offset = t.offset ? ` OFFSET ${t.offset}` : '';
    return `SELECT * FROM ${from}${where}${order ? ` ORDER BY ${order}` : ''}${offset} LIMIT ${n}`;
  }

  return `SELECT * FROM ${from}${where}${order ? ` ORDER BY ${order}` : ''} LIMIT ${n} OFFSET ${t.offset}`;
}

// How a run shows its results: replace those of the run being looked at,
// or add a new result next to the ones there (DBeaver's Ctrl+\\).
export type RunMode = 'replace' | 'append';

let runSeq = 0;

// runElsewhere opens a new query tab and runs there; tabs.ts sets it, as it
// imports this module.
let runElsewhere: ((connectionId: string, sql: string, maxRows: number, timeoutSec: number) => void) | undefined;

export function setRunElsewhere(fn: typeof runElsewhere) {
  runElsewhere = fn;
}

async function execute(t: Tab, sql: string, confirm = false, mode: RunMode = 'replace'): Promise<void> {
  if (t.running) return;
  t.running = true;
  t.runStartedAt = Date.now();
  t.cancelling = false;
  t.error = '';
  t.queryId = newId();
  const run = ++runSeq;
  const inTx = t.kind === 'query' && t.manualTx && !!connection(t.connectionId)?.manual_tx;
  if (inTx && !t.txId) {
    t.txId = newId();
    persist();
  }
  try {
    const res = await api.post<ExecResult>(`/connections/${t.connectionId}/query`, {
      sql,
      query_id: t.queryId,
      confirm,
      max_rows: t.kind === 'query' ? t.maxRows : 0,
      timeout_sec: t.kind === 'query' && t.timeoutSec >= 0 ? t.timeoutSec : undefined,
      ...(inTx ? { tx_id: t.txId, tx_continue: t.txOpen } : {}),
    });
    if (inTx) {
      t.txOpen = !!res.tx_open;
      t.txStatements = res.tx_statements ?? 0;
      if (res.tx_rolled_back) toast(tr('tx.stoppedRolledBack'), 'error');
    }
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
      r.inTx = !!res.tx_open;
    }
    const firstError = res.results.findIndex((r) => r.error);
    const lastRows = res.results.map((r) => r.kind).lastIndexOf('rows');
    place(t, res, res.results, mode, firstError >= 0 ? firstError : Math.max(lastRows, 0));
  } catch (e) {
    if (needsCredentials(e)) {
      t.running = false;
      t.queryId = '';
      // await: the finally below must not end the retried run.
      if (await askCredentials(t.connectionId, (e as ApiError).message)) return await execute(t, sql, confirm, mode);
      fail(t, sql, run, (e as ApiError).message, mode);
      return;
    }
    if (e instanceof ApiError && e.status === 409 && e.data?.confirmation_required) {
      t.running = false;
      t.queryId = '';
      const conn = connection(t.connectionId);
      if (await confirmProduction({ connection: conn?.name ?? '', sql, risk: String(e.data.risk ?? 'write') })) {
        return await execute(t, sql, true, mode);
      }
      return;
    }
    if (e instanceof ApiError && e.status === 410 && e.data?.tx_lost) {
      txLost(t);
    }
    fail(t, sql, run, errorMessage(e), mode);
  } finally {
    t.running = false;
    t.cancelling = false;
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
  const t = findTab(id);
  if (!t) return;
  let text = (sql ?? t.sql).trim();
  if (!text) return;

  // A tab runs one thing at a time; another run while it is busy goes to a
  // tab of its own, so both run side by side.
  if (t.running && t.kind === 'query') {
    runElsewhere?.(t.connectionId, text, t.maxRows, t.timeoutSec);
    return;
  }

  // Placeholders are filled in on the client; @set declarations above the
  // statement in the editor count too when only part of it runs.
  if (prefs.variables) {
    const syn = prefs.variableSyntaxes;
    const dbType = connection(t.connectionId)?.db_type ?? '';
    const scope = sql !== undefined && sql.trim() !== t.sql.trim() ? declarationsBefore(t.sql, sql, dbType) + text : text;
    const missing = placeholders(scope, syn, dbType).filter((p) => p.value === undefined);
    let given: Record<string, string> = {};
    if (missing.length) {
      const answer = await askVariables(missing);
      if (!answer) return;
      given = answer;
    }
    text = substitute(scope, syn, given, dbType);
    if (!text) return;
  }

  return execute(t, text, false, mode);
}

// declarationsBefore returns the @set lines of the document, so running one
// statement still sees the values declared for the script.
function declarationsBefore(doc: string, part: string, dbType: string): string {
  const at = doc.indexOf(part.trim());
  return declarationLines(at > 0 ? doc.slice(0, at) : doc, dbType);
}

export async function runTable(id: string) {
  const t = findTab(id);
  if (!t) return;
  await loadColumns(t.connectionId, t.schema, t.table, 'failed');
  return execute(t, tableSQL(t));
}

export async function cancel(id: string) {
  const t = findTab(id);
  if (!t?.queryId) return;
  t.cancelling = true;
  try {
    await api.post(`/queries/${t.queryId}/cancel`);
  } catch {
    // The query may have finished in the meantime.
  }
}

// ////////////////////////////////////////////
// manual transactions

// txLost forgets a transaction the server no longer holds; the next run
// starts a new one.
function txLost(t: Tab) {
  if (t.txOpen) toast(tr('tx.lost'), 'error');
  t.txOpen = false;
  t.txStatements = 0;
  t.txId = '';
  persist();
}

// endTx commits or rolls back the tab's transaction. quiet is for a tab
// that is closing.
export async function endTx(t: Tab, commit: boolean, quiet = false): Promise<boolean> {
  if (!t.txId || t.txEnding) return false;
  t.txEnding = true;
  try {
    await api.post(`/connections/${t.connectionId}/tx`, { tx_id: t.txId, commit });
    if (!quiet) toast(commit ? tr('tx.committed') : tr('tx.rolledBack'), 'success', 1500);
    t.txOpen = false;
    t.txStatements = 0;
    t.txId = '';
    persist();
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.status === 410) {
      txLost(t);
      return false;
    }
    if (!quiet) toast(errorMessage(e), 'error');
    return false;
  } finally {
    t.txEnding = false;
  }
}

// setManualTx turns the tab's manual transaction on or off and remembers
// the choice for new tabs. An open transaction has to end first.
export function setManualTx(t: Tab, on: boolean) {
  if (t.txOpen) return;
  t.manualTx = on;
  prefs.manualTx = on;
  savePrefs();
  persist();
}

// syncTxs asks the server which transactions are still open, after a
// reload: a tab keeps its transaction only while the server holds it.
export async function syncTxs(): Promise<void> {
  let open: TxInfo[];
  try {
    open = await api.get<TxInfo[]>('/tx');
  } catch {
    return;
  }
  const byId = new Map(open.map((x) => [x.tx_id, x]));
  for (const t of ws.tabs) {
    if (!t.txId) continue;
    const info = byId.get(t.txId);
    if (info && info.connection_id === t.connectionId) {
      t.txOpen = true;
      t.txStatements = info.statements;
    } else {
      t.txOpen = false;
      t.txStatements = 0;
      t.txId = '';
    }
  }
  persist();
}
