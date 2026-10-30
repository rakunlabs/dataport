-- Documentation an admin turns on per connection is built by the server
-- with a documentation account and read by every user of the connection.
-- Its snapshots are kept under the user id 'system', which is no user, so
-- schema_docs loses its foreign key to users; DeleteUser removes a user's
-- snapshots itself.
CREATE TABLE ${TABLE_PREFIX}schema_docs_new (
    connection_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}connections(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    schema_name TEXT NOT NULL,
    source_version TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT '',
    generated_at BIGINT NOT NULL DEFAULT 0,
    attempted_at BIGINT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    lease_token TEXT NOT NULL DEFAULT '',
    lease_until BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (connection_id, user_id, schema_name)
);
INSERT INTO ${TABLE_PREFIX}schema_docs_new (connection_id, user_id, schema_name, source_version, payload, generated_at, attempted_at, last_error, lease_token, lease_until)
SELECT connection_id, user_id, schema_name, source_version, payload, generated_at, attempted_at, last_error, lease_token, lease_until FROM ${TABLE_PREFIX}schema_docs;
DROP TABLE ${TABLE_PREFIX}schema_docs;
ALTER TABLE ${TABLE_PREFIX}schema_docs_new RENAME TO ${TABLE_PREFIX}schema_docs;
CREATE INDEX ${TABLE_PREFIX}schema_docs_attempted ON ${TABLE_PREFIX}schema_docs(attempted_at);

-- Which schemas of a connection are documented for everyone, and the
-- account that reads their catalog. Patterns are JSON arrays of globs.
CREATE TABLE ${TABLE_PREFIX}doc_connections (
    connection_id TEXT PRIMARY KEY REFERENCES ${TABLE_PREFIX}connections(id) ON DELETE CASCADE,
    enabled INTEGER NOT NULL DEFAULT 0,
    include_schemas TEXT NOT NULL DEFAULT '[]',
    exclude_schemas TEXT NOT NULL DEFAULT '[]',
    username TEXT NOT NULL DEFAULT '',
    password_enc TEXT NOT NULL DEFAULT '',
    -- The last time the schemas were listed, and a safe error code when
    -- that failed.
    discovered_at BIGINT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    updated_at BIGINT NOT NULL DEFAULT 0
);
