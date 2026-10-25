export type Role = 'admin' | 'user';

export interface Me {
  id: string;
  username: string;
  display_name: string;
  role: Role;
}

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
}

// Pool settings; 0 or a missing field means "use the default".
export interface Limits {
  max_open?: number;
  max_idle?: number;
  max_lifetime_sec?: number;
  max_idle_time_sec?: number;
}

export interface DbObject {
  name: string;
  kind: 'table' | 'view' | 'materialized_view' | 'foreign_table';
}

export interface ColumnInfo {
  name: string;
  type: string;
  nullable: boolean;
  default: string;
  primary_key: boolean;
}

export interface IndexInfo {
  name: string;
  columns: string[];
  unique: boolean;
  primary: boolean;
  method?: string;
}

export interface ForeignKey {
  name: string;
  columns: string[];
  ref_schema: string;
  ref_table: string;
  ref_columns: string[];
  on_update?: string;
  on_delete?: string;
}

export interface TableStructure {
  columns: ColumnInfo[];
  indexes: IndexInfo[];
  foreign_keys: ForeignKey[];
  ddl: string;
}

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
}

export interface ExecResult {
  results: ResultSet[];
  duration_ms: number;
  committed: boolean;
  transactional: boolean;
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

export interface User {
  id: string;
  username: string;
  display_name: string;
  email: string;
  role: Role;
  disabled: boolean;
  created_at: number;
  updated_at: number;
  last_login_at: number;
  group_ids: string[];
  providers?: string[];
}

export interface Group {
  id: string;
  name: string;
  description: string;
  created_at: number;
  updated_at: number;
  member_ids: string[];
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

export interface DriverInfo {
  type: string;
  default_port: number;
}

export interface SsoProvider {
  name: string;
  display_name: string;
  enabled: boolean;
  issuer_url: string;
  auth_url: string;
  token_url: string;
  userinfo_url: string;
  jwks_url: string;
  client_id: string;
  scopes: string[];
  token_auth_method: string;
  disable_pkce: boolean;
  auto_create: boolean;
  link_by_email: boolean;
  allowed_domains: string[] | null;
  roles_claim: string;
  admin_roles: string[] | null;
  group_mapping: Record<string, string> | null;
  has_secret: boolean;
  callback_url: string;
}

export interface AuthInfo {
  password: boolean;
  providers: { name: string; label: string }[];
}
