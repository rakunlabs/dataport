package engine

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Manual transactions: a query tab may keep one transaction open over
// several runs, until the user commits or rolls it back. The transaction
// holds a pooled connection for that long, on this instance only.

var (
	// ErrTxLost means the transaction the client expected is gone: it was
	// rolled back after the idle time, by a stopped run or by a restart.
	ErrTxLost = errors.New("the transaction is no longer open; it was rolled back")
	ErrTxBusy = errors.New("a run is still going in this transaction")
	// ErrTxConnection means the tab moved to another connection while its
	// transaction is open.
	ErrTxConnection = errors.New("the open transaction belongs to another connection; commit or roll it back first")
	ErrTxLimit      = errors.New("too many open transactions on this connection; commit or roll back one in another tab")
)

// TxIdle rolls back a manual transaction unused for this long, so a
// forgotten one does not hold locks and a connection for ever.
const TxIdle = 30 * time.Minute

type txKey struct{ userID, txID string }

type openTx struct {
	// mu is held while a run or the end of the transaction uses it.
	mu      sync.Mutex
	closed  bool
	key     txKey
	connID  string
	poolKey string
	driver  Driver
	db      *sql.DB
	conn    *sql.Conn
	tx      *sql.Tx
	cancel  context.CancelFunc
	backend string

	started    time.Time
	lastUsed   atomic.Int64
	statements int
}

// TxInfo describes an open transaction for its user.
type TxInfo struct {
	TxID         string `json:"tx_id"`
	ConnectionID string `json:"connection_id"`
	StartedAt    int64  `json:"started_at"`
	LastUsedAt   int64  `json:"last_used_at"`
	Statements   int    `json:"statements"`
}

// TxEnd reports how a transaction ended.
type TxEnd struct {
	Committed  bool `json:"committed"`
	Statements int  `json:"statements"`
}

// ManualTx reports whether a database can keep a transaction open over
// several runs.
func ManualTx(dbType string) bool {
	d, err := GetDriver(dbType)

	return err == nil && transactional(d) && useTransaction(d, false)
}

func (m *Manager) txLimit(c *store.Connection) int {
	// One connection stays free for everything else.
	return max(1, m.Limits(c).MaxOpen-1)
}

// txFor returns the open transaction of the request, or starts one.
func (m *Manager) txFor(ctx context.Context, c *store.Connection, password string, d Driver, req ExecRequest) (*openTx, error) {
	key := txKey{req.UserID, req.TxID}
	pk := poolKey(c)

	m.txMu.Lock()
	otx, ok := m.txs[key]

	if ok {
		m.txMu.Unlock()

		if otx.connID != c.ID || otx.poolKey != pk {
			return nil, ErrTxConnection
		}

		return otx, nil
	}

	if req.TxContinue {
		m.txMu.Unlock()

		return nil, ErrTxLost
	}

	count := 0

	for _, o := range m.txs {
		if o.poolKey == pk {
			count++
		}
	}
	m.txMu.Unlock()

	if count >= m.txLimit(c) {
		return nil, ErrTxLimit
	}

	p, err := m.pool(c, password)
	if err != nil {
		return nil, err
	}

	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, connErrFor(c, "connect; %w", err)
	}

	backend, err := d.BackendID(ctx, conn)
	if err != nil {
		_ = conn.Close()

		return nil, connErrFor(c, "session id; %w", err)
	}

	// The transaction outlives the request, so it gets a context of its own:
	// database/sql rolls back a transaction whose context ends.
	txCtx, cancel := context.WithCancel(context.Background())

	tx, err := beginTx(txCtx, d, conn, false)
	if err != nil {
		cancel()
		release(d, conn, true)

		return nil, err
	}

	otx = &openTx{key: key, connID: c.ID, poolKey: pk, driver: d, db: p.db, conn: conn, tx: tx, cancel: cancel, backend: backend, started: time.Now()}
	otx.lastUsed.Store(time.Now().UnixMilli())

	m.txMu.Lock()
	if _, taken := m.txs[key]; taken {
		m.txMu.Unlock()
		otx.end(false)

		return nil, ErrTxBusy
	}

	m.txs[key] = otx
	m.txMu.Unlock()

	return otx, nil
}

// executeTx runs statements in the manual transaction of the request. A
// failed statement leaves the transaction open; a stopped run rolls it back,
// since the database may have dropped it with the statement.
func (m *Manager) executeTx(ctx context.Context, c *store.Connection, password string, d Driver, stmts []string, req ExecRequest) (*ExecResult, error) {
	otx, err := m.txFor(ctx, c, password, d, req)
	if err != nil {
		return nil, err
	}

	if !otx.mu.TryLock() {
		return nil, ErrTxBusy
	}
	defer otx.mu.Unlock()

	if otx.closed {
		return nil, ErrTxLost
	}

	ctx, cancel := withTimeout(ctx, req.Timeout)
	defer cancel()

	defer m.track(req, c, cancel)()

	start := time.Now()

	var (
		killed  atomic.Bool
		watcher sync.WaitGroup
		done    = make(chan struct{})
	)

	watcher.Go(func() {
		select {
		case <-done:
		case <-ctx.Done():
			killed.Store(true)

			killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer killCancel()

			if err := d.Kill(killCtx, otx.db, otx.backend); err != nil {
				slog.Debug("kill query", "connection", c.ID, "error", err)
			}
		}
	})

	result := &ExecResult{Results: make([]*ResultSet, 0, len(stmts)), Transactional: true}

	if err := d.Prepare(ctx, otx.tx, req.Timeout); err != nil {
		result.Results = append(result.Results, &ResultSet{Statement: "-- prepare session", Kind: "exec", Error: cleanError(ctx, err)})
	} else {
		for _, stmt := range stmts {
			rs := m.runStatement(ctx, d, argumentRunner{Runner: otx.tx, args: req.Args}, stmt, req.MaxRows)
			result.Results = append(result.Results, rs)
			otx.statements++

			if rs.Error != "" {
				break
			}
		}
	}

	close(done)
	watcher.Wait()

	otx.lastUsed.Store(time.Now().UnixMilli())
	result.DurationMS = time.Since(start).Milliseconds()

	if killed.Load() || ctx.Err() != nil {
		m.dropTx(otx)
		otx.end(false)

		result.TxRolledBack = true

		return result, nil
	}

	result.TxOpen = true
	result.TxStatements = otx.statements
	result.TxStartedAt = otx.started.UnixMilli()

	return result, nil
}

// EndTx commits or rolls back a manual transaction of the user.
func (m *Manager) EndTx(_ context.Context, userID, txID, connID string, commit bool) (*TxEnd, error) {
	m.txMu.Lock()
	otx, ok := m.txs[txKey{userID, txID}]
	m.txMu.Unlock()

	if !ok || otx.connID != connID {
		return nil, ErrTxLost
	}

	if !otx.mu.TryLock() {
		return nil, ErrTxBusy
	}

	if otx.closed {
		otx.mu.Unlock()

		return nil, ErrTxLost
	}

	m.dropTx(otx)
	out := &TxEnd{Statements: otx.statements}
	err := otx.endLocked(commit)
	otx.mu.Unlock()

	if err != nil {
		return out, err
	}

	out.Committed = commit

	return out, nil
}

// Txs lists the open manual transactions of a user.
func (m *Manager) Txs(_ context.Context, userID string) []TxInfo {
	m.txMu.Lock()
	defer m.txMu.Unlock()

	out := []TxInfo{}

	for k, o := range m.txs {
		if k.userID == userID {
			out = append(out, TxInfo{TxID: k.txID, ConnectionID: o.connID, StartedAt: o.started.UnixMilli(), LastUsedAt: o.lastUsed.Load(), Statements: o.statements})
		}
	}

	return out
}

func (m *Manager) dropTx(otx *openTx) {
	m.txMu.Lock()
	defer m.txMu.Unlock()

	if m.txs[otx.key] == otx {
		delete(m.txs, otx.key)
	}
}

// rollbackTxs rolls back, in the background, the transactions match picks;
// a run going on in one finishes first.
func (m *Manager) rollbackTxs(match func(*openTx) bool) {
	m.txMu.Lock()

	var list []*openTx

	for k, o := range m.txs {
		if match(o) {
			list = append(list, o)
			delete(m.txs, k)
		}
	}
	m.txMu.Unlock()

	for _, o := range list {
		go o.end(false)
	}
}

// expireTxs rolls back transactions unused for the idle time. Busy ones are
// in use, so they are left alone.
func (m *Manager) expireTxs(now time.Time) {
	cutoff := now.Add(-TxIdle).UnixMilli()

	m.rollbackTxs(func(o *openTx) bool {
		if o.lastUsed.Load() > cutoff || !o.mu.TryLock() {
			return false
		}
		o.mu.Unlock()

		slog.Info("rolling back an idle transaction", "connection", o.connID, "user", o.key.userID)

		return true
	})
}

func (o *openTx) end(commit bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.closed {
		_ = o.endLocked(commit)
	}
}

func (o *openTx) endLocked(commit bool) error {
	o.closed = true

	var err error
	if commit {
		err = o.tx.Commit()
	} else {
		err = o.tx.Rollback()
	}

	o.cancel()
	// After a failed end the session's state is unknown.
	release(o.driver, o.conn, err != nil && !errors.Is(err, sql.ErrTxDone))

	return err
}
