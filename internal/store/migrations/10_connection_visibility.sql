-- Where a connection shows up, and personal connections users create for
-- themselves; owner_id is empty for the connections an admin defines.
ALTER TABLE ${TABLE_PREFIX}connections ADD COLUMN owner_id TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}connections ADD COLUMN hide_workspace INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}connections ADD COLUMN hide_notebooks INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}connections ADD COLUMN hide_mcp INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}connections ADD COLUMN notebook_only INTEGER NOT NULL DEFAULT 0;
CREATE INDEX ${TABLE_PREFIX}connections_owner ON ${TABLE_PREFIX}connections(owner_id);
