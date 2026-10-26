//go:build duckdb

package engine

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/url"
	"strings"

	"github.com/duckdb/duckdb-go/v2"
)

func init() {
	openDuckDB = func(path string, readOnly bool) (*sql.DB, error) {
		q := url.Values{}
		q.Set("enable_external_access", "false")
		q.Set("autoinstall_known_extensions", "false")
		q.Set("autoload_known_extensions", "false")

		if readOnly {
			q.Set("access_mode", "read_only")
		}

		connector, err := duckdb.NewConnector(path+"?"+q.Encode(), func(ex driver.ExecerContext) error {
			// Locked settings cannot be changed back by a statement. The
			// lock is per database, so later connections find it set.
			_, err := ex.ExecContext(context.Background(), "SET lock_configuration = true", nil)
			if err != nil && strings.Contains(err.Error(), "configuration has been locked") {
				return nil
			}

			return err
		})
		if err != nil {
			return nil, err
		}

		return sql.OpenDB(connector), nil
	}
}
