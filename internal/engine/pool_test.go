package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/rakunlabs/dataport/internal/store"
)

func TestCloseIdleKeepsPoolInUse(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "idle.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ConfigureFiles(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ConfigureFiles("") }()

	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	c := &store.Connection{ID: "s", DBType: "sqlite", Database: "idle.db", UpdatedAt: 1}

	p, err := m.pool(c, "")
	if err != nil {
		t.Fatal(err)
	}

	conn, err := p.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// A long export fetched the pool long ago and still holds a connection.
	later := time.Now().Add(time.Hour)
	m.closeIdle(later)

	if !m.Connected(c) {
		t.Fatal("pool with a connection in use was closed")
	}

	_ = conn.Close()

	// The pool counts as used when it was last seen busy.
	m.closeIdle(later.Add(30 * time.Second))

	if !m.Connected(c) {
		t.Fatal("pool closed before the idle timeout since its last use")
	}

	m.closeIdle(later.Add(2 * time.Minute))

	if m.Connected(c) {
		t.Fatal("idle pool was not closed")
	}
}
