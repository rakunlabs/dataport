import type { ColumnInfo, ForeignKey, IndexInfo, Routine, TriggerInfo } from '@/lib/api/types/schema';

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
  triggers?: TriggerInfo[];
  definition?: string;
  partition?: PartitionInfo;
  // Rows of a table an admin chose to show, as text.
  values?: { columns: string[]; rows: string[][] };
}

// How a partitioned table is split; its partitions are not documented.
export interface PartitionInfo {
  key: string;
  count: number;
  first?: string;
  last?: string;
  leaves?: number;
  // Indexes made on partitions one by one, and on how many.
  indexes?: (IndexInfo & { partitions: number })[];
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
  | 'duplicate_index'
  | 'duplicate_foreign_key';

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
  // Set on documentation the server builds for every user of the
  // connection with its documentation account.
  system?: boolean;
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
  system?: boolean;
  // Set on the one snapshot every user of a shared database account reads.
  shared?: boolean;
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
  // Connections documented for everyone: docs lists their schemas.
  documented: string[];
  // Per connection, the patterns of the schemas still open; the others
  // are closed to documentation.
  blocked: Record<string, { include: string[]; exclude: string[] }>;
  next_run: number;
  run: DocRun;
}

// How a connection is documented for every user of it, for admins.
export interface DocConnection {
  connection_id: string;
  name: string;
  db_type: string;
  user_credentials: boolean;
  notebook_only: boolean;
  enabled: boolean;
  include: string[];
  exclude: string[];
  // Schemas not chosen are closed to documentation for everyone.
  block_excluded: boolean;
  lookup_tables?: string[];
  username: string;
  has_password: boolean;
  discovered_at: number;
  last_error: '' | 'discovery_failed' | 'account_needed' | 'limit_reached';
  schemas: number;
  ready: number;
  failed: number;
}

export interface DocSchemaPreview {
  schemas: { name: string; included: boolean }[];
  limit: number;
}
