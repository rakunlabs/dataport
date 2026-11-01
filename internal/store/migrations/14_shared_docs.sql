-- Users of a connection that signs everyone in with its one database account
-- see the same catalog, so they share one snapshot per schema, kept under
-- the user id 'shared'. The newest of their own copies becomes it.
INSERT INTO ${TABLE_PREFIX}schema_docs (connection_id, user_id, schema_name, source_version, payload, generated_at, attempted_at, last_error, lease_token, lease_until)
SELECT d.connection_id, 'shared', d.schema_name, d.source_version, d.payload, d.generated_at, d.attempted_at, d.last_error, '', 0
FROM ${TABLE_PREFIX}schema_docs d
JOIN ${TABLE_PREFIX}connections c ON c.id = d.connection_id
WHERE c.user_credentials = 0 AND c.owner_id = '' AND d.user_id NOT IN ('system', 'shared')
AND NOT EXISTS (
    SELECT 1 FROM ${TABLE_PREFIX}schema_docs o
    WHERE o.connection_id = d.connection_id AND o.schema_name = d.schema_name AND o.user_id NOT IN ('system', 'shared')
    AND (o.generated_at > d.generated_at OR (o.generated_at = d.generated_at AND o.user_id < d.user_id))
);
DELETE FROM ${TABLE_PREFIX}schema_docs
WHERE user_id NOT IN ('system', 'shared')
AND connection_id IN (SELECT id FROM ${TABLE_PREFIX}connections WHERE user_credentials = 0 AND owner_id = '');
