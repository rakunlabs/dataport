package cluster

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/rakunlabs/dataport/internal/crypto"
	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/notebook"
	"github.com/rakunlabs/dataport/internal/store"
)

type replica struct {
	t *memTransport
	m *engine.Manager
	r *Router
}

func setup(t *testing.T, n int) (*memNet, []*replica, string) {
	t.Helper()

	root := t.TempDir()

	for file, seed := range map[string]string{
		"a.db": `CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT, price REAL, data BLOB);
			INSERT INTO items VALUES (1, 'apple', 1.5, x'00ff'), (2, 'pear', 2.25, NULL), (3, 'plum', NULL, x'01');`,
		"b.db": `CREATE TABLE copied (id INTEGER PRIMARY KEY, name TEXT);`,
	} {
		db, err := sql.Open("sqlite", filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}

		if _, err := db.Exec(seed); err != nil {
			t.Fatal(err)
		}

		_ = db.Close()
	}

	if err := engine.ConfigureFiles(root); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = engine.ConfigureFiles("") })

	ci, err := crypto.New("shared")
	if err != nil {
		t.Fatal(err)
	}

	mn := newMemNet()

	out := make([]*replica, n)
	for i := range out {
		m := engine.NewManager(store.Limits{MaxOpen: 3}, time.Minute)
		tr := mn.join(7000 + i)
		r := newRouter(tr, m, ci, string(rune('a'+i)))

		t.Cleanup(func() { r.cancel(); m.Close() })

		out[i] = &replica{t: tr, m: m, r: r}
	}

	return mn, out, root
}

var connA = &store.Connection{ID: "A", Name: "a", DBType: "sqlite", Database: "a.db", UpdatedAt: 1}

func TestOneOwnerServesEveryReplica(t *testing.T) {
	_, rs, _ := setup(t, 3)
	ctx := context.Background()

	// The first replica to need the pool owns it.
	res, err := rs[0].r.Execute(ctx, connA, "", engine.ExecRequest{SQL: "SELECT id, name FROM items ORDER BY id", ReadOnly: true, MaxRows: 10})
	if err != nil || res.FirstError() != "" {
		t.Fatalf("%v %s", err, res.FirstError())
	}

	if !rs[0].t.holds(lockPrefix + "A") {
		t.Fatal("first replica does not own the pool")
	}

	for _, rep := range rs[1:] {
		res, err := rep.r.Execute(ctx, connA, "", engine.ExecRequest{SQL: "SELECT id, name, price, data FROM items ORDER BY id", ReadOnly: true, MaxRows: 2})
		if err != nil || res.FirstError() != "" {
			t.Fatalf("forwarded execute: %v %s", err, res.FirstError())
		}

		rows := res.Results[0].Rows
		if len(rows) != 2 || !res.Results[0].Truncated || rows[0][1] != "apple" || rows[0][2] != 1.5 || rows[0][3] != `\x00ff` || rows[1][3] != nil {
			t.Fatalf("forwarded rows: %#v", res.Results[0])
		}

		if rep.m.HasPool("A") {
			t.Fatal("a replica that does not own the pool opened it")
		}
	}

	schemas, err := rs[2].r.Objects(ctx, connA, "", "")
	if err != nil || len(schemas) != 1 || schemas[0].Name != "items" {
		t.Fatalf("objects: %v %v", schemas, err)
	}

	plan, err := rs[2].r.Explain(ctx, connA, "", engine.ExplainRequest{SQL: "SELECT * FROM items WHERE id = 1"})
	if err != nil || plan.Result == nil || len(plan.Result.Rows) == 0 {
		t.Fatalf("forwarded explain: %+v %v", plan, err)
	}

	n, err := rs[1].r.Count(ctx, connA, "", engine.CountRequest{SQL: "SELECT * FROM items"})
	if err != nil || n.Count != 3 {
		t.Fatalf("forwarded count: %+v %v", n, err)
	}

	if _, err := rs[1].r.Count(ctx, connA, "", engine.CountRequest{SQL: "DELETE FROM items"}); !errors.Is(err, engine.ErrNotCountable) {
		t.Fatalf("count error lost its kind: %v", err)
	}

	cols, err := rs[1].r.Columns(ctx, connA, "", "", "items")
	if err != nil || len(cols) != 4 || !cols[0].PrimaryKey {
		t.Fatalf("columns: %v %v", cols, err)
	}

	st, err := rs[1].r.Structure(ctx, connA, "", "", "items")
	if err != nil || st.Indexes == nil || st.ForeignKeys == nil || st.Triggers == nil {
		t.Fatalf("structure must keep empty lists: %+v %v", st, err)
	}

	ov := rs[1].r.Overview(ctx)
	if len(ov.Nodes) != 3 || !ov.Nodes[0].Self {
		t.Fatalf("overview: %+v", ov)
	}

	owners := 0

	for _, n := range ov.Nodes {
		if len(n.Owned) == 1 && n.Owned[0] == "A" && len(n.Pools) == 1 {
			owners++
		}
	}

	if owners != 1 {
		t.Fatalf("pool owned by %d replicas: %+v", owners, ov.Nodes)
	}
}

func TestErrorsKeepTheirKind(t *testing.T) {
	_, rs, _ := setup(t, 2)
	ctx := context.Background()

	if _, err := rs[0].r.Schemas(ctx, connA, ""); err != nil {
		t.Fatal(err)
	}

	_, err := rs[1].r.Execute(ctx, connA, "", engine.ExecRequest{SQL: "DELETE FROM items", ReadOnly: true})
	if !errors.Is(err, engine.ErrReadOnly) && (err == nil || !strings.Contains(err.Error(), "read-only")) {
		t.Fatalf("read-only error: %v", err)
	}

	missing := &store.Connection{ID: "M", DBType: "sqlite", Database: "../outside.db", UpdatedAt: 1}
	if _, err := rs[0].r.Schemas(ctx, missing, ""); err == nil {
		t.Fatal("expected an error")
	}

	if _, err := rs[1].r.Schemas(ctx, missing, ""); !errors.Is(err, engine.ErrFilePath) {
		t.Fatalf("file path error lost its kind: %v", err)
	}

	// A forwarded call ends the same way as a local one.
	_, local := rs[0].r.Structure(ctx, connA, "", "", "nope")
	_, remote := rs[1].r.Structure(ctx, connA, "", "", "nope")

	if (local == nil) != (remote == nil) || (local != nil && local.Error() != remote.Error()) {
		t.Fatalf("local %v, forwarded %v", local, remote)
	}
}

func TestEditImportExportGenerate(t *testing.T) {
	_, rs, _ := setup(t, 2)
	ctx := context.Background()

	if _, err := rs[0].r.Schemas(ctx, connA, ""); err != nil {
		t.Fatal(err)
	}

	r := rs[1].r

	edit := engine.EditRequest{Table: "items", Edits: []engine.RowEdit{{Op: "update", Key: map[string]any{"id": int64(1)}, Values: map[string]any{"name": "APPLE"}}}}

	preview, err := r.PreviewEdits(ctx, connA, "", edit)
	if err != nil || len(preview) != 1 {
		t.Fatalf("preview: %v %v", preview, err)
	}

	applied, err := r.ApplyEdits(ctx, connA, "", edit)
	if err != nil || applied.Affected != 1 {
		t.Fatalf("apply: %+v %v", applied, err)
	}

	imp, err := r.Import(ctx, connA, "", engine.ImportRequest{Table: "items", Columns: []string{"id", "name"}, Rows: [][]any{{int64(10), "fig"}, {"11", "kiwi"}}})
	if err != nil || imp.Inserted != 2 {
		t.Fatalf("import: %+v %v", imp, err)
	}

	var buf bytes.Buffer

	stats, err := r.Export(ctx, connA, "", engine.ExportRequest{Tables: []string{"items"}, Format: "csv", Data: true}, &buf)
	if err != nil || stats.Rows != 5 || !strings.Contains(buf.String(), "APPLE") || !strings.Contains(buf.String(), "kiwi") {
		t.Fatalf("export: %+v %v\n%s", stats, err, buf.String())
	}

	var progress int

	n, err := r.Generate(ctx, connA, "", engine.GenerateRequest{
		Table: "items", Rows: 50, Seed: 1,
		Columns: []engine.GenColumn{{Name: "id", Generator: "sequence"}, {Name: "name", Generator: "word"}},
	}, func(engine.GenerateProgress) { progress++ })
	if err != nil || n != 50 {
		t.Fatalf("generate: %d %v", n, err)
	}

	if rs[1].m.HasPool("A") {
		t.Fatal("the forwarding replica opened the pool")
	}
}

func TestTransferAcrossOwners(t *testing.T) {
	_, rs, _ := setup(t, 2)
	ctx := context.Background()

	connB := &store.Connection{ID: "B", Name: "b", DBType: "sqlite", Database: "b.db", UpdatedAt: 1}

	// Each replica owns one side; the transfer starts on the source owner.
	if _, err := rs[0].r.Schemas(ctx, connA, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := rs[1].r.Schemas(ctx, connB, ""); err != nil {
		t.Fatal(err)
	}

	var done []string

	err := rs[0].r.Transfer(ctx, connA, "", connB, "", engine.TransferRequest{Tables: []string{"items"}, Create: true, Data: true, Timeout: 10 * time.Second},
		func(p engine.TransferProgress) {
			if p.Error != "" {
				t.Errorf("%s: %s", p.Table, p.Error)
			}

			if p.Done {
				done = append(done, p.Table)
			}
		})
	if err != nil || len(done) != 1 {
		t.Fatalf("transfer: %v %v", err, done)
	}

	res, err := rs[0].r.Execute(ctx, connB, "", engine.ExecRequest{SQL: "SELECT name, data FROM items ORDER BY id", ReadOnly: true, MaxRows: 10})
	if err != nil || len(res.Results[0].Rows) != 3 || res.Results[0].Rows[0][1] != `\x00ff` {
		t.Fatalf("copied rows: %+v %v", res, err)
	}

	tr, err := rs[0].r.QueryTransfer(ctx, connA, "", connB, "", engine.QueryTransferRequest{
		SQL:    "SELECT id, name FROM items WHERE id > ?",
		Args:   []any{int64(1)},
		Target: notebook.Transfer{ConnectionID: "B", Table: "copied", Batch: 1},
	})
	if err != nil || tr.Inserted != 2 {
		t.Fatalf("query transfer: %+v %v", tr, err)
	}

	if rs[0].m.HasPool("B") || rs[1].m.HasPool("A") {
		t.Fatal("a pool was opened outside its owner")
	}
}

func TestTransactionsAndCancel(t *testing.T) {
	_, rs, _ := setup(t, 2)
	ctx := context.Background()

	if _, err := rs[0].r.Schemas(ctx, connA, ""); err != nil {
		t.Fatal(err)
	}

	r := rs[1].r

	res, err := r.Execute(ctx, connA, "", engine.ExecRequest{SQL: "UPDATE items SET name = 'x' WHERE id = 1", UserID: "u", TxID: "tab", MaxRows: 10})
	if err != nil || !res.TxOpen {
		t.Fatalf("open tx: %+v %v", res, err)
	}

	if txs := r.Txs(ctx, "u"); len(txs) != 1 || txs[0].TxID != "tab" {
		t.Fatalf("txs: %+v", txs)
	}

	end, err := r.EndTx(ctx, "u", "tab", "A", false)
	if err != nil || end.Committed || end.Statements != 1 {
		t.Fatalf("end tx: %+v %v", end, err)
	}

	if _, err := r.EndTx(ctx, "u", "tab", "A", false); !errors.Is(err, engine.ErrTxLost) {
		t.Fatalf("ending twice: %v", err)
	}

	if r.Cancel(ctx, "u", "nothing") {
		t.Fatal("nothing to cancel")
	}

	// A long query on the owner, stopped from the other replica.
	errs := make(chan error, 1)

	go func() {
		_, err := r.Execute(ctx, connA, "", engine.ExecRequest{
			SQL:    "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT count(*) FROM c",
			UserID: "u", QueryID: "q", ReadOnly: true, MaxRows: 1,
		})
		errs <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !rs[0].r.Cancel(ctx, "u", "q") {
		if time.Now().After(deadline) {
			t.Fatal("query never became cancellable")
		}

		time.Sleep(20 * time.Millisecond)
	}

	select {
	case <-errs:
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled query did not stop")
	}
}

func TestOwnerLeavesAndReleases(t *testing.T) {
	mn, rs, _ := setup(t, 2)
	ctx := context.Background()

	if _, err := rs[0].r.Schemas(ctx, connA, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := rs[1].r.Schemas(ctx, connA, ""); err != nil {
		t.Fatal(err)
	}

	// The owner stops; the other replica takes the pool over.
	mn.drop(rs[0].t)

	if _, err := rs[1].r.Schemas(ctx, connA, ""); err != nil {
		t.Fatal(err)
	}

	if !rs[1].t.holds(lockPrefix+"A") || !rs[1].m.HasPool("A") {
		t.Fatal("pool was not taken over")
	}

	// Releasing closes the pool and gives up the lock.
	if !rs[1].r.ReleasePool(ctx, "A") {
		t.Fatal("release found no pool")
	}

	deadline := time.Now().Add(2 * time.Second)
	for rs[1].t.holds(lockPrefix + "A") {
		if time.Now().After(deadline) {
			t.Fatal("lock kept after the pool closed")
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func TestForgetReachesTheOwner(t *testing.T) {
	_, rs, _ := setup(t, 2)
	ctx := context.Background()

	if _, err := rs[0].r.Schemas(ctx, connA, ""); err != nil {
		t.Fatal(err)
	}

	rs[1].r.Forget("A")

	deadline := time.Now().Add(2 * time.Second)
	for rs[0].m.HasPool("A") {
		if time.Now().After(deadline) {
			t.Fatal("owner kept the pool of a forgotten connection")
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
