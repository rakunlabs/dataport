import { api } from '@/lib/api/client';
import { withCredentials } from '@/lib/stores/credentials.svelte';
import type { ColumnInfo } from '@/lib/api/types';

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

  value(row: number, col: number, original: unknown): unknown {
    const cols = this.dirty.get(row);
    return cols?.has(col) ? cols.get(col) : original;
  }

  isDirty(row: number, col: number): boolean {
    return this.dirty.get(row)?.has(col) ?? false;
  }

  // edits builds the request for the server. Key values come from the loaded
  // row, never from the pending values, so a changed key still finds the row.
  edits(columns: string[], meta: ColumnInfo[], rows: unknown[][]): RowEdit[] {
    const pk = meta.filter((c) => c.primary_key).map((c) => c.name);
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

function sameValue(a: unknown, b: unknown): boolean {
  if (a === null || a === undefined) return b === null || b === undefined;
  return String(a) === String(b);
}

export async function previewEdits(connectionId: string, schema: string, table: string, edits: RowEdit[]) {
  return withCredentials(connectionId, () =>
    api.post<{ statements: string[] }>(`/connections/${connectionId}/edits/preview`, { schema, table, edits }),
  );
}

export async function saveEdits(connectionId: string, schema: string, table: string, edits: RowEdit[], confirm: boolean) {
  return withCredentials(connectionId, () =>
    api.post<{ affected: number; statements: string[]; duration_ms: number }>(`/connections/${connectionId}/edits`, {
      schema,
      table,
      edits,
      confirm,
    }),
  );
}
