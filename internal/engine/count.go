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

// Counting the rows of a result: a grid shows only the first rows, so the
// footer asks for the total, as DBeaver's "Calculate total row count". The
// query is wrapped in SELECT count(*) and runs read-only, whatever the
// grant.

// CountRequest counts the rows a reading statement returns.
type CountRequest struct {
	SQL     string
	Timeout time.Duration
	QueryID string
	UserID  string
}

// CountResult is the total and the statement that counted it.
type CountResult struct {
	Count      int64  `json:"count"`
	SQL        string `json:"sql"`
	DurationMS int64  `json:"duration_ms"`
}

var ErrNotCountable = errors.New("only a query that returns rows can be counted")

// CountSQL wraps one reading statement in a count.
func CountSQL(d Driver, script string) (string, error) {
	stmts := SplitScript(d, script)
	if len(stmts) != 1 {
		return "", ErrNotCountable
	}

	stmt := strings.TrimRight(strings.TrimSpace(stmts[0]), "; \t\n")

	// Only queries can be a subquery: SHOW, DESCRIBE, PRAGMA or EXPLAIN
	// cannot.
	kw := FirstKeyword(stmt, d.Dialect())

	switch kw {
	case "SELECT", "WITH", "VALUES", "TABLE":
	default:
		return "", ErrNotCountable
	}

	// The read-only transaction stops a write anyway; refuse it up front.
	if writesInCTE(stmt, kw) {
		return "", ErrNotCountable
	}

	if _, err := Validate(d, stmt, true); err != nil {
		return "", err
	}

	// A trailing line comment would swallow the closing parenthesis.
	inner := stmt + "\n"

	switch d.Dialect() {
	case DialectOracle:
		// Oracle takes no AS before a table alias.
		return "SELECT COUNT(*) FROM (\n" + inner + ") dp_count", nil
	case DialectMSSQL:
		// ORDER BY needs TOP or OFFSET inside a derived table; counting does
		// not care about the order.
		return "SELECT COUNT_BIG(*) FROM (\n" + inner + ") AS dp_count", nil
	}

	return "SELECT COUNT(*) FROM (\n" + inner + ") AS dp_count", nil
}

// Count runs the count of a statement in a read-only transaction.
func (m *Manager) Count(ctx context.Context, c *store.Connection, password string, req CountRequest) (*CountResult, error) {
	d, err := DriverFor(c)
	if err != nil {
		return nil, err
	}

	q, err := CountSQL(d, req.SQL)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer m.track(ExecRequest{QueryID: req.QueryID, UserID: req.UserID}, c, cancel)()

	out := &CountResult{SQL: q}

	err = m.Session(ctx, c, password, req.Timeout, func(ctx context.Context, d Driver, conn *sql.Conn) error {
		start := time.Now()

		if cp, ok := d.(ContextPreparer); ok {
			ctx = cp.PrepareContext(ctx, req.Timeout, true)
		}

		var run Runner = conn

		if useTransaction(d, true) {
			tx, err := beginTx(ctx, d, conn, true)
			if err != nil {
				return fmt.Errorf("begin; %w", err)
			}
			defer func() { _ = tx.Rollback() }()

			run = tx
		}

		if err := d.Prepare(ctx, run, req.Timeout); err != nil {
			return fmt.Errorf("prepare session; %w", err)
		}

		rows, err := run.QueryContext(ctx, q)
		if err != nil {
			return errors.New(cleanError(ctx, err))
		}
		defer func() { _ = rows.Close() }()

		var n any
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return errors.New(cleanError(ctx, err))
			}

			return errors.New("the count returned no row")
		}

		if err := rows.Scan(&n); err != nil {
			return errors.New(cleanError(ctx, err))
		}

		total, err := countValue(n)
		if err != nil {
			return err
		}

		out.Count, out.DurationMS = total, time.Since(start).Milliseconds()

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// countValue reads a count, which drivers scan as an integer, a decimal
// (Oracle NUMBER) or text.
func countValue(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int32:
		return int64(x), nil
	case int:
		return int64(x), nil
	case uint64:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case float64:
		return int64(x), nil
	case []byte:
		return parseCount(string(x))
	case string:
		return parseCount(x)
	case fmt.Stringer:
		return parseCount(x.String())
	}

	return 0, fmt.Errorf("unexpected count %v", v)
}

func parseCount(s string) (int64, error) {
	var n int64
	if _, err := fmt.Sscan(strings.TrimSpace(s), &n); err != nil {
		return 0, fmt.Errorf("unexpected count %q", s)
	}

	return n, nil
}
