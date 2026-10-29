package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Execution plans: EXPLAIN in the dialect of each database, with the
// options it takes. Analyze runs the statement, so it happens inside a
// transaction that is always rolled back; where a database cannot roll back
// only reading statements may be analyzed.

// ExplainOptions are the choices of the explain dialog; databases ignore
// what they do not support.
type ExplainOptions struct {
	Analyze  bool   `json:"analyze"`
	Verbose  bool   `json:"verbose"`
	Costs    bool   `json:"costs"`
	Buffers  bool   `json:"buffers"`
	Timing   bool   `json:"timing"`
	Settings bool   `json:"settings"`
	WAL      bool   `json:"wal"`
	Summary  bool   `json:"summary"`
	Format   string `json:"format"` // "text" (default), "json" or "xml"
}

// ExplainSupport tells the UI which options a database offers.
type ExplainSupport struct {
	Supported bool     `json:"supported"`
	Options   []string `json:"options"`
	Formats   []string `json:"formats"`
	// Analyze is set when the plan can be measured by running the
	// statement.
	Analyze bool `json:"analyze"`
	// writesPlan marks databases that store the plan in a table first
	// (Oracle's PLAN_TABLE), which a read-only transaction refuses.
	writesPlan bool
	// session runs the explain on the session itself, for settings that
	// may not change inside a transaction (SQL Server's SHOWPLAN).
	session bool
}

// ExplainRequest explains one statement.
type ExplainRequest struct {
	SQL      string
	Options  ExplainOptions
	ReadOnly bool
	Timeout  time.Duration
	MaxRows  int
	QueryID  string
	UserID   string
}

// ExplainResult is the plan, as the rows the database returned.
type ExplainResult struct {
	// SQL is what ran, to show and to copy.
	SQL      string     `json:"sql"`
	Format   string     `json:"format"`
	Analyzed bool       `json:"analyzed"`
	Result   *ResultSet `json:"result"`
}

var ErrNoExplain = errors.New("execution plans are not available for this database")

// Explainer is implemented by drivers that can show a plan. Explain runs on
// run, the transaction (or session) of the request.
type Explainer interface {
	ExplainSupport() ExplainSupport
	Explain(ctx context.Context, run Runner, stmt string, o ExplainOptions, maxRows int) (string, *ResultSet, error)
}

// ExplainSupportFor reports the explain options of a database type.
func ExplainSupportFor(dbType string) ExplainSupport {
	d, err := GetDriver(dbType)
	if err != nil {
		return ExplainSupport{}
	}

	if e, ok := d.(Explainer); ok {
		return e.ExplainSupport()
	}

	return ExplainSupport{}
}

// ExplainStatement checks the statement of an explain and returns it
// without its final semicolon.
func ExplainStatement(d Driver, script string, o ExplainOptions, readOnly bool) (string, error) {
	stmts := SplitScript(d, script)
	if len(stmts) != 1 {
		return "", errors.New("choose one statement to explain")
	}

	stmt := strings.TrimRight(strings.TrimSpace(stmts[0]), "; \t\n")

	kw := FirstKeyword(stmt, d.Dialect())
	switch {
	case kw == "":
		return "", errors.New("choose one statement to explain")
	case kw == "EXPLAIN" || kw == "ANALYZE":
		return "", errors.New("write the statement without EXPLAIN; choose the options in the dialog")
	case transactionKeywords[kw] || isBlock(d, stmt):
		return "", fmt.Errorf("%s cannot be explained", kw)
	}

	// Analyze runs the statement: a read-only grant only measures reads,
	// and so does a database whose writes cannot be rolled back.
	strict := o.Analyze && (readOnly || !transactional(d) || !useTransaction(d, false))
	if _, err := Validate(d, stmt, strict); err != nil {
		return "", err
	}

	return stmt, nil
}

// Explain shows the plan of one statement. The transaction is always
// rolled back, so analyzing a write leaves nothing behind.
func (m *Manager) Explain(ctx context.Context, c *store.Connection, password string, req ExplainRequest) (*ExplainResult, error) {
	d, err := DriverFor(c)
	if err != nil {
		return nil, err
	}

	e, ok := d.(Explainer)
	if !ok {
		return nil, ErrNoExplain
	}

	sup := e.ExplainSupport()
	if !sup.Analyze {
		req.Options.Analyze = false
	}

	stmt, err := ExplainStatement(d, req.SQL, req.Options, req.ReadOnly)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer m.track(ExecRequest{QueryID: req.QueryID, UserID: req.UserID}, c, cancel)()

	out := &ExplainResult{Format: req.Options.Format, Analyzed: req.Options.Analyze}
	if out.Format == "" {
		out.Format = "text"
	}

	maxRows := req.MaxRows
	if maxRows <= 0 {
		maxRows = 10000
	}

	err = m.Session(ctx, c, password, req.Timeout, func(ctx context.Context, d Driver, conn *sql.Conn) error {
		start := time.Now()

		var run Runner = conn

		if !sup.session && transactional(d) && useTransaction(d, req.ReadOnly) {
			readOnlyTx := req.ReadOnly && !sup.writesPlan

			tx, err := beginTx(ctx, d, conn, readOnlyTx)
			if err != nil {
				return fmt.Errorf("begin; %w", err)
			}
			defer func() { _ = tx.Rollback() }()

			run = tx
		}

		if err := d.Prepare(ctx, run, req.Timeout); err != nil {
			return fmt.Errorf("prepare session; %w", err)
		}

		shown, rs, err := e.Explain(ctx, run, stmt, req.Options, maxRows)
		if err != nil {
			return errors.New(cleanError(ctx, err))
		}

		rs.Statement = shown
		rs.DurationMS = time.Since(start).Milliseconds()
		out.SQL, out.Result = shown, rs

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// queryPlan runs an explain statement and reads its rows.
func queryPlan(ctx context.Context, d Driver, run Runner, q string, maxRows int) (string, *ResultSet, error) {
	rs, err := d.Query(ctx, run, q, maxRows)

	return q, rs, err
}

// ////////////////////////////////////////////
// drivers

func (postgresDriver) ExplainSupport() ExplainSupport {
	return ExplainSupport{
		Supported: true,
		Options:   []string{"analyze", "verbose", "costs", "buffers", "timing", "settings", "wal", "summary"},
		Formats:   []string{"text", "json"},
		Analyze:   true,
	}
}

func (d postgresDriver) Explain(ctx context.Context, run Runner, stmt string, o ExplainOptions, maxRows int) (string, *ResultSet, error) {
	return queryPlan(ctx, d, run, postgresExplain(stmt, o), maxRows)
}

func postgresExplain(stmt string, o ExplainOptions) string {
	opts := []string{}
	if o.Analyze {
		opts = append(opts, "ANALYZE")
	}

	if o.Verbose {
		opts = append(opts, "VERBOSE")
	}

	if !o.Costs {
		opts = append(opts, "COSTS OFF")
	}

	if o.Buffers {
		opts = append(opts, "BUFFERS")
	}

	if o.Settings {
		opts = append(opts, "SETTINGS")
	}

	// TIMING and WAL need ANALYZE.
	if o.Analyze {
		if !o.Timing {
			opts = append(opts, "TIMING OFF")
		}

		if o.WAL {
			opts = append(opts, "WAL")
		}
	}

	if o.Summary {
		opts = append(opts, "SUMMARY")
	}

	if o.Format == "json" {
		opts = append(opts, "FORMAT JSON")
	}

	if len(opts) == 0 {
		return "EXPLAIN " + stmt
	}

	return "EXPLAIN (" + strings.Join(opts, ", ") + ") " + stmt
}

// DuckDB takes EXPLAIN and EXPLAIN ANALYZE only.
func (duckdbDriver) ExplainSupport() ExplainSupport {
	return ExplainSupport{Supported: true, Options: []string{"analyze"}, Formats: []string{"text"}, Analyze: true}
}

func (d duckdbDriver) Explain(ctx context.Context, run Runner, stmt string, o ExplainOptions, maxRows int) (string, *ResultSet, error) {
	q := "EXPLAIN " + stmt
	if o.Analyze {
		q = "EXPLAIN ANALYZE " + stmt
	}

	return queryPlan(ctx, d, run, q, maxRows)
}

// MySQL 8.0.18 added EXPLAIN ANALYZE; MariaDB writes it ANALYZE.
func (mysqlDriver) ExplainSupport() ExplainSupport {
	return ExplainSupport{Supported: true, Options: []string{"analyze"}, Formats: []string{"text", "json"}, Analyze: true}
}

func (d mysqlDriver) Explain(ctx context.Context, run Runner, stmt string, o ExplainOptions, maxRows int) (string, *ResultSet, error) {
	return queryPlan(ctx, d, run, mysqlExplain(d.kind == "mariadb", stmt, o), maxRows)
}

func mysqlExplain(mariadb bool, stmt string, o ExplainOptions) string {
	if mariadb {
		switch {
		case o.Analyze && o.Format == "json":
			return "ANALYZE FORMAT=JSON " + stmt
		case o.Analyze:
			return "ANALYZE " + stmt
		case o.Format == "json":
			return "EXPLAIN FORMAT=JSON " + stmt
		}

		return "EXPLAIN " + stmt
	}

	switch {
	case o.Analyze:
		return "EXPLAIN ANALYZE " + stmt
	case o.Format == "json":
		return "EXPLAIN FORMAT=JSON " + stmt
	}

	return "EXPLAIN FORMAT=TREE " + stmt
}

func (sqliteDriver) ExplainSupport() ExplainSupport {
	return ExplainSupport{Supported: true, Formats: []string{"text"}}
}

func (d sqliteDriver) Explain(ctx context.Context, run Runner, stmt string, _ ExplainOptions, maxRows int) (string, *ResultSet, error) {
	return queryPlan(ctx, d, run, "EXPLAIN QUERY PLAN "+stmt, maxRows)
}

// ClickHouse explains without running; verbose adds the actions of each
// step.
func (clickhouseDriver) ExplainSupport() ExplainSupport {
	return ExplainSupport{Supported: true, Options: []string{"verbose"}, Formats: []string{"text", "json"}}
}

func (d clickhouseDriver) Explain(ctx context.Context, run Runner, stmt string, o ExplainOptions, maxRows int) (string, *ResultSet, error) {
	q := "EXPLAIN indexes = 1, actions = " + boolDigit(o.Verbose) + " " + stmt
	if o.Format == "json" {
		q = "EXPLAIN json = 1, indexes = 1, description = 1, actions = " + boolDigit(o.Verbose) + " " + stmt
	}

	return queryPlan(ctx, d, run, q, maxRows)
}

func (trinoDriver) ExplainSupport() ExplainSupport {
	return ExplainSupport{Supported: true, Options: []string{"analyze", "verbose"}, Formats: []string{"text", "json"}, Analyze: true}
}

func (d trinoDriver) Explain(ctx context.Context, run Runner, stmt string, o ExplainOptions, maxRows int) (string, *ResultSet, error) {
	q := "EXPLAIN " + stmt

	switch {
	case o.Analyze && o.Verbose:
		q = "EXPLAIN ANALYZE VERBOSE " + stmt
	case o.Analyze:
		q = "EXPLAIN ANALYZE " + stmt
	case o.Format == "json":
		q = "EXPLAIN (FORMAT JSON) " + stmt
	}

	return queryPlan(ctx, d, run, q, maxRows)
}

// SQL Server returns the estimated plan instead of running the batch while
// SHOWPLAN is on; the setting is turned off again on the same session.
func (mssqlDriver) ExplainSupport() ExplainSupport {
	return ExplainSupport{Supported: true, Formats: []string{"text", "xml"}, session: true}
}

func (d mssqlDriver) Explain(ctx context.Context, run Runner, stmt string, o ExplainOptions, maxRows int) (string, *ResultSet, error) {
	set := "SHOWPLAN_ALL"
	if o.Format == "xml" {
		set = "SHOWPLAN_XML"
	}

	if _, err := run.ExecContext(ctx, "SET "+set+" ON"); err != nil {
		return "", nil, err
	}

	rs, err := d.Query(ctx, run, stmt, maxRows)

	// A session left in SHOWPLAN would only return plans; the pool drops
	// it when this fails.
	offCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if _, offErr := run.ExecContext(offCtx, "SET "+set+" OFF"); offErr != nil && err == nil {
		err = offErr
	}

	return "SET " + set + " ON;\n" + stmt + ";\nSET " + set + " OFF;", rs, err
}

// Oracle writes the plan to PLAN_TABLE and formats it with DBMS_XPLAN; the
// rows go away with the rollback.
func (oracleDriver) ExplainSupport() ExplainSupport {
	return ExplainSupport{Supported: true, Options: []string{"verbose"}, Formats: []string{"text"}, writesPlan: true}
}

func (d oracleDriver) Explain(ctx context.Context, run Runner, stmt string, o ExplainOptions, maxRows int) (string, *ResultSet, error) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := "dp_" + hex.EncodeToString(b)

	explain := "EXPLAIN PLAN SET STATEMENT_ID = '" + id + "' FOR " + stmt
	if _, err := run.ExecContext(ctx, explain); err != nil {
		return "", nil, err
	}

	format := "TYPICAL"
	if o.Verbose {
		format = "ALL"
	}

	q := "SELECT plan_table_output AS plan FROM TABLE(DBMS_XPLAN.DISPLAY('PLAN_TABLE', '" + id + "', '" + format + "'))"
	rs, err := d.Query(ctx, run, q, maxRows)

	return explain + ";\n" + q, rs, err
}

func boolDigit(b bool) string {
	if b {
		return "1"
	}

	return "0"
}
