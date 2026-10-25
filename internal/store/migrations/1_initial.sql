CREATE TABLE ${TABLE_PREFIX}users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'user',
    disabled INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    last_login_at BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE ${TABLE_PREFIX}sessions (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    last_seen_at BIGINT NOT NULL,
    ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT ''
);

CREATE INDEX ${TABLE_PREFIX}sessions_user_idx ON ${TABLE_PREFIX}sessions(user_id);

CREATE TABLE ${TABLE_PREFIX}user_groups (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

CREATE TABLE ${TABLE_PREFIX}group_members (
    group_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}user_groups(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);

CREATE INDEX ${TABLE_PREFIX}group_members_user_idx ON ${TABLE_PREFIX}group_members(user_id);

CREATE TABLE ${TABLE_PREFIX}connections (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    db_type TEXT NOT NULL,
    host TEXT NOT NULL DEFAULT '',
    port INTEGER NOT NULL DEFAULT 0,
    database_name TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    password_enc TEXT NOT NULL DEFAULT '',
    options TEXT NOT NULL DEFAULT '{}',
    color TEXT NOT NULL DEFAULT '',
    read_only INTEGER NOT NULL DEFAULT 1,
    production INTEGER NOT NULL DEFAULT 0,
    user_credentials INTEGER NOT NULL DEFAULT 0,
    max_rows INTEGER NOT NULL DEFAULT 0,
    timeout_ms INTEGER NOT NULL DEFAULT 0,
    -- Pool settings as JSON (store.Limits); zero values use the server defaults.
    limits TEXT NOT NULL DEFAULT '{}',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

CREATE TABLE ${TABLE_PREFIX}connection_access (
    connection_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}connections(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    read_only INTEGER NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (connection_id, subject_type, subject_id)
);

CREATE INDEX ${TABLE_PREFIX}connection_access_subject_idx ON ${TABLE_PREFIX}connection_access(subject_type, subject_id);

CREATE TABLE ${TABLE_PREFIX}query_log (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    username TEXT NOT NULL,
    connection_id TEXT NOT NULL,
    connection_name TEXT NOT NULL,
    database_name TEXT NOT NULL DEFAULT '',
    sql_text TEXT NOT NULL,
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    row_count BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL
);

CREATE INDEX ${TABLE_PREFIX}query_log_user_idx ON ${TABLE_PREFIX}query_log(user_id, created_at);
CREATE INDEX ${TABLE_PREFIX}query_log_connection_idx ON ${TABLE_PREFIX}query_log(connection_id, created_at);
CREATE INDEX ${TABLE_PREFIX}query_log_created_idx ON ${TABLE_PREFIX}query_log(created_at);

CREATE TABLE ${TABLE_PREFIX}saved_queries (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    sql_text TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

CREATE INDEX ${TABLE_PREFIX}saved_queries_user_idx ON ${TABLE_PREFIX}saved_queries(user_id);

CREATE TABLE ${TABLE_PREFIX}user_identities (
    provider TEXT NOT NULL,
    subject TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    email TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    last_login_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (provider, subject)
);

CREATE INDEX ${TABLE_PREFIX}user_identities_user_idx ON ${TABLE_PREFIX}user_identities(user_id);

-- Users' own accounts for connections with user_credentials set.
CREATE TABLE ${TABLE_PREFIX}user_credentials (
    connection_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}connections(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    username TEXT NOT NULL,
    password_enc TEXT NOT NULL DEFAULT '',
    -- The user's own pool settings (store.Limits); empty keeps the connection's.
    limits TEXT NOT NULL DEFAULT '{}',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    PRIMARY KEY (connection_id, user_id)
);

CREATE INDEX ${TABLE_PREFIX}user_credentials_user_idx ON ${TABLE_PREFIX}user_credentials(user_id);

CREATE TABLE ${TABLE_PREFIX}settings (
    name TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at BIGINT NOT NULL
);
