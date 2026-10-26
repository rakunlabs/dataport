package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/trinodb/trino-go-client/trino"

	"github.com/rakunlabs/dataport/internal/store"
)

func init() {
	register(trinoDriver{})
}

// trinoDriver talks to Trino and Presto over HTTP. The database field is the
// catalog; schemas are listed from it.
//
// A read-only run is a READ ONLY transaction, which Trino enforces. Writes
// run without a transaction: most connectors (Hive, Iceberg, Delta) refuse
// writes inside an explicit one, so each statement is applied on its own.
type trinoDriver struct{}

func (trinoDriver) Type() string      { return "trino" }
func (trinoDriver) Dialect() Dialect  { return DialectStandard }
func (trinoDriver) DefaultPort() int  { return 8080 }
func (trinoDriver) NonTransactional() {}

func (trinoDriver) UseTransaction(readOnly bool) bool { return readOnly }

func (trinoDriver) CheckConnection(c *store.Connection) error {
	if c.Username == "" && !c.UserCredentials {
		return fmt.Errorf("username is required")
	}

	if strings.TrimSpace(c.Database) == "" {
		return fmt.Errorf("catalog is required")
	}

	return nil
}

func (trinoDriver) Open(c *store.Connection, password string) (*sql.DB, error) {
	port := c.Port
	if port == 0 {
		port = 8080
	}

	scheme := "http"

	ssl := optionOr(c.Options, "ssl", "false")
	if ssl != "false" {
		scheme = "https"
	}

	u := url.URL{Scheme: scheme, Host: c.Host + ":" + strconv.Itoa(port)}
	if password != "" {
		u.User = url.UserPassword(c.Username, password)
	} else {
		u.User = url.User(c.Username)
	}

	cfg := &trino.Config{
		ServerURI: u.String(),
		Source:    "dataport",
		Catalog:   c.Database,
	}

	if ssl == "skip-verify" {
		cfg.SSLVerification = "NONE"
	}

	connector, err := trino.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("trino config; %w", err)
	}

	return sql.OpenDB(connector), nil
}

func (trinoDriver) BackendID(context.Context, *sql.Conn) (string, error) { return "", nil }

// Kill has nothing to do: the client cancels the query when the context ends.
func (trinoDriver) Kill(context.Context, *sql.DB, string) error { return nil }

func (trinoDriver) BeginTx(ctx context.Context, conn *sql.Conn, readOnly bool) (*sql.Tx, error) {
	return conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
}

func (trinoDriver) Prepare(context.Context, Runner, time.Duration) error { return nil }

func (trinoDriver) Query(ctx context.Context, tx Runner, stmt string, maxRows int) (*ResultSet, error) {
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanRows(rows, maxRows)
}

func (trinoDriver) CheckReadOnly(string) error { return nil }

// Reset has nothing to clear: the driver resets the session headers when a
// pooled connection is reused.
func (trinoDriver) Reset(context.Context, *sql.Conn) error { return nil }

func (trinoDriver) QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

var trinoCatalog = infoSchema{ph: questionMark, noKeys: true, exclude: []string{"information_schema"}}

func (trinoDriver) Schemas(ctx context.Context, db Querier, _ *store.Connection) ([]string, error) {
	return trinoCatalog.Schemas(ctx, db)
}

func (trinoDriver) Objects(ctx context.Context, db Querier, schema string) ([]Object, error) {
	return trinoCatalog.Objects(ctx, db, schema)
}

func (trinoDriver) Columns(ctx context.Context, db Querier, schema, table string) ([]ColumnInfo, error) {
	return trinoCatalog.Columns(ctx, db, schema, table)
}

// ////////////////////////////////////////////
// structure

func (trinoDriver) Indexes(context.Context, Querier, string, string) ([]IndexInfo, error) {
	return []IndexInfo{}, nil
}

func (trinoDriver) ForeignKeys(context.Context, Querier, string, string) ([]ForeignKey, error) {
	return []ForeignKey{}, nil
}

func (d trinoDriver) DDL(ctx context.Context, db Querier, schema, table string, _ *TableStructure) (string, error) {
	qualified := d.QuoteIdent(schema) + "." + d.QuoteIdent(table)

	kind, err := queryString(ctx, db, `SELECT table_type FROM information_schema.tables WHERE table_schema = ? AND table_name = ?`, schema, table)
	if err != nil {
		return "", err
	}

	stmt := "SHOW CREATE TABLE "
	if kind == "VIEW" {
		stmt = "SHOW CREATE VIEW "
	}

	ddl, err := queryString(ctx, db, stmt+qualified)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(ddl) + ";\n", nil
}
