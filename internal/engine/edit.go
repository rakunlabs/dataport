package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Edits are row changes made in the table grid. The server builds the SQL
// itself from the table's real columns and primary key, binding every value
// as a parameter, so the grid never sends SQL text.

type RowEdit struct {
	Op string `json:"op"` // "update", "insert" or "delete"
	// Key identifies the row by its primary key columns (update, delete).
	Key map[string]any `json:"key,omitempty"`
	// Values are the new column values (update, insert).
	Values map[string]any `json:"values,omitempty"`
}

type EditRequest struct {
	Schema string
	Table  string
	Edits  []RowEdit
	// KeyColumns is a virtual key chosen by the user for a table without a
	// primary key, as DBeaver offers. It is ignored when the table has one.
	KeyColumns []string
	Timeout    time.Duration
}

type EditResult struct {
	Affected   int64    `json:"affected"`
	Statements []string `json:"statements"`
	DurationMS int64    `json:"duration_ms"`
}

var ErrBadEdit = errors.New("invalid edit")

const maxEdits = 1000

// placeholderer is implemented by drivers whose parameter syntax is not "?".
type placeholderer interface {
	Placeholder(n int) string
}

func placeholder(d Driver, n int) string {
	if p, ok := d.(placeholderer); ok {
		return p.Placeholder(n)
	}

	return "?"
}

// editSQL builds one parameterised statement and a readable form for the log.
// A virtual key may hold NULL values, which are matched with IS NULL.
func editSQL(d Driver, qualified string, cols map[string]ColumnInfo, pk []string, virtual bool, e RowEdit) (string, []any, error) {
	var (
		b    strings.Builder
		args []any
	)

	param := func(v any) string {
		args = append(args, v)

		return placeholder(d, len(args))
	}

	sortedKeys := func(m map[string]any) ([]string, error) {
		keys := make([]string, 0, len(m))
		for k := range m {
			if _, ok := cols[k]; !ok {
				return nil, fmt.Errorf("%w: unknown column %q", ErrBadEdit, k)
			}

			keys = append(keys, k)
		}

		slices.Sort(keys)

		return keys, nil
	}

	where := func() error {
		if len(e.Key) != len(pk) {
			return fmt.Errorf("%w: the row key must name every primary key column", ErrBadEdit)
		}

		b.WriteString(" WHERE ")

		for i, k := range pk {
			v, ok := e.Key[k]
			if !ok {
				return fmt.Errorf("%w: missing key column %q", ErrBadEdit, k)
			}

			if i > 0 {
				b.WriteString(" AND ")
			}

			if v == nil {
				if !virtual {
					return fmt.Errorf("%w: key column %q is NULL", ErrBadEdit, k)
				}

				b.WriteString(d.QuoteIdent(k) + " IS NULL")

				continue
			}

			b.WriteString(d.QuoteIdent(k) + " = " + param(v))
		}

		return nil
	}

	switch e.Op {
	case "update":
		keys, err := sortedKeys(e.Values)
		if err != nil {
			return "", nil, err
		}

		if len(keys) == 0 {
			return "", nil, fmt.Errorf("%w: nothing to update", ErrBadEdit)
		}

		b.WriteString("UPDATE " + qualified + " SET ")

		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}

			b.WriteString(d.QuoteIdent(k) + " = " + param(e.Values[k]))
		}

		if err := where(); err != nil {
			return "", nil, err
		}

		limitVirtual(d, &b, virtual)
	case "delete":
		b.WriteString("DELETE FROM " + qualified)

		if err := where(); err != nil {
			return "", nil, err
		}

		limitVirtual(d, &b, virtual)
	case "insert":
		keys, err := sortedKeys(e.Values)
		if err != nil {
			return "", nil, err
		}

		if len(keys) == 0 {
			return "", nil, fmt.Errorf("%w: a new row needs at least one value", ErrBadEdit)
		}

		quoted := make([]string, len(keys))
		params := make([]string, len(keys))

		for i, k := range keys {
			quoted[i] = d.QuoteIdent(k)
			params[i] = param(e.Values[k])
		}

		b.WriteString("INSERT INTO " + qualified + " (" + strings.Join(quoted, ", ") + ") VALUES (" + strings.Join(params, ", ") + ")")
	default:
		return "", nil, fmt.Errorf("%w: unknown operation %q", ErrBadEdit, e.Op)
	}

	return b.String(), args, nil
}

// limitVirtual caps a change found by a virtual key at two rows where the
// database allows it (MySQL, MariaDB). A key that is not unique then
// changes at most one row too many before the one-row check rolls it back,
// even on tables without transactions, such as MyISAM.
func limitVirtual(d Driver, b *strings.Builder, virtual bool) {
	if virtual && d.Dialect() == DialectMySQL {
		b.WriteString(" LIMIT 2")
	}
}

// PreviewEdits builds the statements ApplyEdits would run, with their
// parameters shown inline, without touching the data.
func (m *Manager) PreviewEdits(ctx context.Context, c *store.Connection, password string, req EditRequest) ([]string, error) {
	plan, err := m.planEdits(ctx, c, password, req)
	if err != nil {
		return nil, err
	}

	out := make([]string, len(plan.stmts))
	for i, s := range plan.stmts {
		out[i] = describe(s.sql, s.args)
	}

	return out, nil
}

type editStmt struct {
	sql  string
	args []any
	op   string
}

type editPlan struct {
	pool    *pool
	stmts   []editStmt
	virtual bool
}

func (m *Manager) planEdits(ctx context.Context, c *store.Connection, password string, req EditRequest) (*editPlan, error) {
	if len(req.Edits) == 0 {
		return nil, fmt.Errorf("%w: no changes", ErrBadEdit)
	}

	if len(req.Edits) > maxEdits {
		return nil, fmt.Errorf("%w: at most %d changes at once", ErrBadEdit, maxEdits)
	}

	p, err := m.pool(c, password)
	if err != nil {
		return nil, err
	}

	d := p.driver
	if !transactional(d) {
		return nil, fmt.Errorf("%w: editing rows is not supported for %s", ErrBadEdit, d.Type())
	}

	columns, err := m.Columns(ctx, c, password, req.Schema, req.Table)
	if err != nil {
		return nil, err
	}

	if len(columns) == 0 {
		return nil, fmt.Errorf("%w: table %q not found", ErrBadEdit, req.Table)
	}

	cols := make(map[string]ColumnInfo, len(columns))

	var pk []string

	for _, col := range columns {
		cols[col.Name] = col
		if col.PrimaryKey {
			pk = append(pk, col.Name)
		}
	}

	// Without a primary key, the user's virtual key finds the rows. The
	// one-row check in ApplyEdits keeps a key that is not unique from
	// changing more than one row.
	virtual := false

	if len(pk) == 0 && len(req.KeyColumns) > 0 {
		seen := make(map[string]bool, len(req.KeyColumns))

		for _, k := range req.KeyColumns {
			if _, ok := cols[k]; !ok {
				return nil, fmt.Errorf("%w: unknown key column %q", ErrBadEdit, k)
			}

			if seen[k] {
				return nil, fmt.Errorf("%w: key column %q is given twice", ErrBadEdit, k)
			}

			seen[k] = true
			pk = append(pk, k)
		}

		virtual = true
	}

	qualified := qualify(d, req.Schema, req.Table)

	plan := &editPlan{pool: p, stmts: make([]editStmt, 0, len(req.Edits)), virtual: virtual}

	// Updates first, then deletes, then inserts, so a new row can reuse the
	// key of one deleted in the same batch.
	for _, op := range []string{"update", "delete", "insert"} {
		for _, e := range req.Edits {
			if e.Op != op {
				continue
			}

			if e.Op != "insert" && len(pk) == 0 {
				return nil, fmt.Errorf("%w: the table has no primary key; choose key columns to change or delete rows", ErrBadEdit)
			}

			query, args, err := editSQL(d, qualified, cols, pk, virtual, e)
			if err != nil {
				return nil, err
			}

			plan.stmts = append(plan.stmts, editStmt{sql: query, args: args, op: e.Op})
		}
	}

	for _, e := range req.Edits {
		if e.Op != "update" && e.Op != "delete" && e.Op != "insert" {
			return nil, fmt.Errorf("%w: unknown operation %q", ErrBadEdit, e.Op)
		}
	}

	return plan, nil
}

// ApplyEdits runs the edits in one transaction. Every update and delete must
// hit exactly one row, otherwise everything is rolled back, so a stale grid
// or a duplicate key can never change more than the user saw.
func (m *Manager) ApplyEdits(ctx context.Context, c *store.Connection, password string, req EditRequest) (*EditResult, error) {
	start := time.Now()

	plan, err := m.planEdits(ctx, c, password, req)
	if err != nil {
		return nil, err
	}

	p, d, stmts := plan.pool, plan.pool.driver, plan.stmts

	ctx, cancel := withTimeout(ctx, req.Timeout)
	defer cancel()

	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, connErrFor(c, "connect; %w", err)
	}

	defer release(d, conn, false)

	tx, err := beginTx(ctx, d, conn, false)
	if err != nil {
		return nil, fmt.Errorf("begin; %w", err)
	}

	if err := d.Prepare(ctx, tx, req.Timeout); err != nil {
		_ = tx.Rollback()

		return nil, fmt.Errorf("prepare session; %w", err)
	}

	result := &EditResult{Statements: make([]string, 0, len(stmts))}

	for i, s := range stmts {
		res, err := tx.ExecContext(ctx, s.sql, s.args...)
		if err != nil {
			_ = tx.Rollback()

			return nil, fmt.Errorf("change %d: %s", i+1, cleanError(ctx, err))
		}

		n, _ := res.RowsAffected()
		if s.op != "insert" && n != 1 {
			_ = tx.Rollback()

			if plan.virtual && n > 1 {
				return nil, fmt.Errorf("%w: change %d matched %d rows: the key columns do not identify one row; choose more columns", ErrBadEdit, i+1, n)
			}

			return nil, fmt.Errorf("%w: change %d matched %d rows instead of 1; reload the table and try again", ErrBadEdit, i+1, n)
		}

		result.Affected += n
		result.Statements = append(result.Statements, describe(s.sql, s.args))
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit; %s", cleanError(ctx, err))
	}

	result.DurationMS = time.Since(start).Milliseconds()

	return result, nil
}

// describe renders a statement with its parameters for the query log.
func describe(query string, args []any) string {
	if len(args) == 0 {
		return query
	}

	parts := make([]string, len(args))
	for i, a := range args {
		if a == nil {
			parts[i] = "$" + strconv.Itoa(i+1) + "=NULL"

			continue
		}

		s := fmt.Sprint(a)
		if len(s) > 200 {
			s = s[:200] + "…"
		}

		parts[i] = "$" + strconv.Itoa(i+1) + "=" + strconv.Quote(s)
	}

	return query + " -- " + strings.Join(parts, ", ")
}
