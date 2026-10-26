-- "Remember me": a remembered session lives longer and its cookie survives
-- closing the browser.
ALTER TABLE ${TABLE_PREFIX}sessions ADD COLUMN remember INTEGER NOT NULL DEFAULT 0;

-- An MCP client the user chose to remember is approved again without
-- asking, with the same choices.
CREATE TABLE ${TABLE_PREFIX}oauth_consents (
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    client_id TEXT NOT NULL,
    connection_id TEXT NOT NULL DEFAULT '',
    read_only INTEGER NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (user_id, client_id)
);
