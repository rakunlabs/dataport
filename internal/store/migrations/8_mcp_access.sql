-- What MCP agents may do on each connection, chosen by each user. A user
-- without a policy keeps the previous behaviour: every connection, as far as
-- the grant and token allow.
CREATE TABLE ${TABLE_PREFIX}mcp_policies (
    user_id TEXT PRIMARY KEY REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    -- Mode of connections without a row below, such as newly shared ones.
    default_mode TEXT NOT NULL DEFAULT 'off',
    updated_at BIGINT NOT NULL
);

CREATE TABLE ${TABLE_PREFIX}mcp_access (
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}connections(id) ON DELETE CASCADE,
    -- off, read, write or ddl.
    mode TEXT NOT NULL,
    PRIMARY KEY (user_id, connection_id)
);
