export interface ConnectionView {
  id: string;
  name: string;
  description: string;
  db_type: string;
  database: string;
  color: string;
  read_only: boolean;
  production: boolean;
  max_rows: number;
  timeout_ms: number;
  // Set when users sign in with their own database account.
  user_credentials?: boolean;
  credential_user?: string;
  // Own-account connections: the connection's effective limits and the
  // user's overrides.
  limits?: Limits;
  my_limits?: Limits;
  // Rows can be edited in the grid; needs transactions.
  editable?: boolean;
  // A query tab may keep a transaction open over several runs.
  manual_tx?: boolean;
  // Most rows one statement may return; 0 means no limit.
  max_rows_limit?: number;
  // Longest time limit a run may have; 0 means runs may have none.
  max_timeout_ms?: number;
}

// Pool settings; 0 or a missing field means "use the default".
export interface Limits {
  max_open?: number;
  max_idle?: number;
  max_lifetime_sec?: number;
  max_idle_time_sec?: number;
}

export interface Access {
  subject_type: 'user' | 'group';
  subject_id: string;
  read_only: boolean;
}

export interface Connection {
  id: string;
  name: string;
  description: string;
  db_type: string;
  host: string;
  port: number;
  database: string;
  username: string;
  has_password: boolean;
  options: Record<string, string>;
  color: string;
  read_only: boolean;
  production: boolean;
  user_credentials: boolean;
  limits: Limits;
  max_rows: number;
  timeout_ms: number;
  created_at: number;
  updated_at: number;
  access: Access[];
}

export type DriverTarget = 'network' | 'file' | 'dsn';

export interface DriverInfo {
  type: string;
  default_port: number;
  target: DriverTarget;
  transactional: boolean;
  // False when this dataport build lacks the driver (ODBC, DuckDB need cgo).
  available: boolean;
}

export interface DriverList {
  drivers: DriverInfo[];
  files_enabled: boolean;
  odbc_dialects: string[];
}
