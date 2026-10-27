package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

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

	slices.SortFunc(list, func(a, b DriverInfo) int { return strings.Compare(a.Type, b.Type) })

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
