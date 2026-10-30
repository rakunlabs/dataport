import { api, ApiError } from '@/lib/api/client';
import { withCredentials } from '@/lib/stores/credentials.svelte';
import { confirmProduction } from '@/lib/stores/confirm.svelte';
import { errorMessage, toast } from '@/lib/stores/toast.svelte';
import { t } from '@/lib/i18n/index.svelte';
import type { ColumnInfo, ExecResult } from '@/lib/api/types';

// Pending changes of one table tab, kept until saved or discarded. Rows are
// addressed by their index in the loaded result; new rows live apart.

export interface RowEdit {
  op: 'update' | 'insert' | 'delete';
  key?: Record<string, unknown>;
  values?: Record<string, unknown>;
}

export class EditState {
  // row index -> column index -> new value
  dirty = $state(new Map<number, Map<number, unknown>>());
  deleted = $state(new Set<number>());
  inserted = $state<unknown[][]>([]);

  get count(): number {
    let n = this.deleted.size + this.inserted.length;
    for (const [row, cols] of this.dirty) if (!this.deleted.has(row) && cols.size) n++;
    return n;
  }

  clear() {
    this.dirty = new Map();
    this.deleted = new Set();
    this.inserted = [];
  }

  set(row: number, col: number, value: unknown, original: unknown) {
    const next = new Map(this.dirty);
    const cols = new Map(next.get(row) ?? []);
    if (sameValue(value, original)) cols.delete(col);
    else cols.set(col, value);
    if (cols.size) next.set(row, cols);
    else next.delete(row);
    this.dirty = next;
  }

  setNew(index: number, col: number, value: unknown) {
    this.inserted[index][col] = value;
  }

  toggleDelete(rows: number[]) {
    const next = new Set(this.deleted);
    const allDeleted = rows.every((r) => next.has(r));
    for (const r of rows) {
      if (allDeleted) next.delete(r);
      else next.add(r);
    }
    this.deleted = next;
  }

  addRow(width: number, from?: unknown[]) {
    this.inserted.push(from ? [...from] : Array(width).fill(null));
  }

  removeNew(index: number) {
    this.inserted.splice(index, 1);
  }

  // clone adds the selected rows again as new rows, as shown; key columns
  // are cleared so the database assigns new ones.
  clone(rows: unknown[][], sel: { existing: number[]; added: number[] }, width: number, keyCols: Set<number>) {
    for (const r of sel.existing) this.addRow(width, rows[r].map((v, i) => (keyCols.has(i) ? null : this.value(r, i, v))));
    for (const i of sel.added) this.addRow(width, [...this.inserted[i]]);
  }

  // remove drops selected new rows and marks or unmarks the others for
  // deletion. added must be in descending order.
  remove(sel: { existing: number[]; added: number[] }) {
    for (const i of sel.added) this.removeNew(i);
    if (sel.existing.length) this.toggleDelete(sel.existing);
  }

  value(row: number, col: number, original: unknown): unknown {
    const cols = this.dirty.get(row);
    return cols?.has(col) ? cols.get(col) : original;
  }

  isDirty(row: number, col: number): boolean {
    return this.dirty.get(row)?.has(col) ?? false;
  }

  // edits builds the request for the server. Key values come from the loaded
  // row, never from the pending values, so a changed key still finds the row.
  // virtualKey stands in for a missing primary key.
  edits(columns: string[], meta: ColumnInfo[], rows: unknown[][], virtualKey: string[] = []): RowEdit[] {
    const primary = meta.filter((c) => c.primary_key).map((c) => c.name);
    const pk = primary.length ? primary : virtualKey;
    const keyOf = (row: unknown[]) => Object.fromEntries(pk.map((name) => [name, row[columns.indexOf(name)]]));
    const out: RowEdit[] = [];

    for (const [row, cols] of this.dirty) {
      if (this.deleted.has(row) || !cols.size) continue;
      const values: Record<string, unknown> = {};
      for (const [col, v] of cols) values[columns[col]] = v;
      out.push({ op: 'update', key: keyOf(rows[row]), values });
    }

    for (const row of this.deleted) out.push({ op: 'delete', key: keyOf(rows[row]) });

    for (const row of this.inserted) {
      const values: Record<string, unknown> = {};
      // NULL is left out so the database applies the column default.
      row.forEach((v, i) => {
        if (v !== null && v !== undefined) values[columns[i]] = v;
      });
      out.push({ op: 'insert', values });
    }

    return out;
  }
}

// pendingEdits counts the unsaved changes of a tab: its table's, and those
// of query results read from one table.
export function pendingEdits(t: { edit: EditState; result: ExecResult | null }): number {
  let n = t.edit.count;
  for (const r of t.result?.results ?? []) n += r.edit?.count ?? 0;
  return n;
}

// keepEdits asks before results with unsaved edits are dropped; true means
// go on.
export function keepEdits(results: { edit?: EditState }[] | undefined): boolean {
  const n = (results ?? []).reduce((sum, r) => sum + (r.edit?.count ?? 0), 0);
  return !n || confirm(t('edit.discardConfirm', { count: n }));
}

function sameValue(a: unknown, b: unknown): boolean {
  if (a === null || a === undefined) return b === null || b === undefined;
  // JSON values arrive as objects and are edited as their text.
  const text = (v: unknown) => (v !== null && typeof v === 'object' ? JSON.stringify(v) : String(v));
  return text(a) === text(b);
}

export async function previewEdits(connectionId: string, schema: string, table: string, edits: RowEdit[], keyColumns?: string[]) {
  return withCredentials(connectionId, () =>
    api.post<{ statements: string[] }>(`/connections/${connectionId}/edits/preview`, { schema, table, edits, key_columns: keyColumns?.length ? keyColumns : undefined }),
  );
}

export interface EditTarget {
  connectionId: string;
  // Named in the production confirmation.
  connectionName: string;
  schema: string;
  table: string;
  // A virtual key, for a table without a primary key.
  keyColumns?: string[];
}

// commitEdits saves edits in one transaction, asking first on a production
// database, and tells the outcome in a toast. It reports whether they were
// saved; failed edits stay pending so the user can fix and retry.
export async function commitEdits(to: EditTarget, edits: RowEdit[], preview?: string[] | null): Promise<boolean> {
  let confirmed = false;
  for (;;) {
    try {
      const res = await saveEdits(to.connectionId, to.schema, to.table, edits, confirmed, to.keyColumns);
      toast(t('edit.saved', { count: res.affected }), 'success');
      return true;
    } catch (e) {
      if (!confirmed && e instanceof ApiError && e.status === 409 && e.data?.confirmation_required) {
        let sql = preview?.join(';\n') ?? '';
        if (!sql) {
          try {
            sql = (await previewEdits(to.connectionId, to.schema, to.table, edits, to.keyColumns)).statements.join(';\n');
          } catch {
            // The confirmation then shows no statements.
          }
        }
        if (!(await confirmProduction({ connection: to.connectionName, sql, risk: 'write' }))) return false;
        confirmed = true;
        continue;
      }
      toast(errorMessage(e), 'error', 8000);
      return false;
    }
  }
}

export async function saveEdits(connectionId: string, schema: string, table: string, edits: RowEdit[], confirm: boolean, keyColumns?: string[]) {
  return withCredentials(connectionId, () =>
    api.post<{ affected: number; statements: string[]; duration_ms: number }>(`/connections/${connectionId}/edits`, {
      schema,
      table,
      edits,
      confirm,
      key_columns: keyColumns?.length ? keyColumns : undefined,
    }),
  );
}
