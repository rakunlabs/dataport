package engine

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/rakunlabs/dataport/internal/store"
)

func init() {
	register(clickhouseDriver{})
}

// clickhouseDriver uses the native protocol.
//
// ClickHouse has no transactions, so every statement takes effect on its
// own. Read-only runs are enforced by the server instead: the session runs
// with readonly=2, which refuses every write and DDL while still allowing
// query settings. The statement keyword check is kept as a first line.
type clickhouseDriver struct{}

func (clickhouseDriver) Type() string      { return "clickhouse" }
func (clickhouseDriver) Dialect() Dialect  { return DialectClickHouse }
func (clickhouseDriver) DefaultPort() int  { return 9000 }
func (clickhouseDriver) NonTransactional() {}

func (clickhouseDriver) Open(c *store.Connection, password string) (*sql.DB, error) {
	port := c.Port
	if port == 0 {
		port = 9000
	}

	opts := &clickhouse.Options{
		Addr: []string{net.JoinHostPort(c.Host, strconv.Itoa(port))},
		Auth: clickhouse.Auth{
			Database: c.Database,
			Username: c.Username,
			Password: password,
		},
		DialTimeout: 10 * time.Second,
		ClientInfo: clickhouse.ClientInfo{
			Products: []struct{ Name, Version string }{{Name: "dataport", Version: "1"}},
		},
	}

	switch optionOr(c.Options, "secure", "false") {
	case "true":
		opts.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	case "skip-verify":
		opts.TLS = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true} //nolint:gosec // admin opt-in
	}

	return clickhouse.OpenDB(opts), nil
}

// BeginTx returns the driver's pass-through transaction. The read-only flag
// travels in the context, see PrepareContext.
func (clickhouseDriver) BeginTx(ctx context.Context, conn *sql.Conn, _ bool) (*sql.Tx, error) {
	return conn.BeginTx(ctx, nil)
}

// BackendID returns nothing: queries are cancelled with the query id that
// the context carries, see Kill.
func (clickhouseDriver) BackendID(context.Context, *sql.Conn) (string, error) {
	return "", nil
}

// Kill has nothing to do: cancelling the context makes the native protocol
// send a cancel packet for the running query.
func (clickhouseDriver) Kill(context.Context, *sql.DB, string) error {
	return nil
}

func (clickhouseDriver) Prepare(context.Context, *sql.Tx, time.Duration) error {
	return nil
}

func (clickhouseDriver) PrepareContext(ctx context.Context, timeout time.Duration, readOnly bool) context.Context {
	s := clickhouse.Settings{}
	if readOnly {
		s["readonly"] = 2
	}

	if timeout > 0 {
		s["max_execution_time"] = int(timeout.Seconds()) + 1
	}

	return clickhouse.Context(ctx, clickhouse.WithSettings(s))
}

func (clickhouseDriver) Query(ctx context.Context, tx *sql.Tx, stmt string, maxRows int) (*ResultSet, error) {
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanRows(rows, maxRows)
}

func (clickhouseDriver) CheckReadOnly(string) error {
	return nil
}

func (clickhouseDriver) Reset(context.Context, *sql.Conn) error {
	return nil
}

func (clickhouseDriver) QuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(strings.ReplaceAll(name, `\`, `\\`), "`", "\\`") + "`"
}

func (clickhouseDriver) Schemas(ctx context.Context, db Querier, c *store.Connection) ([]string, error) {
	if c.Database != "" {
		return []string{c.Database}, nil
	}

	rows, err := db.QueryContext(ctx, `SELECT name FROM system.databases
WHERE name NOT IN ('system', 'INFORMATION_SCHEMA', 'information_schema') ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanStrings(rows)
}

func (clickhouseDriver) Objects(ctx context.Context, db Querier, schema string) ([]Object, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, if(engine IN ('View', 'MaterializedView'),
  if(engine = 'View', 'view', 'materialized_view'), 'table')
FROM system.tables WHERE database = ? AND NOT is_temporary ORDER BY name`, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanObjects(rows)
}

func (clickhouseDriver) Columns(ctx context.Context, db Querier, schema, table string) ([]ColumnInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, type, startsWith(type, 'Nullable('), default_expression, toBool(is_in_primary_key)
FROM system.columns WHERE database = ? AND table = ? ORDER BY position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list, err := scanColumns(rows)
	if err != nil {
		return nil, fmt.Errorf("columns; %w", err)
	}

	return list, nil
}

// ////////////////////////////////////////////
// structure

// Indexes lists the primary key and the data skipping indexes. Unique
// indexes do not exist in ClickHouse.
func (clickhouseDriver) Indexes(ctx context.Context, db Querier, schema, table string) ([]IndexInfo, error) {
	// UNION ALL has no order in ClickHouse; the primary key goes first.
	rows, err := db.QueryContext(ctx, `SELECT ix_name, ix_unique, ix_primary, ix_method, ix_cols FROM (
SELECT 'PRIMARY KEY' AS ix_name, toBool(0) AS ix_unique, toBool(1) AS ix_primary, '' AS ix_method,
  replaceAll(primary_key, ', ', char(31)) AS ix_cols
FROM system.tables WHERE database = ? AND name = ? AND primary_key != ''
UNION ALL
SELECT name, toBool(0), toBool(0), type, expr FROM system.data_skipping_indices WHERE database = ? AND table = ?)
ORDER BY ix_primary DESC, ix_name`,
		schema, table, schema, table)
	if err != nil {
		return nil, err
	}

	return scanIndexes(rows)
}

func (clickhouseDriver) ForeignKeys(context.Context, Querier, string, string) ([]ForeignKey, error) {
	return []ForeignKey{}, nil
}

func (d clickhouseDriver) DDL(ctx context.Context, db Querier, schema, table string, _ *TableStructure) (string, error) {
	ddl, err := queryString(ctx, db, "SHOW CREATE TABLE "+d.QuoteIdent(schema)+"."+d.QuoteIdent(table))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(ddl) + "\n", nil
}
