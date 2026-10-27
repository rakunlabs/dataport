-- Documentation is refreshed by one server-wide schedule kept in settings;
-- per-schema schedules are gone.
DROP INDEX ${TABLE_PREFIX}schema_docs_due;
ALTER TABLE ${TABLE_PREFIX}schema_docs DROP COLUMN cron_spec;
ALTER TABLE ${TABLE_PREFIX}schema_docs DROP COLUMN next_run;
CREATE INDEX ${TABLE_PREFIX}schema_docs_attempted ON ${TABLE_PREFIX}schema_docs(attempted_at);
