package engine

import (
	"strings"
	"testing"
)

func TestDuplicateForeignKeys(t *testing.T) {
	tbl := DocTable{
		Name: "transaction", Kind: "table",
		Columns: []DocColumn{{ColumnInfo: ColumnInfo{Name: "payment_domain"}}, {ColumnInfo: ColumnInfo{Name: "payer_type"}}},
		ForeignKeys: []ForeignKey{
			{Name: "transaction_n_payment_domain_fk", Columns: []string{"payment_domain"}, RefTable: "payment_domain", RefColumns: []string{"payment_domain"}},
			{Name: "transaction_n_payer_type_fk", Columns: []string{"payer_type"}, RefTable: "payer_type", RefColumns: []string{"payer_type"}},
			{Name: "transactions_n_payment_domain_fk", Columns: []string{"payment_domain"}, RefTable: "payment_domain", RefColumns: []string{"payment_domain"}},
		},
	}

	dup := duplicateFKs(tbl)
	if len(dup) != 1 || dup[0].Kind != AnomalyDuplicateFK || dup[0].Ref != "transaction_n_payment_domain_fk / transactions_n_payment_domain_fk" {
		t.Fatalf("duplicate keys: %+v", dup)
	}

	// The key declared twice is one missing index, not two.
	if un := unindexedFKs(tbl); len(un) != 2 {
		t.Fatalf("unindexed keys: %+v", un)
	}
}

func TestLookupConventionsAndPartialIndexes(t *testing.T) {
	col := func(name, typ string, pk bool) DocColumn {
		return DocColumn{ColumnInfo: ColumnInfo{Name: name, Type: typ, PrimaryKey: pk, Nullable: !pk}}
	}

	method := DocTable{Name: "payment_method", Kind: "table", Columns: []DocColumn{col("payment_method", "character varying(8)", true), col("method_name", "character varying(64)", false)}}
	trx := DocTable{Name: "transaction", Kind: "table", Columns: []DocColumn{col("transaction_id", "character varying", false), col("payment_method", "character varying", false)}}

	// A column named after a table whose key has the same name holds its
	// codes, without a foreign key.
	implied := impliedRelationships(trx, primaryKeys([]DocTable{method, trx}))
	if len(implied) != 1 || implied[0].Ref != "payment_method" || implied[0].Columns[0] != "payment_method" {
		t.Fatalf("implied: %+v", implied)
	}

	if !LookupChosen([]string{"Payment_*"}, "payment_method") || LookupChosen([]string{"payment"}, "payment_method") || LookupChosen(nil, "x") {
		t.Fatal("chosen tables")
	}

	// A partial index on the same columns is not a duplicate, and does not
	// serve a foreign key on every row.
	events := DocTable{Name: "outbox_events", Kind: "table",
		Indexes: []IndexInfo{
			{Name: "all_published", Columns: []string{"published_at"}},
			{Name: "unpublished", Columns: []string{"published_at"}, Where: "published_at IS NULL"},
			{Name: "partial_ref", Columns: []string{"ref"}, Where: "ref IS NOT NULL"},
		},
		ForeignKeys: []ForeignKey{{Name: "fk", Columns: []string{"ref"}, RefTable: "x", RefColumns: []string{"id"}}},
	}

	if dup := duplicateIndexes(events); len(dup) != 0 {
		t.Fatalf("partial index taken for a duplicate: %+v", dup)
	}

	if un := unindexedFKs(events); len(un) != 1 {
		t.Fatalf("a partial index covered a foreign key: %+v", un)
	}
}

func TestCompactSource(t *testing.T) {
	src := "CREATE FUNCTION f()   \r\nAS $$      \n\n\n\nBEGIN        \n  RETURN NEW;    \nEND $$   \n\n"
	if got := compactSource(src, 1000); got != "CREATE FUNCTION f()\nAS $$\n\nBEGIN\n  RETURN NEW;\nEND $$" {
		t.Fatalf("compact: %q", got)
	}

	long := strings.Repeat("line of code\n", 400)
	got := compactSource(long, 100)
	if !strings.HasPrefix(got, "line of code\nline of code\n") || !strings.Contains(got, "more characters; read the whole function") || len(got) > 200 {
		t.Fatalf("cut: %q", got)
	}

	// A single long line is cut on a character boundary.
	if got := compactSource(strings.Repeat("ç", 100), 51); !strings.HasPrefix(got, strings.Repeat("ç", 25)+"\n--") {
		t.Fatalf("rune cut: %q", got)
	}
}
