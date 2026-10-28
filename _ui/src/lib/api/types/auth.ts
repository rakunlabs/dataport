export type Role = 'admin' | 'user';

export interface Me {
  id: string;
  username: string;
  display_name: string;
  role: Role;
  // Users may add connections of their own.
  can_create_connections?: boolean;
}

export interface AuthInfo {
  // Days a "remember me" sign-in lasts; 0 when the server turned it off.
  remember_days?: number;
  password: boolean;
  providers: { name: string; label: string }[];
}

export interface AccessToken {
  id: string;
  name: string;
  read_only: boolean;
  created_at: number;
  expires_at: number;
  last_used_at: number;
  // The one connection the token may use; empty for all.
  connection_id: string;
  // Set for tokens an MCP client got by signing in (OAuth).
  client_id: string;
}

export interface TokenList {
  tokens: AccessToken[];
  mcp_enabled: boolean;
  mcp_path: string;
  max_ttl_days: number;
  default_rows: number;
  external_base: string;
  // The public URL of dataport with its base path, as the server sees it.
  base_url: string;
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

export interface BuildInfo {
  version: string;
  commit: string;
  date: string;
  go_version: string;
  os: string;
  arch: string;
  cgo: boolean;
  repository: string;
}
