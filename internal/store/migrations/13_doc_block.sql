-- Schemas a connection does not choose may be closed to documentation
-- altogether: nobody builds or reads them, also through MCP and the push.
ALTER TABLE ${TABLE_PREFIX}doc_connections ADD COLUMN block_excluded INTEGER NOT NULL DEFAULT 0;
