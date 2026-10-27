package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
		return connErrFor(c, "connect; %w", err)
	}

	killed := false

	defer func() { release(p.driver, conn, killed || ctx.Err() != nil) }()

	err = fn(ctx, p.driver, conn)
	if ctx.Err() != nil {
		killed = true
	}

	return err
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
