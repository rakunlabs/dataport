package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// putDoc saves a built snapshot under k, generated at the given time.
func putDoc(t *testing.T, s *Store, k DocKey, payload string, at int64) {
	t.Helper()

	ctx := context.Background()
	if err := s.EnsureDoc(ctx, k); err != nil {
		t.Fatal(err)
	}

	if ok, err := s.ClaimDoc(ctx, k, "tok", time.Now().Add(time.Minute).UnixMilli(), 0); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}

	d := &DocSnapshot{DocKey: k, Payload: payload, GeneratedAt: at, SourceVersion: "v"}
	if ok, err := s.FinishDoc(ctx, d, "tok", true); err != nil || !ok {
		t.Fatalf("finish: %v %v", ok, err)
	}
}

func docOwners(t *testing.T, s *Store, connectionID string) map[string]string {
	t.Helper()

	keys, err := s.ConnectionDocKeys(context.Background(), connectionID)
	if err != nil {
		t.Fatal(err)
	}

	out := map[string]string{}

	for _, k := range keys {
		d, err := s.GetDoc(context.Background(), k)
		if err != nil {
			t.Fatal(err)
		}

		out[k.UserID+"/"+k.Schema] = d.Payload
	}

	return out
}

func TestAdoptSharedDocs(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			c := &Connection{Name: "db", DBType: "sqlite"}
			if err := s.CreateConnection(ctx, c); err != nil {
				t.Fatal(err)
			}

			// Two users built main, one of them also sales; the system
			// snapshot is not a user's and stays.
			putDoc(t, s, DocKey{c.ID, "alice", "main"}, "alice-main", 100)
			putDoc(t, s, DocKey{c.ID, "bob", "main"}, "bob-main", 200)
			putDoc(t, s, DocKey{c.ID, "alice", "sales"}, "alice-sales", 50)
			putDoc(t, s, DocKey{c.ID, DocSystemUser, "main"}, "system-main", 10)

			if err := s.AdoptSharedDocs(ctx, c.ID); err != nil {
				t.Fatal(err)
			}

			want := map[string]string{"shared/main": "bob-main", "shared/sales": "alice-sales", "system/main": "system-main"}
			if got := docOwners(t, s, c.ID); len(got) != len(want) || got["shared/main"] != "bob-main" || got["shared/sales"] != "alice-sales" || got["system/main"] != "system-main" {
				t.Fatalf("after adopt: %v", got)
			}

			// A shared snapshot already there is not replaced by an older
			// user's copy, which still goes.
			putDoc(t, s, DocKey{c.ID, "carol", "main"}, "carol-main", 150)

			if err := s.AdoptSharedDocs(ctx, c.ID); err != nil {
				t.Fatal(err)
			}

			if got := docOwners(t, s, c.ID); got["shared/main"] != "bob-main" || got["carol/main"] != "" {
				t.Fatalf("second adopt: %v", got)
			}
		})
	}
}

// The migration does for every shared-account connection what
// AdoptSharedDocs does for one, and leaves connections of users' own
// accounts and personal ones alone.
func TestSharedDocsMigration(t *testing.T) {
	raw, err := migrationsFS.ReadFile("migrations/14_shared_docs.sql")
	if err != nil {
		t.Fatal(err)
	}

	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			owner := &User{Username: "owner", Role: RoleUser}
			if err := s.CreateUser(ctx, owner); err != nil {
				t.Fatal(err)
			}

			shared := &Connection{Name: "shared", DBType: "sqlite"}
			own := &Connection{Name: "own", DBType: "sqlite", UserCredentials: true}
			personal := &Connection{Name: "personal", DBType: "sqlite", OwnerID: owner.ID}

			for _, c := range []*Connection{shared, own, personal} {
				if err := s.CreateConnection(ctx, c); err != nil {
					t.Fatal(err)
				}

				putDoc(t, s, DocKey{c.ID, "alice", "main"}, c.Name+"-alice", 100)
				putDoc(t, s, DocKey{c.ID, "bob", "main"}, c.Name+"-bob", 200)
			}

			for _, stmt := range strings.Split(strings.ReplaceAll(string(raw), "${TABLE_PREFIX}", s.prefix), ";\n") {
				if strings.TrimSpace(stmt) == "" {
					continue
				}

				if _, err := s.db.ExecContext(ctx, stmt); err != nil {
					t.Fatalf("%v\n%s", err, stmt)
				}
			}

			if got := docOwners(t, s, shared.ID); len(got) != 1 || got["shared/main"] != "shared-bob" {
				t.Fatalf("shared: %v", got)
			}

			for _, c := range []*Connection{own, personal} {
				if got := docOwners(t, s, c.ID); len(got) != 2 || got["shared/main"] != "" {
					t.Fatalf("%s: %v", c.Name, got)
				}
			}
		})
	}
}
