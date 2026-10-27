package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// ImportRequest adds rows read from a file in the browser to a table. The
// browser sends a large file in several requests; the first may create the
// table.
type ImportRequest struct {
	Schema  string
	Table   string
	Columns []string
	Rows    [][]any
	// Create makes the table first, with the given column types; the types
	// are generic names such as bigint, decimal, bool, timestamp or text.
	Create []ColumnInfo
	// Truncate empties the table before the rows go in.
	Truncate bool
	// EmptyNull stores empty text as NULL.
	EmptyNull bool
	Timeout   time.Duration
}

type ImportResult struct {
	Inserted   int64    `json:"inserted"`
	Statements []string `json:"statements"`
	DurationMS int64    `json:"duration_ms"`
}

var ErrBadImport = errors.New("invalid import")

const maxImportRows = 5000

// Import runs one batch of an import in a transaction where the database
// has them, so a failing row leaves nothing of its batch behind.
func (m *Manager) Import(ctx context.Context, c *store.Connection, password string, req ImportRequest) (*ImportResult, error) {
	switch {
	case strings.TrimSpace(req.Table) == "":
		return nil, fmt.Errorf("%w: table is required", ErrBadImport)
	case len(req.Columns) == 0:
		return nil, fmt.Errorf("%w: no columns", ErrBadImport)
	case len(req.Rows) > maxImportRows:
		return nil, fmt.Errorf("%w: at most %d rows per request", ErrBadImport, maxImportRows)
	}

	start := time.Now()
	out := &ImportResult{Statements: []string{}}

	err := m.Session(ctx, c, password, req.Timeout, func(ctx context.Context, d Driver, conn *sql.Conn) error {
		return inTransaction(ctx, d, conn, req.Timeout, func(run Runner) error {
			if len(req.Create) > 0 {
				stmt := CreateTableSQL("generic", d, req.Schema, req.Table, req.Create)
				if _, err := run.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("create table: %s", cleanError(ctx, err))
				}

				out.Statements = append(out.Statements, stmt)
			}

			if req.Truncate {
				stmt := "DELETE FROM " + qualify(d, req.Schema, req.Table)
				if _, err := run.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("empty table: %s", cleanError(ctx, err))
				}

				out.Statements = append(out.Statements, stmt)
			}

			if len(req.Rows) == 0 {
				return nil
			}

			cols, err := d.Columns(ctx, run, req.Schema, req.Table)
			if err != nil {
				return err
			}

			types := make(map[string]string, len(cols))
			for _, col := range cols {
				types[strings.ToLower(col.Name)] = col.Type
			}

			convert := make([]func(any) any, len(req.Columns))

			for i, name := range req.Columns {
				typ, ok := types[strings.ToLower(name)]
				if !ok && len(cols) > 0 {
					return fmt.Errorf("%w: the table has no column %q", ErrBadImport, name)
				}

				convert[i] = importValue(d.Type(), typ, req.EmptyNull)
			}

			ins := NewInserter(ctx, d, run, req.Schema, req.Table, req.Columns)
			ins.Convert(convert)

			for n, row := range req.Rows {
				if err := ins.Add(row); err != nil {
					return fmt.Errorf("row %d: %w", n+1, err)
				}
			}

			if err := ins.Flush(); err != nil {
				return err
			}

			out.Inserted = ins.Inserted
			out.Statements = append(out.Statements, fmt.Sprintf("INSERT INTO %s (%d rows)", qualify(d, req.Schema, req.Table), ins.Inserted))

			return nil
		})
	})

	out.DurationMS = time.Since(start).Milliseconds()

	return out, err
}

// importValue converts text from a file into the type of the column, so
// every driver gets a value it can bind. What does not parse goes in as
// text and the database reports it.
func importValue(dbType, colType string, emptyNull bool) func(any) any {
	family := typeFamily(strings.ToLower(strings.TrimSpace(typeArgs.ReplaceAllString(strings.ToLower(colType), ""))))

	return func(v any) any {
		switch x := v.(type) {
		case nil:
			return nil
		case json.Number:
			v = x.String()
		case float64:
			if family == "bigint" || family == "int" || family == "smallint" {
				// 2^63 is exact as a float64; anything from it up overflows.
				if x >= -math.MaxInt64-1 && x < math.MaxInt64 && x == math.Trunc(x) {
					return int64(x)
				}
			}

			return x
		case bool:
			if dbType == "postgres" || dbType == "duckdb" || family == "bool" {
				return x
			}

			if x {
				return 1
			}

			return 0
		case map[string]any, []any:
			b, _ := json.Marshal(x)

			return string(b)
		}

		s, ok := v.(string)
		if !ok {
			return v
		}

		if s == "" {
			if emptyNull || family != "text" && family != "varchar" && family != "char" {
				return nil
			}

			return s
		}

		t := strings.TrimSpace(s)

		switch family {
		case "bigint", "int", "smallint":
			if n, err := strconv.ParseInt(t, 10, 64); err == nil {
				return n
			}
		case "real", "double":
			if f, err := strconv.ParseFloat(t, 64); err == nil {
				return f
			}
		case "bool":
			switch strings.ToLower(t) {
			case "true", "t", "yes", "y", "1":
				return boolFor(dbType, true)
			case "false", "f", "no", "n", "0":
				return boolFor(dbType, false)
			}
		case "timestamp", "timestamptz", "date":
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999", "2006-01-02"} {
				if tm, err := time.Parse(layout, t); err == nil {
					return tm
				}
			}
		}

		return s
	}
}

func boolFor(dbType string, b bool) any {
	switch dbType {
	case "mysql", "mariadb", "sqlite", "oracle":
		if b {
			return 1
		}

		return 0
	}

	return b
}
