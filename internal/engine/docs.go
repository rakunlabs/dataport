package engine

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rakunlabs/dataport/internal/store"
)

// Schema documentation in the manner of SchemaSpy: every table with its
// columns, keys, comments and estimated size, who references whom, and the
// design smells SchemaSpy reports as anomalies. It reads the catalog, and
// of the table data only the rows of small tables an admin chose.

// DocColumn is a column of a documented table, with its comment.
type DocColumn struct {
	ColumnInfo

	Comment string `json:"comment,omitempty"`
}

// Reference is a foreign key of another table pointing at this one.
type Reference struct {
	Table      string   `json:"table"`
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefColumns []string `json:"ref_columns"`
}

type DocTable struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Comment string `json:"comment,omitempty"`
	// Rows is the database's own estimate, from its statistics; nil when it
	// keeps none.
	Rows         *int64        `json:"rows,omitempty"`
	Columns      []DocColumn   `json:"columns"`
	Indexes      []IndexInfo   `json:"indexes"`
	ForeignKeys  []ForeignKey  `json:"foreign_keys"`
	ReferencedBy []Reference   `json:"referenced_by"`
	Triggers     []TriggerInfo `json:"triggers"`
	// Definition is the source of a view.
	Definition string `json:"definition,omitempty"`
	// Partition describes a partitioned table; its partitions are not
	// documented on their own.
	Partition *PartitionInfo `json:"partition,omitempty"`
	// Values are the rows of a small table an admin chose to show, such as
	// status codes and their names.
	Values *LookupValues `json:"values,omitempty"`
}

// LookupValues are the rows of a lookup table, as text, in key order.
type LookupValues struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// Chosen tables are read when they are this small; the codes of a status
// or type table explain the columns that hold them elsewhere.
const (
	maxLookupRows    = 50
	maxLookupColumns = 6
	maxLookupValue   = 80
	// maxTriggerFunction bounds the source of a trigger's function kept in
	// the documentation of its table; a longer one is cut, with a note.
	maxTriggerFunction = 3000
)

// Anomaly kinds, after SchemaSpy's list.
const (
	AnomalyNoPrimaryKey   = "no_primary_key"
	AnomalyUnindexedFK    = "unindexed_foreign_key"
	AnomalyImplied        = "implied_relationship"
	AnomalyOrphan         = "orphan_table"
	AnomalySingleColumn   = "single_column"
	AnomalyNullableUnique = "nullable_unique"
	AnomalyNullDefault    = "null_string_default"
	AnomalyIncrementing   = "incrementing_columns"
	AnomalyDuplicateIndex = "duplicate_index"
	AnomalyDuplicateFK    = "duplicate_foreign_key"
)

// Anomaly is a design smell found in a table. Ref names the table an
// implied relationship points at, or the index another one duplicates.
type Anomaly struct {
	Kind    string   `json:"kind"`
	Table   string   `json:"table"`
	Columns []string `json:"columns,omitempty"`
	Ref     string   `json:"ref,omitempty"`
	RefCols []string `json:"ref_columns,omitempty"`
}

type SchemaDoc struct {
	Schema      string     `json:"schema"`
	DBType      string     `json:"db_type"`
	GeneratedAt int64      `json:"generated_at"`
	Tables      []DocTable `json:"tables"`
	Routines    []Routine  `json:"routines"`
	Anomalies   []Anomaly  `json:"anomalies"`
	// Keys is false where the database has no foreign keys or indexes to
	// read (ClickHouse, Trino), so their absence says nothing.
	Keys bool `json:"keys"`
	// Truncated is set when the schema has more tables than documented.
	Truncated  bool `json:"truncated"`
	TableCount int  `json:"table_count"`
}

// MaxDocTables bounds the catalog queries of one document.
const MaxDocTables = 300

// docTimeout is longer than other metadata reads: a document asks a few
// catalog queries for every table.
const docTimeout = 90 * time.Second

// RowEstimator is implemented by drivers whose statistics estimate the
// rows of every table of a schema in one query.
type RowEstimator interface {
	RowEstimates(ctx context.Context, q Querier, schema string) (map[string]int64, error)
}

// SchemaDoc documents a schema, with the rows of the tables lookups names
// (see LookupChosen). Parts a restricted account cannot read,
// such as comments or the constraint catalog, are left out rather than
// failing the whole document.
func (m *Manager) SchemaDoc(ctx context.Context, c *store.Connection, password, schema string, lookups []string) (*SchemaDoc, error) {
	out := &SchemaDoc{
		Schema: schema, DBType: c.DBType, GeneratedAt: time.Now().UnixMilli(),
		Tables: []DocTable{}, Routines: []Routine{}, Anomalies: []Anomaly{},
	}

	ctx, cancel := context.WithTimeout(ctx, docTimeout)
	defer cancel()

	// meta bounds its context to a short read; the document keeps its own,
	// longer one.
	err := m.meta(ctx, c, password, func(_ context.Context, d Driver, conn *sql.Conn) error {
		objects, err := d.Objects(ctx, conn, schema)
		if err != nil {
			return err
		}

		out.TableCount = len(objects)
		if len(objects) > MaxDocTables {
			objects = objects[:MaxDocTables]
			out.Truncated = true
		}

		var rows map[string]int64
		if re, ok := d.(RowEstimator); ok {
			rows, _ = re.RowEstimates(ctx, conn, schema)
		}

		var parts map[string]PartitionInfo
		if pt, ok := d.(Partitioner); ok {
			parts, _ = pt.Partitions(ctx, conn, schema)
		}

		s, _ := d.(Structurer)
		cm, _ := d.(Commenter)
		out.Keys = s != nil && d.Type() != "clickhouse" && d.Type() != "trino"

		for _, o := range objects {
			t, err := docTable(ctx, d, s, cm, conn, schema, o)
			if err != nil {
				return err
			}

			if n, ok := rows[o.Name]; ok {
				t.Rows = &n
			}

			if p, ok := parts[o.Name]; ok {
				if pi, ok := d.(PartitionIndexer); ok {
					if list, leaves, err := pi.PartitionIndexes(ctx, conn, schema, o.Name); err == nil {
						p.Indexes, p.Leaves = list, leaves
					}
				}

				t.Partition = &p
			}

			out.Tables = append(out.Tables, t)
		}

		readLookups(ctx, d, conn, schema, out.Tables, lookups)

		if p, ok := d.(Programmer); ok {
			if list, err := p.Routines(ctx, conn, schema); err == nil {
				out.Routines = list
			}
		}

		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}

	linkReferences(out.Tables, schema)
	out.Anomalies = findAnomalies(out.Tables, out.Keys, schema)

	return out, nil
}

func docTable(ctx context.Context, d Driver, s Structurer, cm Commenter, conn *sql.Conn, schema string, o Object) (DocTable, error) {
	t := DocTable{Name: o.Name, Kind: o.Kind, Columns: []DocColumn{}, Indexes: []IndexInfo{}, ForeignKeys: []ForeignKey{}, ReferencedBy: []Reference{}, Triggers: []TriggerInfo{}}

	cols, err := d.Columns(ctx, conn, schema, o.Name)
	if err != nil {
		return t, err
	}

	comments := map[string]string{}

	if cm != nil {
		if tc, cc, err := cm.Comments(ctx, conn, schema, o.Name); err == nil {
			t.Comment, comments = tc, cc
		}
	}

	for _, col := range cols {
		t.Columns = append(t.Columns, DocColumn{ColumnInfo: col, Comment: comments[col.Name]})
	}

	if s == nil {
		return t, nil
	}

	if o.Kind == "table" || o.Kind == "materialized_view" {
		if list, err := s.Indexes(ctx, conn, schema, o.Name); err == nil {
			t.Indexes = list
		}
	}

	if o.Kind == "table" {
		if list, err := s.ForeignKeys(ctx, conn, schema, o.Name); err == nil {
			t.ForeignKeys = list
		}
		if tg, ok := d.(Triggerer); ok {
			if list, err := tg.Triggers(ctx, conn, schema, o.Name); err == nil {
				t.Triggers = list
			}
		}

		if ts, ok := d.(TriggerSourcer); ok && len(t.Triggers) > 0 {
			if src, err := ts.TriggerFunctions(ctx, conn, schema, o.Name); err == nil {
				for i, tr := range t.Triggers {
					t.Triggers[i].Function = compactSource(src[tr.Name], maxTriggerFunction)
				}
			}
		}
	}

	if o.Kind == "view" || o.Kind == "materialized_view" {
		if ddl, err := s.DDL(ctx, conn, schema, o.Name, &TableStructure{}); err == nil {
			t.Definition = ddl
		}
	}

	return t, nil
}

// sameSchema tells whether a foreign key points into the documented schema;
// databases without schemas leave RefSchema empty.
func sameSchema(fk ForeignKey, schema string) bool {
	return fk.RefSchema == "" || fk.RefSchema == schema
}

// readLookups fills the values of the tables an admin chose, by name
// pattern ("*" matches any run of characters). Nothing else of the table
// data is read. A table it cannot read, or one larger than the limits, is
// left without values.
func readLookups(ctx context.Context, d Driver, conn *sql.Conn, schema string, tables []DocTable, patterns []string) {
	if len(patterns) == 0 {
		return
	}

	for i := range tables {
		t := &tables[i]
		if t.Kind != "table" || t.Partition != nil || (t.Rows != nil && *t.Rows > maxLookupRows) || !LookupChosen(patterns, t.Name) {
			continue
		}

		if ctx.Err() != nil {
			return
		}

		t.Values = readLookup(ctx, d, conn, schema, *t)
	}
}

// LookupChosen tells whether a table name matches one of the patterns,
// in any case.
func LookupChosen(patterns []string, name string) bool {
	name = strings.ToLower(name)

	for _, p := range patterns {
		if ok, err := path.Match(strings.ToLower(p), name); err == nil && ok {
			return true
		}
	}

	return false
}

func readLookup(ctx context.Context, d Driver, conn *sql.Conn, schema string, t DocTable) *LookupValues {
	var cols []string

	for _, c := range t.Columns {
		switch baseType(c.Type) {
		case "bytea", "blob", "longblob", "mediumblob", "varbinary", "binary", "image", "raw", "json", "jsonb", "xml":
			continue
		}

		cols = append(cols, c.Name)
	}

	if len(cols) == 0 || len(cols) > maxLookupColumns {
		return nil
	}

	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = d.QuoteIdent(c)
	}

	sel := strings.Join(quoted, ", ")
	from := qualify(d, schema, t.Name)
	// One row more than kept tells a table that outgrew its estimate.
	n := maxLookupRows + 1

	var stmt string

	switch d.Dialect() {
	case DialectMSSQL:
		stmt = fmt.Sprintf("SELECT TOP %d %s FROM %s ORDER BY 1", n, sel, from)
	case DialectOracle:
		stmt = fmt.Sprintf("SELECT %s FROM %s ORDER BY 1 FETCH FIRST %d ROWS ONLY", sel, from, n)
	default:
		stmt = fmt.Sprintf("SELECT %s FROM %s ORDER BY 1 LIMIT %d", sel, from, n)
	}

	rows, err := conn.QueryContext(ctx, stmt)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	out := &LookupValues{Columns: cols, Rows: [][]string{}}

	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))

		for i := range vals {
			ptrs[i] = &vals[i]
		}

		if err := rows.Scan(ptrs...); err != nil {
			return nil
		}

		if len(out.Rows) == maxLookupRows {
			return nil
		}

		row := make([]string, len(cols))
		for i, v := range vals {
			row[i] = lookupText(v)
		}

		out.Rows = append(out.Rows, row)
	}

	if rows.Err() != nil || len(out.Rows) == 0 {
		return nil
	}

	return out
}

func lookupText(v any) string {
	var s string

	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		s = string(x)
	case time.Time:
		s = x.Format(time.RFC3339)
	default:
		s = fmt.Sprint(x)
	}

	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxLookupValue {
		r := []rune(s)
		if len(r) > maxLookupValue {
			s = string(r[:maxLookupValue]) + "…"
		}
	}

	return s
}

// compactSource drops trailing spaces and runs of blank lines from routine
// source, which editors often leave by the hundred, and cuts what remains
// at max bytes on a line, saying how much was left out: the start of a
// long function still tells what it does.
func compactSource(src string, max int) string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	blank := false

	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			if blank || len(out) == 0 {
				continue
			}

			blank = true
		} else {
			blank = false
		}

		out = append(out, l)
	}

	s := strings.TrimSpace(strings.Join(out, "\n"))
	if len(s) <= max {
		return s
	}

	cut := strings.LastIndexByte(s[:max], '\n')
	if cut <= 0 {
		cut = max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
	}

	return s[:cut] + fmt.Sprintf("\n-- … %d more characters; read the whole function with its definition", len(s)-cut)
}

func linkReferences(tables []DocTable, schema string) {
	at := map[string]int{}
	for i, t := range tables {
		at[t.Name] = i
	}

	for _, t := range tables {
		for _, fk := range t.ForeignKeys {
			i, ok := at[fk.RefTable]
			if !ok || !sameSchema(fk, schema) {
				continue
			}

			tables[i].ReferencedBy = append(tables[i].ReferencedBy, Reference{
				Table: t.Name, Name: fk.Name, Columns: fk.Columns, RefColumns: fk.RefColumns,
			})
		}
	}
}

// ////////////////////////////////////////////
// row estimates

// scanEstimates reads (table, rows) pairs. Rows come back as text so every
// driver's numeric type scans the same; negative values mean "unknown".
func scanEstimates(rows *sql.Rows, err error) (map[string]int64, error) {
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]int64{}

	for rows.Next() {
		var (
			name string
			n    sql.NullString
		)

		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}

		if !n.Valid {
			continue
		}

		f, err := strconv.ParseFloat(strings.TrimSpace(n.String), 64)
		if err != nil || f < 0 {
			continue
		}

		out[name] = int64(f)
	}

	return out, rows.Err()
}

// A partitioned table keeps no statistics of its own; its estimate is the
// sum of its leaf partitions.
// pg_partition_tree arrived in PostgreSQL 12; older servers fall back to
// plain tables.
func (postgresDriver) RowEstimates(ctx context.Context, q Querier, schema string) (map[string]int64, error) {
	out, err := scanEstimates(q.QueryContext(ctx, `SELECT c.relname,
CASE WHEN c.relkind = 'p' THEN (SELECT COALESCE(sum(GREATEST(l.reltuples, 0)), 0)
  FROM pg_catalog.pg_partition_tree(c.oid) pt JOIN pg_catalog.pg_class l ON l.oid = pt.relid WHERE pt.isleaf)
ELSE c.reltuples END::bigint::text
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind IN ('r', 'm', 'p')`, schema))
	if err == nil || ctx.Err() != nil {
		return out, err
	}

	return scanEstimates(q.QueryContext(ctx, `SELECT c.relname, c.reltuples::bigint::text
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind IN ('r', 'm')`, schema))
}

// ////////////////////////////////////////////
// partitions

// Partitioner is implemented by drivers that describe the partitioned
// tables of a schema in one query, by table name.
type Partitioner interface {
	Partitions(ctx context.Context, q Querier, schema string) (map[string]PartitionInfo, error)
}

// PartitionInfo is how a table is split: its key, as the database writes
// it, and its direct partitions, the first and last by name.
type PartitionInfo struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
	First string `json:"first,omitempty"`
	Last  string `json:"last,omitempty"`
	// Leaves counts the partitions that hold rows, below every level.
	Leaves int `json:"leaves,omitempty"`
	// Indexes are those created on partitions one by one rather than on
	// the table, so the table's own list leaves them out; a query on the
	// table still uses them.
	Indexes []PartitionIndex `json:"indexes,omitempty"`
}

// PartitionIndex is an index found on some partitions of a table: one of
// them by name, and on how many partitions it is.
type PartitionIndex struct {
	IndexInfo

	Partitions int `json:"partitions"`
}

// PartitionIndexer is implemented by drivers that read the indexes of a
// partitioned table's partitions that the table itself does not declare.
type PartitionIndexer interface {
	PartitionIndexes(ctx context.Context, q Querier, schema, table string) ([]PartitionIndex, int, error)
}

func (postgresDriver) PartitionIndexes(ctx context.Context, q Querier, schema, table string) ([]PartitionIndex, int, error) {
	rows, err := q.QueryContext(ctx, `WITH parent AS (
  SELECT c.oid FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind = 'p'),
leaves AS (
  SELECT pt.relid FROM parent, pg_catalog.pg_partition_tree(parent.oid) pt WHERE pt.isleaf),
own AS (
  SELECT i.relname AS name, ix.indisunique AS uniq, am.amname AS method,
    COALESCE((SELECT string_agg(COALESCE(a.attname, pg_catalog.pg_get_indexdef(ix.indexrelid, k.ord::int, true)), chr(31) ORDER BY k.ord)
      FROM unnest(ix.indkey::int2[]) WITH ORDINALITY k(attnum, ord)
      LEFT JOIN pg_catalog.pg_attribute a ON a.attrelid = ix.indrelid AND a.attnum = k.attnum AND k.attnum > 0
      WHERE k.ord <= ix.indnkeyatts), '') AS cols,
    COALESCE(pg_catalog.pg_get_expr(ix.indpred, ix.indrelid, true), '') AS pred
  FROM leaves l
  JOIN pg_catalog.pg_index ix ON ix.indrelid = l.relid
  JOIN pg_catalog.pg_class i ON i.oid = ix.indexrelid
  JOIN pg_catalog.pg_am am ON am.oid = i.relam
  WHERE NOT ix.indisprimary
    AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_inherits inh WHERE inh.inhrelid = ix.indexrelid))
SELECT min(name), uniq, method, cols, pred, count(*), (SELECT count(*) FROM leaves)
FROM own GROUP BY uniq, method, cols, pred ORDER BY count(*) DESC, cols`, schema, table)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	var (
		list   []PartitionIndex
		leaves int
	)

	for rows.Next() {
		var (
			ix   PartitionIndex
			cols string
		)

		if err := rows.Scan(&ix.Name, &ix.Unique, &ix.Method, &cols, &ix.Where, &ix.Partitions, &leaves); err != nil {
			return nil, 0, err
		}

		ix.Columns = splitList(cols)
		list = append(list, ix)
	}

	return list, leaves, rows.Err()
}

func (postgresDriver) Partitions(ctx context.Context, q Querier, schema string) (map[string]PartitionInfo, error) {
	rows, err := q.QueryContext(ctx, `SELECT c.relname, pg_catalog.pg_get_partkeydef(c.oid), count(ch.oid),
COALESCE(min(ch.relname::text), ''), COALESCE(max(ch.relname::text), '')
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_catalog.pg_inherits i ON i.inhparent = c.oid
LEFT JOIN pg_catalog.pg_class ch ON ch.oid = i.inhrelid
WHERE n.nspname = $1 AND c.relkind = 'p'
GROUP BY c.oid, c.relname`, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]PartitionInfo{}

	for rows.Next() {
		var (
			name string
			p    PartitionInfo
		)

		if err := rows.Scan(&name, &p.Key, &p.Count, &p.First, &p.Last); err != nil {
			return nil, err
		}

		out[name] = p
	}

	return out, rows.Err()
}

func (mysqlDriver) RowEstimates(ctx context.Context, q Querier, schema string) (map[string]int64, error) {
	return scanEstimates(q.QueryContext(ctx, `SELECT TABLE_NAME, CAST(TABLE_ROWS AS CHAR) FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'`, schema))
}

func (mssqlDriver) RowEstimates(ctx context.Context, q Querier, schema string) (map[string]int64, error) {
	return scanEstimates(q.QueryContext(ctx, `SELECT t.name, CAST(SUM(p.rows) AS varchar(30))
FROM sys.tables t JOIN sys.schemas s ON s.schema_id = t.schema_id
JOIN sys.partitions p ON p.object_id = t.object_id AND p.index_id IN (0, 1)
WHERE s.name = @p1 GROUP BY t.name`, schema))
}

func (oracleDriver) RowEstimates(ctx context.Context, q Querier, schema string) (map[string]int64, error) {
	return scanEstimates(q.QueryContext(ctx, `SELECT table_name, TO_CHAR(num_rows) FROM all_tables WHERE owner = :1`, schema))
}

func (clickhouseDriver) RowEstimates(ctx context.Context, q Querier, schema string) (map[string]int64, error) {
	return scanEstimates(q.QueryContext(ctx, `SELECT name, toString(total_rows) FROM system.tables WHERE database = ?`, schema))
}

func (duckdbDriver) RowEstimates(ctx context.Context, q Querier, schema string) (map[string]int64, error) {
	return scanEstimates(q.QueryContext(ctx, `SELECT table_name, CAST(estimated_size AS VARCHAR) FROM duckdb_tables()
WHERE database_name = current_database() AND schema_name = ?`, schema))
}
