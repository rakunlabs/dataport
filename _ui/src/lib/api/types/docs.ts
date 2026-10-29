import type { ColumnInfo, ForeignKey, IndexInfo, Routine } from '@/lib/api/types/schema';

export interface DocColumn extends ColumnInfo {
  comment?: string;
}

export interface DocReference {
  table: string;
  name: string;
  columns: string[];
  ref_columns: string[];
}

export interface DocTable {
  name: string;
  kind: string;
  comment?: string;
  // The database's own estimate; absent when it keeps no statistics.
  rows?: number;
  columns: DocColumn[];
  indexes: IndexInfo[];
  foreign_keys: ForeignKey[];
  referenced_by: DocReference[];
  definition?: string;
}

export type AnomalyKind =
  | 'no_primary_key'
  | 'unindexed_foreign_key'
  | 'implied_relationship'
  | 'orphan_table'
  | 'single_column'
  | 'nullable_unique'
  | 'null_string_default'
  | 'incrementing_columns'
  | 'duplicate_index';

export interface Anomaly {
  kind: AnomalyKind;
  table: string;
  columns?: string[];
  ref?: string;
  ref_columns?: string[];
}

export interface SchemaDoc {
  schema: string;
  db_type: string;
  generated_at: number;
  tables: DocTable[];
  routines: Routine[];
  anomalies: Anomaly[];
  // False where the database keeps no keys or indexes to check.
  keys: boolean;
  truncated: boolean;
  table_count: number;
}

export interface DocStatus {
  next_run: number;
  attempted_at: number;
  last_error: string;
  refreshing: boolean;
  // Set when the user reads a snapshot they may not refresh.
  read_only?: boolean;
}

export interface SavedDoc {
  document: SchemaDoc;
  status: DocStatus;
}

// A saved documentation snapshot, without its document.
export interface DocInfo {
  connection_id: string;
  connection_name: string;
  db_type: string;
  user_id?: string;
  username?: string;
  schema: string;
  generated_at: number;
  attempted_at: number;
  last_error: string;
  refreshing: boolean;
  ready: boolean;
}

// A refresh of every snapshot, scheduled or started by an admin.
export interface DocRun {
  id?: string;
  running: boolean;
  manual: boolean;
  username?: string;
  status?: '' | 'running' | 'done' | 'failed' | 'stopped';
  started_at: number;
  finished_at: number;
  total: number;
  done: number;
  failed: number;
  estimated_end?: number;
}

export interface DocList {
  docs: DocInfo[];
  next_run: number;
  run: DocRun;
}
