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
	Schema  string
	Table   string
	Edits   []RowEdit
	Timeout time.Duration
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
func editSQL(d Driver, qualified string, cols map[string]ColumnInfo, pk []string, e RowEdit) (string, []any, error) {
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
				return fmt.Errorf("%w: key column %q is NULL", ErrBadEdit, k)
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
	case "delete":
		b.WriteString("DELETE FROM " + qualified)

		if err := where(); err != nil {
			return "", nil, err
		}
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
	pool  *pool
	stmts []editStmt
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

	qualified := d.QuoteIdent(req.Table)
	if req.Schema != "" {
		qualified = d.QuoteIdent(req.Schema) + "." + qualified
	}

	plan := &editPlan{pool: p, stmts: make([]editStmt, 0, len(req.Edits))}

	// Updates first, then deletes, then inserts, so a new row can reuse the
	// key of one deleted in the same batch.
	for _, op := range []string{"update", "delete", "insert"} {
		for _, e := range req.Edits {
			if e.Op != op {
				continue
			}

			if e.Op != "insert" && len(pk) == 0 {
				return nil, fmt.Errorf("%w: the table has no primary key; only new rows can be added", ErrBadEdit)
			}

			query, args, err := editSQL(d, qualified, cols, pk, e)
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

	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, connErr("connect; %w", err)
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
