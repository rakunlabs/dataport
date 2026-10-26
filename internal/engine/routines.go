package engine

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/rakunlabs/dataport/internal/store"
)

// Routine is a stored procedure or function, or a sequence, of a schema;
// what the tree shows under its groups besides tables and views.
type Routine struct {
	Name string `json:"name"`
	// Kind is "procedure", "function" or "sequence".
	Kind string `json:"kind"`
	// Signature tells overloads apart, such as "(a integer, b text)".
	Signature string `json:"signature,omitempty"`
}

// Programmer is implemented by drivers that list stored procedures,
// functions and sequences.
type Programmer interface {
	Routines(ctx context.Context, q Querier, schema string) ([]Routine, error)
}

// Definer is implemented by drivers that return the source of a routine.
type Definer interface {
	Definition(ctx context.Context, q Querier, schema string, r Routine) (string, error)
}

var ErrNoRoutines = errors.New("procedures and functions are not available for this database")

func (m *Manager) Routines(ctx context.Context, c *store.Connection, password, schema string) ([]Routine, error) {
	list := []Routine{}

	err := m.meta(ctx, c, password, func(ctx context.Context, d Driver, conn *sql.Conn) (err error) {
		p, ok := d.(Programmer)
		if !ok {
			return nil
		}

		list, err = p.Routines(ctx, conn, schema)

		return err
	})

	return list, err
}

func (m *Manager) Definition(ctx context.Context, c *store.Connection, password, schema string, r Routine) (string, error) {
	var def string

	err := m.meta(ctx, c, password, func(ctx context.Context, d Driver, conn *sql.Conn) (err error) {
		p, ok := d.(Definer)
		if !ok {
			return ErrNoRoutines
		}

		def, err = p.Definition(ctx, conn, schema, r)

		return err
	})

	return def, err
}

func scanRoutines(rows *sql.Rows) ([]Routine, error) {
	defer func() { _ = rows.Close() }()

	list := []Routine{}

	for rows.Next() {
		var (
			r   Routine
			sig sql.NullString
		)

		if err := rows.Scan(&r.Name, &r.Kind, &sig); err != nil {
			return nil, err
		}

		r.Signature = sig.String
		list = append(list, r)
	}

	return list, rows.Err()
}

// ////////////////////////////////////////////
// PostgreSQL

func (postgresDriver) Routines(ctx context.Context, db Querier, schema string) ([]Routine, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.proname, CASE p.prokind WHEN 'p' THEN 'procedure' ELSE 'function' END,
'(' || pg_catalog.pg_get_function_identity_arguments(p.oid) || ')'
FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname = $1 AND p.prokind IN ('f', 'p')
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')
UNION ALL
SELECT c.relname, 'sequence', NULL FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind = 'S'
ORDER BY 2, 1, 3`, schema)
	if err != nil {
		return nil, err
	}

	return scanRoutines(rows)
}

func (postgresDriver) Definition(ctx context.Context, db Querier, schema string, r Routine) (string, error) {
	if r.Kind == "sequence" {
		// Like pg_dump: the sequence, its owner and the column that owns it,
		// whose table drops the sequence with it.
		stmts, err := queryStrings(ctx, db, `WITH s AS (
  SELECT c.oid, quote_ident(n.nspname) || '.' || quote_ident(c.relname) AS name, pg_catalog.pg_get_userbyid(c.relowner) AS owner
  FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind = 'S')
SELECT stmt FROM (
SELECT 0 AS part, 'CREATE SEQUENCE ' || s.name || E'\n    AS ' || format_type(q.seqtypid, NULL) || E'\n    INCREMENT BY ' || q.seqincrement
  || E'\n    MINVALUE ' || q.seqmin || E'\n    MAXVALUE ' || q.seqmax || E'\n    START WITH ' || q.seqstart
  || E'\n    CACHE ' || q.seqcache || CASE WHEN q.seqcycle THEN E'\n    CYCLE' ELSE '' END || ';' AS stmt
FROM s JOIN pg_catalog.pg_sequence q ON q.seqrelid = s.oid
UNION ALL
SELECT 1, 'ALTER SEQUENCE ' || s.name || ' OWNER TO ' || quote_ident(s.owner) || ';' FROM s
UNION ALL
SELECT 2, 'ALTER SEQUENCE ' || s.name || ' OWNED BY ' || quote_ident(tn.nspname) || '.' || quote_ident(t.relname) || '.' || quote_ident(a.attname) || ';'
FROM s
JOIN pg_catalog.pg_depend d ON d.objid = s.oid AND d.classid = 'pg_catalog.pg_class'::regclass AND d.refclassid = 'pg_catalog.pg_class'::regclass AND d.deptype = 'a'
JOIN pg_catalog.pg_class t ON t.oid = d.refobjid JOIN pg_catalog.pg_namespace tn ON tn.oid = t.relnamespace
JOIN pg_catalog.pg_attribute a ON a.attrelid = t.oid AND a.attnum = d.refobjsubid
UNION ALL
SELECT 3, 'COMMENT ON SEQUENCE ' || s.name || ' IS ' || quote_literal(obj_description(s.oid, 'pg_class')) || ';' FROM s
WHERE obj_description(s.oid, 'pg_class') IS NOT NULL
) x ORDER BY part`, schema, r.Name)
		if err != nil {
			return "", err
		}

		return strings.Join(stmts, "\n\n") + "\n", nil
	}

	return queryString(ctx, db, `SELECT pg_catalog.pg_get_functiondef(p.oid)
FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname = $1 AND p.proname = $2 AND '(' || pg_catalog.pg_get_function_identity_arguments(p.oid) || ')' = $3`,
		schema, r.Name, r.Signature)
}

// ////////////////////////////////////////////
// MySQL / MariaDB

func (mysqlDriver) Routines(ctx context.Context, db Querier, schema string) ([]Routine, error) {
	rows, err := db.QueryContext(ctx, `SELECT ROUTINE_NAME, LOWER(ROUTINE_TYPE), NULL FROM information_schema.ROUTINES
WHERE ROUTINE_SCHEMA = ? ORDER BY 2, 1`, schema)
	if err != nil {
		return nil, err
	}

	return scanRoutines(rows)
}

func (d mysqlDriver) Definition(ctx context.Context, db Querier, schema string, r Routine) (string, error) {
	kind := "PROCEDURE"
	if r.Kind == "function" {
		kind = "FUNCTION"
	}

	rows, err := db.QueryContext(ctx, "SHOW CREATE "+kind+" "+d.QuoteIdent(schema)+"."+d.QuoteIdent(r.Name))
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}

	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))

	for i := range vals {
		ptrs[i] = &vals[i]
	}

	// The statement is the third column: name, sql_mode, Create ...
	if rows.Next() && len(cols) > 2 {
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}

		return vals[2].String, rows.Err()
	}

	return "", rows.Err()
}

// ////////////////////////////////////////////
// SQL Server

func (mssqlDriver) Routines(ctx context.Context, db Querier, schema string) ([]Routine, error) {
	rows, err := db.QueryContext(ctx, `SELECT o.name, CASE WHEN o.type IN ('P', 'PC') THEN 'procedure' WHEN o.type = 'SO' THEN 'sequence' ELSE 'function' END, NULL
FROM sys.objects o JOIN sys.schemas s ON s.schema_id = o.schema_id
WHERE s.name = @p1 AND o.type IN ('P', 'PC', 'FN', 'IF', 'TF', 'FS', 'FT', 'SO') AND o.is_ms_shipped = 0
ORDER BY 2, 1`, schema)
	if err != nil {
		return nil, err
	}

	return scanRoutines(rows)
}

func (mssqlDriver) Definition(ctx context.Context, db Querier, schema string, r Routine) (string, error) {
	if r.Kind == "sequence" {
		return queryString(ctx, db, `SELECT 'CREATE SEQUENCE ' + QUOTENAME(@p1) + '.' + QUOTENAME(name) + ' AS ' + TYPE_NAME(user_type_id)
+ ' START WITH ' + CAST(start_value AS varchar(40)) + ' INCREMENT BY ' + CAST(increment AS varchar(40)) + ';'
FROM sys.sequences WHERE object_id = OBJECT_ID(QUOTENAME(@p1) + '.' + QUOTENAME(@p2))`, schema, r.Name)
	}

	return queryString(ctx, db, `SELECT OBJECT_DEFINITION(OBJECT_ID(QUOTENAME(@p1) + '.' + QUOTENAME(@p2)))`, schema, r.Name)
}

// ////////////////////////////////////////////
// Oracle

func (oracleDriver) Routines(ctx context.Context, db Querier, schema string) ([]Routine, error) {
	rows, err := db.QueryContext(ctx, `SELECT object_name, LOWER(object_type), NULL FROM all_objects
WHERE owner = :1 AND object_type IN ('PROCEDURE', 'FUNCTION', 'PACKAGE', 'SEQUENCE')
ORDER BY 2, 1`, schema)
	if err != nil {
		return nil, err
	}

	return scanRoutines(rows)
}

func (oracleDriver) Definition(ctx context.Context, db Querier, schema string, r Routine) (string, error) {
	kind := map[string]string{"procedure": "PROCEDURE", "function": "FUNCTION", "package": "PACKAGE", "sequence": "SEQUENCE"}[r.Kind]
	if kind == "" {
		return "", ErrNoRoutines
	}

	return queryString(ctx, db, `SELECT DBMS_METADATA.GET_DDL(:1, :2, :3) FROM dual`, kind, r.Name, schema)
}

// ////////////////////////////////////////////
// DuckDB

func (duckdbDriver) Routines(ctx context.Context, db Querier, schema string) ([]Routine, error) {
	rows, err := db.QueryContext(ctx, `SELECT function_name, 'function', NULL
FROM duckdb_functions() WHERE database_name = current_database() AND schema_name = ? AND NOT internal
UNION ALL SELECT sequence_name, 'sequence', NULL FROM duckdb_sequences() WHERE database_name = current_database() AND schema_name = ?
ORDER BY 2, 1`, schema, schema)
	if err != nil {
		return nil, err
	}

	return scanRoutines(rows)
}

func (duckdbDriver) Definition(ctx context.Context, db Querier, schema string, r Routine) (string, error) {
	if r.Kind == "sequence" {
		return queryString(ctx, db, `SELECT sql FROM duckdb_sequences() WHERE database_name = current_database() AND schema_name = ? AND sequence_name = ?`, schema, r.Name)
	}

	return queryString(ctx, db, `SELECT macro_definition FROM duckdb_functions() WHERE database_name = current_database() AND schema_name = ? AND function_name = ? LIMIT 1`, schema, r.Name)
}
