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
	Prepare(ctx context.Context, tx *sql.Tx, timeout time.Duration) error
	// Query runs a statement that returns rows and reads at most maxRows+1 of
	// them, so the caller can tell whether the result was truncated.
	Query(ctx context.Context, tx *sql.Tx, stmt string, maxRows int) (*ResultSet, error)
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

type DriverInfo struct {
	Type        string `json:"type"`
	DefaultPort int    `json:"default_port"`
}

func Drivers() []DriverInfo {
	list := make([]DriverInfo, 0, len(drivers))
	for _, d := range drivers {
		list = append(list, DriverInfo{Type: d.Type(), DefaultPort: d.DefaultPort()})
	}

	return list
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

	d, err := GetDriver(c.DBType)
	if err != nil {
		return nil, err
	}

	db, err := d.Open(c, password)
	if err != nil {
		return nil, connErr("open "+c.DBType+"; %w", err)
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
	d, err := GetDriver(c.DBType)
	if err != nil {
		return err
	}

	db, err := d.Open(c, password)
	if err != nil {
		return connErr("open; %w", err)
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

// Validate splits the script and checks it against the grant without
// running anything.
func Validate(d Driver, script string, readOnly bool) ([]string, error) {
	stmts := Split(script, d.Dialect())
	if len(stmts) == 0 {
		return nil, errors.New("no statement to run")
	}

	for _, stmt := range stmts {
		kw := FirstKeyword(stmt, d.Dialect())

		if transactionKeywords[kw] {
			return nil, fmt.Errorf("%s is not allowed; each run is a single transaction managed by dataport", kw)
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

func orUnknown(kw string) string {
	if kw == "" {
		return "these"
	}

	return kw
}

func (m *Manager) Execute(ctx context.Context, c *store.Connection, password string, req ExecRequest) (*ExecResult, error) {
	d, err := GetDriver(c.DBType)
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

	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
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

	tx, err := beginTx(ctx, d, conn, req.ReadOnly)
	if err != nil {
		return nil, fmt.Errorf("begin; %w", err)
	}

	if err := d.Prepare(ctx, tx, req.Timeout); err != nil {
		_ = tx.Rollback()

		return nil, fmt.Errorf("prepare session; %w", err)
	}

	result := &ExecResult{Results: make([]*ResultSet, 0, len(stmts)), Transactional: transactional(d)}
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

func (m *Manager) runStatement(ctx context.Context, d Driver, tx *sql.Tx, stmt string, maxRows int) *ResultSet {
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
		name := strings.ToLower(t.DatabaseTypeName())
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
			if convert != nil {
				if out, ok := convert(rs.Columns[i].Type, v); ok {
					row[i] = out

					continue
				}
			}

			if b, ok := v.([]byte); ok && binary[i] {
				row[i] = "\\x" + hex.EncodeToString(b)

				continue
			}

			row[i] = jsonValue(v)
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
