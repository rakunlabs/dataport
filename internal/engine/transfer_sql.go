package engine

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

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
