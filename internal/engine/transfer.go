package engine

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Moving data in and out of a database: exports stream the rows of a table,
// imports and transfers insert rows in batches with bound parameters.

// TableRef names a table of a schema.
type TableRef struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
}

func qualify(d Driver, schema, table string) string {
	if schema == "" {
		return d.QuoteIdent(table)
	}

	return d.QuoteIdent(schema) + "." + d.QuoteIdent(table)
}

// RowFunc receives the rows of a table one by one; the slice is reused.
type RowFunc func(row []any) error

// StreamTable reads every row of a table on conn, in batches the driver
// fetches, and hands the raw scanned values to fn. The columns describe them.
func StreamTable(ctx context.Context, d Driver, conn Querier, schema, table string, columns func([]Column) error, fn RowFunc) error {
	rows, err := conn.QueryContext(ctx, "SELECT * FROM "+qualify(d, schema, table))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	types, err := rows.ColumnTypes()
	if err != nil {
		return err
	}

	cols := make([]Column, len(types))
	for i, t := range types {
		cols[i] = Column{Name: t.Name(), Type: columnTypeName(t)}
	}

	if err := columns(cols); err != nil {
		return err
	}

	values := make([]any, len(types))
	ptrs := make([]any, len(types))

	for i := range values {
		ptrs[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}

		if err := fn(values); err != nil {
			return err
		}
	}

	return rows.Err()
}

// Session runs fn on one pooled connection of c, with no other time limit
// than timeout; exports and transfers can take long.
func (m *Manager) Session(ctx context.Context, c *store.Connection, password string, timeout time.Duration,
	fn func(ctx context.Context, d Driver, conn *sql.Conn) error,
) error {
	p, err := m.pool(c, password)
	if err != nil {
		return err
	}

	ctx, cancel := withTimeout(ctx, timeout)
	defer cancel()

	if cp, ok := p.driver.(ContextPreparer); ok {
		ctx = cp.PrepareContext(ctx, timeout, false)
	}

	conn, err := p.db.Conn(ctx)
	if err != nil {
		return connErr("connect; %w", err)
	}

	killed := false

	defer func() { release(p.driver, conn, killed || ctx.Err() != nil) }()

	err = fn(ctx, p.driver, conn)
	if ctx.Err() != nil {
		killed = true
	}

	return err
}

// ////////////////////////////////////////////
// SQL literals, for SQL exports

// Literal writes a scanned value as a SQL literal of the dialect of d.
func Literal(d Driver, typ string, v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case bool:
		switch d.Type() {
		case "postgres", "duckdb", "trino", "clickhouse":
			return strconv.FormatBool(x)
		}

		if x {
			return "1"
		}

		return "0"
	case int64:
		return strconv.FormatInt(x, 10)
	case int32, int16, int8, int, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(x)
	case float64:
		return floatLiteral(x)
	case float32:
		return floatLiteral(float64(x))
	case time.Time:
		return timeLiteral(d, typ, x)
	case []byte:
		if binaryTypes[typ] || !isText(x) {
			return bytesLiteral(d, x)
		}

		return stringLiteral(d, string(x))
	case string:
		return stringLiteral(d, x)
	case *big.Int:
		return x.String()
	case fmt.Stringer:
		return stringLiteral(d, x.String())
	}

	if s, ok := jsonValue(v).(string); ok {
		return stringLiteral(d, s)
	}

	return stringLiteral(d, fmt.Sprint(v))
}

func isText(b []byte) bool {
	s, ok := jsonValue(b).(string)

	return ok && !strings.HasPrefix(s, "\\x")
}

func floatLiteral(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "NULL"
	}

	return strconv.FormatFloat(f, 'g', -1, 64)
}

func stringLiteral(d Driver, s string) string {
	s = strings.ReplaceAll(s, "'", "''")

	switch d.Dialect() {
	case DialectMySQL, DialectClickHouse:
		s = strings.ReplaceAll(s, `\`, `\\`)
	case DialectMSSQL:
		return "N'" + s + "'"
	}

	return "'" + s + "'"
}

func bytesLiteral(d Driver, b []byte) string {
	h := hex.EncodeToString(b)

	switch d.Type() {
	case "postgres":
		return `'\x` + h + `'::bytea`
	case "mssql":
		return "0x" + h
	case "oracle":
		return "HEXTORAW('" + h + "')"
	case "clickhouse":
		return "unhex('" + h + "')"
	case "duckdb":
		return "from_hex('" + h + "')"
	case "trino":
		return "X'" + h + "'"
	}

	return "X'" + h + "'"
}

func timeLiteral(d Driver, typ string, t time.Time) string {
	switch {
	case strings.Contains(typ, "date") && !strings.Contains(typ, "time"):
		s := "'" + t.Format("2006-01-02") + "'"
		if d.Type() == "oracle" {
			return "DATE " + s
		}

		return s
	case typ == "time" || strings.HasPrefix(typ, "time ") || strings.HasPrefix(typ, "time("):
		return "'" + t.Format("15:04:05.999999") + "'"
	}

	switch d.Type() {
	case "postgres", "duckdb":
		if strings.Contains(typ, "tz") || strings.Contains(typ, "zone") {
			return "'" + t.Format("2006-01-02 15:04:05.999999-07:00") + "'"
		}
	case "oracle":
		return "TIMESTAMP '" + t.Format("2006-01-02 15:04:05.999999") + "'"
	case "mssql":
		if strings.Contains(typ, "offset") {
			return "'" + t.Format("2006-01-02 15:04:05.9999999 -07:00") + "'"
		}

		return "'" + t.Format("2006-01-02T15:04:05.999") + "'"
	}

	return "'" + t.Format("2006-01-02 15:04:05.999999") + "'"
}

// ////////////////////////////////////////////
// inserting rows

// Inserter adds rows to one table in batches of bound parameters.
type Inserter struct {
	d         Driver
	run       Runner
	ctx       context.Context
	head      string
	cols      int
	perStmt   int
	batch     [][]any
	convert   []func(any) any
	Inserted  int64
	statement string
}

// maxParams keeps a statement under the parameter limits of the drivers:
// SQL Server allows 2100, PostgreSQL 65535.
const maxParams = 2000

// NewInserter prepares inserts into a table with the given columns.
func NewInserter(ctx context.Context, d Driver, run Runner, schema, table string, columns []string) *Inserter {
	quoted := make([]string, len(columns))
	for i, c := range columns {
		quoted[i] = d.QuoteIdent(c)
	}

	per := max(1, min(500, maxParams/max(1, len(columns))))
	// Oracle has no multi-row VALUES.
	if d.Type() == "oracle" {
		per = 1
	}

	head := "INSERT INTO " + qualify(d, schema, table) + " (" + strings.Join(quoted, ", ") + ") VALUES "

	return &Inserter{d: d, run: run, ctx: ctx, head: head, cols: len(columns), perStmt: per}
}

// Convert sets a conversion applied to the values of each column, such as
// turning text into the column's type; nil entries keep the value.
func (in *Inserter) Convert(fns []func(any) any) { in.convert = fns }

// Add queues a row, copying it, and sends a full batch.
func (in *Inserter) Add(row []any) error {
	if len(row) != in.cols {
		return fmt.Errorf("row has %d values, expected %d", len(row), in.cols)
	}

	cp := make([]any, len(row))
	for i, v := range row {
		if in.convert != nil && in.convert[i] != nil {
			v = in.convert[i](v)
		}

		cp[i] = v
	}

	in.batch = append(in.batch, cp)
	if len(in.batch) >= in.perStmt {
		return in.Flush()
	}

	return nil
}

// Flush sends the queued rows.
func (in *Inserter) Flush() error {
	if len(in.batch) == 0 {
		return nil
	}

	var b strings.Builder

	b.WriteString(in.head)

	args := make([]any, 0, len(in.batch)*in.cols)

	for r, row := range in.batch {
		if r > 0 {
			b.WriteString(", ")
		}

		b.WriteByte('(')

		for i, v := range row {
			if i > 0 {
				b.WriteString(", ")
			}

			args = append(args, v)
			b.WriteString(placeholder(in.d, len(args)))
		}

		b.WriteByte(')')
	}

	in.statement = b.String()

	if _, err := in.run.ExecContext(in.ctx, in.statement, args...); err != nil {
		return errors.New(cleanError(in.ctx, err))
	}

	in.Inserted += int64(len(in.batch))
	in.batch = in.batch[:0]

	return nil
}

// ////////////////////////////////////////////
// column types across databases

var (
	typeArgs    = regexp.MustCompile(`\(([^)]*)\)`)
	numericArgs = regexp.MustCompile(`^\s*(\d+|max)\s*(,\s*\d+\s*)?$`)
)

// MapType translates a column type of one database into one the target
// understands. The same database keeps the type as it is.
func MapType(from, to string, typ string) string {
	if from == to {
		return typ
	}

	t := strings.ToLower(strings.TrimSpace(typ))

	// ClickHouse wraps types: Nullable(Int32), LowCardinality(String).
	for _, w := range []string{"nullable(", "lowcardinality("} {
		for strings.HasPrefix(t, w) && strings.HasSuffix(t, ")") {
			t = strings.TrimSpace(t[len(w) : len(t)-1])
		}
	}

	args := ""

	// Only lengths and precisions carry over; anything else in the
	// parentheses (an enum list, a time zone) does not fit the target.
	if m := typeArgs.FindStringSubmatch(t); m != nil && numericArgs.MatchString(m[1]) {
		args = strings.ReplaceAll(m[1], " ", "")
	}

	base := strings.TrimSpace(typeArgs.ReplaceAllString(t, ""))

	// Arrays (text[], _int4) have no common form; their text goes over.
	if strings.HasSuffix(base, "]") || strings.HasPrefix(base, "_") || strings.HasPrefix(t, "array(") {
		return targetType(to, "text", "")
	}

	family := typeFamily(base)

	return targetType(to, family, args)
}

func typeFamily(t string) string {
	switch {
	case strings.Contains(t, "bigint") || t == "int8" || t == "int64" || t == "bigserial" || t == "uint32" || t == "uint64" || t == "long" || t == "hugeint":
		return "bigint"
	case t == "smallint" || t == "int2" || t == "int16" || t == "uint8" || t == "uint16" || t == "smallserial" || t == "tinyint" || t == "year":
		return "smallint"
	case strings.Contains(t, "int") || t == "serial" || t == "mediumint" || t == "integer":
		return "int"
	case strings.HasPrefix(t, "numeric") || strings.HasPrefix(t, "decimal") || t == "number" || t == "money" || t == "smallmoney":
		return "decimal"
	case t == "real" || t == "float4" || t == "float32" || t == "binary_float":
		return "real"
	case strings.Contains(t, "double") || t == "float" || t == "float8" || t == "float64" || t == "binary_double":
		return "double"
	case t == "bool" || t == "boolean" || t == "bit":
		return "bool"
	case strings.Contains(t, "timestamp") && (strings.Contains(t, "zone") || strings.Contains(t, "tz")) || t == "datetimeoffset" || t == "timestamptz":
		return "timestamptz"
	case strings.Contains(t, "timestamp") || strings.Contains(t, "datetime") || t == "smalldatetime":
		return "timestamp"
	case t == "date" || t == "date32":
		return "date"
	case strings.HasPrefix(t, "time"):
		return "time"
	case t == "uuid" || t == "uniqueidentifier":
		return "uuid"
	case t == "json" || t == "jsonb":
		return "json"
	case strings.Contains(t, "blob") || strings.Contains(t, "binary") || t == "bytea" || t == "raw" || t == "image" || t == "varbinary":
		return "binary"
	case strings.HasPrefix(t, "varchar") || strings.HasPrefix(t, "character varying") || t == "nvarchar" || t == "varchar2" || t == "nvarchar2":
		return "varchar"
	case t == "char" || t == "character" || t == "nchar" || t == "bpchar":
		return "char"
	}

	return "text"
}

func targetType(db, family, args string) string {
	n := strings.TrimSpace(strings.Split(args, ",")[0])
	if n == "max" || n == "-1" {
		n = ""
	}

	withArgs := func(t string) string {
		if args == "" {
			return t
		}

		return t + "(" + args + ")"
	}

	switch db {
	case "postgres", "duckdb":
		switch family {
		case "bigint", "smallint", "real", "date", "uuid", "time":
			return family
		case "int":
			return "integer"
		case "decimal":
			return withArgs("numeric")
		case "double":
			return "double precision"
		case "bool":
			return "boolean"
		case "timestamptz":
			return "timestamptz"
		case "timestamp":
			return "timestamp"
		case "json":
			if db == "duckdb" {
				return "json"
			}

			return "jsonb"
		case "binary":
			if db == "duckdb" {
				return "blob"
			}

			return "bytea"
		case "varchar":
			if n != "" {
				return "varchar(" + n + ")"
			}

			return "text"
		case "char":
			if n != "" {
				return "char(" + n + ")"
			}

			return "text"
		}

		return "text"
	case "mysql", "mariadb":
		switch family {
		case "bigint", "smallint", "int", "date", "time", "double":
			return family
		case "decimal":
			if args == "" {
				return "decimal(38,10)"
			}

			return withArgs("decimal")
		case "real":
			return "float"
		case "bool":
			return "tinyint(1)"
		case "timestamptz", "timestamp":
			return "datetime(6)"
		case "uuid":
			return "char(36)"
		case "json":
			return "json"
		case "binary":
			return "longblob"
		case "varchar":
			if n != "" {
				return "varchar(" + n + ")"
			}

			return "longtext"
		case "char":
			if n != "" {
				return "char(" + n + ")"
			}

			return "longtext"
		}

		return "longtext"
	case "mssql":
		switch family {
		case "bigint", "smallint", "int", "date", "real":
			return family
		case "decimal":
			if args == "" {
				return "decimal(38,10)"
			}

			return withArgs("decimal")
		case "double":
			return "float"
		case "bool":
			return "bit"
		case "timestamptz":
			return "datetimeoffset"
		case "timestamp":
			return "datetime2"
		case "time":
			return "time"
		case "uuid":
			return "uniqueidentifier"
		case "binary":
			return "varbinary(max)"
		case "varchar":
			if n != "" {
				if v, _ := strconv.Atoi(n); v > 4000 {
					return "nvarchar(max)"
				}

				return "nvarchar(" + n + ")"
			}

			return "nvarchar(max)"
		case "char":
			if n != "" {
				return "nchar(" + n + ")"
			}

			return "nvarchar(max)"
		}

		return "nvarchar(max)"
	case "oracle":
		switch family {
		case "bigint":
			return "NUMBER(19)"
		case "int":
			return "NUMBER(10)"
		case "smallint":
			return "NUMBER(5)"
		case "decimal":
			return withArgs("NUMBER")
		case "real":
			return "BINARY_FLOAT"
		case "double":
			return "BINARY_DOUBLE"
		case "bool":
			return "NUMBER(1)"
		case "timestamptz":
			return "TIMESTAMP WITH TIME ZONE"
		case "timestamp":
			return "TIMESTAMP"
		case "date":
			return "DATE"
		case "time":
			return "VARCHAR2(20)"
		case "uuid":
			return "VARCHAR2(36)"
		case "binary":
			return "BLOB"
		case "varchar":
			if v, _ := strconv.Atoi(n); v > 0 && v <= 4000 {
				return "VARCHAR2(" + n + " CHAR)"
			}

			return "CLOB"
		case "char":
			if n != "" {
				return "CHAR(" + n + ")"
			}

			return "CLOB"
		}

		return "CLOB"
	case "sqlite":
		switch family {
		case "bigint", "smallint", "int", "bool":
			return "INTEGER"
		case "real", "double":
			return "REAL"
		case "decimal":
			return "NUMERIC"
		case "binary":
			return "BLOB"
		}

		return "TEXT"
	case "clickhouse":
		switch family {
		case "bigint":
			return "Int64"
		case "int":
			return "Int32"
		case "smallint":
			return "Int16"
		case "decimal":
			if args == "" {
				return "Decimal(38,10)"
			}

			return withArgs("Decimal")
		case "real":
			return "Float32"
		case "double":
			return "Float64"
		case "bool":
			return "Bool"
		case "timestamptz", "timestamp":
			return "DateTime64(6)"
		case "date":
			return "Date32"
		case "uuid":
			return "UUID"
		}

		return "String"
	case "trino":
		switch family {
		case "bigint", "smallint", "real", "date", "uuid", "json":
			return family
		case "int":
			return "integer"
		case "decimal":
			if args == "" {
				return "decimal(38,10)"
			}

			return withArgs("decimal")
		case "double":
			return "double"
		case "bool":
			return "boolean"
		case "timestamptz":
			return "timestamp(6) with time zone"
		case "timestamp":
			return "timestamp(6)"
		case "time":
			return "time(6)"
		case "binary":
			return "varbinary"
		}

		return "varchar"
	}

	// ODBC and others: plain standard names.
	switch family {
	case "bigint", "smallint", "date", "time", "timestamp":
		return strings.ToUpper(family)
	case "int":
		return "INTEGER"
	case "decimal":
		return withArgs("DECIMAL")
	case "real", "double":
		return "DOUBLE PRECISION"
	case "timestamptz":
		return "TIMESTAMP"
	case "varchar":
		if n != "" {
			return "VARCHAR(" + n + ")"
		}
	}

	return "VARCHAR(4000)"
}

// CreateTableSQL writes a CREATE TABLE of the target database for columns
// read from the source, with the primary key when it has one.
func CreateTableSQL(from string, to Driver, schema, table string, cols []ColumnInfo) string {
	var b strings.Builder

	b.WriteString("CREATE TABLE " + qualify(to, schema, table) + " (\n")

	var pk []string

	for i, c := range cols {
		if i > 0 {
			b.WriteString(",\n")
		}

		typ := MapType(from, to.Type(), c.Type)
		notNull := !c.Nullable || c.PrimaryKey

		// ClickHouse columns hold no NULL unless wrapped in Nullable.
		if to.Type() == "clickhouse" {
			if !notNull {
				typ = "Nullable(" + typ + ")"
			}

			notNull = false
		}

		b.WriteString("  " + to.QuoteIdent(c.Name) + " " + typ)

		if notNull {
			b.WriteString(" NOT NULL")
		}

		if c.PrimaryKey {
			pk = append(pk, to.QuoteIdent(c.Name))
		}
	}

	switch {
	case to.Type() == "clickhouse":
		b.WriteString("\n) ENGINE = MergeTree ORDER BY ")

		if len(pk) > 0 {
			b.WriteString("(" + strings.Join(pk, ", ") + ")")
		} else {
			b.WriteString("tuple()")
		}

		return b.String()
	case len(pk) > 0 && to.Type() != "trino":
		b.WriteString(",\n  PRIMARY KEY (" + strings.Join(pk, ", ") + ")")
	}

	b.WriteString("\n)")

	return b.String()
}
