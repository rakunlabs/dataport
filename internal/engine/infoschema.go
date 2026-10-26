package engine

import (
	"context"
	"strings"
)

// infoSchema reads the catalog of databases that follow the SQL standard
// information_schema: Trino, DuckDB and many ODBC targets. ph renders the
// n-th parameter placeholder.
type infoSchema struct {
	ph func(n int) string
	// noKeys skips the constraint tables, which Trino does not have.
	noKeys bool
	// exclude lists system schemas that are not shown.
	exclude []string
}

func questionMark(int) string { return "?" }

func (s infoSchema) Schemas(ctx context.Context, db Querier) ([]string, error) {
	q := `SELECT schema_name FROM information_schema.schemata`
	if len(s.exclude) > 0 {
		q += ` WHERE LOWER(schema_name) NOT IN (` + quotedList(s.exclude) + `)`
	}

	rows, err := db.QueryContext(ctx, q+` ORDER BY schema_name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanStrings(rows)
}

func (s infoSchema) Objects(ctx context.Context, db Querier, schema string) ([]Object, error) {
	rows, err := db.QueryContext(ctx, `SELECT table_name, CASE WHEN table_type = 'VIEW' THEN 'view' ELSE 'table' END
FROM information_schema.tables WHERE table_schema = `+s.ph(1)+` ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanObjects(rows)
}

func (s infoSchema) Columns(ctx context.Context, db Querier, schema, table string) ([]ColumnInfo, error) {
	pk := `CASE WHEN EXISTS (SELECT 1 FROM information_schema.table_constraints tc
  JOIN information_schema.key_column_usage k ON k.constraint_name = tc.constraint_name AND k.table_schema = tc.table_schema AND k.table_name = tc.table_name
  WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_schema = c.table_schema AND tc.table_name = c.table_name AND k.column_name = c.column_name)
  THEN 1 ELSE 0 END`
	if s.noKeys {
		pk = `0`
	}

	rows, err := db.QueryContext(ctx, `SELECT c.column_name, c.data_type, CASE WHEN c.is_nullable = 'YES' THEN 1 ELSE 0 END,
COALESCE(CAST(c.column_default AS VARCHAR(4000)), ''), `+pk+`
FROM information_schema.columns c
WHERE c.table_schema = `+s.ph(1)+` AND c.table_name = `+s.ph(2)+`
ORDER BY c.ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanIntColumns(rows)
}

// scanIntColumns reads columns whose nullable and primary key flags are
// numbers, for databases without a boolean type.
func scanIntColumns(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
},
) ([]ColumnInfo, error) {
	list := []ColumnInfo{}

	for rows.Next() {
		var (
			c            ColumnInfo
			nullable, pk int64
			def          *string
		)

		if err := rows.Scan(&c.Name, &c.Type, &nullable, &def, &pk); err != nil {
			return nil, err
		}

		c.Name = strings.TrimSpace(c.Name)
		c.Type = strings.TrimSpace(c.Type)
		c.Nullable = nullable != 0
		c.PrimaryKey = pk != 0

		if def != nil {
			c.Default = strings.TrimSpace(*def)
		}

		list = append(list, c)
	}

	return list, rows.Err()
}

func quotedList(values []string) string {
	q := make([]string, len(values))
	for i, v := range values {
		q[i] = "'" + strings.ReplaceAll(strings.ToLower(v), "'", "''") + "'"
	}

	return strings.Join(q, ", ")
}
