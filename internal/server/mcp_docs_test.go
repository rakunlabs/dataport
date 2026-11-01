package server

import (
	"strings"
	"testing"

	"github.com/rakunlabs/dataport/internal/engine"
)

func TestWriteTableDocDetails(t *testing.T) {
	tbl := engine.DocTable{
		Name: "transaction_event", Kind: "table",
		Columns: []engine.DocColumn{{ColumnInfo: engine.ColumnInfo{Name: "id", Type: "bigint", PrimaryKey: true}}},
		Indexes: []engine.IndexInfo{
			{Name: "te_pkey", Columns: []string{"id"}, Primary: true, Method: "btree"},
			{Name: "te_refs", Columns: []string{"refs"}, Method: "gin"},
			{Name: "te_open", Columns: []string{"originators_id"}, Unique: true, Where: "closed_at IS NULL"},
		},
		ForeignKeys: []engine.ForeignKey{
			{Name: "a", Columns: []string{"event_type"}, RefTable: "event_type", RefColumns: []string{"event_type"}},
			{Name: "b", Columns: []string{"event_type"}, RefTable: "event_type", RefColumns: []string{"event_type"}},
		},
		Partition: &engine.PartitionInfo{Key: "RANGE (id)", Count: 3, First: "te_part_2021_10", Last: "te_part_2026_10"},
	}

	out := tableDocsOut{
		Triggers: map[string][]engine.TriggerInfo{tbl.Name: {{
			Name: "check_originators", Timing: "BEFORE", Event: "INSERT", Enabled: true,
			Definition: "CREATE TRIGGER check_originators BEFORE INSERT ON transaction.transaction_event FOR EACH ROW EXECUTE FUNCTION func_check_unique_originators_id()",
			Function:   "CREATE FUNCTION func_check_unique_originators_id() ... INSERT INTO transaction_event_originators_id ...",
		}}},
		Anomalies: []engine.Anomaly{{Kind: engine.AnomalyOrphan, Table: tbl.Name}},
	}

	var b strings.Builder
	writeTableDoc(&b, "postgres", tbl, &out)
	got := b.String()

	for _, want := range []string{
		"partitioned by RANGE (id), 3 partitions (te_part_2021_10 … te_part_2026_10)",
		"index te_refs (refs) using gin\n",
		"primary key te_pkey (id)\n",
		"trigger check_originators BEFORE INSERT -> func_check_unique_originators_id()",
		"design notes: no_fk_relations",
		"unique te_open (originators_id) where closed_at IS NULL\n",
		"```sql\nCREATE FUNCTION func_check_unique_originators_id() ... INSERT INTO transaction_event_originators_id ...\n```\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "upsert targets") {
		t.Errorf("a partial unique index offered as an upsert target:\n%s", got)
	}

	if n := strings.Count(got, "references: "); n != 1 {
		t.Errorf("a key declared twice is one join, got %d:\n%s", n, got)
	}
}
