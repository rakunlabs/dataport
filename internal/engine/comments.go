package engine

import (
	"context"
	"database/sql"
	"strings"
)

// Table and column comments, and triggers, for the structure editor.

func scanComments(rows *sql.Rows, err error) (string, map[string]string, error) {
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = rows.Close() }()

	table, cols := "", map[string]string{}

	for rows.Next() {
		var col, text sql.NullString
		if err := rows.Scan(&col, &text); err != nil {
			return "", nil, err
		}

		if !text.Valid || text.String == "" {
			continue
		}

		if !col.Valid || col.String == "" {
			table = text.String
		} else {
			cols[col.String] = text.String
		}
	}

	return table, cols, rows.Err()
}

func scanTriggers(rows *sql.Rows, err error) ([]TriggerInfo, error) {
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []TriggerInfo{}

	for rows.Next() {
		var (
			t                         TriggerInfo
			timing, event, definition sql.NullString
			enabled                   bool
		)

		if err := rows.Scan(&t.Name, &timing, &event, &enabled, &definition); err != nil {
			return nil, err
		}

		t.Timing, t.Event, t.Enabled, t.Definition = strings.TrimSpace(timing.String), strings.TrimSpace(event.String), enabled, definition.String
		list = append(list, t)
	}

	return list, rows.Err()
}

func (postgresDriver) Comments(ctx context.Context, q Querier, schema, table string) (string, map[string]string, error) {
	return scanComments(q.QueryContext(ctx, `SELECT NULL, obj_description(c.oid, 'pg_class')
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = $2
UNION ALL
SELECT a.attname, col_description(c.oid, a.attnum)
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid = a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped`, schema, table))
}

func (postgresDriver) Triggers(ctx context.Context, q Querier, schema, table string) ([]TriggerInfo, error) {
	return scanTriggers(q.QueryContext(ctx, `SELECT t.tgname,
  CASE WHEN t.tgtype & 2 <> 0 THEN 'BEFORE' WHEN t.tgtype & 64 <> 0 THEN 'INSTEAD OF' ELSE 'AFTER' END,
  concat_ws(' OR ', CASE WHEN t.tgtype & 4 <> 0 THEN 'INSERT' END, CASE WHEN t.tgtype & 16 <> 0 THEN 'UPDATE' END,
    CASE WHEN t.tgtype & 8 <> 0 THEN 'DELETE' END, CASE WHEN t.tgtype & 32 <> 0 THEN 'TRUNCATE' END),
  t.tgenabled <> 'D',
  pg_catalog.pg_get_triggerdef(t.oid, true)
FROM pg_catalog.pg_trigger t
JOIN pg_catalog.pg_class c ON c.oid = t.tgrelid JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND NOT t.tgisinternal ORDER BY t.tgname`, schema, table))
}

func (mysqlDriver) Comments(ctx context.Context, q Querier, schema, table string) (string, map[string]string, error) {
	return scanComments(q.QueryContext(ctx, `SELECT NULL, TABLE_COMMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
UNION ALL
SELECT COLUMN_NAME, COLUMN_COMMENT FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, schema, table, schema, table))
}

func (mysqlDriver) Triggers(ctx context.Context, q Querier, schema, table string) ([]TriggerInfo, error) {
	return scanTriggers(q.QueryContext(ctx, `SELECT TRIGGER_NAME, ACTION_TIMING, EVENT_MANIPULATION, TRUE, ACTION_STATEMENT
FROM information_schema.TRIGGERS WHERE EVENT_OBJECT_SCHEMA = ? AND EVENT_OBJECT_TABLE = ? ORDER BY TRIGGER_NAME`, schema, table))
}

func (mssqlDriver) Comments(ctx context.Context, q Querier, schema, table string) (string, map[string]string, error) {
	return scanComments(q.QueryContext(ctx, `SELECT c.name, CAST(p.value AS nvarchar(max))
FROM sys.extended_properties p
JOIN sys.tables t ON t.object_id = p.major_id
JOIN sys.schemas s ON s.schema_id = t.schema_id
LEFT JOIN sys.columns c ON c.object_id = p.major_id AND c.column_id = p.minor_id
WHERE p.class = 1 AND p.name = 'MS_Description' AND s.name = @p1 AND t.name = @p2`, schema, table))
}

func (mssqlDriver) Triggers(ctx context.Context, q Querier, schema, table string) ([]TriggerInfo, error) {
	return scanTriggers(q.QueryContext(ctx, `SELECT tr.name,
  CASE WHEN tr.is_instead_of_trigger = 1 THEN 'INSTEAD OF' ELSE 'AFTER' END,
  STUFF((SELECT ' OR ' + te.type_desc FROM sys.trigger_events te WHERE te.object_id = tr.object_id FOR XML PATH('')), 1, 4, ''),
  CAST(CASE WHEN tr.is_disabled = 1 THEN 0 ELSE 1 END AS bit),
  OBJECT_DEFINITION(tr.object_id)
FROM sys.triggers tr
JOIN sys.tables t ON t.object_id = tr.parent_id JOIN sys.schemas s ON s.schema_id = t.schema_id
WHERE s.name = @p1 AND t.name = @p2 ORDER BY tr.name`, schema, table))
}

func (oracleDriver) Comments(ctx context.Context, q Querier, schema, table string) (string, map[string]string, error) {
	return scanComments(q.QueryContext(ctx, `SELECT NULL, comments FROM all_tab_comments WHERE owner = :1 AND table_name = :2
UNION ALL
SELECT column_name, comments FROM all_col_comments WHERE owner = :1 AND table_name = :2`, schema, table))
}

func (oracleDriver) Triggers(ctx context.Context, q Querier, schema, table string) ([]TriggerInfo, error) {
	return scanTriggers(q.QueryContext(ctx, `SELECT trigger_name, trigger_type, triggering_event, CASE status WHEN 'ENABLED' THEN 1 ELSE 0 END, description
FROM all_triggers WHERE table_owner = :1 AND table_name = :2 ORDER BY trigger_name`, schema, table))
}

func (sqliteDriver) Triggers(ctx context.Context, q Querier, _, table string) ([]TriggerInfo, error) {
	return scanTriggers(q.QueryContext(ctx, `SELECT name, '', '', 1, sql FROM sqlite_schema WHERE type = 'trigger' AND tbl_name = ? ORDER BY name`, table))
}

func (clickhouseDriver) Comments(ctx context.Context, q Querier, schema, table string) (string, map[string]string, error) {
	return scanComments(q.QueryContext(ctx, `SELECT '', comment FROM system.tables WHERE database = ? AND name = ?
UNION ALL
SELECT name, comment FROM system.columns WHERE database = ? AND table = ?`, schema, table, schema, table))
}
