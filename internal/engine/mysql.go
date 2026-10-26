package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/rakunlabs/dataport/internal/store"
)

func init() {
	register(mysqlDriver{kind: "mysql"})
	register(mysqlDriver{kind: "mariadb"})
}

type mysqlDriver struct {
	kind string
}

func (d mysqlDriver) Type() string   { return d.kind }
func (mysqlDriver) Dialect() Dialect { return DialectMySQL }
func (mysqlDriver) DefaultPort() int { return 3306 }

func (d mysqlDriver) Open(c *store.Connection, password string) (*sql.DB, error) {
	port := c.Port
	if port == 0 {
		port = 3306
	}

	cfg := mysql.NewConfig()
	cfg.User = c.Username
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(port))
	cfg.DBName = c.Database
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Timeout = 10 * time.Second
	cfg.Collation = "utf8mb4_general_ci"
	// One statement per call; see the note on the PostgreSQL exec mode.
	cfg.MultiStatements = false
	cfg.AllowNativePasswords = true

	switch tls := optionOr(c.Options, "tls", "preferred"); tls {
	case "true", "false", "skip-verify", "preferred":
		cfg.TLSConfig = tls
	default:
		return nil, fmt.Errorf("unknown tls option %q", tls)
	}

	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}

	return sql.OpenDB(connector), nil
}

func (mysqlDriver) BackendID(ctx context.Context, conn *sql.Conn) (string, error) {
	var id uint64
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id); err != nil {
		return "", err
	}

	return strconv.FormatUint(id, 10), nil
}

func (mysqlDriver) Kill(ctx context.Context, db *sql.DB, backendID string) error {
	id, err := strconv.ParseUint(backendID, 10, 64)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, "KILL QUERY "+strconv.FormatUint(id, 10))

	return err
}

func (d mysqlDriver) Prepare(ctx context.Context, tx Runner, timeout time.Duration) error {
	ms := timeout.Milliseconds()
	if ms <= 0 {
		return nil
	}

	variable := "max_execution_time"
	value := strconv.FormatInt(ms, 10)

	if d.kind == "mariadb" {
		variable = "max_statement_time"
		value = strconv.FormatFloat(timeout.Seconds(), 'f', 3, 64)
	}

	// Best effort: older servers do not know the variable, and the context
	// timeout with KILL QUERY still applies.
	_, _ = tx.ExecContext(ctx, "SET SESSION "+variable+" = "+value)

	return nil
}

func (mysqlDriver) Query(ctx context.Context, tx Runner, stmt string, maxRows int) (*ResultSet, error) {
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanRows(rows, maxRows)
}

// CheckReadOnly rejects what a READ ONLY transaction in MySQL does not stop:
// writing files on the server, and versioned comments (/*! ... */), which
// MySQL executes although they look like comments to the keyword check.
func (mysqlDriver) CheckReadOnly(stmt string) error {
	upper := strings.ToUpper(stmt)

	if strings.Contains(upper, "/*!") || strings.Contains(upper, "/*M!") {
		return fmt.Errorf("%w: executable comments are not allowed", ErrReadOnly)
	}

	if containsWord(upper, "OUTFILE") || containsWord(upper, "DUMPFILE") {
		return fmt.Errorf("%w: writing files is not allowed", ErrReadOnly)
	}

	return nil
}

func (mysqlDriver) QuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

var errNoReset = errors.New("mysql sessions are not reused")

// Reset always drops the connection. MySQL has no statement that clears user
// variables, session variables and temporary tables at once without a
// protocol-level reset, which the driver does not expose.
func (mysqlDriver) Reset(context.Context, *sql.Conn) error {
	return errNoReset
}

// Schemas lists the configured database only when one is set, so the admin
// can pin a connection to it.
func (mysqlDriver) Schemas(ctx context.Context, db Querier, c *store.Connection) ([]string, error) {
	if c.Database != "" {
		return []string{c.Database}, nil
	}

	rows, err := db.QueryContext(ctx, `SELECT SCHEMA_NAME FROM information_schema.SCHEMATA
WHERE SCHEMA_NAME NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
ORDER BY SCHEMA_NAME`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanStrings(rows)
}

func (mysqlDriver) Objects(ctx context.Context, db Querier, schema string) ([]Object, error) {
	rows, err := db.QueryContext(ctx, `SELECT TABLE_NAME, CASE TABLE_TYPE WHEN 'VIEW' THEN 'view' ELSE 'table' END
FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanObjects(rows)
}

func (mysqlDriver) Columns(ctx context.Context, db Querier, schema, table string) ([]ColumnInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE = 'YES', COALESCE(COLUMN_DEFAULT, ''), COLUMN_KEY = 'PRI'
FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanColumns(rows)
}

// ////////////////////////////////////////////
// structure

func (mysqlDriver) Indexes(ctx context.Context, db Querier, schema, table string) ([]IndexInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT INDEX_NAME, MAX(NON_UNIQUE) = 0, INDEX_NAME = 'PRIMARY', MAX(INDEX_TYPE),
COALESCE(GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX SEPARATOR '`+listSep+`'), '')
FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
GROUP BY INDEX_NAME ORDER BY INDEX_NAME = 'PRIMARY' DESC, INDEX_NAME`, schema, table)
	if err != nil {
		return nil, err
	}

	return scanIndexes(rows)
}

func (mysqlDriver) ForeignKeys(ctx context.Context, db Querier, schema, table string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT k.CONSTRAINT_NAME, k.REFERENCED_TABLE_SCHEMA, k.REFERENCED_TABLE_NAME,
GROUP_CONCAT(k.COLUMN_NAME ORDER BY k.ORDINAL_POSITION SEPARATOR '`+listSep+`'),
GROUP_CONCAT(k.REFERENCED_COLUMN_NAME ORDER BY k.ORDINAL_POSITION SEPARATOR '`+listSep+`'),
r.UPDATE_RULE, r.DELETE_RULE
FROM information_schema.KEY_COLUMN_USAGE k
JOIN information_schema.REFERENTIAL_CONSTRAINTS r
  ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME AND r.TABLE_NAME = k.TABLE_NAME
WHERE k.TABLE_SCHEMA = ? AND k.TABLE_NAME = ? AND k.REFERENCED_TABLE_NAME IS NOT NULL
GROUP BY k.CONSTRAINT_NAME, k.REFERENCED_TABLE_SCHEMA, k.REFERENCED_TABLE_NAME, r.UPDATE_RULE, r.DELETE_RULE
ORDER BY k.CONSTRAINT_NAME`, schema, table)
	if err != nil {
		return nil, err
	}

	return scanForeignKeys(rows)
}

// DDL uses SHOW CREATE, which returns two columns for tables and four for
// views; the statement is always the second.
func (d mysqlDriver) DDL(ctx context.Context, db Querier, schema, table string, _ *TableStructure) (string, error) {
	rows, err := db.QueryContext(ctx, "SHOW CREATE TABLE "+d.QuoteIdent(schema)+"."+d.QuoteIdent(table))
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}

	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))

	for i := range vals {
		ptrs[i] = &vals[i]
	}

	if rows.Next() && len(cols) > 1 {
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}

		return vals[1].String + ";\n", rows.Err()
	}

	return "", rows.Err()
}
