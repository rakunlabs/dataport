package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

type running struct {
	cancel context.CancelFunc
}

// Cancel stops a running request of the user.
func (m *Manager) Cancel(userID, queryID string) bool {
	m.runMu.Lock()
	defer m.runMu.Unlock()

	list := m.running[runKey{userID, queryID}]
	if len(list) == 0 {
		return false
	}

	for _, r := range list {
		r.cancel()
	}

	return true
}

func (m *Manager) track(req ExecRequest, cancel context.CancelFunc) func() {
	if req.QueryID == "" {
		return func() {}
	}

	key := runKey{req.UserID, req.QueryID}
	r := &running{cancel: cancel}

	m.runMu.Lock()
	m.running[key] = append(m.running[key], r)
	m.runMu.Unlock()

	return func() {
		m.runMu.Lock()
		defer m.runMu.Unlock()

		list := slices.DeleteFunc(m.running[key], func(x *running) bool { return x == r })
		if len(list) == 0 {
			delete(m.running, key)
		} else {
			m.running[key] = list
		}
	}
}

// Splitter is implemented by drivers whose scripts need more than the
// generic splitter, such as Oracle with its PL/SQL blocks.
type Splitter interface {
	Split(script string) []string
}

// SplitScript cuts a script into the statements the driver runs one by one.
func SplitScript(d Driver, script string) []string {
	if s, ok := d.(Splitter); ok {
		return s.Split(script)
	}

	return Split(script, d.Dialect())
}

// Validate splits the script and checks it against the grant without
// running anything.
func Validate(d Driver, script string, readOnly bool) ([]string, error) {
	stmts := SplitScript(d, script)
	if len(stmts) == 0 {
		return nil, errors.New("no statement to run")
	}

	checker, _ := d.(StatementChecker)

	for _, stmt := range stmts {
		kw := FirstKeyword(stmt, d.Dialect())

		if transactionKeywords[kw] && !isBlock(d, stmt) {
			return nil, fmt.Errorf("%s is not allowed; each run is a single transaction managed by dataport", kw)
		}

		if checker != nil {
			if err := checker.CheckStatement(stmt); err != nil {
				return nil, err
			}
		}

		if !readOnly {
			continue
		}

		if !readOnlyKeywords[kw] {
			return nil, fmt.Errorf("%w: %s statements are not allowed", ErrReadOnly, orUnknown(kw))
		}

		if err := d.CheckReadOnly(stmt); err != nil {
			return nil, err
		}
	}

	return stmts, nil
}

// BlockDetector is implemented by drivers where BEGIN opens a procedural
// block rather than a transaction.
type BlockDetector interface {
	IsBlock(stmt string) bool
}

func isBlock(d Driver, stmt string) bool {
	b, ok := d.(BlockDetector)

	return ok && b.IsBlock(stmt)
}

func orUnknown(kw string) string {
	if kw == "" {
		return "these"
	}

	return kw
}

func (m *Manager) Execute(ctx context.Context, c *store.Connection, password string, req ExecRequest) (*ExecResult, error) {
	d, err := DriverFor(c)
	if err != nil {
		return nil, err
	}

	stmts, err := Validate(d, req.SQL, req.ReadOnly)
	if err != nil {
		return nil, err
	}
	if len(req.Args) > 0 && len(stmts) != 1 {
		return nil, errors.New("parameterized cells must contain exactly one statement")
	}

	// A read-only run rolls back anyway, so it needs no manual transaction.
	if req.TxID != "" && !req.ReadOnly && transactional(d) && useTransaction(d, false) {
		return m.executeTx(ctx, c, password, d, stmts, req)
	}

	p, err := m.pool(c, password)
	if err != nil {
		return nil, err
	}

	ctx, cancel := withTimeout(ctx, req.Timeout)
	defer cancel()

	defer m.track(req, cancel)()

	if cp, ok := d.(ContextPreparer); ok {
		ctx = cp.PrepareContext(ctx, req.Timeout, req.ReadOnly)
	}

	start := time.Now()

	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, connErrFor(c, "connect; %w", err)
	}

	backendID, err := d.BackendID(ctx, conn)
	if err != nil {
		_ = conn.Close()

		return nil, connErrFor(c, "session id; %w", err)
	}

	var (
		killed  atomic.Bool
		watcher sync.WaitGroup
		done    = make(chan struct{})
	)

	// A cancelled context only closes the client side; tell the server to
	// stop the statement as well. The connection goes back to the pool only
	// after this goroutine has finished, so a late kill can never reach the
	// next request that reuses the same server session.
	watcher.Go(func() {
		select {
		case <-done:
		case <-ctx.Done():
			killed.Store(true)

			killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer killCancel()

			if err := d.Kill(killCtx, p.db, backendID); err != nil {
				slog.Debug("kill query", "connection", c.ID, "error", err)
			}
		}
	})

	defer func() {
		close(done)
		watcher.Wait()
		release(d, conn, killed.Load())
	}()

	result := &ExecResult{Results: make([]*ResultSet, 0, len(stmts)), Transactional: transactional(d)}

	// Without a transaction every statement takes effect on its own.
	if !useTransaction(d, req.ReadOnly) {
		if err := d.Prepare(ctx, conn, req.Timeout); err != nil {
			return nil, fmt.Errorf("prepare session; %w", err)
		}

		failed := false

		for _, stmt := range stmts {
			rs := m.runStatement(ctx, d, argumentRunner{Runner: conn, args: req.Args}, stmt, req.MaxRows)
			result.Results = append(result.Results, rs)

			if rs.Error != "" {
				failed = true

				break
			}
		}

		result.Committed = !failed && !req.ReadOnly
		result.DurationMS = time.Since(start).Milliseconds()

		return result, nil
	}

	tx, err := beginTx(ctx, d, conn, req.ReadOnly)
	if err != nil {
		return nil, fmt.Errorf("begin; %w", err)
	}

	if err := d.Prepare(ctx, tx, req.Timeout); err != nil {
		_ = tx.Rollback()

		return nil, fmt.Errorf("prepare session; %w", err)
	}

	failed := false

	for _, stmt := range stmts {
		rs := m.runStatement(ctx, d, argumentRunner{Runner: tx, args: req.Args}, stmt, req.MaxRows)
		result.Results = append(result.Results, rs)

		if rs.Error != "" {
			failed = true

			break
		}
	}

	if req.ReadOnly || failed {
		_ = tx.Rollback()
	} else if err := tx.Commit(); err != nil {
		result.Results = append(result.Results, &ResultSet{Statement: "COMMIT", Kind: "exec", Error: cleanError(ctx, err)})
	} else {
		result.Committed = true
	}

	result.DurationMS = time.Since(start).Milliseconds()

	return result, nil
}

type argumentRunner struct {
	Runner
	args []any
}

func (r argumentRunner) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return r.Runner.QueryContext(ctx, q, slices.Concat(r.args, args)...)
}
func (r argumentRunner) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return r.Runner.ExecContext(ctx, q, slices.Concat(r.args, args)...)
}

// Placeholder returns a bound parameter marker of the target dialect.
func Placeholder(d Driver, n int) string { return placeholder(d, n) }

func (m *Manager) runStatement(ctx context.Context, d Driver, tx Runner, stmt string, maxRows int) *ResultSet {
	start := time.Now()
	kw := FirstKeyword(stmt, d.Dialect())

	var (
		rs  *ResultSet
		err error
	)

	if returnsRows(stmt, kw) {
		rs, err = d.Query(ctx, tx, stmt, maxRows)
	} else {
		var res sql.Result

		res, err = tx.ExecContext(ctx, stmt)
		rs = &ResultSet{Kind: "exec"}

		if err == nil {
			rs.RowsAffected, _ = res.RowsAffected()
		}
	}

	if rs == nil {
		rs = &ResultSet{Kind: "exec"}
	}

	rs.Statement = stmt
	rs.DurationMS = time.Since(start).Milliseconds()

	if err != nil {
		rs.Error = cleanError(ctx, err)
	}

	return rs
}

// withTimeout limits a run to d; zero or less means no time limit, and the
// run can still be cancelled.
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(ctx)
	}

	return context.WithTimeout(ctx, d)
}

func cleanError(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "query timed out"
	}

	if errors.Is(ctx.Err(), context.Canceled) {
		return "query cancelled"
	}

	return err.Error()
}
