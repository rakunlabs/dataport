-- Each refresh of every documentation snapshot, scheduled or started by an
-- admin, so the settings page can show what ran, when and how it ended,
-- also after a restart and from any replica.
CREATE TABLE ${TABLE_PREFIX}doc_runs (
    id TEXT PRIMARY KEY,
    manual INTEGER NOT NULL DEFAULT 0,
    -- Who started a manual run; empty for the schedule.
    user_id TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    -- running, done, failed (some snapshots failed) or stopped.
    status TEXT NOT NULL,
    started_at BIGINT NOT NULL,
    finished_at BIGINT NOT NULL DEFAULT 0,
    total INTEGER NOT NULL DEFAULT 0,
    done INTEGER NOT NULL DEFAULT 0,
    failed INTEGER NOT NULL DEFAULT 0,
    -- Updated while running; a run whose heartbeat stopped was cut off.
    heartbeat_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX ${TABLE_PREFIX}doc_runs_started ON ${TABLE_PREFIX}doc_runs(started_at);
