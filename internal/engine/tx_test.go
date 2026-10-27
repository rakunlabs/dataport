package engine

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

func TestManualTx(t *testing.T) {
	root := t.TempDir()

	db, err := sql.Open("sqlite", filepath.Join(root, "tx.db"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}

	db.Close()

	if err := ConfigureFiles(root); err != nil {
		t.Fatal(err)
	}
	defer ConfigureFiles("")

	m := NewManager(store.Limits{MaxOpen: 3}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "tx", DBType: "sqlite", Database: "tx.db"}
	ctx := context.Background()

	run := func(txID, sqlText string, cont bool) (*ExecResult, error) {
		return m.Execute(ctx, c, "", ExecRequest{UserID: "u", SQL: sqlText, MaxRows: 10, TxID: txID, TxContinue: cont})
	}

	count := func() int64 {
		t.Helper()

		res, err := run("", "SELECT COUNT(*) FROM t", false)
		if err != nil || res.FirstError() != "" {
			t.Fatalf("count: %v %s", err, res.FirstError())
		}

		return res.Results[0].Rows[0][0].(int64)
	}

	// Two runs in one transaction; the rows only count once committed.
	res, err := run("tab1", "INSERT INTO t (v) VALUES ('a')", false)
	if err != nil || !res.TxOpen || res.Committed || res.TxStatements != 1 {
		t.Fatalf("first run: %+v %v", res, err)
	}

	res, err = run("tab1", "INSERT INTO t (v) VALUES ('b'); SELECT COUNT(*) FROM t", true)
	if err != nil || res.Results[1].Rows[0][0] != int64(2) || res.TxStatements != 3 {
		t.Fatalf("second run: %+v %v", res, err)
	}

	if txs := m.Txs("u"); len(txs) != 1 || txs[0].TxID != "tab1" || len(m.Txs("other")) != 0 {
		t.Fatalf("txs: %+v", txs)
	}

	// A failed statement keeps the transaction and what ran before it.
	res, err = run("tab1", "INSERT INTO nope VALUES (1)", true)
	if err != nil || res.FirstError() == "" || !res.TxOpen {
		t.Fatalf("failed run: %+v %v", res, err)
	}

	if _, err := m.EndTx("other", "tab1", "tx", true); !errors.Is(err, ErrTxLost) {
		t.Fatalf("another user ended the transaction: %v", err)
	}

	end, err := m.EndTx("u", "tab1", "tx", true)
	if err != nil || !end.Committed {
		t.Fatalf("commit: %+v %v", end, err)
	}

	if n := count(); n != 2 {
		t.Fatalf("rows after commit: %d", n)
	}

	// Rollback drops what the transaction did.
	if _, err := run("tab2", "DELETE FROM t", false); err != nil {
		t.Fatal(err)
	}

	if _, err := m.EndTx("u", "tab2", "tx", false); err != nil {
		t.Fatal(err)
	}

	if n := count(); n != 2 {
		t.Fatalf("rows after rollback: %d", n)
	}

	// The client learns of a transaction that is gone instead of getting a
	// new one silently.
	if _, err := run("tab2", "SELECT 1", true); !errors.Is(err, ErrTxLost) {
		t.Fatalf("continue a closed transaction: %v", err)
	}

	if _, err := m.EndTx("u", "tab2", "tx", true); !errors.Is(err, ErrTxLost) {
		t.Fatalf("end a closed transaction: %v", err)
	}

	// An idle transaction is rolled back.
	if _, err := run("tab3", "INSERT INTO t (v) VALUES ('c')", false); err != nil {
		t.Fatal(err)
	}

	m.expireTxs(time.Now().Add(TxIdle + time.Minute))
	time.Sleep(100 * time.Millisecond)

	if len(m.Txs("u")) != 0 || count() != 2 {
		t.Fatalf("idle transaction kept: %+v", m.Txs("u"))
	}

	// One pooled connection stays free for everything else.
	for _, id := range []string{"a", "b"} {
		if _, err := run(id, "SELECT 1", false); err != nil {
			t.Fatalf("open %s: %v", id, err)
		}
	}

	if _, err := run("c", "SELECT 1", false); !errors.Is(err, ErrTxLimit) {
		t.Fatalf("third transaction on a pool of 3: %v", err)
	}

	// Forgetting the connection rolls its transactions back.
	m.Forget("tx")
	time.Sleep(100 * time.Millisecond)

	if len(m.Txs("u")) != 0 {
		t.Fatalf("transactions kept after Forget: %+v", m.Txs("u"))
	}
}
