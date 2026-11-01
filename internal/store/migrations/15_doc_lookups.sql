-- Tables whose rows the documentation shows, as an admin chose them: JSON
-- array of "schema.table" patterns. Empty, no table data is read.
ALTER TABLE ${TABLE_PREFIX}doc_connections ADD COLUMN lookup_tables TEXT NOT NULL DEFAULT '[]';
