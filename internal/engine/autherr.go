package engine

import (
	"errors"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
)

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

	// Some drivers flatten the error during the handshake.
	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "login failed for user") || strings.Contains(msg, "authentication failed")
}
