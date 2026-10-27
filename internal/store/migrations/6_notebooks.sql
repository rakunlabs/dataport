CREATE TABLE ${TABLE_PREFIX}notebooks (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    path TEXT NOT NULL UNIQUE,
    document TEXT NOT NULL,
    published TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    published_version INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE TABLE ${TABLE_PREFIX}notebook_access (
    notebook_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}notebooks(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    permission TEXT NOT NULL,
    PRIMARY KEY (notebook_id, subject_type, subject_id)
);
CREATE TABLE ${TABLE_PREFIX}notebook_runs (
    id TEXT PRIMARY KEY,
    notebook_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}notebooks(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    status TEXT NOT NULL,
    source TEXT NOT NULL,
    info TEXT NOT NULL DEFAULT '{}',
    cancel_requested INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE INDEX ${TABLE_PREFIX}notebook_runs_user_idx ON ${TABLE_PREFIX}notebook_runs(user_id, created_at);
CREATE INDEX ${TABLE_PREFIX}notebook_runs_notebook_idx ON ${TABLE_PREFIX}notebook_runs(notebook_id, created_at);
CREATE INDEX ${TABLE_PREFIX}notebook_runs_status_idx ON ${TABLE_PREFIX}notebook_runs(status, updated_at);
CREATE INDEX ${TABLE_PREFIX}notebook_access_subject_idx ON ${TABLE_PREFIX}notebook_access(subject_type, subject_id);
CREATE INDEX ${TABLE_PREFIX}notebooks_owner_idx ON ${TABLE_PREFIX}notebooks(owner_id);
