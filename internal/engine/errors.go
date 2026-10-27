package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"

	"github.com/rakunlabs/dataport/internal/store"
)

var (
	ErrUnsupported = errors.New("unsupported database type")
	ErrReadOnly    = errors.New("this connection is read-only")
)

// ConnError wraps a failure to reach the target database. Its text can name
// the host, so only admins see it. Auth is set when the database refused the
// username or password, and UserCredentials when that was the user's own
// account rather than the shared one.
type ConnError struct {
	Err             error
	Auth            bool
	UserCredentials bool
}

func (e *ConnError) Error() string { return e.Err.Error() }
func (e *ConnError) Unwrap() error { return e.Err }

// openErr reports a failure to open a pool. A driver missing from the build
// is a setup problem the user may read; anything else may name the host.
func openErr(c *store.Connection, err error) error {
	if errors.Is(err, ErrUnsupported) || errors.Is(err, ErrFilesDisabled) || errors.Is(err, ErrFilePath) {
		return err
	}

	return connErrFor(c, "open "+c.DBType+"; %w", err)
}

func connErr(format string, err error) error {
	return &ConnError{Err: fmt.Errorf(format, err), Auth: isAuthError(err)}
}

func connErrFor(c *store.Connection, format string, err error) error {
	return &ConnError{Err: fmt.Errorf(format, err), Auth: isAuthError(err), UserCredentials: c.UserCredentials}
}

// isAuthError reports whether the database refused the account, as opposed
// to being unreachable.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// invalid_password, invalid_authorization_specification
		return pgErr.Code == "28P01" || pgErr.Code == "28000"
	}

	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		// ER_ACCESS_DENIED_ERROR, ER_DBACCESS_DENIED_ERROR, ER_ACCESS_DENIED_NO_PASSWORD_ERROR
		return myErr.Number == 1045 || myErr.Number == 1044 || myErr.Number == 1698
	}

	var msErr mssql.Error
	if errors.As(err, &msErr) {
		// Login failed for user.
		return msErr.Number == 18456
	}

	var chErr *clickhouse.Exception
	if errors.As(err, &chErr) {
		// UNKNOWN_USER, WRONG_PASSWORD, REQUIRED_PASSWORD, AUTHENTICATION_FAILED
		switch chErr.Code {
		case 192, 193, 194, 516:
			return true
		}

		return false
	}

	if auth, ok := oracleAuthError(err); ok {
		return auth
	}

	// Some drivers flatten the error during the handshake.
	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "login failed for user") || strings.Contains(msg, "authentication failed") ||
		strings.Contains(msg, "401 unauthorized") || strings.Contains(msg, "invalid authorization specification") ||
		strings.Contains(msg, "{28000}")
}
