package engine

import (
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
