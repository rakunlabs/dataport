package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Sessions of the server, as DBX's process list shows them. Each driver
// lists them with its own catalog; the first column is the id a kill names.

// ProcessLister is implemented by drivers that can list and stop sessions.
type ProcessLister interface {
	ProcessQuery() string
	// KillSQL returns the statement that stops a session; terminate ends
	// the session, otherwise only its running statement is cancelled.
	KillSQL(id string, terminate bool) (string, error)
}

// ProcessList is the server's sessions; Self is the id of the session that
// read them, which belongs to dataport.
type ProcessList struct {
	Result *ResultSet `json:"result"`
	Self   string     `json:"self"`
	// CanCancel is false when the database can only end whole sessions.
	CanCancel bool `json:"can_cancel"`
}

var (
	ErrNoProcesses = errors.New("the process list is not available for this database")
	errProcessID   = errors.New("invalid session id")
)

const maxProcesses = 2000

func (m *Manager) Processes(ctx context.Context, c *store.Connection, password string) (*ProcessList, error) {
	out := &ProcessList{}

	err := m.meta(ctx, c, password, func(ctx context.Context, d Driver, conn *sql.Conn) error {
		p, ok := d.(ProcessLister)
		if !ok {
			return ErrNoProcesses
		}

		if id, err := d.BackendID(ctx, conn); err == nil {
			out.Self = id
		}

		_, canCancel := p.KillSQL("1", false)
		out.CanCancel = canCancel == nil

		rs, err := d.Query(ctx, conn, p.ProcessQuery(), maxProcesses)
		if err != nil {
			return err
		}

		out.Result = rs

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// KillProcess stops a session or its running statement, outside any
// transaction. It returns the statement it ran, for the query log.
func (m *Manager) KillProcess(ctx context.Context, c *store.Connection, password, id string, terminate bool) (string, error) {
	d, err := DriverFor(c)
	if err != nil {
		return "", err
	}

	p, ok := d.(ProcessLister)
	if !ok {
		return "", ErrNoProcesses
	}

	stmt, err := p.KillSQL(id, terminate)
	if err != nil {
		return "", err
	}

	pl, err := m.pool(c, password)
	if err != nil {
		return stmt, err
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	conn, err := pl.db.Conn(ctx)
	if err != nil {
		return stmt, connErr("connect; %w", err)
	}

	defer release(d, conn, false)

	if _, err := conn.ExecContext(ctx, stmt); err != nil {
		return stmt, errors.New(cleanError(ctx, err))
	}

	return stmt, nil
}

func intID(id string) (int64, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return 0, errProcessID
	}

	return n, nil
}

var queryIDRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

func (postgresDriver) ProcessQuery() string {
	return `SELECT pid AS id,
       usename AS "user",
       datname AS db,
       coalesce(host(client_addr), client_hostname, 'local') AS client,
       application_name AS app,
       state,
       coalesce(nullif(wait_event_type, '') || ':' || wait_event, wait_event_type, '') AS wait,
       greatest(0, floor(extract(epoch FROM (clock_timestamp() - coalesce(query_start, xact_start, backend_start)))))::bigint AS time,
       query
FROM pg_stat_activity
WHERE backend_type IS NULL OR backend_type = 'client backend'
ORDER BY time DESC NULLS LAST`
}

func (postgresDriver) KillSQL(id string, terminate bool) (string, error) {
	n, err := intID(id)
	if err != nil {
		return "", err
	}

	if terminate {
		return fmt.Sprintf("SELECT pg_terminate_backend(%d)", n), nil
	}

	return fmt.Sprintf("SELECT pg_cancel_backend(%d)", n), nil
}

func (mysqlDriver) ProcessQuery() string {
	return "SELECT ID AS id, USER AS `user`, HOST AS client, DB AS db, COMMAND AS command, TIME AS time, STATE AS state, INFO AS query " +
		"FROM information_schema.PROCESSLIST ORDER BY TIME DESC"
}

func (mysqlDriver) KillSQL(id string, terminate bool) (string, error) {
	n, err := intID(id)
	if err != nil {
		return "", err
	}

	if terminate {
		return fmt.Sprintf("KILL CONNECTION %d", n), nil
	}

	return fmt.Sprintf("KILL QUERY %d", n), nil
}

func (mssqlDriver) ProcessQuery() string {
	return `SELECT s.session_id AS id,
       s.login_name AS [user],
       s.host_name AS client,
       DB_NAME(s.database_id) AS db,
       s.program_name AS app,
       COALESCE(r.status, s.status) AS state,
       r.command AS command,
       r.wait_type AS wait,
       DATEDIFF(SECOND, COALESCE(r.start_time, s.last_request_start_time), GETDATE()) AS time,
       t.text AS query
FROM sys.dm_exec_sessions s
LEFT JOIN sys.dm_exec_requests r ON r.session_id = s.session_id
OUTER APPLY sys.dm_exec_sql_text(r.sql_handle) t
WHERE s.is_user_process = 1
ORDER BY time DESC`
}

// KillSQL ends the whole session: SQL Server cannot cancel another
// session's statement alone.
func (mssqlDriver) KillSQL(id string, terminate bool) (string, error) {
	if !terminate {
		return "", errors.New("sql server can only end the whole session")
	}

	n, err := intID(id)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("KILL %d", n), nil
}

func (clickhouseDriver) ProcessQuery() string {
	return `SELECT query_id AS id, user, address AS client, current_database AS db,
       round(elapsed) AS time, read_rows, formatReadableSize(memory_usage) AS memory, query
FROM system.processes ORDER BY elapsed DESC`
}

// KillSQL stops a query; ClickHouse sessions hold nothing else to end.
func (clickhouseDriver) KillSQL(id string, terminate bool) (string, error) {
	if !terminate {
		return "", errors.New("clickhouse stops whole queries")
	}

	if !queryIDRe.MatchString(id) {
		return "", errProcessID
	}

	return "KILL QUERY WHERE query_id = '" + id + "'", nil
}

func (oracleDriver) ProcessQuery() string {
	return `SELECT s.sid || ',' || s.serial# AS id, s.username AS "user", s.machine AS client,
       s.program AS app, s.status AS state, s.event AS wait, s.last_call_et AS time, q.sql_text AS query
FROM v$session s
LEFT JOIN v$sql q ON q.sql_id = s.sql_id AND q.child_number = s.sql_child_number
WHERE s.type = 'USER'
ORDER BY s.last_call_et DESC`
}

var oracleSessionRe = regexp.MustCompile(`^\d{1,10},\d{1,10}$`)

func (oracleDriver) KillSQL(id string, terminate bool) (string, error) {
	if !oracleSessionRe.MatchString(id) {
		return "", errProcessID
	}

	if terminate {
		return "ALTER SYSTEM KILL SESSION '" + id + "' IMMEDIATE", nil
	}

	return "ALTER SYSTEM CANCEL SQL '" + id + "'", nil
}

func (trinoDriver) ProcessQuery() string {
	return `SELECT query_id AS id, "user", source AS app, state,
       date_diff('second', created, now()) AS time, query
FROM system.runtime.queries WHERE state NOT IN ('FINISHED', 'FAILED') ORDER BY created`
}

func (trinoDriver) KillSQL(id string, terminate bool) (string, error) {
	if !terminate {
		return "", errors.New("trino stops whole queries")
	}

	if !queryIDRe.MatchString(id) {
		return "", errProcessID
	}

	return "CALL system.runtime.kill_query(query_id => '" + id + "', message => 'stopped from dataport')", nil
}

// Disconnect releases what dataport holds open on a connection. A pool used
// by one user closes; a shared pool lets go of its idle sessions, so other
// users' running statements are not cut off.
func (m *Manager) Disconnect(c *store.Connection) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := poolKey(c)

	p, ok := m.pools[key]
	if !ok {
		return
	}

	if c.Owner != "" {
		_ = p.db.Close()
		delete(m.pools, key)

		return
	}

	p.db.SetMaxIdleConns(0)
	p.db.SetMaxIdleConns(min(p.limits.MaxIdle, p.limits.MaxOpen))
}

// Connected reports whether dataport holds a pool for the connection.
func (m *Manager) Connected(c *store.Connection) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.pools[poolKey(c)]

	return ok
}
