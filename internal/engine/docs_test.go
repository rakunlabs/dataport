package engine

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

func TestSchemaDoc(t *testing.T) {
	root := t.TempDir()

	seed, err := sql.Open("sqlite", filepath.Join(root, "doc.db"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := seed.Exec(`CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT, phone1 TEXT, phone2 TEXT);
CREATE UNIQUE INDEX customers_email ON customers(email);
CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER REFERENCES customers(id), total REAL, note TEXT DEFAULT 'null');
CREATE TABLE order_lines (order_id INTEGER, product_id INTEGER, qty INTEGER);
CREATE INDEX lines_order ON order_lines(order_id);
CREATE INDEX lines_order_again ON order_lines(order_id);
CREATE TABLE products (id INTEGER PRIMARY KEY, title TEXT);
CREATE TABLE audit (msg TEXT);
CREATE VIEW big_orders AS SELECT * FROM orders WHERE total > 100;
CREATE TABLE status (code TEXT PRIMARY KEY, name TEXT NOT NULL);
INSERT INTO status VALUES ('NEW', 'New'), ('DONE', 'Done');
INSERT INTO customers (id, name) VALUES (1, 'Ada');
INSERT INTO orders (id, customer_id, total) VALUES (1, 1, 5);`); err != nil {
		t.Fatal(err)
	}

	_ = seed.Close()

	if err := ConfigureFiles(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ConfigureFiles("") }()

	m := NewManager(store.Limits{MaxOpen: 1}, time.Minute)
	defer m.Close()

	doc, err := m.SchemaDoc(context.Background(), &store.Connection{ID: "d", DBType: "sqlite", Database: "doc.db", UpdatedAt: 1}, "", "main")
	if err != nil {
		t.Fatal(err)
	}

	if len(doc.Tables) != 7 || !doc.Keys {
		t.Fatalf("tables: %d keys %v", len(doc.Tables), doc.Keys)
	}

	byName := map[string]DocTable{}
	for _, tb := range doc.Tables {
		byName[tb.Name] = tb
	}

	if refs := byName["customers"].ReferencedBy; len(refs) != 1 || refs[0].Table != "orders" {
		t.Fatalf("customers referenced by: %+v", refs)
	}

	if byName["big_orders"].Definition == "" {
		t.Fatal("view without definition")
	}

	// customers is referenced by orders and small: its rows are documented.
	// products is referenced by no declared key, so it is not read.
	if v := byName["customers"].Values; v == nil || len(v.Rows) != 1 || v.Rows[0][1] != "Ada" {
		t.Fatalf("lookup values of customers: %+v", v)
	}

	// status is keyed by a code: a lookup, though nothing references it.
	if v := byName["status"].Values; v == nil || len(v.Rows) != 2 || v.Rows[0][0] != "DONE" {
		t.Fatalf("lookup values of status: %+v", v)
	}

	if byName["products"].Values != nil || byName["orders"].Values != nil {
		t.Fatal("values read from a table nothing references")
	}

	has := func(kind, table string, cols ...string) bool {
		return slices.ContainsFunc(doc.Anomalies, func(a Anomaly) bool {
			return a.Kind == kind && a.Table == table && (len(cols) == 0 || slices.Equal(a.Columns, cols))
		})
	}

	for _, want := range []struct {
		kind, table string
		cols        []string
	}{
		{AnomalyNoPrimaryKey, "order_lines", nil},
		{AnomalyNoPrimaryKey, "audit", nil},
		{AnomalyUnindexedFK, "orders", []string{"customer_id"}},
		{AnomalyImplied, "order_lines", []string{"order_id"}},
		{AnomalyImplied, "order_lines", []string{"product_id"}},
		{AnomalyOrphan, "audit", nil},
		{AnomalySingleColumn, "audit", nil},
		{AnomalyNullableUnique, "customers", []string{"email"}},
		{AnomalyNullDefault, "orders", []string{"note"}},
		{AnomalyIncrementing, "customers", []string{"phone1", "phone2"}},
		{AnomalyDuplicateIndex, "order_lines", []string{"order_id"}},
	} {
		if !has(want.kind, want.table, want.cols...) {
			t.Errorf("missing %s on %s%v; got %+v", want.kind, want.table, want.cols, doc.Anomalies)
		}
	}

	// Declared keys are not implied ones, and views are not checked.
	if has(AnomalyImplied, "orders") || has(AnomalyNoPrimaryKey, "big_orders") || has(AnomalyOrphan, "order_lines") {
		t.Errorf("false positives: %+v", doc.Anomalies)
	}
}
