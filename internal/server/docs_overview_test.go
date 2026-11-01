package server

import (
	"strings"
	"testing"

	"github.com/rakunlabs/dataport/internal/engine"
)

func TestSchemaOverviewMap(t *testing.T) {
	fk := func(col, ref string) engine.ForeignKey {
		return engine.ForeignKey{Name: col + "_fk", Columns: []string{col}, RefTable: ref, RefColumns: []string{col}}
	}
	tbl := func(name string, fks ...engine.ForeignKey) engine.DocTable {
		return engine.DocTable{Name: name, Kind: "table", ForeignKeys: fks}
	}

	doc := &engine.SchemaDoc{Schema: "transaction", Tables: []engine.DocTable{
		tbl("event_type"),
		tbl("outbox_events"),
		tbl("outbox_partition", fk("instance_id", "outbox_status")),
		tbl("outbox_routing"),
		tbl("outbox_status"),
		tbl("payer_type"),
		tbl("sandbox_contract"),
		tbl("status_type"),
		tbl("transaction", fk("payer_type", "payer_type"), fk("status_type", "status_type"), fk("status_type", "status_type")),
		tbl("transaction_event", fk("event_type", "event_type"), fk("linked_event_id", "transaction_event")),
		tbl("transaction_migrations"),
		tbl("x1_migration"),
	}}

	groups := map[string]string{}
	for _, g := range schemaGroups(doc) {
		groups[g.Name] = strings.Join(g.Tables, ",")
	}

	for name, want := range map[string]string{
		"transaction":           "payer_type,status_type,transaction",
		"outbox_*":              "outbox_events,outbox_partition,outbox_routing,outbox_status",
		"migration bookkeeping": "transaction_migrations,x1_migration",
		"standalone":            "sandbox_contract",
	} {
		if groups[name] != want {
			t.Errorf("group %s = %q, want %q (all: %v)", name, groups[name], want, groups)
		}
	}

	var b strings.Builder
	writeSchemaDiagram(&b, doc)
	got := b.String()

	for _, want := range []string{"```mermaid\nerDiagram\n", `status_type ||--o{ transaction : "status_type"`, `transaction_event ||--o{ transaction_event : "linked_event_id"`} {
		if !strings.Contains(got, want) {
			t.Errorf("diagram lacks %q:\n%s", want, got)
		}
	}

	if strings.Count(got, `status_type ||--o{ transaction :`) != 1 {
		t.Errorf("a key declared twice is one edge:\n%s", got)
	}
}
