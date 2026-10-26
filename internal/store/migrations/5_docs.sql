-- Documentation is isolated by dataport user, even for shared DB accounts.
-- A lease fences concurrent refreshes across replicas; only its holder may
-- replace the last successful document. JSON is text for SQLite/PostgreSQL.
CREATE TABLE ${TABLE_PREFIX}schema_docs (
    connection_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}connections(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    schema_name TEXT NOT NULL,
    source_version TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT '',
    generated_at BIGINT NOT NULL DEFAULT 0,
    attempted_at BIGINT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    cron_spec TEXT NOT NULL DEFAULT '',
    next_run BIGINT NOT NULL DEFAULT 0,
    lease_token TEXT NOT NULL DEFAULT '',
    lease_until BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (connection_id, user_id, schema_name)
);
CREATE INDEX ${TABLE_PREFIX}schema_docs_due ON ${TABLE_PREFIX}schema_docs(next_run);
