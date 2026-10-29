import type { EditState } from '@/lib/stores/edits.svelte';

export interface Column {
  name: string;
  type: string;
}

export interface ResultSet {
  statement: string;
  kind: 'rows' | 'exec';
  columns?: Column[];
  rows?: unknown[][];
  rows_affected: number;
  truncated: boolean;
  duration_ms: number;
  error?: string;
  // Set in the browser: the run the result came from, and how that run
  // ended, so results of several runs can sit side by side.
  run?: number;
  committed?: boolean;
  transactional?: boolean;
  // The run went into a manual transaction that was still open after it.
  inTx?: boolean;
  // Set while a run started into a new result (Ctrl+\) is still going; the
  // result shows its timer and stop button until the run's results replace it.
  pending?: { queryId: string; startedAt: number; cancelling: boolean };
  // Pending edits of a query result read from one table.
  edit?: EditState;
  // Set on the result of an explain: the plan and how it was read.
  plan?: { format: string; analyzed: boolean; source: string; options: ExplainOptions };
  // The total the footer counted for this result, or the count running.
  total?: { count?: number; running?: boolean; error?: string; queryId?: string };
}

export interface ExplainOptions {
  analyze: boolean;
  verbose: boolean;
  costs: boolean;
  buffers: boolean;
  timing: boolean;
  settings: boolean;
  wal: boolean;
  summary: boolean;
  format: string;
}

// ExplainSupport is what a database offers in the explain dialog.
export interface ExplainSupport {
  supported: boolean;
  options: string[] | null;
  formats: string[] | null;
  analyze: boolean;
}

export interface ExplainResult {
  sql: string;
  format: string;
  analyzed: boolean;
  result: ResultSet;
}

export interface CountResult {
  count: number;
  sql: string;
  duration_ms: number;
}

export interface ExecResult {
  results: ResultSet[];
  duration_ms: number;
  committed: boolean;
  transactional: boolean;
  tx_open?: boolean;
  tx_statements?: number;
  tx_started_at?: number;
  tx_rolled_back?: boolean;
}

export interface TxInfo {
  tx_id: string;
  connection_id: string;
  started_at: number;
  last_used_at: number;
  statements: number;
}

export interface QueryLog {
  id: string;
  user_id: string;
  username: string;
  connection_id: string;
  connection_name: string;
  database: string;
  sql: string;
  status: 'ok' | 'error' | 'rejected' | 'unreachable';
  error: string;
  row_count: number;
  duration_ms: number;
  source?: 'web' | 'mcp';
  created_at: number;
}

export interface SavedQuery {
  id: string;
  connection_id: string;
  name: string;
  sql: string;
  created_at: number;
  updated_at: number;
}

export interface ProcessList {
  result: ResultSet;
  self: string;
  can_cancel: boolean;
}

export interface DbUser {
  name: string;
  host?: string;
  attributes: string;
  login: boolean;
  locked: boolean;
}

export interface ImportResult {
  inserted: number;
  statements: string[];
  duration_ms: number;
}

export interface TransferProgress {
  table?: string;
  rows?: number;
  created?: boolean;
  done?: boolean;
  error?: string;
  finished?: boolean;
  tables?: number;
  failed?: number;
}

// How the values of one column of generated test data are made.
export interface GenColumn {
  name: string;
  type?: string;
  nullable?: boolean;
  // identity: numbered by the database; generated: computed, never written.
  auto?: '' | 'identity' | 'generated';
  generator: string;
  min?: number;
  max?: number;
  decimals?: number;
  from?: string;
  to?: string;
  values?: string[];
  template?: string;
  ref?: { schema: string; table: string; column: string };
  null_percent?: number;
}

export interface GeneratePlan {
  columns: GenColumn[];
  rows: unknown[][];
  seed: number;
  generators: string[];
}

export interface GenerateProgress {
  rows: number;
  done?: boolean;
  finished?: boolean;
  error?: string;
}
