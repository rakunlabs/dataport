-- Personal access tokens for the MCP endpoint. Only a hash of the token is
-- kept; it acts as its user with the user's grants.
CREATE TABLE ${TABLE_PREFIX}access_tokens (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    -- read_only limits the token to reading even where the user may write.
    read_only INTEGER NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL DEFAULT 0,
    last_used_at BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX ${TABLE_PREFIX}access_tokens_user_idx ON ${TABLE_PREFIX}access_tokens(user_id);

-- Where a statement came from: "web" or "mcp".
ALTER TABLE ${TABLE_PREFIX}query_log ADD COLUMN source TEXT NOT NULL DEFAULT 'web';
