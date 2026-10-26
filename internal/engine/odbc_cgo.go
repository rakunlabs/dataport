//go:build odbc

package engine

import (
	"database/sql"

	// The ODBC driver needs cgo and unixODBC, so it is only part of builds
	// made with the odbc tag. It registers itself as "odbc".
	_ "github.com/alexbrainman/odbc"
)

func init() {
	openODBC = func(connString string) (*sql.DB, error) {
		return sql.Open("odbc", connString)
	}
}
