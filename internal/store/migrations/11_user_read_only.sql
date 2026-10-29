-- Connections a user chose to use read-only for themselves, although their
-- grant allows writes. The user turns it back off whenever they like.
CREATE TABLE ${TABLE_PREFIX}user_read_only (
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}connections(id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (user_id, connection_id)
);
