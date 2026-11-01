package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

func TestSharedAccountDocs(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv
	k := docKeyFor(t, env)

	c, _ := env.st.GetConnection(ctx, k.ConnectionID)
	c.Access = append(c.Access, store.Access{SubjectType: store.SubjectUser, SubjectID: env.bob.ID})
	if err := env.st.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}

	bobKey := k
	bobKey.UserID = env.bob.ID

	// Alice builds it; bob, a builder too, reads the same snapshot instead
	// of scanning the catalog again.
	first, err := s.documentation(ctx, k, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.documentation(ctx, bobKey, false)
	if err != nil || got.Document.GeneratedAt != first.Document.GeneratedAt || got.Status.ReadOnly {
		t.Fatalf("bob's read: %v %+v", err, got)
	}

	// Bob's refresh is alice's too: there is one snapshot.
	g, pw, err := s.resolveGrant(ctx, env.alice, k.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.engine.Execute(ctx, g.Connection, pw, engineExec("CREATE TABLE added (id INTEGER PRIMARY KEY)")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := s.documentation(ctx, bobKey, true); err != nil {
		t.Fatal(err)
	}
	mine, err := s.documentation(ctx, k, false)
	if err != nil || len(mine.Document.Tables) != len(first.Document.Tables)+1 {
		t.Fatalf("alice did not see bob's refresh: %v %d tables", err, len(mine.Document.Tables))
	}

	keys, _ := env.st.ConnectionDocKeys(ctx, k.ConnectionID)
	if len(keys) != 1 || keys[0].UserID != store.DocSharedUser {
		t.Fatalf("snapshots: %+v", keys)
	}

	// Copies users kept from before are adopted, not rebuilt: the newest
	// one becomes the shared snapshot.
	if err := env.st.DeleteDoc(ctx, sharedKey(k)); err != nil {
		t.Fatal(err)
	}
	shared, _, version, err := s.sharedDocAccess(ctx, k.ConnectionID)
	if err != nil || shared == nil {
		t.Fatal(err)
	}
	old := &store.DocSnapshot{DocKey: bobKey, Payload: `{"schema":"main","generated_at":42,"tables":[]}`, GeneratedAt: 42, SourceVersion: version}
	if err := env.st.EnsureDoc(ctx, bobKey); err != nil {
		t.Fatal(err)
	}
	if ok, err := env.st.ClaimDoc(ctx, bobKey, "t", time.Now().Add(time.Minute).UnixMilli(), 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := env.st.FinishDoc(ctx, old, "t", true); err != nil || !ok {
		t.Fatal(ok, err)
	}
	adopted, err := s.documentation(ctx, k, false)
	if err != nil || adopted.Document.GeneratedAt != 42 {
		t.Fatalf("not adopted: %v %+v", err, adopted)
	}
	if _, err := env.st.GetDoc(ctx, bobKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("bob's copy kept after adoption")
	}

	// Once users sign in with their own accounts, the shared snapshot goes
	// on the next refresh of all documentation, and is not served.
	c, _ = env.st.GetConnection(ctx, k.ConnectionID)
	c.UserCredentials = true
	if err := env.st.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := env.st.SetSetting(ctx, docScheduleSetting, `{"enabled":true,"cron_spec":"@every 1h"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.dispatchDocsAt(ctx, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := env.st.GetDoc(ctx, sharedKey(k)); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("shared snapshot kept for a connection of users' own accounts")
	}
	if run := s.docRunStatus(ctx); run.Failed != 0 {
		t.Fatalf("run: %+v", run)
	}
}
