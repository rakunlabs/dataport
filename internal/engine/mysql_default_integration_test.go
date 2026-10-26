//go:build integration

package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Columns gives MySQL defaults as SQL and keeps AUTO_INCREMENT, so a column
// restated with MODIFY COLUMN loses nothing.
func TestIntegrationMySQLColumnDefaults(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "myd", DBType: "mysql", Host: "127.0.0.1", Port: 53306, Database: "app", Username: "root", UpdatedAt: 1}
	if err := m.Test(context.Background(), c, "pw"); err != nil {
		t.Skip("mysql is not reachable:", err)
	}

	run(t, m, c, "pw", "DROP TABLE IF EXISTS dp_d; CREATE TABLE dp_d (id int AUTO_INCREMENT PRIMARY KEY, s varchar(10) DEFAULT 'x''y\\\\z', "+
		"n int DEFAULT 5, ts timestamp DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, e varchar(20) DEFAULT (concat('a','b')), "+
		"b bit(1) DEFAULT b'1', nn varchar(5) DEFAULT NULL, emp varchar(5) DEFAULT '')", false)

	st, err := m.Structure(context.Background(), c, "pw", "app", "dp_d")
	if err != nil {
		t.Fatal(err)
	}

	var stmts []string

	for _, col := range st.Columns {
		def := "`" + col.Name + "` " + col.Type
		if !col.Nullable {
			def += " NOT NULL"
		}

		if col.Default != "" {
			def += " DEFAULT " + col.Default
		}

		if col.Extra != "" {
			def += " " + col.Extra
		}

		stmts = append(stmts, "ALTER TABLE dp_d MODIFY COLUMN "+def)
	}

	if e := run(t, m, c, "pw", strings.Join(stmts, ";\n"), false).FirstError(); e != "" {
		t.Fatalf("%s\n%s", e, strings.Join(stmts, "\n"))
	}

	after, err := m.Structure(context.Background(), c, "pw", "app", "dp_d")
	if err != nil {
		t.Fatal(err)
	}

	for i, col := range after.Columns {
		if col != st.Columns[i] {
			t.Errorf("column changed: %+v -> %+v", st.Columns[i], col)
		}
	}

	t.Log(strings.Join(stmts, "\n"))
}
