// Package engine runs SQL against the target databases the admin defined.
//
// Every request runs on one pooled connection inside one transaction. A
// read-only grant gets a read-only transaction and only statements that read;
// the database privileges of the configured account remain the real limit.
package engine

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

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
	// Args belong to exactly one SQL statement; notebook templates bind values.
	Args     []any
	QueryID  string
	UserID   string
	SQL      string
	ReadOnly bool
	MaxRows  int
	Timeout  time.Duration
	// TxID keeps a transaction open over several runs, until EndTx; empty
	// runs in a transaction of its own. TxContinue says the client expects
	// it open already, so a lost one is reported instead of replaced.
	TxID       string
	TxContinue bool
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
	// TxOpen is set when the run went into a manual transaction that stays
	// open, with its statements so far and its start in Unix milliseconds.
	TxOpen       bool  `json:"tx_open,omitempty"`
	TxStatements int   `json:"tx_statements,omitempty"`
	TxStartedAt  int64 `json:"tx_started_at,omitempty"`
	// TxRolledBack is set when a stopped run rolled the manual transaction
	// back.
	TxRolledBack bool `json:"tx_rolled_back,omitempty"`
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

type Manager struct {
	mu       sync.Mutex
	pools    map[string]*pool
	defaults store.Limits
	idle     time.Duration

	runMu sync.Mutex
	// running holds the cancel functions of tracked requests by user and
	// query id; a reused id cancels every request that carries it.
	running map[runKey][]*running

	// txMu guards the manual transactions, by user and tab.
	txMu sync.Mutex
	txs  map[txKey]*openTx
}

type runKey struct{ userID, queryID string }

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
		running:  map[runKey][]*running{},
		txs:      map[txKey]*openTx{},
	}
}

// Limits returns the effective pool settings of a connection.
func (m *Manager) Limits(c *store.Connection) store.Limits {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.defaults.Merge(c.Limits)
}

// SetDefaults changes the pool defaults and the idle timeout while running;
// open pools pick up new limits on their next use.
func (m *Manager) SetDefaults(defaults store.Limits, idle time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.defaults = DefaultLimits.Merge(defaults)
	m.idle = idle
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
		return connErrFor(c, "ping; %w", err)
	}

	return nil
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
		return connErrFor(c, "connect; %w", err)
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
