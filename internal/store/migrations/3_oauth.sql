-- OAuth sign-in for MCP clients. A client registers itself, the user
-- approves it in the browser, and it gets an access token (a row of
-- access_tokens) that it renews with a refresh token.

-- connection_id limits a token to one connection; empty allows all of the
-- user's. client_id is set for tokens an OAuth client got.
ALTER TABLE ${TABLE_PREFIX}access_tokens ADD COLUMN connection_id TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}access_tokens ADD COLUMN client_id TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}access_tokens ADD COLUMN refresh_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}access_tokens ADD COLUMN refresh_expires_at BIGINT NOT NULL DEFAULT 0;

CREATE INDEX ${TABLE_PREFIX}access_tokens_refresh_idx ON ${TABLE_PREFIX}access_tokens(refresh_hash);

CREATE TABLE ${TABLE_PREFIX}oauth_clients (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    -- JSON array of the redirect URIs the client registered.
    redirect_uris TEXT NOT NULL,
    created_at BIGINT NOT NULL
);

-- Authorization codes live a few minutes and are used once.
CREATE TABLE ${TABLE_PREFIX}oauth_codes (
    code_hash TEXT PRIMARY KEY,
    client_id TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    redirect_uri TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    connection_id TEXT NOT NULL DEFAULT '',
    read_only INTEGER NOT NULL DEFAULT 1,
    expires_at BIGINT NOT NULL
);
