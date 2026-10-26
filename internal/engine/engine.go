// Package engine runs SQL against the target databases the admin defined.
//
// Every request runs on one pooled connection inside one transaction. A
// read-only grant gets a read-only transaction and only statements that read;
// the database privileges of the configured account remain the real limit.
package engine

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/rakunlabs/dataport/internal/store"
)

var (
	ErrUnsupported = errors.New("unsupported database type")
	ErrReadOnly    = errors.New("this connection is read-only")
)

// ConnError wraps a failure to reach the target database. Its text can name
// the host, so only admins see it. Auth is set when the database refused the
// username or password.
type ConnError struct {
	Err  error
	Auth bool
}

func (e *ConnError) Error() string { return e.Err.Error() }
func (e *ConnError) Unwrap() error { return e.Err }

// openErr reports a failure to open a pool. A driver missing from the build
// is a setup problem the user may read; anything else may name the host.
func openErr(c *store.Connection, err error) error {
	if errors.Is(err, ErrUnsupported) || errors.Is(err, ErrFilesDisabled) || errors.Is(err, ErrFilePath) {
		return err
	}

	return connErr("open "+c.DBType+"; %w", err)
}

func connErr(format string, err error) error {
	return &ConnError{Err: fmt.Errorf(format, err), Auth: isAuthError(err)}
}

// Driver is implemented once per database type.
type Driver interface {
	Type() string
	Dialect() Dialect
	DefaultPort() int

	Open(c *store.Connection, password string) (*sql.DB, error)
	// BackendID returns the server-side id of the session, used to cancel a
	// running statement from another connection.
	BackendID(ctx context.Context, conn *sql.Conn) (string, error)
	Kill(ctx context.Context, db *sql.DB, backendID string) error
	// Prepare applies per-transaction settings such as the statement timeout.
	Prepare(ctx context.Context, tx Runner, timeout time.Duration) error
	// Query runs a statement that returns rows and reads at most maxRows+1 of
	// them, so the caller can tell whether the result was truncated.
	Query(ctx context.Context, tx Runner, stmt string, maxRows int) (*ResultSet, error)
	// CheckReadOnly rejects dialect-specific constructs that could escape a
	// read-only transaction.
	CheckReadOnly(stmt string) error
	// Reset clears the session state a script may have left on a pooled
	// connection, such as SET or temporary tables, so it cannot leak into the
	// next user's request. Returning an error drops the connection instead.
	Reset(ctx context.Context, conn *sql.Conn) error

	QuoteIdent(name string) string
	Schemas(ctx context.Context, q Querier, c *store.Connection) ([]string, error)
	Objects(ctx context.Context, q Querier, schema string) ([]Object, error)
	Columns(ctx context.Context, q Querier, schema, table string) ([]ColumnInfo, error)
}

// Querier is satisfied by *sql.DB, *sql.Conn and *sql.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Runner runs the statements of a request: a transaction, or the connection
// itself for drivers that run every statement on its own.
type Runner interface {
	Querier
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

var drivers = map[string]Driver{}

func register(d Driver) {
	drivers[d.Type()] = d
}

func GetDriver(dbType string) (Driver, error) {
	d, ok := drivers[dbType]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, dbType)
	}

	return d, nil
}

// Specializer is implemented by drivers whose behaviour depends on the
// connection, such as ODBC, where an option names the catalog dialect.
type Specializer interface {
	ForConnection(c *store.Connection) Driver
}

// DriverFor returns the driver that serves a connection.
func DriverFor(c *store.Connection) (Driver, error) {
	d, err := GetDriver(c.DBType)
	if err != nil {
		return nil, err
	}

	if s, ok := d.(Specializer); ok {
		return s.ForConnection(c), nil
	}

	return d, nil
}

// Targets name what a connection points at, which decides the fields the
// admin fills in.
const (
	// TargetNetwork is a server reached by host and port.
	TargetNetwork = "network"
	// TargetFile is a database file on the dataport server, in Database.
	TargetFile = "file"
	// TargetDSN is an ODBC connection string in the "dsn" option.
	TargetDSN = "dsn"
)

// Targeter is implemented by drivers that do not connect by host and port.
type Targeter interface {
	Target() string
}

func target(d Driver) string {
	if t, ok := d.(Targeter); ok {
		return t.Target()
	}

	return TargetNetwork
}

// ConnectionChecker is implemented by drivers with requirements beyond the
// fields every target needs.
type ConnectionChecker interface {
	CheckConnection(c *store.Connection) error
}

// CheckConnection reports the first setting the driver of c cannot work
// without.
func CheckConnection(c *store.Connection) error {
	d, err := DriverFor(c)
	if err != nil {
		return err
	}

	switch target(d) {
	case TargetFile:
		if strings.TrimSpace(c.Database) == "" {
			return errors.New("database file path is required")
		}

		if c.UserCredentials {
			return errors.New("a database file has no accounts to sign in with")
		}

		if _, err := resolveFile(c.Database, false); err != nil {
			return err
		}
	case TargetDSN:
		if strings.TrimSpace(c.Options["dsn"]) == "" {
			return errors.New("connection string is required")
		}
	default:
		if strings.TrimSpace(c.Host) == "" {
			return errors.New("host is required")
		}
	}

	if cc, ok := d.(ConnectionChecker); ok {
		return cc.CheckConnection(c)
	}

	return nil
}

type DriverInfo struct {
	Type        string `json:"type"`
	DefaultPort int    `json:"default_port"`
	Target      string `json:"target"`
	// Transactional is false when every statement takes effect on its own;
	// such databases cannot be edited in the grid.
	Transactional bool `json:"transactional"`
	// Available is false when this build of dataport lacks the driver, such
	// as ODBC and DuckDB, which need cgo.
	Available bool `json:"available"`
}

// Availability is implemented by drivers that may be missing from a build.
type Availability interface {
	Available() bool
}

func available(d Driver) bool {
	a, ok := d.(Availability)

	return !ok || a.Available()
}

func errNotBuilt(name string) error {
	return fmt.Errorf("%w: this dataport build has no %s driver; use the cgo build (dataport-cgo or the container image)", ErrUnsupported, name)
}

func Drivers() []DriverInfo {
	list := make([]DriverInfo, 0, len(drivers))
	for _, d := range drivers {
		list = append(list, Info(d))
	}

	return list
}

func Info(d Driver) DriverInfo {
	return DriverInfo{Type: d.Type(), DefaultPort: d.DefaultPort(), Target: target(d), Transactional: transactional(d), Available: available(d)}
}

// Editable reports whether rows of the database can be edited in the grid.
func Editable(dbType string) bool {
	d, err := GetDriver(dbType)

	return err == nil && transactional(d)
}

// ////////////////////////////////////////////

type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type ResultSet struct {
	Statement    string   `json:"statement"`
	Kind         string   `json:"kind"` // "rows" or "exec"
	Columns      []Column `json:"columns,omitempty"`
	Rows         [][]any  `json:"rows,omitempty"`
	RowsAffected int64    `json:"rows_affected"`
	Truncated    bool     `json:"truncated"`
	DurationMS   int64    `json:"duration_ms"`
	Error        string   `json:"error,omitempty"`
}

type Object struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "table", "view", "materialized_view", "foreign_table"
}

type ColumnInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	Default    string `json:"default"`
	PrimaryKey bool   `json:"primary_key"`
	// Extra holds what the type and default leave out but the column keeps,
	// such as MySQL's AUTO_INCREMENT.
	Extra string `json:"extra,omitempty"`
}

type ExecRequest struct {
	QueryID  string
	UserID   string
	SQL      string
	ReadOnly bool
	MaxRows  int
	Timeout  time.Duration
}

type ExecResult struct {
	Results    []*ResultSet `json:"results"`
	DurationMS int64        `json:"duration_ms"`
	// Committed is false when the transaction was rolled back, because the
	// grant is read-only or a statement failed.
	Committed bool `json:"committed"`
	// Transactional is false for databases without transactions, where each
	// statement takes effect on its own and nothing can be rolled back.
	Transactional bool `json:"transactional"`
}

// TotalRows is the number of rows read plus rows affected, for the log.
func (r *ExecResult) TotalRows() int64 {
	var n int64
	for _, rs := range r.Results {
		n += int64(len(rs.Rows)) + rs.RowsAffected
	}

	return n
}

func (r *ExecResult) FirstError() string {
	for _, rs := range r.Results {
		if rs.Error != "" {
			return rs.Error
		}
	}

	return ""
}

// ////////////////////////////////////////////

type pool struct {
	db       *sql.DB
	driver   Driver
	version  int64
	limits   store.Limits
	lastUsed time.Time
}

type running struct {
	userID string
	cancel context.CancelFunc
}

type Manager struct {
	mu       sync.Mutex
	pools    map[string]*pool
	defaults store.Limits
	idle     time.Duration

	runMu   sync.Mutex
	running map[string]*running
}

// DefaultLimits are the pool settings a connection gets when neither the
// admin nor the user set them.
var DefaultLimits = store.Limits{MaxOpen: 5, MaxIdle: 5, MaxLifetimeSec: 3600, MaxIdleTimeSec: 300}

// NewManager keeps one pool per connection, or per user for connections where
// users sign in with their own account. defaults fill the limits a connection
// leaves at zero; idle closes pools unused for that long.
func NewManager(defaults store.Limits, idle time.Duration) *Manager {
	return &Manager{
		pools:    map[string]*pool{},
		defaults: DefaultLimits.Merge(defaults),
		idle:     idle,
		running:  map[string]*running{},
	}
}

// Limits returns the effective pool settings of a connection.
func (m *Manager) Limits(c *store.Connection) store.Limits {
	return m.defaults.Merge(c.Limits)
}

// poolKey separates the pools of users who sign in with their own account,
// so one user's session can never serve another user.
func poolKey(c *store.Connection) string {
	if c.Owner == "" {
		return c.ID
	}

	return c.ID + "/" + c.Owner
}

// pool returns the pool of a connection, reopening it when the connection was
// edited since the pool was created.
func (m *Manager) pool(c *store.Connection, password string) (*pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := poolKey(c)

	limits := m.Limits(c)

	// A pool is reopened when the connection was edited or, for a user's own
	// account, when the user changed their limits.
	if p, ok := m.pools[key]; ok {
		if p.version == c.UpdatedAt && p.limits == limits {
			p.lastUsed = time.Now()

			return p, nil
		}

		_ = p.db.Close()
		delete(m.pools, key)
	}

	d, err := DriverFor(c)
	if err != nil {
		return nil, err
	}

	db, err := d.Open(c, password)
	if err != nil {
		return nil, openErr(c, err)
	}

	applyLimits(db, limits)

	p := &pool{db: db, driver: d, version: c.UpdatedAt, limits: limits, lastUsed: time.Now()}
	m.pools[key] = p

	return p, nil
}

func applyLimits(db *sql.DB, l store.Limits) {
	db.SetMaxOpenConns(l.MaxOpen)
	// More idle connections than open ones are never kept.
	db.SetMaxIdleConns(min(l.MaxIdle, l.MaxOpen))
	db.SetConnMaxLifetime(time.Duration(l.MaxLifetimeSec) * time.Second)
	db.SetConnMaxIdleTime(time.Duration(l.MaxIdleTimeSec) * time.Second)
}

// Forget closes the pools of a deleted or edited connection, the per-user
// pools included.
func (m *Manager) Forget(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, p := range m.pools {
		if key == id || strings.HasPrefix(key, id+"/") {
			_ = p.db.Close()
			delete(m.pools, key)
		}
	}
}

// ForgetUser closes the pool a user opened with their own account.
func (m *Manager) ForgetUser(id, userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := id + "/" + userID
	if p, ok := m.pools[key]; ok {
		_ = p.db.Close()
		delete(m.pools, key)
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, p := range m.pools {
		_ = p.db.Close()
		delete(m.pools, id)
	}
}

// Janitor closes pools that were idle longer than the idle timeout.
func (m *Manager) Janitor(ctx context.Context) {
	if m.idle <= 0 {
		return
	}

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			for id, p := range m.pools {
				if time.Since(p.lastUsed) > m.idle {
					_ = p.db.Close()
					delete(m.pools, id)
				}
			}
			m.mu.Unlock()
		}
	}
}

// Test opens a fresh connection with the given settings and pings it.
func (m *Manager) Test(ctx context.Context, c *store.Connection, password string) error {
	d, err := DriverFor(c)
	if err != nil {
		return err
	}

	db, err := d.Open(c, password)
	if err != nil {
		return openErr(c, err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return connErr("ping; %w", err)
	}

	return nil
}

// Cancel stops a running request of the user.
func (m *Manager) Cancel(userID, queryID string) bool {
	m.runMu.Lock()
	defer m.runMu.Unlock()

	r, ok := m.running[queryID]
	if !ok || r.userID != userID {
		return false
	}

	r.cancel()

	return true
}

func (m *Manager) track(req ExecRequest, cancel context.CancelFunc) func() {
	if req.QueryID == "" {
		return func() {}
	}

	key := req.QueryID

	m.runMu.Lock()
	if _, exists := m.running[key]; exists {
		key = req.QueryID + "-" + store.NewID()
	}

	m.running[key] = &running{userID: req.UserID, cancel: cancel}
	m.runMu.Unlock()

	return func() {
		m.runMu.Lock()
		delete(m.running, key)
		m.runMu.Unlock()
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
		return nil, connErr("connect; %w", err)
	}

	backendID, err := d.BackendID(ctx, conn)
	if err != nil {
		_ = conn.Close()

		return nil, connErr("session id; %w", err)
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
			rs := m.runStatement(ctx, d, conn, stmt, req.MaxRows)
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
		rs := m.runStatement(ctx, d, tx, stmt, req.MaxRows)
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

// TxBeginner is implemented by drivers that start transactions themselves,
// because the database/sql read-only option does not fit them.
type TxBeginner interface {
	BeginTx(ctx context.Context, conn *sql.Conn, readOnly bool) (*sql.Tx, error)
}

// ContextPreparer is implemented by drivers that pass per-run settings
// through the context instead of SQL, such as ClickHouse.
type ContextPreparer interface {
	PrepareContext(ctx context.Context, timeout time.Duration, readOnly bool) context.Context
}

// NonTransactional is implemented by drivers of databases without
// transactions.
type NonTransactional interface {
	NonTransactional()
}

// TxPolicy is implemented by drivers that run some requests without a
// transaction, such as Trino, whose connectors often accept writes only in
// autocommit mode.
type TxPolicy interface {
	UseTransaction(readOnly bool) bool
}

func useTransaction(d Driver, readOnly bool) bool {
	if p, ok := d.(TxPolicy); ok {
		return p.UseTransaction(readOnly)
	}

	return true
}

// StatementChecker is implemented by drivers that refuse some statements
// whatever the grant, such as ATTACH in SQLite, which reaches files on the
// dataport server.
type StatementChecker interface {
	CheckStatement(stmt string) error
}

func beginTx(ctx context.Context, d Driver, conn *sql.Conn, readOnly bool) (*sql.Tx, error) {
	if b, ok := d.(TxBeginner); ok {
		return b.BeginTx(ctx, conn, readOnly)
	}

	return conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
}

func transactional(d Driver) bool {
	_, no := d.(NonTransactional)

	return !no
}

// release resets the session and returns the connection to the pool, or
// drops it when the reset fails or a kill was sent.
func release(d Driver, conn *sql.Conn, killed bool) {
	discard := killed

	if !discard {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		discard = d.Reset(ctx, conn) != nil
	}

	if discard {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}

	_ = conn.Close()
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

// Schemas, Objects and Columns read metadata on a pooled connection. The
// connection is taken first so that failing to reach the database is
// reported as a ConnError, apart from errors of the metadata query.

func (m *Manager) meta(ctx context.Context, c *store.Connection, password string,
	fn func(ctx context.Context, d Driver, conn *sql.Conn) error,
) error {
	p, err := m.pool(c, password)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	conn, err := p.db.Conn(ctx)
	if err != nil {
		return connErr("connect; %w", err)
	}
	defer func() { _ = conn.Close() }()

	return fn(ctx, p.driver, conn)
}

func (m *Manager) Schemas(ctx context.Context, c *store.Connection, password string) ([]string, error) {
	var list []string

	err := m.meta(ctx, c, password, func(ctx context.Context, d Driver, conn *sql.Conn) (err error) {
		list, err = d.Schemas(ctx, conn, c)

		return err
	})

	return list, err
}

func (m *Manager) Objects(ctx context.Context, c *store.Connection, password, schema string) ([]Object, error) {
	var list []Object

	err := m.meta(ctx, c, password, func(ctx context.Context, d Driver, conn *sql.Conn) (err error) {
		list, err = d.Objects(ctx, conn, schema)

		return err
	})

	return list, err
}

func (m *Manager) Columns(ctx context.Context, c *store.Connection, password, schema, table string) ([]ColumnInfo, error) {
	var list []ColumnInfo

	err := m.meta(ctx, c, password, func(ctx context.Context, d Driver, conn *sql.Conn) (err error) {
		list, err = d.Columns(ctx, conn, schema, table)

		return err
	})

	return list, err
}

// ////////////////////////////////////////////

// scanRows reads at most maxRows rows and reports whether more were
// available.
func scanRows(rows *sql.Rows, maxRows int) (*ResultSet, error) {
	return scanRowsWith(rows, maxRows, nil)
}

// ValueConverter turns a driver-specific scanned value into a JSON value. It
// returns false to fall back to the generic conversion.
type ValueConverter func(dbType string, v any) (any, bool)

func scanRowsWith(rows *sql.Rows, maxRows int, convert ValueConverter) (*ResultSet, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}

	rs := &ResultSet{Kind: "rows", Columns: make([]Column, len(types)), Rows: [][]any{}}
	binary := make([]bool, len(types))

	for i, t := range types {
		name := columnTypeName(t)
		rs.Columns[i] = Column{Name: t.Name(), Type: name}
		binary[i] = binaryTypes[name]
	}

	values := make([]any, len(types))
	ptrs := make([]any, len(types))

	for i := range values {
		ptrs[i] = &values[i]
	}

	for rows.Next() {
		if len(rs.Rows) >= maxRows {
			rs.Truncated = true

			break
		}

		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		row := make([]any, len(values))
		for i, v := range values {
			row[i] = cellValue(rs.Columns[i].Type, binary[i], convert, v)
		}

		rs.Rows = append(rs.Rows, row)
	}

	if !rs.Truncated {
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	return rs, nil
}

// cellValue turns a scanned value into what the grid and the exports show.
func cellValue(typ string, binary bool, convert ValueConverter, v any) any {
	if convert != nil {
		if out, ok := convert(typ, v); ok {
			return out
		}
	}

	if b, ok := v.([]byte); ok && binary {
		return "\\x" + hex.EncodeToString(b)
	}

	return jsonValue(v)
}

// converterFor returns the value converter of a driver's results.
func converterFor(d Driver) ValueConverter {
	switch d.Type() {
	case "mssql":
		return mssqlValue
	case "odbc":
		return trimChar
	}

	return nil
}

// columnTypeName is the lower-case type of a result column. PostgreSQL
// reports arrays by their internal name, _text for text[]; they are written
// the way they are declared.
func columnTypeName(t *sql.ColumnType) string {
	name := strings.ToLower(t.DatabaseTypeName())
	if len(name) > 1 && name[0] == '_' {
		return name[1:] + "[]"
	}

	return name
}

var binaryTypes = map[string]bool{
	"bytea": true, "blob": true, "tinyblob": true, "mediumblob": true, "longblob": true,
	"binary": true, "varbinary": true, "geometry": true, "bit": true,
}

const maxSafeInt = 1<<53 - 1

// jsonValue converts a scanned value into something JSON and JavaScript can
// show without losing information.
func jsonValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		if utf8.Valid(x) {
			return string(x)
		}

		return "\\x" + hex.EncodeToString(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case int64:
		if x > maxSafeInt || x < -maxSafeInt {
			return fmt.Sprintf("%d", x)
		}

		return x
	case uint64:
		if x > maxSafeInt {
			return fmt.Sprintf("%d", x)
		}

		return x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Sprintf("%v", x)
		}

		return x
	case float32:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Sprintf("%v", x)
		}

		return f
	case string, bool, int, int8, int16, int32, uint, uint8, uint16, uint32:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		if b, ok := v.([16]byte); ok {
			return base64.StdEncoding.EncodeToString(b[:])
		}

		// Arrays, maps and tuples (ClickHouse) are shown as JSON.
		if data, err := json.Marshal(x); err == nil {
			return string(data)
		}

		return fmt.Sprintf("%v", x)
	}
}
