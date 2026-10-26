package engine

import (
	"context"
	"database/sql"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Schema documentation in the manner of SchemaSpy: every table with its
// columns, keys, comments and estimated size, who references whom, and the
// design smells SchemaSpy reports as anomalies. It reads only the catalog,
// never table data.

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
}

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

// SchemaDoc documents a schema. Parts a restricted account cannot read,
// such as comments or the constraint catalog, are left out rather than
// failing the whole document.
func (m *Manager) SchemaDoc(ctx context.Context, c *store.Connection, password, schema string) (*SchemaDoc, error) {
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

			out.Tables = append(out.Tables, t)
		}

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
// anomalies

var trailingDigits = regexp.MustCompile(`^(.*?)(\d+)$`)

func findAnomalies(tables []DocTable, keys bool, schema string) []Anomaly {
	list := []Anomaly{}
	pks := primaryKeys(tables)

	// Tables joined by a key, declared or implied; without a single link in
	// the schema every table would be an orphan, which tells nothing.
	linked := map[string]bool{}
	implied := map[string][]Anomaly{}

	for _, t := range tables {
		for _, fk := range t.ForeignKeys {
			if sameSchema(fk, schema) {
				linked[t.Name], linked[fk.RefTable] = true, true
			}
		}

		if keys && t.Kind == "table" {
			implied[t.Name] = impliedRelationships(t, pks)
			for _, a := range implied[t.Name] {
				linked[t.Name], linked[a.Ref] = true, true
			}
		}
	}

	for _, t := range tables {
		if t.Kind != "table" {
			continue
		}

		if keys {
			if countPK(t) == 0 {
				list = append(list, Anomaly{Kind: AnomalyNoPrimaryKey, Table: t.Name})
			}

			list = append(list, unindexedFKs(t)...)
			list = append(list, implied[t.Name]...)
			list = append(list, nullableUnique(t)...)
			list = append(list, duplicateIndexes(t)...)

			if len(linked) > 0 && !linked[t.Name] {
				list = append(list, Anomaly{Kind: AnomalyOrphan, Table: t.Name})
			}
		}

		if len(t.Columns) == 1 {
			list = append(list, Anomaly{Kind: AnomalySingleColumn, Table: t.Name, Columns: []string{t.Columns[0].Name}})
		}

		for _, c := range t.Columns {
			if isNullString(c.Default) {
				list = append(list, Anomaly{Kind: AnomalyNullDefault, Table: t.Name, Columns: []string{c.Name}})
			}
		}

		list = append(list, incrementingColumns(t)...)
	}

	return list
}

// unindexedFKs finds foreign keys whose columns do not lead any index, so
// a delete on the referenced table scans this one.
func unindexedFKs(t DocTable) []Anomaly {
	var list []Anomaly

	for _, fk := range t.ForeignKeys {
		covered := slices.ContainsFunc(t.Indexes, func(ix IndexInfo) bool {
			return len(ix.Columns) >= len(fk.Columns) && sameSet(ix.Columns[:len(fk.Columns)], fk.Columns)
		})

		if !covered {
			list = append(list, Anomaly{Kind: AnomalyUnindexedFK, Table: t.Name, Columns: fk.Columns, Ref: fk.RefTable})
		}
	}

	return list
}

type pkInfo struct {
	table, column, typ string
}

// primaryKeys indexes the tables with a single-column primary key by their
// lowercased name and the singular forms a column may use for them.
func primaryKeys(tables []DocTable) map[string]pkInfo {
	out := map[string]pkInfo{}

	for _, t := range tables {
		if t.Kind != "table" {
			continue
		}

		var pk []DocColumn

		for _, c := range t.Columns {
			if c.PrimaryKey {
				pk = append(pk, c)
			}
		}

		if len(pk) != 1 {
			continue
		}

		info := pkInfo{table: t.Name, column: pk[0].Name, typ: baseType(pk[0].Type)}

		for _, n := range singulars(strings.ToLower(t.Name)) {
			if _, taken := out[n]; !taken {
				out[n] = info
			}
		}
	}

	return out
}

func singulars(name string) []string {
	list := []string{name}

	switch {
	case strings.HasSuffix(name, "ies"):
		list = append(list, strings.TrimSuffix(name, "ies")+"y")
	case strings.HasSuffix(name, "ses"), strings.HasSuffix(name, "xes"), strings.HasSuffix(name, "ches"), strings.HasSuffix(name, "shes"):
		list = append(list, strings.TrimSuffix(name, "es"))
	case strings.HasSuffix(name, "s") && !strings.HasSuffix(name, "ss"):
		list = append(list, strings.TrimSuffix(name, "s"))
	}

	return list
}

// impliedRelationships finds columns named after another table's key, such
// as customer_id next to customers(id), that no foreign key declares.
func impliedRelationships(t DocTable, pks map[string]pkInfo) []Anomaly {
	declared := map[string]bool{}

	for _, fk := range t.ForeignKeys {
		for _, c := range fk.Columns {
			declared[c] = true
		}
	}

	var list []Anomaly

	for _, c := range t.Columns {
		// A table's own single key is not a reference to another table.
		if declared[c.Name] || (c.PrimaryKey && countPK(t) == 1) {
			continue
		}

		name := strings.ToLower(c.Name)

		var target string

		for _, suffix := range []string{"_id", "id"} {
			if strings.HasSuffix(name, suffix) && len(name) > len(suffix) {
				target = strings.TrimSuffix(strings.TrimSuffix(name, suffix), "_")

				break
			}
		}

		if target == "" {
			continue
		}

		pk, ok := pks[target]
		if !ok || pk.table == t.Name || !compatibleTypes(pk.typ, baseType(c.Type)) {
			continue
		}

		list = append(list, Anomaly{Kind: AnomalyImplied, Table: t.Name, Columns: []string{c.Name}, Ref: pk.table, RefCols: []string{pk.column}})
	}

	return list
}

func countPK(t DocTable) int {
	n := 0

	for _, c := range t.Columns {
		if c.PrimaryKey {
			n++
		}
	}

	return n
}

// nullableUnique finds columns a unique index covers alone while they
// still accept NULL, which most databases allow more than once.
func nullableUnique(t DocTable) []Anomaly {
	nullable := map[string]bool{}

	for _, c := range t.Columns {
		nullable[c.Name] = c.Nullable && !c.PrimaryKey
	}

	var list []Anomaly

	for _, ix := range t.Indexes {
		if ix.Unique && !ix.Primary && len(ix.Columns) == 1 && nullable[ix.Columns[0]] {
			list = append(list, Anomaly{Kind: AnomalyNullableUnique, Table: t.Name, Columns: ix.Columns, Ref: ix.Name})
		}
	}

	return list
}

// duplicateIndexes finds indexes on the same columns, in the same order,
// as another one of the table.
func duplicateIndexes(t DocTable) []Anomaly {
	seen := map[string]string{}

	var list []Anomaly

	for _, ix := range t.Indexes {
		if len(ix.Columns) == 0 {
			continue
		}

		key := strings.Join(ix.Columns, "\x1f")
		if first, ok := seen[key]; ok {
			list = append(list, Anomaly{Kind: AnomalyDuplicateIndex, Table: t.Name, Columns: ix.Columns, Ref: first + " / " + ix.Name})

			continue
		}

		seen[key] = ix.Name
	}

	return list
}

// incrementingColumns finds runs like phone1, phone2, phone3: a repeating
// group that usually belongs in a table of its own.
func incrementingColumns(t DocTable) []Anomaly {
	groups := map[string][]string{}

	var order []string

	for _, c := range t.Columns {
		m := trailingDigits.FindStringSubmatch(c.Name)
		if m == nil || m[1] == "" {
			continue
		}

		base := strings.TrimRight(strings.ToLower(m[1]), "_")
		if _, ok := groups[base]; !ok {
			order = append(order, base)
		}

		groups[base] = append(groups[base], c.Name)
	}

	var list []Anomaly

	for _, base := range order {
		if len(groups[base]) >= 2 {
			list = append(list, Anomaly{Kind: AnomalyIncrementing, Table: t.Name, Columns: groups[base]})
		}
	}

	return list
}

// isNullString tells a default of the text 'null', almost always meant as
// NULL, from NULL itself.
func isNullString(def string) bool {
	d := strings.ToLower(strings.TrimSpace(def))
	if i := strings.Index(d, "::"); i > 0 {
		d = d[:i]
	}

	return d == "'null'" || d == `"null"` || d == "n'null'"
}

func baseType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if i := strings.IndexAny(t, "( "); i > 0 {
		t = t[:i]
	}

	return t
}

var typeFamilies = map[string]string{
	"int": "int", "int2": "int", "int4": "int", "int8": "int", "integer": "int", "bigint": "int", "smallint": "int",
	"tinyint": "int", "mediumint": "int", "serial": "int", "bigserial": "int", "number": "int", "numeric": "int", "decimal": "int",
	"int32": "int", "int64": "int", "uint32": "int", "uint64": "int", "hugeint": "int", "ubigint": "int", "uinteger": "int",
	"varchar": "text", "char": "text", "text": "text", "character": "text", "nvarchar": "text", "nchar": "text",
	"varchar2": "text", "nvarchar2": "text", "string": "text", "uuid": "uuid", "uniqueidentifier": "uuid",
}

func compatibleTypes(a, b string) bool {
	if a == b {
		return true
	}

	fa, fb := typeFamilies[a], typeFamilies[b]

	return fa != "" && fa == fb
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)

	return slices.Equal(x, y)
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

func (postgresDriver) RowEstimates(ctx context.Context, q Querier, schema string) (map[string]int64, error) {
	return scanEstimates(q.QueryContext(ctx, `SELECT c.relname, c.reltuples::bigint::text
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind IN ('r', 'm')`, schema))
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
