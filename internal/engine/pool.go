package engine

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

type pool struct {
	db       *sql.DB
	driver   Driver
	version  int64
	limits   store.Limits
	lastUsed time.Time
}

// PoolKey names the pool a connection is served from.
func PoolKey(c *store.Connection) string { return poolKey(c) }

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

	limits := m.defaults.Merge(c.Limits)

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
	m.rollbackTxs(func(o *openTx) bool { return o.connID == id })

	m.mu.Lock()
	defer m.mu.Unlock()

	var closed []string

	for key, p := range m.pools {
		if key == id || strings.HasPrefix(key, id+"/") {
			_ = p.db.Close()
			delete(m.pools, key)
			closed = append(closed, key)
		}
	}

	m.closed(closed...)
}

// OnClose sets a function told the keys of pools that closed, such as the
// cluster giving up the ownership of a pool. It runs in a goroutine of its
// own, so it may find the pool open again.
func (m *Manager) OnClose(fn func(keys []string)) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.onClose = fn
}

// closed reports closed pools; m.mu is held.
func (m *Manager) closed(keys ...string) {
	if m.onClose != nil && len(keys) > 0 {
		go m.onClose(keys)
	}
}

// ForgetUser closes the pool a user opened with their own account.
func (m *Manager) ForgetUser(id, userID string) {
	key := id + "/" + userID
	m.rollbackTxs(func(o *openTx) bool { return o.poolKey == key })

	m.mu.Lock()
	defer m.mu.Unlock()

	if p, ok := m.pools[key]; ok {
		_ = p.db.Close()
		delete(m.pools, key)
		m.closed(key)
	}
}

// Release closes one pool by its key, rolling back its open transactions,
// and reports whether it was open.
func (m *Manager) Release(key string) bool {
	m.rollbackTxs(func(o *openTx) bool { return o.poolKey == key })

	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.pools[key]
	if ok {
		_ = p.db.Close()
		delete(m.pools, key)
		m.closed(key)
	}

	return ok
}

// HasPool reports whether the pool of a key is open.
func (m *Manager) HasPool(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.pools[key]

	return ok
}

func (m *Manager) Close() {
	m.txMu.Lock()
	txs := m.txs
	m.txs = map[txKey]*openTx{}
	m.txMu.Unlock()

	for _, o := range txs {
		o.end(false)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, p := range m.pools {
		_ = p.db.Close()
		delete(m.pools, id)
	}
}

// Janitor closes pools that were idle longer than the idle timeout; a zero
// timeout keeps them open.
func (m *Manager) Janitor(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.expireTxs(time.Now())
			m.closeIdle(time.Now())
		}
	}
}

// closeIdle closes the pools unused since the idle timeout. lastUsed is set
// only when a pool is fetched, so a pool with a connection checked out, such
// as one running a long query or export, counts as used now.
func (m *Manager) closeIdle(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.idle <= 0 {
		return
	}

	for id, p := range m.pools {
		if p.db.Stats().InUse > 0 {
			p.lastUsed = now

			continue
		}

		if now.Sub(p.lastUsed) > m.idle {
			_ = p.db.Close()
			delete(m.pools, id)
			m.closed(id)
		}
	}
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
