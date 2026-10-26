package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

// inTransaction runs fn in a transaction, or on the connection itself for
// databases without transactions, and commits when fn succeeds.
func inTransaction(ctx context.Context, d Driver, conn *sql.Conn, timeout time.Duration, fn func(run Runner) error) error {
	if !transactional(d) || !useTransaction(d, false) {
		if err := d.Prepare(ctx, conn, timeout); err != nil {
			return fmt.Errorf("prepare session; %w", err)
		}

		return fn(conn)
	}

	tx, err := beginTx(ctx, d, conn, false)
	if err != nil {
		return fmt.Errorf("begin; %w", err)
	}

	if err := d.Prepare(ctx, tx, timeout); err != nil {
		_ = tx.Rollback()

		return fmt.Errorf("prepare session; %w", err)
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()

		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit; %s", cleanError(ctx, err))
	}

	return nil
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
				if x == float64(int64(x)) {
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

// ////////////////////////////////////////////
// transfer between connections

// TransferRequest copies tables of a schema of one connection into a
// schema of another.
type TransferRequest struct {
	Schema       string
	Tables       []string
	TargetSchema string
	// Create makes missing target tables from the source columns.
	Create bool
	// Truncate empties existing target tables first.
	Truncate bool
	// Data copies the rows; without it only the tables are made.
	Data    bool
	Timeout time.Duration
}

// TransferProgress reports one table of a transfer.
type TransferProgress struct {
	Table   string `json:"table"`
	Rows    int64  `json:"rows"`
	Created bool   `json:"created,omitempty"`
	Done    bool   `json:"done,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Transfer copies the tables one by one. Each table goes in one transaction
// on the target where it has them, so a failed table leaves nothing behind;
// tables done before it stay. progress is called as rows arrive.
func (m *Manager) Transfer(ctx context.Context, src *store.Connection, srcPassword string, dst *store.Connection, dstPassword string,
	req TransferRequest, progress func(TransferProgress),
) error {
	if len(req.Tables) == 0 {
		return errors.New("no table chosen")
	}

	if len(req.Tables) > maxExportTables {
		return fmt.Errorf("at most %d tables at once", maxExportTables)
	}

	if !req.Create && !req.Data {
		return errors.New("choose to create the tables, copy the rows or both")
	}

	// The copy holds a session on each side at once; with one pool of a
	// single connection the second would wait forever.
	if poolKey(src) == poolKey(dst) && m.Limits(src).MaxOpen == 1 {
		return errors.New("copying within a connection needs a pool of at least two connections")
	}

	return m.Session(ctx, src, srcPassword, req.Timeout, func(ctx context.Context, sd Driver, sconn *sql.Conn) error {
		return m.Session(ctx, dst, dstPassword, req.Timeout, func(ctx context.Context, dd Driver, dconn *sql.Conn) error {
			existing := map[string]bool{}

			objects, err := dd.Objects(ctx, dconn, req.TargetSchema)
			if err != nil {
				return fmt.Errorf("read target tables: %w", err)
			}

			for _, o := range objects {
				existing[strings.ToLower(o.Name)] = true
			}

			for _, table := range req.Tables {
				p := TransferProgress{Table: table}

				err := transferTable(ctx, sd, sconn, dd, dconn, req, table, existing[strings.ToLower(table)], &p, progress)
				if err != nil {
					p.Error = err.Error()
					progress(p)

					if ctx.Err() != nil {
						return errors.New(cleanError(ctx, ctx.Err()))
					}

					continue
				}

				p.Done = true
				progress(p)
			}

			return nil
		})
	})
}

func transferTable(ctx context.Context, sd Driver, sconn *sql.Conn, dd Driver, dconn *sql.Conn, req TransferRequest,
	table string, exists bool, p *TransferProgress, progress func(TransferProgress),
) error {
	cols, err := sd.Columns(ctx, sconn, req.Schema, table)
	if err != nil {
		return err
	}

	if len(cols) == 0 {
		return fmt.Errorf("table %q not found", table)
	}

	if !exists && !req.Create {
		return errors.New("the target has no such table; choose to create missing tables")
	}

	return inTransaction(ctx, dd, dconn, req.Timeout, func(run Runner) error {
		if !exists {
			if _, err := run.ExecContext(ctx, CreateTableSQL(sd.Type(), dd, req.TargetSchema, table, cols)); err != nil {
				return fmt.Errorf("create table: %s", cleanError(ctx, err))
			}

			p.Created = true
		} else if req.Truncate && req.Data {
			if _, err := run.ExecContext(ctx, "DELETE FROM "+qualify(dd, req.TargetSchema, table)); err != nil {
				return fmt.Errorf("empty table: %s", cleanError(ctx, err))
			}
		}

		if !req.Data {
			return nil
		}

		var (
			ins     *Inserter
			srcCols []Column
			convert = converterFor(sd)
			last    = time.Now()
		)

		err := StreamTable(ctx, sd, sconn, req.Schema, table, func(c []Column) error {
			srcCols = c
			names := make([]string, len(c))

			for i, col := range c {
				names[i] = col.Name
			}

			ins = NewInserter(ctx, dd, run, req.TargetSchema, table, names)

			return nil
		}, func(row []any) error {
			for i, v := range row {
				row[i] = transferValue(sd, dd, srcCols[i].Type, convert, v)
			}

			if err := ins.Add(row); err != nil {
				return err
			}

			p.Rows++
			if time.Since(last) > 500*time.Millisecond {
				last = time.Now()
				progress(*p)
			}

			return nil
		})
		if err != nil {
			return err
		}

		return ins.Flush()
	})
}

// transferValue keeps what the target can bind as it is and turns the
// rest, such as driver-specific types, into text.
func transferValue(sd, dd Driver, typ string, convert ValueConverter, v any) any {
	switch x := v.(type) {
	case nil, int64, float64, string, time.Time, int32, int, float32:
		return x
	case bool:
		return boolFor(dd.Type(), x)
	case []byte:
		if binaryTypes[typ] {
			return append([]byte(nil), x...)
		}

		return string(x)
	}

	if convert != nil {
		if out, ok := convert(typ, v); ok {
			return out
		}
	}

	return jsonValue(v)
}
